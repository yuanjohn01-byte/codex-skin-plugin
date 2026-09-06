package restarttrace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestTraceStagesRedactionAndCleanup(t *testing.T) {
	var reports []Report
	ctx := WithRecorder(context.Background(), "rst_test", "0.1.0-test", "development", func(r Report) error { reports = append(reports, r); return nil })
	finish := Start(ctx, LaunchControlled)
	if reports[0].Events[0].Status != "running" {
		t.Fatal("stage not persisted before execution")
	}
	finish(errors.New("C:\\Users\\private secret"))
	finish(nil)
	Start(context.WithoutCancel(ctx), FailureRecovery)(nil)
	last := reports[len(reports)-1]
	if last.Events[0].Status != "failed" || last.Events[1].Status != "passed" || last.FirstFailureStage != "launch_controlled" {
		t.Fatalf("original failure lost: %+v", last)
	}
	raw, err := json.Marshal(last)
	if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
		t.Fatal("raw diagnostic escaped")
	}
	if reports[0].Events[0].Status != "running" {
		t.Fatal("persisted snapshot mutated later")
	}
}

func TestTraceBoundsAndConcurrentCompletion(t *testing.T) {
	var last Report
	ctx := WithRecorder(context.Background(), "rst_test", "/private/version", "\nsecret", func(r Report) error { last = r; return nil })
	var group sync.WaitGroup
	for i := 0; i < MaxEntries+20; i++ {
		group.Add(1)
		go func() { defer group.Done(); Start(ctx, WaitListener)(context.DeadlineExceeded) }()
	}
	group.Wait()
	if len(last.Events) != MaxEntries || !last.Truncated || last.HelperVersion != "unavailable" || last.BuildCommit != "unavailable" {
		t.Fatalf("unbounded/unsafe report: %+v", last)
	}
	for _, entry := range last.Events {
		if entry.Status != "failed" || entry.Reason != "deadline_exceeded" {
			t.Fatal("completion lost")
		}
	}
	raw, _ := json.Marshal(last)
	if len(raw) > 16*1024 {
		t.Fatal("report exceeds store budget")
	}
}

func TestTraceWriteFailureAndDisabledContexts(t *testing.T) {
	calls := 0
	var last Report
	ctx := WithRecorder(context.Background(), "rst_test", "dev", "dev", func(r Report) error {
		calls++
		last = r
		if calls == 1 {
			return errors.New("synthetic disk failure")
		}
		return nil
	})
	Start(ctx, StopProcess)(context.Canceled)
	if !last.WriteFailed || last.Events[0].Reason != "cancelled" {
		t.Fatal("write failure or cancellation lost")
	}
	Start(nil, Worker)(nil)
	Start(context.Background(), Worker)(nil)
	Start(ctx, Stage(255))(nil)
	if calls != 2 {
		t.Fatal("disabled trace wrote data")
	}
}
