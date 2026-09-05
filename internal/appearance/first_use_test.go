package appearance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstUsePreservesLaterSettingsAndComments(t *testing.T) {
	const original = `model = "fixture"`
	for _, fixture := range []struct {
		name string
		edit func(string) string
		want string
	}{
		{"desktop_setting", func(s string) string { return s + "newSetting = true\n" }, original + "\n[desktop]\nnewSetting = true\n"},
		{"body_comment", func(s string) string { return s + "# user's later note\n" }, original + "\n[desktop]\n# user's later note\n"},
		{"header_comment", func(s string) string { return strings.Replace(s, "[desktop]", "[desktop] # user's note", 1) }, original + "\n[desktop] # user's note\n"},
		{"new_table", func(s string) string { return s + "[other]\nvalue = true\n" }, original + "\n[other]\nvalue = true\n"},
		{"desktop_child", func(s string) string { return s + "[desktop.editor]\nvalue = true\n" }, original + "\n[desktop.editor]\nvalue = true\n"},
		{"prefix_edit", func(s string) string { return strings.Replace(s, `"fixture"`, `"new choice"`, 1) }, "model = \"new choice\"\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			manager, config, _ := firstUseManager(t, "windows", original)
			if _, err := manager.Pin("dark"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, []byte(fixture.edit(readFirstUseFile(t, config))), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal(err)
			}
			if got := readFirstUseFile(t, config); got != fixture.want {
				t.Fatalf("later user data changed\nwant %q\n got %q", fixture.want, got)
			}
		})
	}
}

