package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restarttrace"
)

func TestOrdinaryRecoveryTraceDoesNotMaskOriginalFailure(t *testing.T) {
	for _, waitFails := range []bool{false, true} {
		var report restarttrace.Report
		ctx := restarttrace.WithRecorder(context.Background(), "rst_fixture", "test", "test", func(r restarttrace.Report) error { report = r; return nil })
		original := errors.New("synthetic controlled launch failed")
		restarttrace.Start(ctx, restarttrace.LaunchControlled)(original)
		err := reopenOrdinaryIfMissingWith(context.WithoutCancel(ctx), original, codexRecoveryOperations{
			discoverStableInstallation: func(context.Context) (codex.Installation, error) { return codex.Installation{}, nil },
			discoverCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
				return codex.CurrentInstance{}, codex.ErrCurrentMissing
			},
			launchOrdinary: func(context.Context, codex.Installation) error { return nil },
			waitForCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
				if waitFails {
					return codex.CurrentInstance{}, context.DeadlineExceeded
				}
				return codex.CurrentInstance{Process: codex.ProcessIdentity{ProcessID: 42}}, nil
			},
		})
		if !errors.Is(err, original) || report.FirstFailureStage != "launch_controlled" {
			t.Fatal("original failure lost")
		}
		if len(report.Events) != 3 || report.Events[1].Stage != "failure_recovery" || report.Events[2].Stage != "wait_ordinary" {
			t.Fatalf("missing recovery stages: %+v", report)
		}
		want := "passed"
		if waitFails {
			want = "failed"
		}
		if report.Events[1].Status != want || report.Events[2].Status != want {
			t.Fatalf("cleanup confused with original failure: %+v", report)
		}
	}
}
