package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/appearance"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

// This exercises the real restore coordinator and real disk transaction with
// only the verified UI boundary replaced. It never launches or controls Codex.
type restoreUITestDriver struct {
	config           string
	state            appearanceUIState
	host             string
	osMode           string
	switches         int
	readErr          error
	switchErr        error
	wrongMode        bool
	reload           bool
	changeCodeTheme  bool
	cleanupCalls     int
	cleanupErr       error
	rendererOfficial bool
}

func (ui *restoreUITestDriver) restoreOfficialRenderer(ctx context.Context, _ *liveSession) error {
	ui.cleanupCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if ui.cleanupErr != nil {
		return ui.cleanupErr
	}
	ui.rendererOfficial = true
	return nil
}

func (ui *restoreUITestDriver) readVerifiedAppearance(ctx context.Context, _ *liveSession) (appearanceUIState, string, error) {
	if err := ctx.Err(); err != nil {
		return appearanceUIState{}, "", err
	}
	return ui.state, ui.host, ui.readErr
}

func (ui *restoreUITestDriver) switchAppearanceWithTransaction(_ context.Context, _ *liveSession, mode string, transaction appearanceModeTransaction) error {
	ui.switches++
	if ui.switchErr != nil {
		return ui.switchErr
	}
	if err := transaction.VerifyMode(ui.host); err != nil {
		return err
	}
	if ui.wrongMode {
		return nil
	}
	raw, err := os.ReadFile(ui.config)
	if err != nil {
		return err
	}
	updated := strings.Replace(string(raw), `appearanceTheme = "`+ui.host+`"`, `appearanceTheme = "`+mode+`"`, 1)
	if ui.changeCodeTheme && ui.switches == 1 {
		updated += "appearanceDarkCodeThemeId = \"unexpected\"\n"
	}
	if err := os.WriteFile(ui.config, []byte(updated), 0o600); err != nil {
		return err
	}
	ui.host = mode
	effective := mode
	if mode == "system" {
		effective = ui.osMode
	}
	ui.state.SystemVariant = effective
	ui.state.DarkMedia = effective == "dark"
	ui.state.ColorScheme = effective
	if ui.reload {
		ui.state.TimeOrigin++
	}
	return nil
}

func restoreCoordinatorFixture(t *testing.T, mode string) (*appearance.Manager, *restoreUITestDriver, string) {
	t.Helper()
	root := t.TempDir()
	config, recovery := filepath.Join(root, "config.toml"), filepath.Join(root, "appearance.json")
	original := "[desktop]\nappearanceTheme = \"" + mode + "\" # original\nfont = \"unchanged\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := appearance.New(config, recovery, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pin("dark"); err != nil {
		t.Fatal(err)
	}
	ui := &restoreUITestDriver{
		config: config, host: "dark", osMode: "light",
		state: appearanceUIState{TrustedOrigin: true, BridgeAvailable: true,
			Route: "/thread/fixture", TimeOrigin: 123, SystemVariant: "dark", ColorScheme: "dark", DarkMedia: true},
	}
	return manager, ui, recovery
}

func TestInAppAppearanceRoutingPreservesMacRestore(t *testing.T) {
	for _, tc := range []struct {
		platform, mode string
		restore, want  bool
	}{
		{"windows", "dark", false, true},
		{"windows", "light", false, true},
		{"windows", "", true, true},
		{"darwin", "dark", false, true},
		{"darwin", "light", false, true},
		{"darwin", "", true, false},
		{"linux", "dark", false, false},
		{"linux", "", true, false},
		{"windows", "", false, false},
	} {
		if got := canPrepareAppearanceInPlace(tc.platform, tc.mode, tc.restore); got != tc.want {
			t.Fatalf("route %+v = %v", tc, got)
		}
	}
}