func TestFirstUseInterruptedBeforeConfigWrite(t *testing.T) {
	const original = "model = \"fixture\"\n"
	manager, config, recovery := firstUseManager(t, "windows", original)
	if created, err := manager.ensureBackup(original); err != nil || !created {
		t.Fatalf("ensureBackup = %t, %v", created, err)
	}
	manager, err := New(config, recovery, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if needed, err := manager.NeedsRestore(); err != nil || needed {
		t.Fatalf("unchanged configuration requested reload: %t, %v", needed, err)
	}
	if changed, err := manager.Restore(); err != nil || changed {
		t.Fatalf("interrupted Restore = %t, %v", changed, err)
	}
	if got := readFirstUseFile(t, config); got != original {
		t.Fatal("interrupted Restore changed original file")
	}
	if _, err := os.Stat(recovery); !os.IsNotExist(err) {
		t.Fatalf("interrupted recovery not consumed: %v", err)
	}
}

func TestFirstUseFailedRestoreRetainsRecoveryForRetry(t *testing.T) {
	const original = `model = "fixture"`
	manager, config, recovery := firstUseManager(t, "windows", original)
	if _, err := manager.Pin("dark"); err != nil {
		t.Fatal(err)
	}
	backupBefore := readFirstUseFile(t, recovery)
	// Make the temporary fixture config unavailable without touching any user
	// profile or depending on privileged symlink creation on Windows.
	saved := config + ".saved"
	if err := os.Rename(config, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(); err == nil {
		t.Fatal("Restore accepted a non-file config")
	}
	if got := readFirstUseFile(t, recovery); got != backupBefore {
		t.Fatal("failed Restore lost its recovery point")
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := readFirstUseFile(t, config); got != original {
		t.Fatalf("retry did not restore original: %q", got)
	}
}

func TestFirstUseBackupFailureDoesNotChangeConfig(t *testing.T) {
	const original = `model = "fixture"`
	manager, config, recovery := firstUseManager(t, "windows", original)
	if err := os.WriteFile(filepath.Dir(recovery), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pin("dark"); err == nil {
		t.Fatal("Pin succeeded without a recovery point")
	}
	if got := readFirstUseFile(t, config); got != original {
		t.Fatal("failed backup changed original configuration")
	}
}

func TestFirstUseLiveSwitchRestoresAbsentDesktop(t *testing.T) {
	const original = `model = "fixture"`
	manager, config, recovery := firstUseManager(t, "darwin", original)
	transaction, err := manager.BeginLiveSwitch("system")
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	if got := readFirstUseFile(t, config); got != original {
		t.Fatal("BeginLiveSwitch wrote config before the native UI mutation")
	}
	// Simulate only Codex persisting its own UI choice. No real UI is touched.
	if err := os.WriteFile(config, []byte(original+"\n[desktop]\nappearanceTheme = \"light\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.VerifyMode("light"); err != nil {
		t.Fatal(err)
	}
	transaction.Close()
	manager, err = New(config, recovery, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := readFirstUseFile(t, config); got != original {
		t.Fatalf("live-switch restore = %q", got)
	}
}

func TestFirstUseRejectsConflictingRootDefinitions(t *testing.T) {
	for _, original := range []string{
		`desktop = { appearanceTheme = "system" }`,
		`desktop.appearanceTheme = "system"`,
		`"desktop" = {}`, `'desktop' = {}`, `"\u0064esktop" = {}`,
		"[desktop.appearanceTheme]\nvalue = true\n",
		"[\"desktop\".\"appearanceDarkCodeThemeId\"]\nvalue = true\n",
		"[desktop]\n\"appearanceTheme\" = \"system\"\n",
		"[desktop]\nappearanceTheme.sub = true\n",
	} {
		t.Run(original, func(t *testing.T) {
			manager, config, recovery := firstUseManager(t, "windows", original)
			if _, err := manager.NeedsPin("dark"); err == nil {
				t.Fatal("conflict was not rejected before requesting a restart")
			}
			if _, err := manager.Pin("dark"); err == nil {
				t.Fatal("Pin appended a conflicting desktop table")
			}
			if got := readFirstUseFile(t, config); got != original {
				t.Fatal("unsafe creation changed config")
			}
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatalf("unsafe creation wrote recovery: %v", err)
			}
		})
	}
}

func TestFirstUseRecognizesQuotedExistingDesktop(t *testing.T) {
	for _, header := range []string{`["desktop"]`, `['desktop']`, `["\u0064esktop"]`} {
		t.Run(header, func(t *testing.T) {
			original := header + "\nappearanceTheme = \"system\"\n"
			manager, config, recovery := firstUseManager(t, "windows", original)
			if _, err := manager.Pin("dark"); err != nil {
				t.Fatal(err)
			}
			var stored backup
			if err := json.Unmarshal([]byte(readFirstUseFile(t, recovery)), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.SchemaVersion != backupSchemaVersion || stored.AbsentDesktop != nil {
				t.Fatal("existing desktop table was mistaken for an absent table")
			}
			if _, err := manager.Restore(); err != nil {
				t.Fatal(err)
			}
			if got := readFirstUseFile(t, config); got != original {
				t.Fatalf("quoted existing header changed: %q", got)
			}
		})
	}
}

func TestFirstUseBackupIsMinimalAndRejectsInvalidMetadata(t *testing.T) {
	const original = "model = \"fixture\"\nprivate_unrelated_fixture = \"must-not-be-copied\"\n"
	for name, mutate := range map[string]func(*backup){
		"missing_metadata":    func(b *backup) { b.AbsentDesktop = nil },
		"wrong_version":       func(b *backup) { b.SchemaVersion = backupSchemaVersion },
		"invalid_digest":      func(b *backup) { b.AbsentDesktop.OriginalSHA256 = "not-a-hash" },
		"invalid_separator":   func(b *backup) { b.AbsentDesktop.Separator = "model" },
		"invalid_newline":     func(b *backup) { b.AbsentDesktop.Newline = "[desktop]" },
		"contradictory_value": func(b *backup) { b.Values["appearanceTheme"] = pointer(`appearanceTheme = "dark"`) },
	} {
		t.Run(name, func(t *testing.T) {
			manager, config, recovery := firstUseManager(t, "windows", original)
			if _, err := manager.Pin("dark"); err != nil {
				t.Fatal(err)
			}
			stored, found, err := manager.readBackup()
			if err != nil || !found || stored.SchemaVersion != absentDesktopBackupVersion || stored.AbsentDesktop == nil {
				t.Fatalf("new recovery metadata missing: %#v, %v", stored, err)
			}
			if strings.Contains(readFirstUseFile(t, recovery), "must-not-be-copied") {
				t.Fatal("backup copied unrelated configuration")
			}
			mutate(&stored)
			payload, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(recovery, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			before := readFirstUseFile(t, config)
			if _, err := manager.Restore(); err == nil {
				t.Fatal("Restore accepted invalid creation metadata")
			}
			if got := readFirstUseFile(t, config); got != before {
				t.Fatal("invalid backup changed configuration")
			}
			if _, err := os.Stat(recovery); err != nil {
				t.Fatal("invalid backup was discarded")
			}
		})
	}
}

func TestFirstUseWithoutDesktopRoundTrip(t *testing.T) {
	for _, platform := range []string{"darwin", "windows"} {
		for name, original := range map[string]string{
			"empty":        "",
			"no_newline":   "model = \"fixture\"",
			"lf":           "model = \"fixture\"\n\n[features]\nfixture = true\n",
			"crlf":         "model = \"fixture\"\r\n\r\n[features]\r\nfixture = true\r\n",
			"blank_tail":   "model = \"fixture\"\n\n\n",
			"comment_tail": "# A user comment without a final newline",
		} {
			t.Run(platform+"/"+name, func(t *testing.T) {
				manager, config, recovery := firstUseManager(t, platform, original)
				for _, mode := range []string{"dark", "light", "dark"} {
					if needed, err := manager.NeedsPin(mode); err != nil || !needed {
						t.Fatalf("NeedsPin(%s) = %t, %v", mode, needed, err)
					}
					if _, err := manager.Pin(mode); err != nil {
						t.Fatalf("first-use Pin(%s) failed: %v", mode, err)
					}
					content := readFirstUseFile(t, config)
					if !strings.HasPrefix(content, original) {
						t.Fatal("Pin rewrote unrelated original configuration")
					}
					if got, err := appearanceMode(content); err != nil || got != mode {
						t.Fatalf("pinned mode = %s, %v", got, err)
					}
					if needed, err := manager.NeedsPin(mode); err != nil || needed {
						t.Fatalf("same-mode requested reload: %t, %v", needed, err)
					}
					if changed, err := manager.Pin(mode); err != nil || changed {
						t.Fatalf("same-mode rewrote config: %t, %v", changed, err)
					}
					// A new process must recover from disk, not in-memory flags.
					var err error
					manager, err = New(config, recovery, platform)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := manager.Restore(); err != nil {
					t.Fatal(err)
				}
				if got := readFirstUseFile(t, config); got != original {
					t.Fatalf("Restore was not byte-exact\nwant %q\n got %q", original, got)
				}
				if _, err := os.Stat(recovery); !os.IsNotExist(err) {
					t.Fatalf("recovery not consumed: %v", err)
				}
				if changed, err := manager.Restore(); err != nil || changed {
					t.Fatalf("repeated Restore = %t, %v", changed, err)
				}
			})
		}
	}
}

func firstUseManager(t *testing.T, platform, original string) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	recovery := filepath.Join(root, "recovery", "appearance.json")
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(config, recovery, platform)
	if err != nil {
		t.Fatal(err)
	}
	return manager, config, recovery
}

func readFirstUseFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
