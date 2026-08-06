package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/renderer"
)

func TestRuntimeFunctionsSupportStableAndModuleMainSurfaces(t *testing.T) {
	selectors, err := renderer.SelectorMap()
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{".main-surface", `_MainContentSurface_`, "data-app-shell-main-surface"} {
		if !strings.Contains(selectors["shell-main"], selector) {
			t.Fatalf("selector contract is missing %q", selector)
		}
	}
	for name, function := range map[string]string{"probe": probeFunction, "apply": applyFunction} {
		if !strings.Contains(function, `"shell-main"`) {
			t.Fatalf("%s function does not consume the selector contract", name)
		}
	}
	for name, function := range map[string]string{
		"apply":    applyFunction,
		"verify":   verifyFunction,
		"restore":  restoreFunction,
		"official": officialFunction,
	} {
		if !strings.Contains(function, `data-codex-skin-main`) {
			t.Fatalf("%s function does not preserve the scoped main marker", name)
		}
	}
	for _, fragment := range []string{
		`[class*="_MainContentTopFade_"]`,
		`--cs-top-fade-contract: 6`,
		`expectedTemplateVersion < 6`,
		`topFades.every`,
		`--cs-shell-edge-contract: 7`,
		`expectedTemplateVersion < 7`,
		`[data-app-shell-header-edge-scroll]`,
		`[class*="_Header_"]`,
		`[data-app-shell-main-content-top-fade]`,
		`visible(header) && shellEdgeContractSafe`,
		`--cs-scope-contract: 8`,
		`data-codex-skin-scope`,
		`expectedTemplateVersion < 8`,
	} {
		if !strings.Contains(verifyFunction, fragment) {
			t.Fatalf("verify function is missing top-fade contract %q", fragment)
		}
	}
}

func TestEnsureOrdinaryInstanceAcceptsOnlyAStableNonCDPProcess(t *testing.T) {
	installation := codex.Installation{Platform: "macos", AppIdentifier: "com.openai.codex"}
	ordinary := codex.CurrentInstance{Process: codex.ProcessIdentity{ProcessID: 42}}
	launched := false
	instance, err := ensureOrdinaryInstanceWith(context.Background(), codexRecoveryOperations{
		discoverStableInstallation: func(context.Context) (codex.Installation, error) { return installation, nil },
		discoverCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
			if !launched {
				return codex.CurrentInstance{}, codex.ErrCurrentMissing
			}
			return ordinary, nil
		},
		launchOrdinary: func(context.Context, codex.Installation) error { launched = true; return nil },
		waitForCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
			return ordinary, nil
		},
	})
	if err != nil || !launched || instance.Process.ProcessID != ordinary.Process.ProcessID {
		t.Fatalf("ordinary recovery = %#v, launched=%t, err=%v", instance, launched, err)
	}

	_, err = ensureOrdinaryInstanceWith(context.Background(), codexRecoveryOperations{
		discoverStableInstallation: func(context.Context) (codex.Installation, error) { return installation, nil },
		discoverCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
			return codex.CurrentInstance{ControlledPort: 9222}, nil
		},
		launchOrdinary: func(context.Context, codex.Installation) error { return nil },
		waitForCurrentInstance: func(context.Context, codex.Installation) (codex.CurrentInstance, error) {
			return codex.CurrentInstance{}, nil
		},
	})
	if !errors.Is(err, codex.ErrCurrentUnsafe) {
		t.Fatalf("controlled recovery instance error = %v", err)
	}
}

