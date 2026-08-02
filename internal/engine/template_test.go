package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

func TestCurrentTemplateScopesArtworkAndDynamicActivityAwayFromNativeUtilitySurfaces(t *testing.T) {
	tokens := theme.Tokens{
		BackgroundOverlay: 0.42,
		SurfaceOpacity:    0.84,
		SurfaceBlurPx:     20,
		TextPrimary:       "#FFF5EC",
		TextSecondary:     "#D9C0AE",
		Accent:            "#E78A4E",
		Border:            "#FFD8BF29",
		RadiusScale:       1,
	}
	style, err := compileTemplateV4("dark", tokens, "14 18 24", "20 24 32", ".18")
	if err != nil {
		t.Fatalf("compileTemplateV2() error = %v", err)
	}
	required := []string{
		`:has(`,
		`main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)`,
		`.thread-scroll-container`,
		`.bg-gradient-to-t.from-token-main-surface-primary`,
		`[class*="_markdown"]`,
		`[data-message-author-role]`,
		`button.group\/activity-header`,
		`button[class~="group/activity-header"]`,
		`--cs-activity-contract: 3`,
		`--cs-diff-resource-contract: 4`,
		`[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]`,
		`text-shadow: none !important`,
		`aside.app-shell-left-panel`,
	}
	for _, fragment := range required {
		if !strings.Contains(style, fragment) {
			t.Fatalf("Template v2 is missing %q", fragment)
		}
	}
	forbidden := []string{
		`:root[data-codex-skin="active"] main[data-codex-skin-main="true"],
:root[data-codex-skin="active"] aside.app-shell-left-panel`,
		`:root[data-codex-skin="active"] main[data-codex-skin-main="true"] [class~="text-token-text-primary"],
:root[data-codex-skin="active"] main[data-codex-skin-main="true"] [class~="text-token-foreground"]`,
		`.thread-scroll-container
  [class~="text-token-text-secondary"]`,
		`.thread-scroll-container
  [class~="text-token-text-tertiary"]`,
		`button.group\/activity-header
  * {
  color: var(--cs-text-secondary)`,
	}
	for _, fragment := range forbidden {
		if strings.Contains(style, fragment) {
			t.Fatalf("Template v2 contains unsafe global selector %q", fragment)
		}
	}
	for _, block := range strings.Split(style, "}") {
		selector, _, found := strings.Cut(block, "{")
		if !found || !strings.Contains(selector, `main[data-codex-skin-main="true"]`) {
			continue
		}
		if !strings.Contains(selector, ":has(") {
			t.Fatalf("main surface rule is not structurally scoped: %q", strings.TrimSpace(selector))
		}
	}
}

func TestTemplateV2RejectsUnreadableThemeTokens(t *testing.T) {
	tokens := theme.Tokens{
		TextPrimary:   "#151921",
		TextSecondary: "#202630",
		Accent:        "#242A34",
	}
	_, err := compileTemplateV4("dark", tokens, "14 18 24", "20 24 32", ".18")
	if !errors.Is(err, ErrConfiguration) {
		t.Fatalf("compileTemplateV2() error = %v, want ErrConfiguration", err)
	}
}

func TestCurrentTemplateRejectsReadableButWrongModeText(t *testing.T) {
	testCases := []struct {
		name   string
		mode   string
		tokens theme.Tokens
	}{
		{
			name: "dark theme uses mid-tone text",
			mode: "dark",
			tokens: theme.Tokens{
				TextPrimary: "#B07E55", TextSecondary: "#B58A68", Accent: "#E78A4E",
			},
		},
		{
			name: "light theme uses mid-tone text",
			mode: "light",
			tokens: theme.Tokens{
				TextPrimary: "#52606F", TextSecondary: "#5F6B78", Accent: "#2E6F9E",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := compileTemplateV4(testCase.mode, testCase.tokens, "14 18 24", "20 24 32", ".18")
			if !errors.Is(err, ErrConfiguration) {
				t.Fatalf("compileTemplateV3() error = %v, want ErrConfiguration", err)
			}
		})
	}
}