func TestAppearanceRestoreCoordinatorRetainsBackupThroughRendererVerification(t *testing.T) {
	for _, mode := range []string{"system", "light", "dark"} {
		t.Run(mode, func(t *testing.T) {
			manager, ui, recovery := restoreCoordinatorFixture(t, mode)
			live := &liveSession{}
			pending, err := prepareAppearanceRestore(context.Background(), manager, live, ui)
			if err != nil || pending == nil {
				t.Fatalf("prepare = %v", err)
			}
			defer pending.transaction.Close()
			wantSwitches := 1
			if mode == "dark" {
				wantSwitches = 0
			}
			if ui.switches != wantSwitches || ui.host != mode || ui.state.TimeOrigin != 123 {
				t.Fatalf("wrong UI transition: %+v", ui)
			}
			if _, err := os.Stat(recovery); err != nil {
				t.Fatal("prepare consumed backup before official renderer verification")
			}
			if err := pending.finish(context.Background(), live, ui); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("verified restore retained stale backup")
			}
			got, _ := os.ReadFile(ui.config)
			want := "[desktop]\nappearanceTheme = \"" + mode + "\" # original\nfont = \"unchanged\"\n"
			if string(got) != want {
				t.Fatalf("original settings not restored: %q", got)
			}
		})
	}
}

func TestAppearanceRestoreCoordinatorFailsClosedAndKeepsRecovery(t *testing.T) {
	for _, failure := range []string{"identity", "unavailable", "unsafe-switch", "wrong-mode", "renderer-reload", "final-mode", "final-route", "final-palette", "final-identity"} {
		t.Run(failure, func(t *testing.T) {
			manager, ui, recovery := restoreCoordinatorFixture(t, "system")
			switch failure {
			case "identity":
				ui.readErr = codex.ErrListenerUntrusted
			case "unavailable":
				ui.switchErr = errAppearanceUIUnavailable
			case "unsafe-switch":
				ui.switchErr = errors.Join(engine.ErrStateUnsafe, errAppearanceUIUnavailable)
			case "wrong-mode":
				ui.wrongMode = true
			case "renderer-reload":
				ui.reload = true
			}
			pending, err := prepareAppearanceRestore(context.Background(), manager, &liveSession{}, ui)
			if strings.HasPrefix(failure, "final-") {
				if err != nil || pending == nil {
					t.Fatal("prepare failed:", err)
				}
				switch failure {
				case "final-mode":
					ui.host = "light" // Effective color alone must not stand in for system.
				case "final-route":
					ui.state.Route = "/different"
				case "final-palette":
					ui.state.DarkMedia = true
				case "final-identity":
					ui.readErr = codex.ErrListenerUntrusted
				}
				err = pending.finish(context.Background(), &liveSession{}, ui)
				pending.transaction.Close()
			}
			if err == nil {
				t.Fatal("accepted failed Restore")
			}
			if !strings.HasPrefix(failure, "final-") && appearanceRestartFallbackAllowed(err) != (failure == "unavailable") {
				t.Fatalf("wrong restart fallback classification: %v", err)
			}
			if _, err := os.Stat(recovery); err != nil {
				t.Fatal("failure lost offline recovery")
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal("offline fallback failed or appearance lock leaked:", err)
			}
		})
	}
}

func TestAppearanceRestoreCoordinatorNoBackupDoesNotCreateOne(t *testing.T) {
	manager, ui, recovery := restoreCoordinatorFixture(t, "system")
	if _, err := manager.Restore(); err != nil {
		t.Fatal(err)
	}
	ui.host = "system"
	pending, err := prepareAppearanceRestore(context.Background(), manager, &liveSession{}, ui)
	if pending != nil || err != nil || ui.switches != 0 {
		t.Fatalf("no-backup restore = %v, %v", pending, err)
	}
	if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created a restore point during Restore")
	}
}

func TestAppearanceRestorePrepareFailureRollsBackModeWithoutConsumingBackup(t *testing.T) {
	manager, ui, recovery := restoreCoordinatorFixture(t, "system")
	ui.changeCodeTheme = true
	pending, err := prepareAppearanceRestore(context.Background(), manager, &liveSession{}, ui)
	if pending != nil || !errors.Is(err, engine.ErrStateUnsafe) || appearanceRestartFallbackAllowed(err) {
		t.Fatalf("unexpected failure classification: %v", err)
	}
	if ui.host != "dark" || ui.switches != 2 || !ui.state.DarkMedia {
		t.Fatalf("failed restore did not roll native mode back: %+v", ui)
	}
	if _, err := os.Stat(recovery); err != nil {
		t.Fatal("failed restore consumed recovery")
	}
	if _, err := manager.Restore(); err != nil {
		t.Fatal("offline recovery unavailable:", err)
	}
}
