package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

// TestCompileSignedDarkLightFixtures is the explicit cross-repository Gate B
// compile check. Baseline Public CI has no dependency on Private artifacts; a
// Gate candidate supplies an absolute, locally exported calibration index.
func TestCompileSignedDarkLightFixtures(t *testing.T) {
	indexPath := os.Getenv("CODEX_SKIN_GATEB_SIGNED_INDEX")
	if indexPath == "" {
		t.Skip("CODEX_SKIN_GATEB_SIGNED_INDEX is not set")
	}
	if !filepath.IsAbs(indexPath) {
		t.Fatal("CODEX_SKIN_GATEB_SIGNED_INDEX must be absolute")
	}
	index := calibrationIndex{}
	if err := readStrictJSON(indexPath, maxIndexBytes, &index); err != nil {
		t.Fatal(err)
	}
	indexRoot := filepath.Dir(indexPath)
	wantModes := map[string]string{"100002": "dark", "100004": "light"}
	compiledCount := 0
	for _, item := range index.Variants {
		wantMode, selected := wantModes[item.ThemePublicID]
		if !selected || item.OptionID != "balanced" {
			continue
		}
		if err := validateVariant(item); err != nil {
			t.Fatalf("validate %s: %v", item.ThemePublicID, err)
		}
		packageRoot := filepath.Join(indexRoot, item.PackageDirectory)
		if err := requireContained(indexRoot, packageRoot); err != nil {
			t.Fatal(err)
		}
		descriptor, err := readLimited(filepath.Join(packageRoot, "release-descriptor.json"), 64*1024)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := readLimited(filepath.Join(packageRoot, "release-descriptor.sig"), 1024)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := theme.Verify(filepath.Join(packageRoot, "package.cskin"), descriptor, signature)
		if err != nil {
			t.Fatalf("verify %s: %v", item.ThemePublicID, err)
		}
		stage := filepath.Join(t.TempDir(), item.ThemePublicID)
		if err := theme.Extract(verified, stage); err != nil {
			t.Fatalf("extract %s: %v", item.ThemePublicID, err)
		}
		compiled, err := engine.CompileTheme(verified, stage)
		if err != nil {
			t.Fatalf("compile %s: %v", item.ThemePublicID, err)
		}
		if compiled.AppearanceMode != wantMode || compiled.TemplateVersion != engine.TemplateVersion {
			t.Fatalf(
				"%s compiled mode/template = %s/%d, want %s/%d",
				item.ThemePublicID,
				compiled.AppearanceMode,
				compiled.TemplateVersion,
				wantMode,
				engine.TemplateVersion,
			)
		}
		for _, fragment := range []string{
			"--cs-native-token-contract: 5",
			"--cs-top-fade-contract: 6",
			"--cs-shell-edge-contract: 7",
			`[class*="_MainContentTopFade_"]`,
			`[data-app-shell-header-edge-scroll]`,
			`[class*="_Header_"]`,
			"--color-token-foreground: var(--cs-text-primary)",
			"--color-token-description-foreground: var(--cs-text-secondary)",
			"color-scheme: " + wantMode,
		} {
			if !strings.Contains(compiled.StyleText, fragment) {
				t.Fatalf("%s compiled CSS is missing %q", item.ThemePublicID, fragment)
			}
		}
		compiledCount++
	}
	if compiledCount != len(wantModes) {
		t.Fatalf("compiled %d signed balanced fixtures, want %d", compiledCount, len(wantModes))
	}
}
