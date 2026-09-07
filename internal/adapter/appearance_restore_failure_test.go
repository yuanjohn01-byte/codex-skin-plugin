package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

// Exercise the real engine, pending appearance transaction and adapter cleanup.
// Only the verified UI and capability boundaries are simulated; no Codex runs.
type restoreFailureAdapter struct {
	*Live
	ui        *restoreUITestDriver
	removed   bool
	failure   string
	stateRoot string
	cancel    context.CancelFunc
}

func (adapter *restoreFailureAdapter) OpenVerifiedOfficialSession(ctx context.Context) (engine.Session, error) {
	live := &liveSession{}
	pending, err := prepareAppearanceRestore(ctx, adapter.appearance, live, adapter.ui)
	if err != nil {
		return engine.Session{}, err
	}
	live.pendingRestore = pending
	adapter.sessions["fixture"] = live
	return engine.Session{OpaqueID: "fixture"}, nil
}

func (adapter *restoreFailureAdapter) WaitForCapabilities(ctx context.Context, _ engine.Session) (engine.RegionReport, error) {
	switch adapter.failure {
	case "controller":
		if err := os.WriteFile(filepath.Join(adapter.stateRoot, "state", "renderer-controller.json"), []byte("invalid fixture controller"), 0o600); err != nil {
			panic(err)
		}
	case "journal":
		// Corrupt only this test's existing synthetic journal so the next
		// engine write fails without a platform-dependent permissions trick.
		paths, err := filepath.Glob(filepath.Join(adapter.stateRoot, "state", "operations", "*.json"))
		if err != nil || len(paths) != 1 {
			panic("synthetic journal fixture missing")
		}
		if err := os.WriteFile(paths[0], []byte("invalid fixture journal"), 0o600); err != nil {
			panic(err)
		}
	case "identity":
		adapter.ui.readErr = codex.ErrListenerUntrusted
	case "reload":
		adapter.ui.state.TimeOrigin++
	case "route":
		adapter.ui.state.Route = "/another-fixture"
	case "rollback":
		adapter.ui.switchErr = errors.New("synthetic rollback control failure")
	case "cancel":
		adapter.cancel()
		return engine.RegionReport{}, ctx.Err()
	}
	switch adapter.failure {
	case "probe", "identity", "reload", "route", "rollback", "":
		return engine.RegionReport{}, errors.New("synthetic capability failure with trusted UI")
	}
	return engine.RegionReport{StyleMarkerCount: 1}, nil
}

func (adapter *restoreFailureAdapter) RestoreOfficial(ctx context.Context, session engine.Session) error {
	live := adapter.sessions[session.OpaqueID]
	if adapter.failure == "controller" {
		// Real adapter removal exits on the synthetic unreadable controller
		// record before any renderer/CDP mutation. Native rollback is safe.
		return adapter.Live.removeThemeRenderer(ctx, live)
	}
	live.pendingRestore.removalStarted = true
	adapter.removed = true
	if adapter.failure == "removal" || adapter.failure == "cleanup" || adapter.failure == "removal-identity" {
		if adapter.failure == "cleanup" {
			adapter.ui.cleanupErr = errors.New("synthetic cleanup failure")
		}
		if adapter.failure == "removal-identity" {
			adapter.ui.readErr = codex.ErrListenerUntrusted
		}
		return errors.New("synthetic interrupted renderer removal")
	}
	adapter.ui.rendererOfficial = true
	return nil
}

func (adapter *restoreFailureAdapter) VerifyOfficial(ctx context.Context, session engine.Session) error {
	if adapter.failure == "verify" {
		return engine.ErrVerifyFailed
	}
	live := adapter.sessions[session.OpaqueID]
	if err := live.pendingRestore.finish(ctx, live, adapter.ui); err != nil {
		return err
	}
	live.pendingRestore = nil
	return nil
}

func TestAppearanceRestoreEngineProbeFailureRollsBackNativeMode(t *testing.T) {
	manager, ui, recovery := restoreCoordinatorFixture(t, "light")
	adapter := &restoreFailureAdapter{
		Live: &Live{appearance: manager, sessions: map[string]*liveSession{}}, ui: ui,
	}
	store, err := engine.OpenStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := engine.New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RestoreOfficial(context.Background()); err == nil {
		t.Fatal("expected Restore failure")
	}
	if adapter.removed {
		t.Fatal("failed capability check must not remove the old skin")
	}
	if ui.host != "dark" || ui.switches != 2 {
		t.Fatalf("old dark skin left with host=%s switches=%d; native rollback missing", ui.host, ui.switches)
	}
	if _, err := os.Stat(recovery); err != nil {
		t.Fatal("failure lost the original recovery point:", err)
	}
	if _, err := manager.Restore(); err != nil {
		t.Fatal("cleanup leaked the appearance lock:", err)
	}
}

func TestAppearanceRestoreEngineFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		failure                               string
		wantMode                              string
		wantSwitches, wantCleanup             int
		wantRemoved, wantOfficial, wantUnsafe bool
	}{
		{"probe", "dark", 2, 0, false, false, false},
		{"journal", "dark", 2, 0, false, false, true},
		// The existing engine removal-error contract wraps ErrRestoreFailed;
		// a successful native rollback must not add a new ErrStateUnsafe.
		{"controller", "dark", 2, 0, false, false, false},
		{"cancel", "dark", 2, 0, false, false, false},
		{"identity", "light", 1, 0, false, false, true},
		{"reload", "light", 1, 0, false, false, true},
		{"route", "light", 1, 0, false, false, true},
		{"rollback", "light", 2, 0, false, false, true},
		{"removal", "light", 1, 1, true, true, false},
		{"verify", "light", 1, 1, true, true, false},
		{"cleanup", "light", 1, 1, true, false, true},
		{"removal-identity", "light", 1, 0, true, false, true},
	} {
		t.Run(tc.failure, func(t *testing.T) {
			manager, ui, recovery := restoreCoordinatorFixture(t, "light")
			before, err := os.ReadFile(recovery)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := t.TempDir()
			adapter := &restoreFailureAdapter{
				Live: &Live{root: root, appearance: manager, sessions: map[string]*liveSession{}},
				ui:   ui, failure: tc.failure, stateRoot: root, cancel: cancel,
			}
			store, err := engine.OpenStore(root, "")
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := engine.New(store, adapter)
			if err != nil {
				t.Fatal(err)
			}
			_, err = runtime.RestoreOfficial(ctx)
			if err == nil || errors.Is(err, engine.ErrStateUnsafe) != tc.wantUnsafe {
				t.Fatalf("failure classification = %v", err)
			}
			if ui.host != tc.wantMode || ui.switches != tc.wantSwitches || ui.cleanupCalls != tc.wantCleanup ||
				adapter.removed != tc.wantRemoved || ui.rendererOfficial != tc.wantOfficial {
				t.Fatalf("wrong cleanup: host=%s switches=%d cleanup=%d removal=%v official=%v", ui.host, ui.switches, ui.cleanupCalls, adapter.removed, ui.rendererOfficial)
			}
			after, err := os.ReadFile(recovery)
			if err != nil || string(before) != string(after) {
				t.Fatal("cleanup consumed/changed backup", err)
			}
			if len(adapter.sessions) != 0 {
				t.Fatal("session was not closed after cleanup")
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal("offline recovery unavailable", err)
			}
		})
	}
}

func TestAppearanceRestoreEngineSuccessDoesNotAbort(t *testing.T) {
	for _, mode := range []string{"system", "light", "dark"} {
		t.Run(mode, func(t *testing.T) {
			manager, ui, recovery := restoreCoordinatorFixture(t, mode)
			adapter := &restoreFailureAdapter{
				Live: &Live{appearance: manager, sessions: map[string]*liveSession{}}, ui: ui, failure: "none",
			}
			store, err := engine.OpenStore(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := engine.New(store, adapter)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.RestoreOfficial(context.Background()); err != nil {
				t.Fatal(err)
			}
			if ui.host != mode || ui.cleanupCalls != 0 || !ui.rendererOfficial {
				t.Fatalf("unexpected successful cleanup %+v", ui)
			}
			if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("success retained recovery", err)
			}
		})
	}
}

func TestAppearanceRestoreAbortWithoutPendingIsNoOp(t *testing.T) {
	adapter := &Live{sessions: map[string]*liveSession{"fixture": {}}}
	for _, id := range []string{"fixture", "missing"} {
		if err := adapter.AbortOfficialRestore(context.Background(), engine.Session{OpaqueID: id}); err != nil {
			t.Fatal(err)
		}
	}
}
