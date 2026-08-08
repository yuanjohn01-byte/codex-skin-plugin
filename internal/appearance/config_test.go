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
