// Package restarttrace records bounded, local-only restart stages. It is opt-in
// through context and never records error strings, paths, ports or app content.
package restarttrace

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"
)

type Stage uint8

const (
	Worker Stage = iota
	Apply
	Restore
	InterruptedRecovery
	OpenSession
	Installation
	CurrentProcess
	StopProcess
	StableInstallation
	Appearance
	LaunchControlled
	WaitListener
	ConnectPage
	FailureRecovery
	LaunchOrdinary
	WaitOrdinary
	AppearanceCheck
)

var stageNames = [...]string{"worker", "apply", "restore", "interrupted_recovery", "open_session", "installation", "current_process", "stop_process", "stable_installation", "appearance", "launch_controlled", "wait_listener", "connect_page", "failure_recovery", "launch_ordinary", "wait_ordinary", "appearance_check"}

const MaxEntries = 64

type Entry struct {
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	ElapsedMS  int64  `json:"elapsedMs"`
	DurationMS int64  `json:"durationMs"`
	Reason     string `json:"reason,omitempty"`
}

type Report struct {
	SchemaVersion     int     `json:"schemaVersion"`
	RequestID         string  `json:"requestId"`
	HelperVersion     string  `json:"helperVersion"`
	BuildCommit       string  `json:"buildCommit"`
	StartedAt         string  `json:"startedAt"`
	Events            []Entry `json:"events"`
	Truncated         bool    `json:"truncated,omitempty"`
	WriteFailed       bool    `json:"writeFailed,omitempty"`
	FirstFailureStage string  `json:"firstFailureStage,omitempty"`
}

type recorder struct {
	mu     sync.Mutex
	start  time.Time
	report Report
	write  func(Report) error
}
type contextKey struct{}

var safeBuildLabel = regexp.MustCompile(`^[A-Za-z0-9.+_-]{1,96}$`)

func buildLabel(value string) string {
	if safeBuildLabel.MatchString(value) {
		return value
	}
	return "unavailable"
}

// WithRecorder enables diagnostics only for this worker and its cleanup contexts.
// A diagnostic write failure must never alter Apply, Restore or rollback.
func WithRecorder(ctx context.Context, requestID, version, commit string, write func(Report) error) context.Context {
	r := &recorder{start: time.Now(), write: write}
	r.report = Report{SchemaVersion: 1, RequestID: requestID, HelperVersion: buildLabel(version), BuildCommit: buildLabel(commit), StartedAt: r.start.UTC().Format(time.RFC3339Nano), Events: []Entry{}}
	return context.WithValue(ctx, contextKey{}, r)
}

// Start persists entry immediately, so an interrupted process leaves a running
// stage. The returned function is idempotent; only fixed error classes are kept.
func Start(ctx context.Context, stage Stage) func(error) {
	if ctx == nil {
		return func(error) {}
	}
	r, _ := ctx.Value(contextKey{}).(*recorder)
	if r == nil || int(stage) >= len(stageNames) {
		return func(error) {}
	}
	r.mu.Lock()
	if len(r.report.Events) >= MaxEntries {
		if !r.report.Truncated {
			r.report.Truncated = true
			r.persist()
		}
		r.mu.Unlock()
		return func(error) {}
	}
	index := len(r.report.Events)
	started := time.Now()
	r.report.Events = append(r.report.Events, Entry{Stage: stageNames[stage], Status: "running", ElapsedMS: started.Sub(r.start).Milliseconds()})
	r.persist()
	r.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			entry := &r.report.Events[index]
			entry.DurationMS = time.Since(started).Milliseconds()
			entry.Status = "passed"
			if err != nil {
				if r.report.FirstFailureStage == "" {
					r.report.FirstFailureStage = entry.Stage
				}
				entry.Status, entry.Reason = "failed", "operation_failed"
				switch {
				case errors.Is(err, context.DeadlineExceeded):
					entry.Reason = "deadline_exceeded"
				case errors.Is(err, context.Canceled):
					entry.Reason = "cancelled"
				}
			}
			r.persist()
		})
	}
}

func (r *recorder) persist() {
	copy := r.report
	copy.Events = append([]Entry(nil), copy.Events...)
	if r.write(copy) != nil {
		r.report.WriteFailed = true
	}
}
