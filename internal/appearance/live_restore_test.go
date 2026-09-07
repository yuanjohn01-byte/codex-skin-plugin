package appearance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveRestoreFixture(t *testing.T, original string) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	config, recovery := filepath.Join(root, "config.toml"), filepath.Join(root, "appearance.json")
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, recovery, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pin("dark"); err != nil {
		t.Fatal(err)
	}
	return manager, config, recovery
}

func writeLiveRestoreMode(t *testing.T, config, mode string) {
	t.Helper()
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := replaceSetting(string(raw), "appearanceTheme", pointer(`appearanceTheme = "`+mode+`"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveRestoreStagesExactSettingsAndKeepsRecoveryUntilCommit(t *testing.T) {
	for _, tc := range []struct{ name, original, mode string }{
		{"system", "[desktop]\nappearanceTheme = \"system\" # original\nappearanceDarkCodeThemeId = \"codex\"\nfont = \"keep\"\n", "system"},
		{"light", "[desktop]\nappearanceTheme = \"light\"\nfont = \"keep\"\n", "light"},
		{"same-dark", "[desktop]\nappearanceTheme = \"dark\" # original\n", "dark"},
		{"implicit-system", "[desktop]\nfont = \"keep\"\n", "system"},
		{"absent-desktop", "model = \"fixture\"\n", "system"},
		{"CRLF", "[desktop]\r\nappearanceTheme = \"system\" # original\r\nfont = \"keep\"\r\n", "system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, config, recovery := liveRestoreFixture(t, tc.original)
			before, _ := os.ReadFile(recovery)
			transaction, err := manager.BeginLiveRestore("dark")
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Close()
			if transaction.Mode() != tc.mode {
				t.Fatalf("mode = %q", transaction.Mode())
			}
			if _, err := manager.Pin("light"); err == nil {
				t.Fatal("concurrent Helper acquired appearance lock")
			}
			if err := transaction.Commit(); err == nil {
				t.Fatal("unstaged restore consumed recovery")
			}
			writeLiveRestoreMode(t, config, tc.mode)
			if err := transaction.Stage(); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(config)
			if string(got) != tc.original {
				t.Fatalf("exact restore mismatch: %q", got)
			}
			after, err := os.ReadFile(recovery)
			if err != nil || string(after) != string(before) {
				t.Fatal("Stage lost or changed recovery")
			}
			if err := transaction.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery not consumed: %v", err)
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal("idempotent offline restore:", err)
			}
		})
	}
}

func TestLiveRestoreFailureRetainsOfflineRecovery(t *testing.T) {
	original := "[desktop]\nappearanceTheme = \"system\" # original\nfont = \"keep\"\n"
	for _, stage := range []string{"before-ui", "wrong-mode", "after-stage", "changed-mode", "changed-backup"} {
		t.Run(stage, func(t *testing.T) {
			manager, config, recovery := liveRestoreFixture(t, original)
			backupBytes, _ := os.ReadFile(recovery)
			transaction, err := manager.BeginLiveRestore("dark")
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "wrong-mode":
				if err := transaction.Stage(); err == nil {
					t.Fatal("Stage accepted live/disk dark instead of system")
				}
			case "after-stage", "changed-mode":
				writeLiveRestoreMode(t, config, "system")
				if err := transaction.Stage(); err != nil {
					t.Fatal(err)
				}
				if stage == "changed-mode" {
					writeLiveRestoreMode(t, config, "light")
					if err := transaction.Commit(); err == nil {
						t.Fatal("Commit accepted a changed mode")
					}
				}
			case "changed-backup":
				stored := transaction.stored
				stored.Values = map[string]*string{"appearanceTheme": pointer(`appearanceTheme = "light"`), "appearanceDarkCodeThemeId": nil}
				if err := manager.writeBackup(stored); err != nil {
					t.Fatal(err)
				}
				writeLiveRestoreMode(t, config, "system")
				if err := transaction.Stage(); err == nil {
					t.Fatal("Stage accepted a replaced recovery point")
				}
				if err := os.WriteFile(recovery, backupBytes, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			transaction.Close()
			if _, err := os.Stat(recovery); err != nil {
				t.Fatal("failure consumed recovery:", err)
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(config)
			if string(got) != original {
				t.Fatalf("offline restore = %q", got)
			}
		})
	}
}

func TestLiveRestoreRejectsUnsupportedOrChangingCodeTheme(t *testing.T) {
	for _, afterBegin := range []bool{false, true} {
		manager, config, recovery := liveRestoreFixture(t, "[desktop]\nappearanceTheme = \"system\"\nappearanceDarkCodeThemeId = \"original\"\n")
		var transaction *LiveRestore
		if afterBegin {
			var err error
			transaction, err = manager.BeginLiveRestore("dark")
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Close()
		}
		raw, _ := os.ReadFile(config)
		if err := os.WriteFile(config, []byte(strings.Replace(string(raw), `"original"`, `"changed"`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if afterBegin {
			writeLiveRestoreMode(t, config, "system")
			if !errors.Is(transaction.Stage(), ErrLiveRestoreUnavailable) {
				t.Fatal("Stage ignored concurrent code-theme change")
			}
		} else if _, err := manager.BeginLiveRestore("dark"); !errors.Is(err, ErrLiveRestoreUnavailable) {
			t.Fatalf("unsupported legacy code theme = %v", err)
		}
		if _, err := os.Stat(recovery); err != nil {
			t.Fatal("unsupported restore lost recovery")
		}
	}
}

func TestLiveRestorePreservesLaterUnmanagedSettingsAndRejectsHostMismatch(t *testing.T) {
	manager, config, recovery := liveRestoreFixture(t, "[desktop]\nappearanceTheme = \"system\"\nfont = \"original\"\n")
	if _, err := manager.BeginLiveRestore("light"); err == nil {
		t.Fatal("accepted host/config disagreement")
	}
	transaction, err := manager.BeginLiveRestore("dark")
	if err != nil {
		t.Fatal("failed Begin leaked lock:", err)
	}
	defer transaction.Close()
	writeLiveRestoreMode(t, config, "system")
	raw, _ := os.ReadFile(config)
	if err := os.WriteFile(config, []byte(strings.Replace(string(raw), `font = "original"`, `font = "later choice"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Stage(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(config)
	if !strings.Contains(string(got), `font = "later choice"`) || !strings.Contains(string(got), `appearanceTheme = "system"`) {
		t.Fatalf("overwrote unrelated settings: %q", got)
	}
	if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery was not committed")
	}
}
