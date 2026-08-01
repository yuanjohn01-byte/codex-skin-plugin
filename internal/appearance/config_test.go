package appearance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinAndRestorePreserveExactDesktopSettings(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backup := filepath.Join(root, "state", "appearance.json")
	original := "model = \"codex\"\n\n[desktop]\nappearanceTheme = \"system\" # keep\nappearanceDarkCodeThemeId = \"night\"\n\n[features]\nitems = [\n  \"one\",\n  \"two\",\n]\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, backup, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := manager.Pin("dark")
	if err != nil || !changed {
		t.Fatalf("Pin() = %t, %v", changed, err)
	}
	pinned, _ := os.ReadFile(config)
	if !strings.Contains(string(pinned), `appearanceTheme = "dark"`) ||
		!strings.Contains(string(pinned), `appearanceDarkCodeThemeId = "night"`) {
		t.Fatalf("pinned config = %s", pinned)
	}
	changed, err = manager.Restore()
	if err != nil || !changed {
		t.Fatalf("Restore() = %t, %v", changed, err)
	}
	restored, _ := os.ReadFile(config)
	if string(restored) != original {
		t.Fatalf("restore changed config\nwant: %q\n got: %q", original, restored)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}

func TestPinRejectsDuplicateDesktopSettingWithoutWriting(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backup := filepath.Join(root, "appearance.json")
	original := "[desktop]\nappearanceTheme = \"light\"\nappearanceTheme = \"dark\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, _ := New(config, backup, "darwin")
	if _, err := manager.Pin("dark"); err == nil {
		t.Fatal("Pin accepted duplicate appearanceTheme")
	}
	current, _ := os.ReadFile(config)
	if string(current) != original {
		t.Fatal("invalid config was modified")
	}
}

func TestPinRejectsSymlinkedConfig(t *testing.T) {
	root := t.TempDir()
	realConfig := filepath.Join(root, "real.toml")
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(realConfig, []byte("[desktop]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realConfig, config); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager, _ := New(config, filepath.Join(root, "appearance.json"), "darwin")
	if _, err := manager.Pin("dark"); err == nil {
		t.Fatal("Pin accepted symlinked config")
	}
}

func TestCrossModePinKeepsSingleOriginalBackup(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backup := filepath.Join(root, "state", "appearance.json")
	original := "[desktop]\nappearanceTheme = \"system\"\nappearanceDarkCodeThemeId = \"night\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, _ := New(config, backup, "darwin")
	if changed, err := manager.Pin("dark"); err != nil || !changed {
		t.Fatalf("Pin(dark) = %t, %v", changed, err)
	}
	firstBackup, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.Pin("light"); err != nil || !changed {
		t.Fatalf("Pin(light) = %t, %v", changed, err)
	}
	secondBackup, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBackup) != string(secondBackup) {
		t.Fatal("cross-mode pin replaced the original appearance backup")
	}
	pinned, _ := os.ReadFile(config)
	if !strings.Contains(string(pinned), `appearanceTheme = "light"`) {
		t.Fatalf("cross-mode config = %s", pinned)
	}
	if _, err := manager.Restore(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(config)
	if string(restored) != original {
		t.Fatalf("restore changed original\nwant: %q\n got: %q", original, restored)
	}
}

func TestMatchingModeStillCreatesRestorableFirstApplyBackup(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	backup := filepath.Join(root, "state", "appearance.json")
	original := "[desktop]\nappearanceTheme = \"dark\" # user choice\nappearanceDarkCodeThemeId = \"night\"\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, _ := New(config, backup, "darwin")
	if needed, err := manager.NeedsPin("dark"); err != nil || !needed {
		t.Fatalf("NeedsPin(dark) = %t, %v", needed, err)
	}
	if changed, err := manager.Pin("dark"); err != nil || !changed {
		t.Fatalf("Pin(dark) = %t, %v", changed, err)
	}
	pinned, _ := os.ReadFile(config)
	if string(pinned) != original {
		t.Fatalf("matching pin rewrote config\nwant: %q\n got: %q", original, pinned)
	}
	if needed, err := manager.NeedsPin("dark"); err != nil || needed {
		t.Fatalf("NeedsPin(dark) after backup = %t, %v", needed, err)
	}
	if changed, err := manager.Restore(); err != nil || changed {
		t.Fatalf("Restore() = %t, %v", changed, err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}
