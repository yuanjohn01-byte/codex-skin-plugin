//go:build isolatede2e

package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

// TestIsolatedCodexRendererSessionLifecycle is an opt-in real-app smoke test.
// It launches Codex only with a temporary user-data directory rooted below
// t.TempDir; it never discovers, stops, or attaches to the Founder's current
// Codex profile. Keep this out of ordinary CI and run it only before a Founder
// runtime candidate is handed over for local acceptance.
func TestIsolatedCodexRendererSessionLifecycle(t *testing.T) {
	if os.Getenv("CODEX_SKIN_ISOLATED_E2E") != "1" {
		t.Skip("set CODEX_SKIN_ISOLATED_E2E=1 to launch an isolated Codex profile")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the current isolated live-app smoke runs on macOS only")
	}
	stage := "created"
	var verification engine.ThemeVerificationResult
	var switchVerification engine.ThemeVerificationResult
	var switchBackVerification engine.ThemeVerificationResult
	var verificationErr string
	reportPath := os.Getenv("CODEX_SKIN_ISOLATED_E2E_REPORT")
	writeReport := func() {
		if reportPath == "" {
			return
		}
		// The launcher may own the Codex window it is testing, which can make
		// a terminal transport disappear before Go prints its final PASS/FAIL
		// line. Persist every completed stage, rather than only a deferred final
		// record, so the opt-in local smoke can distinguish that transport event
		// from an earlier renderer failure without collecting user data.
		payload, err := json.Marshal(struct {
			Stage                  string                         `json:"stage"`
			Verification           engine.ThemeVerificationResult `json:"verification"`
			SwitchVerification     engine.ThemeVerificationResult `json:"switchVerification"`
			SwitchBackVerification engine.ThemeVerificationResult `json:"switchBackVerification"`
			VerificationErr        string                         `json:"verificationError,omitempty"`
		}{
			Stage:                  stage,
			Verification:           verification,
			SwitchVerification:     switchVerification,
			SwitchBackVerification: switchBackVerification,
			VerificationErr:        verificationErr,
		})
		if err == nil {
			_ = os.WriteFile(reportPath, append(payload, '\n'), 0o600)
		}
	}
	defer writeReport()
	writeReport()

	root := filepath.Join(t.TempDir(), "CodexSkin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "free-test-theme-v1", "signed-release-v1")
	descriptor, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.json"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(filepath.Join(fixture, "package.cskin"), descriptor, signature)
	if err != nil {
		t.Fatal(err)
	}
	stagedTheme := filepath.Join(root, "staged-theme")
	if err := theme.Extract(verified, stagedTheme); err != nil {
		t.Fatal(err)
	}
	compiled, err := engine.CompileTheme(verified, stagedTheme)
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{
		Root:            root,
		CurrentProfile:  false,
		RestartApproved: true,
		LaunchWait:      40 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	session, err := live.OpenVerifiedThemeSession(ctx, compiled)
	if err != nil {
		t.Fatal(err)
	}
	stage = "session_opened"
	writeReport()
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = live.RestoreOfficial(cleanupCtx, session)
		_ = live.StopOwned(cleanupCtx, session)
	})

	if _, err := live.WaitForCapabilities(ctx, session); err != nil {
		t.Fatal(err)
	}
	stage = "capabilities_ready"
	writeReport()
	if err := live.Apply(ctx, session, compiled); err != nil {
		t.Fatal(err)
	}
	stage = "style_applied"
	writeReport()

	verification, err = live.WaitForThemeVerification(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(verification.Report, compiled) {
		if err != nil {
			verificationErr = err.Error()
		}
		t.Fatalf("isolated renderer did not reach a verified themed state: %#v err=%v", verification, err)
	}
	stage = "theme_verified"
	writeReport()

	// The live process remains open only to prove the one-shot style is still
	// present shortly after the Helper would have exited. It deliberately does
	// not exercise a background controller, watcher, or heartbeat.
	time.Sleep(3 * time.Second)
	report, err := live.Verify(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(report, compiled) {
		t.Fatalf("isolated renderer lost its verified style: %#v err=%v", report, err)
	}
	stage = "theme_stable"
	writeReport()

	// Switch directly from the fixture's dark skin to a light skin in the same
	// owned renderer. The source package remains the verified fixture; this
	// variant gives the renderer a distinct public ID, a light scoped colour
	// scheme, and a harmless marker token. It proves the Runtime v2.4 contract:
	// an already controlled Codex session does not need a native preference
	// rewrite, Restart, or Restore merely because the skin mode changes.
	switched := compiled
	switched.ThemePublicID = "100002"
	switched.ThemeVersion = "1.0.1"
	switched.AppearanceMode = "light"
	switched.StyleText += "\n:root[data-codex-skin=\"active\"] { color-scheme: light; --cs-isolated-switch: 1; }\n"
	if err := live.Apply(ctx, session, switched); err != nil {
		t.Fatal(err)
	}
	stage = "switch_style_applied"
	writeReport()
	switchVerification, err = live.WaitForThemeVerification(ctx, session, switched)
	if err != nil || !engine.ReportAllowsTheme(switchVerification.Report, switched) {
		if err != nil {
			verificationErr = err.Error()
		}
		t.Fatalf("isolated renderer did not verify a direct theme switch: %#v err=%v", switchVerification, err)
	}
	stage = "cross_appearance_theme_switched"
	writeReport()

	// Complete the reverse light-to-dark replacement before Restore. This is
	// intentionally another direct controller change in the same renderer: no
	// process restart, native preference change, or offline recovery is allowed.
	switchedBack := compiled
	switchedBack.ThemePublicID = "100003"
	switchedBack.ThemeVersion = "1.0.2"
	switchedBack.StyleText += "\n:root[data-codex-skin=\"active\"] { color-scheme: dark; --cs-isolated-switch-back: 1; }\n"
	if err := live.Apply(ctx, session, switchedBack); err != nil {
		t.Fatal(err)
	}
	stage = "switch_back_style_applied"
	writeReport()
	switchBackVerification, err = live.WaitForThemeVerification(ctx, session, switchedBack)
	if err != nil || !engine.ReportAllowsTheme(switchBackVerification.Report, switchedBack) {
		if err != nil {
			verificationErr = err.Error()
		}
		t.Fatalf("isolated renderer did not verify a reverse theme switch: %#v err=%v", switchBackVerification, err)
	}
	stage = "cross_appearance_theme_returned"
	writeReport()

	if err := live.RestoreOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	// Reaching an official renderer is the product assertion. The test cleanup
	// below then terminates only the isolated process. Keeping that transport
	// teardown out of the assertion avoids treating a host-terminal disconnect
	// as a failed theme transaction.
	stage = "pass"
	writeReport()
}
