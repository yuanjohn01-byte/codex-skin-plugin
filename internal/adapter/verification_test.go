package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

var _ engine.ThemeVerificationWaiter = (*Live)(nil)

func TestThemeVerificationWaitsThenReappliesOnce(t *testing.T) {
	compiled := engine.CompiledTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0",
		TemplateVersion: engine.TemplateVersion, AppearanceMode: "dark",
	}
	failing := verificationReport(compiled)
	failing.BackgroundLoaded = false
	checks := 0
	reapplies := 0
	result, err := waitForThemeVerificationWithRepair(
		context.Background(), 3*time.Millisecond, 3*time.Millisecond, time.Millisecond,
		func(context.Context) (engine.RegionReport, error) {
			checks++
			if reapplies == 0 {
				return failing, nil
			}
			return verificationReport(compiled), nil
		},
		func(context.Context) error {
			reapplies++
			return nil
		},
		compiled,
	)
	if err != nil {
		t.Fatalf("waitForThemeVerificationWithRepair() error = %v", err)
	}
	if !result.ReapplyAttempted || result.Attempts < 2 || checks != result.Attempts || reapplies != 1 {
		t.Fatalf("result = %#v, checks=%d reapply=%d", result, checks, reapplies)
	}
	if !engine.ReportAllowsTheme(result.Report, compiled) {
		t.Fatalf("result report did not prove the compiled theme: %#v", result.Report)
	}
}

func TestThemeVerificationNeverCommitsAnUnsettledRenderer(t *testing.T) {
	compiled := engine.CompiledTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0",
		TemplateVersion: engine.TemplateVersion, AppearanceMode: "dark",
	}
	failing := verificationReport(compiled)
	failing.Regions["sidebar"] = engine.RegionFail
	reapplies := 0
	result, err := waitForThemeVerificationWithRepair(
		context.Background(), 2*time.Millisecond, 2*time.Millisecond, time.Millisecond,
		func(context.Context) (engine.RegionReport, error) { return failing, nil },
		func(context.Context) error { reapplies++; return nil },
		compiled,
	)
	if err == nil || !result.ReapplyAttempted || reapplies != 1 {
		t.Fatalf("result = %#v, err=%v, reapply=%d", result, err, reapplies)
	}
	if engine.ReportAllowsTheme(result.Report, compiled) {
		t.Fatalf("unsettled report was accepted: %#v", result.Report)
	}
}

func verificationReport(compiled engine.CompiledTheme) engine.RegionReport {
	return engine.RegionReport{
		Scope: "home", StyleMarkerCount: 1, TemplateVersion: compiled.TemplateVersion,
		ThemePublicID: compiled.ThemePublicID, BackgroundLoaded: true,
		Regions: map[string]engine.RegionStatus{
			"shellMain": engine.RegionPass, "sidebar": engine.RegionPass,
			"headerTint": engine.RegionPass, "templateScope": engine.RegionPass,
			"themeContrast": engine.RegionPass, "home": engine.RegionPass,
			"mainBoundary": engine.RegionPass, "composer": engine.RegionPass,
			"topFade": engine.RegionNotPresent, "bottomFade": engine.RegionNotPresent,
			"composerUtilityBar":       engine.RegionNotPresent,
			"conversationActivity":     engine.RegionNotPresent,
			"conversationDiffResource": engine.RegionNotPresent,
			"suggestionCards":          engine.RegionNotPresent, "projectPicker": engine.RegionNotPresent,
		},
	}
}