func TestCurrentProfileModeCannotBeRedirectedToIsolatedProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLive(Config{
		Root:           store.Root(),
		CurrentProfile: true,
		Profile:        filepath.Join(store.Root(), "state", "other"),
	}); err == nil {
		t.Fatal("current profile mode accepted a caller-supplied profile")
	}
	live, err := NewLive(Config{Root: store.Root(), CurrentProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	if live.profile != "" || !live.currentProfile {
		t.Fatalf("current profile adapter = %#v", live)
	}
}

func TestSessionActivationRestoresNativeAppearanceBytesBeforeSuccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.toml")
	original := []byte("[desktop]\nappearanceTheme = \"system\"\nappearanceDarkCodeThemeId = \"night\"\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{Root: store.Root(), CurrentProfile: true, UserHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := live.appearance.Pin("dark"); err != nil || !changed {
		t.Fatalf("Pin() = %t, %v", changed, err)
	}
	if err := live.RestoreNativeAppearanceBackup(); err != nil {
		t.Fatalf("RestoreNativeAppearanceBackup() error = %v", err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("native config was not restored\nwant: %q\n got: %q", original, restored)
	}
	if _, err := os.Lstat(filepath.Join(store.Root(), "recovery", "appearance.json")); !os.IsNotExist(err) {
		t.Fatalf("appearance backup remains after activation: %v", err)
	}
	if err := live.RestoreNativeAppearanceBackup(); err != nil {
		t.Fatalf("idempotent restore error = %v", err)
	}
}

func TestReusableControlledThemeSnapshotRequiresAnExistingMatchingMode(t *testing.T) {
	base := engine.Snapshot{
		StylePresent:    true,
		ThemePublicID:   "100005",
		ThemeVersion:    "1.0.1",
		TemplateVersion: engine.TemplateVersion,
		StyleText:       "verified-template",
		AppearanceMode:  "dark",
	}
	if !reusableControlledThemeSnapshot(base, "dark") {
		t.Fatal("verified dark renderer was not reusable for dark replacement")
	}
	if reusableControlledThemeSnapshot(base, "light") {
		t.Fatal("dark renderer was reusable for light replacement")
	}
	base.StylePresent = false
	if reusableControlledThemeSnapshot(base, "dark") {
		t.Fatal("missing style marker was reusable")
	}
	base.StylePresent = true
	base.ThemePublicID = "not-a-theme"
	if reusableControlledThemeSnapshot(base, "dark") {
		t.Fatal("invalid theme marker was reusable")
	}
}

func TestSelectPrimedThemeOnlyAcceptsCurrentOrExactMigrationTemplates(t *testing.T) {
	compiled := engine.CompiledTheme{
		ThemePublicID: "100002", ThemeVersion: "1.0.0",
		TemplateVersion: engine.TemplateVersion,
		StyleText:       "template-v3", PreviousStyleText: "template-v2", LegacyStyleText: "template-v1",
		BackgroundDataURL: "data:image/png;base64,AAAA",
		AppearanceMode:    "dark",
	}
	testCases := []struct {
		name         string
		snapshot     engine.Snapshot
		wantVersion  int
		wantStyle    string
		wantRejected bool
	}{
		{
			name: "current",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.TemplateVersion, StyleText: "template-v3", AppearanceMode: "dark",
			},
			wantVersion: engine.TemplateVersion, wantStyle: "template-v3",
		},
		{
			name: "exact previous",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.TemplateVersion - 1, StyleText: "template-v2", AppearanceMode: "dark",
			},
			wantVersion: engine.TemplateVersion - 1, wantStyle: "template-v2",
		},
		{
			name: "tampered previous style",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.TemplateVersion - 1, StyleText: "tampered", AppearanceMode: "dark",
			},
			wantRejected: true,
		},
		{
			name: "exact legacy",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.MinimumTemplateVersion, StyleText: "template-v1", AppearanceMode: "dark",
			},
			wantVersion: engine.MinimumTemplateVersion, wantStyle: "template-v1",
		},
		{
			name: "tampered legacy style",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.MinimumTemplateVersion, StyleText: "tampered", AppearanceMode: "dark",
			},
			wantRejected: true,
		},
		{
			name: "different theme",
			snapshot: engine.Snapshot{
				ThemePublicID: "100003", ThemeVersion: "1.0.0",
				TemplateVersion: engine.MinimumTemplateVersion, StyleText: "template-v1", AppearanceMode: "dark",
			},
			wantRejected: true,
		},
		{
			name: "different native appearance",
			snapshot: engine.Snapshot{
				ThemePublicID: "100002", ThemeVersion: "1.0.0",
				TemplateVersion: engine.TemplateVersion, StyleText: "template-v3", AppearanceMode: "light",
			},
			wantRejected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			primed, err := selectPrimedTheme(testCase.snapshot, compiled)
			if testCase.wantRejected {
				if !errors.Is(err, engine.ErrCapabilityBlocked) {
					t.Fatalf("selectPrimedTheme() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectPrimedTheme() error = %v", err)
			}
			if primed.TemplateVersion != testCase.wantVersion ||
				primed.StyleText != testCase.wantStyle {
				t.Fatalf("primed = %#v", primed)
			}
		})
	}
}

