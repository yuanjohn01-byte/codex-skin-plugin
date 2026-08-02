package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

func TestRuntimeFunctionsSupportStableAndModuleMainSurfaces(t *testing.T) {
	for name, function := range map[string]string{
		"probe": probeFunction,
		"apply": applyFunction,
	} {
		for _, selector := range []string{
			"main.main-surface",
			`main[class*="_MainContentSurface_"]`,
		} {
			if !strings.Contains(function, selector) {
				t.Fatalf("%s function is missing %q", name, selector)
			}
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
	} {
		if !strings.Contains(verifyFunction, fragment) {
			t.Fatalf("verify function is missing top-fade contract %q", fragment)
		}
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

func TestRendererControllerSelfHealsAndPausesInAppearanceSettings(t *testing.T) {
	for _, fragment := range []string{
		`Page.addScriptToEvaluateOnNewDocument`,
		`new MutationObserver(schedule)`,
		`partObserver?.observe(document.documentElement, { childList: true, subtree: true })`,
		`document.addEventListener("DOMContentLoaded", bodyReadyHandler, { once: true })`,
		`globalThis.navigation?.addEventListener("navigate", navigationHandler)`,
		`setInterval(ensure, 30000)`,
		`input[name="appearance-theme"]`,
		`[data-testid="theme-preview"]`,
		`!mainSurface()`,
		`if (settingsScope()) deactivate()`,
		`URL.revokeObjectURL(backgroundURL)`,
	} {
		source := applyFunction
		if strings.HasPrefix(fragment, "Page.") {
			source = mustReadControllerSource(t)
		}
		if !strings.Contains(source, fragment) {
			t.Fatalf("renderer controller is missing %q", fragment)
		}
	}
}

func mustReadControllerSource(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
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
