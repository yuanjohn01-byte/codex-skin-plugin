package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restarttrace"
)

type unavailableRecoveryAdapter struct{ *restartApplyAdapter }

func (a *unavailableRecoveryAdapter) OpenVerifiedOfficialSession(context.Context) (engine.Session, error) {
	return engine.Session{}, errors.New("synthetic private path C:\\Users\\private and token must not be recorded")
}

func TestRestartWorkerDiagnosticDoesNotChangeSuccessfulApply(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		store, err := engine.OpenStore(filepath.Join(t.TempDir(), "CodexSkin"), "")
		if err != nil {
			t.Fatal(err)
		}
		restarts, err := restartflow.New(store.Root())
		if err != nil {
			t.Fatal(err)
		}
		request, err := restarts.StageApply(verifiedRestartTheme(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := restarts.Approve(request.RequestID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(store.Root(), "restart", "diagnostic.json")
		if blocked {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		fake := &restartApplyAdapter{identity: engine.Identity{
			Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
			Version: "26.727.0", ExecutableHash: strings.Repeat("a", 64), ProcessID: 4312, ProcessStartID: "fixture-4312",
		}}
		code, _, _ := run(t, []string{"__restart-worker", request.RequestID}, Runtime{GOOS: "windows", GOARCH: "amd64", Root: store.Root(), RestartDelay: -1, Adapter: fake})
		current, found, err := restarts.Current()
		if err != nil || !found || code != exitSuccess || current.Status != restartflow.StatusCompleted || current.OperationID == "" || fake.applies != 1 {
			t.Fatalf("diagnostic changed Apply (blocked=%v): %d %+v %v", blocked, code, current, err)
		}
		if blocked {
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() {
				t.Fatal("unsafe diagnostic target was replaced")
			}
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report restarttrace.Report
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		if report.HelperVersion != buildinfo.Version || report.BuildCommit != buildinfo.Commit || report.RequestID != request.RequestID || report.Events[0].Stage != "worker" || report.Events[0].Status != "passed" || report.FirstFailureStage != "" {
			t.Fatalf("wrong worker identity/outcome: %+v", report)
		}
	}
}

// Exercise the actual detached worker and interrupted recovery before a new
// operation ID exists. No live app, PowerShell, configuration or network is used.
func TestRestartWorkerDiagnosticBeforeOperationID(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			store, err := engine.OpenStore(filepath.Join(t.TempDir(), "CodexSkin"), "")
			if err != nil {
				t.Fatal(err)
			}
			oldID := "op_" + strings.Repeat("1", 32)
			recoveryID := "rec_" + strings.Repeat("2", 32)
			if err := store.WriteRecoveryPoint(engine.RecoveryPoint{RecoveryID: recoveryID, OperationID: oldID}, engine.Snapshot{}); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteJournal(engine.Journal{OperationID: oldID, Kind: "apply", Stage: "prepare", Status: "running", RecoveryID: recoveryID}); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(store.Root(), "state", "operations", oldID+".json")
			before, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			restarts, err := restartflow.New(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			request, err := restarts.StageApply(verifiedRestartTheme(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restarts.Approve(request.RequestID); err != nil {
				t.Fatal(err)
			}
			code, _, _ := run(t, []string{"__restart-worker", request.RequestID}, Runtime{
				GOOS: platform, GOARCH: "amd64", Root: store.Root(), RestartDelay: -1,
				Adapter: &unavailableRecoveryAdapter{&restartApplyAdapter{}},
			})
			current, found, err := restarts.Current()
			if err != nil || !found || code != exitApply || current.Status != restartflow.StatusFailed || current.ErrorCode != "CS-FLOW-RESTART-006" || current.OperationID != "" {
				t.Fatalf("unexpected result: %d %+v %v", code, current, err)
			}
			after, err := os.ReadFile(oldPath)
			if err != nil || string(before) != string(after) {
				t.Fatal("diagnostic changed old journal")
			}
			raw, err := os.ReadFile(filepath.Join(store.Root(), "restart", "diagnostic.json"))
			if platform == "darwin" {
				if !os.IsNotExist(err) {
					t.Fatal("macOS must not enable Windows diagnostic")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				RequestID string `json:"requestId"`
				Events    []struct {
					Stage  string `json:"stage"`
					Status string `json:"status"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if report.RequestID != request.RequestID {
				t.Fatal("wrong request")
			}
			failedRecovery := false
			for _, event := range report.Events {
				if event.Stage == "interrupted_recovery" && event.Status == "failed" {
					failedRecovery = true
				}
			}
			if !failedRecovery {
				t.Fatalf("missing recovery failure: %s", raw)
			}
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "token") {
				t.Fatal("raw error leaked")
			}
		})
	}
}