func TestVerifyFunctionRequiresLateRenderedActivityContract(t *testing.T) {
	for _, fragment := range []string{
		`--cs-activity-contract: 3`,
		`button[class~="group/activity-header"]`,
		`activityHeaders.length === 0`,
		`? "not_present"`,
		`effectiveBackground(label, surface)`,
		`contrast(foreground, background) >= 4.5`,
		`^oklab\(`,
	} {
		if !strings.Contains(verifyFunction, fragment) {
			t.Fatalf("verify function is missing %q", fragment)
		}
	}
	if strings.Contains(verifyFunction, `activityContractSafe ? "pass" : "fail"`) {
		t.Fatal("verify function still reports an absent activity fixture as pass")
	}
}

func TestVerifyFunctionRequiresDiffResourceContrastContract(t *testing.T) {
	for _, fragment := range []string{
		`--cs-diff-resource-contract: 4`,
		`[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]`,
		`conversationDiffResource`,
		`diffResourceControls.every`,
		`effectiveBackground(control, surface)`,
		`contrast(foreground, background) >= 4.5`,
	} {
		if !strings.Contains(verifyFunction, fragment) {
			t.Fatalf("verify function is missing %q", fragment)
		}
	}
}

func TestApplyFunctionKeepsRuntimeBackgroundInOwnedStylesheet(t *testing.T) {
	for _, fragment := range []string{
		`new CSSStyleSheet()`,
		`styleSheet.replaceSync(ownedCSS)`,
		`document.adoptedStyleSheets = [...retained, styleSheet]`,
		`styleRegistry.add(styleSheet)`,
		`styleNode.textContent = ownedCSS`,
		`data-codex-skin="active"] {`,
		`' --cs-background-image: url("' + backgroundURL`,
		`root.style.removeProperty("--cs-background-image")`,
	} {
		if !strings.Contains(applyFunction, fragment) {
			t.Fatalf("apply function is missing %q", fragment)
		}
	}
	if strings.Contains(
		applyFunction,
		`root.style.setProperty("--cs-background-image"`,
	) {
		t.Fatal("apply function still stores the runtime background on the Codex root style")
	}
	if !strings.Contains(verifyFunction, `rootBackground.includes("blob:")`) {
		t.Fatal("verify function does not require the stylesheet-backed background token")
	}
}

func TestRendererInjectorDoesNotInstallAWatcherOrPersistentBootstrap(t *testing.T) {
	for _, fragment := range []string{
		`selector("settings-panel")`,
		`selector("appearance-radio")`,
		`if (!main) return settingsScope() ? activateRoot() : false`,
		`removeMainMarkers()`,
		`URL.revokeObjectURL(backgroundURL)`,
	} {
		if !strings.Contains(applyFunction, fragment) {
			t.Fatalf("renderer injector is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		`MutationObserver`, `setInterval(`, `addEventListener("popstate"`,
		`addEventListener("hashchange"`, `Page.addScriptToEvaluateOnNewDocument`,
	} {
		if strings.Contains(applyFunction, forbidden) {
			t.Fatalf("on-demand injector still contains watcher/bootstrap %q", forbidden)
		}
	}
}

func TestIsolatedProfileRemainsAvailableOnlyForExplicitCalibration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{Root: store.Root()})
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(store.Root(), "state", "codex-profile")
	if live.profile != expected || live.currentProfile {
		t.Fatalf("isolated adapter profile = %q", live.profile)
	}
}