func TestTemplateV2AcceptsGoldenThemeContrast(t *testing.T) {
	testCases := []struct {
		name       string
		mode       string
		tokens     theme.Tokens
		sidebarRGB string
		surfaceRGB string
	}{
		{
			name: "ember dune",
			mode: "dark",
			tokens: theme.Tokens{
				TextPrimary: "#FFF5EC", TextSecondary: "#D9C0AE", Accent: "#E78A4E",
			},
			sidebarRGB: "14 18 24", surfaceRGB: "20 24 32",
		},
		{
			name: "polar archive",
			mode: "light",
			tokens: theme.Tokens{
				TextPrimary: "#17202B", TextSecondary: "#536273", Accent: "#2E6F9E",
			},
			sidebarRGB: "244 248 252", surfaceRGB: "249 252 255",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := compileTemplateV4(
				testCase.mode,
				testCase.tokens,
				testCase.sidebarRGB,
				testCase.surfaceRGB,
				".18",
			); err != nil {
				t.Fatalf("compileTemplateV2() error = %v", err)
			}
		})
	}
}

func TestCurrentTemplateKeepsExactTemplateV4MigrationStyle(t *testing.T) {
	tokens := theme.Tokens{
		TextPrimary: "#FFF5EC", TextSecondary: "#D9C0AE", Accent: "#E78A4E",
	}
	previous, err := compileTemplateV4("dark", tokens, "14 18 24", "20 24 32", ".18")
	if err != nil {
		t.Fatal(err)
	}
	current, err := compileTemplateV5("dark", tokens, "14 18 24", "20 24 32", ".18")
	if err != nil {
		t.Fatal(err)
	}
	if previous == current {
		t.Fatal("current template unexpectedly equals Template v4")
	}
	if !strings.Contains(previous, "--cs-activity-contract: 3") ||
		!strings.Contains(previous, "color: var(--cs-text-primary) !important") {
		t.Fatal("Template v4 migration style changed unexpectedly")
	}
	if !strings.Contains(current, "--cs-diff-resource-contract: 4") ||
		!strings.Contains(current, "[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]") ||
		!strings.Contains(current, "text-shadow: none !important") {
		t.Fatal("current diff resource contrast contract is incomplete")
	}
}

func TestCurrentTemplateBridgesNativeDropdownTokensForBothAppearances(t *testing.T) {
	testCases := []struct {
		mode        string
		tokens      theme.Tokens
		sidebarRGB  string
		surfaceRGB  string
		colorScheme string
	}{
		{
			mode: "dark",
			tokens: theme.Tokens{
				TextPrimary: "#FFF5EC", TextSecondary: "#D9C0AE", Accent: "#E78A4E",
			},
			sidebarRGB: "14 18 24", surfaceRGB: "20 24 32", colorScheme: "dark",
		},
		{
			mode: "light",
			tokens: theme.Tokens{
				TextPrimary: "#17202B", TextSecondary: "#536273", Accent: "#2E6F9E",
			},
			sidebarRGB: "244 248 252", surfaceRGB: "249 252 255", colorScheme: "light",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.mode, func(t *testing.T) {
			style, err := compileTemplateV5(
				testCase.mode, testCase.tokens, testCase.sidebarRGB, testCase.surfaceRGB, ".18",
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{
				`--cs-native-token-contract: 5`,
				`--color-token-dropdown-background: rgb(var(--cs-surface-rgb) / .98)`,
				`--color-token-foreground: var(--cs-text-primary)`,
				`--color-token-text-secondary: var(--cs-text-secondary)`,
				`--color-token-border-default: var(--cs-border)`,
				`[class~="bg-token-dropdown-background"]`,
				`color-scheme: ` + testCase.colorScheme,
			} {
				if !strings.Contains(style, fragment) {
					t.Fatalf("Template v5 %s is missing %q", testCase.mode, fragment)
				}
			}
		})
	}
}

func TestCurrentTemplateNeutralizesStableAndModuleTopFades(t *testing.T) {
	tokens := theme.Tokens{
		TextPrimary: "#FFF5EC", TextSecondary: "#D9C0AE", Accent: "#E78A4E",
	}
	previous, err := compileTemplateV5("dark", tokens, "14 18 24", "20 24 32", ".18")
	if err != nil {
		t.Fatal(err)
	}
	current, err := compileTemplateV6("dark", tokens, "14 18 24", "20 24 32", ".18")
	if err != nil {
		t.Fatal(err)
	}
	if previous == current || strings.Contains(previous, `--cs-top-fade-contract: 6`) {
		t.Fatal("Template v5 migration style was not preserved")
	}
	for _, fragment := range []string{
		`--cs-top-fade-contract: 6`,
		`.app-shell-main-content-top-fade`,
		`[class*="_MainContentTopFade_"]`,
		`display: none !important`,
	} {
		if !strings.Contains(current, fragment) {
			t.Fatalf("Template v6 is missing %q", fragment)
		}
	}
}
