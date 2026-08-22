package appearance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreConsumesLegacyBackupAndPreservesExactDesktopSettings(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backupPath := filepath.Join(root, "state", "appearance.json")
	original := "model = \"codex\"\n\n[desktop]\nappearanceTheme = \"system\" # keep\nappearanceDarkCodeThemeId = \"night\"\n\n[features]\nitems = [\n  \"one\",\n  \"two\",\n]\n"
	legacyCurrent := strings.Replace(original, `appearanceTheme = "system" # keep`, `appearanceTheme = "dark"`, 1)
	if err := os.WriteFile(config, []byte(legacyCurrent), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backupPath, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := backup{
		SchemaVersion: backupSchemaVersion,
		Platform:      "darwin",
		ConfigPath:    config,
		Values: map[string]*string{
			"appearanceTheme":           pointer(`appearanceTheme = "system" # keep`),
			"appearanceDarkCodeThemeId": pointer(`appearanceDarkCodeThemeId = "night"`),
		},
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if needed, err := manager.NeedsRestore(); err != nil || !needed {
		t.Fatalf("NeedsRestore() = %t, %v", needed, err)
	}
	changed, err := manager.Restore()
	if err != nil || !changed {
		t.Fatalf("Restore() = %t, %v", changed, err)
	}
	restored, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("restore changed config\nwant: %q\n got: %q", original, restored)
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}

func TestPinSwitchesOnlyNativeAppearanceAndRestoreReturnsExactOriginal(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backupPath := filepath.Join(root, "state", "appearance.json")
	original := "model = \"codex\"\n\n[desktop]\nappearanceTheme = \"system\" # preserve this exact choice\nappearanceDarkCodeThemeId = \"night\"\n\n[features]\nenabled = true\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backupPath, "darwin")
	if err != nil {
		t.Fatal(err)
	}

	if needed, err := manager.NeedsPin("dark"); err != nil || !needed {
		t.Fatalf("first NeedsPin(dark) = %t, %v", needed, err)
	}
	changed, err := manager.Pin("dark")
	if err != nil || !changed {
		t.Fatalf("Pin(dark) = %t, %v", changed, err)
	}
	dark, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(dark), strings.Replace(original,
		`appearanceTheme = "system" # preserve this exact choice`, `appearanceTheme = "dark"`, 1); got != want {
		t.Fatalf("dark pin changed unrelated config\nwant: %q\n got: %q", want, got)
	}
	if needed, err := manager.NeedsPin("dark"); err != nil || needed {
		t.Fatalf("same-mode NeedsPin(dark) = %t, %v", needed, err)
	}
	if needed, err := manager.NeedsRestore(); err != nil || !needed {
		t.Fatalf("NeedsRestore() after dark pin = %t, %v", needed, err)
	}
	if needed, err := manager.NeedsPin("light"); err != nil || !needed {
		t.Fatalf("cross-mode NeedsPin(light) = %t, %v", needed, err)
	}
	changed, err = manager.Pin("light")
	if err != nil || !changed {
		t.Fatalf("Pin(light) = %t, %v", changed, err)
	}
	light, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(light), strings.Replace(original,
		`appearanceTheme = "system" # preserve this exact choice`, `appearanceTheme = "light"`, 1); got != want {
		t.Fatalf("light pin changed unrelated config\nwant: %q\n got: %q", want, got)
	}
	if changed, err := manager.Restore(); err != nil || !changed {
		t.Fatalf("Restore() = %t, %v", changed, err)
	}
	restored, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(restored); got != original {
		t.Fatalf("restore did not preserve exact original\nwant: %q\n got: %q", original, got)
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}

func TestPinCreatesRestorePointWhenRequestedModeIsAlreadyActive(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backupPath := filepath.Join(root, "state", "appearance.json")
	original := "[desktop]\nappearanceTheme = \"dark\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backupPath, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := manager.Pin("dark")
	if err != nil || !changed {
		t.Fatalf("Pin(dark) = %t, %v", changed, err)
	}
	current, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(current); got != original {
		t.Fatalf("already-dark pin rewrote config: %q", got)
	}
	if needed, err := manager.NeedsPin("dark"); err != nil || needed {
		t.Fatalf("NeedsPin(dark) with already-dark native appearance = %t, %v", needed, err)
	}
	if needed, err := manager.NeedsRestore(); err != nil || needed {
		t.Fatalf("NeedsRestore() with matching original native appearance = %t, %v", needed, err)
	}
	if _, err := os.Lstat(backupPath); err != nil {
		t.Fatalf("backup missing after same-mode pin: %v", err)
	}
}

func TestPinRejectsMissingDesktopTableWithoutCreatingBackup(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backupPath := filepath.Join(root, "state", "appearance.json")
	original := "model = \"codex\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backupPath, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pin("dark"); err == nil {
		t.Fatal("Pin(dark) unexpectedly created a desktop table")
	}
	current, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != original {
		t.Fatalf("Pin changed config without a desktop table: %q", current)
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("Pin created a backup without a desktop table: %v", err)
	}
}

func TestRestoreConsumesAbsentDesktopBackupWithoutManufacturingTable(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backupPath := filepath.Join(root, "state", "appearance.json")
	original := "model = \"codex\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backupPath, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(backup{
		SchemaVersion: backupSchemaVersion,
		Platform:      "darwin",
		ConfigPath:    config,
		Values: map[string]*string{
			"appearanceTheme":           nil,
			"appearanceDarkCodeThemeId": nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := manager.Restore()
	if err != nil || changed {
		t.Fatalf("Restore() = %t, %v", changed, err)
	}
	current, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != original {
		t.Fatalf("Restore manufactured desktop table: %q", current)
	}
	if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("Restore left absent-table backup behind: %v", err)
	}
}
