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
	var verificationErr string
	if reportPath := os.Getenv("CODEX_SKIN_ISOLATED_E2E_REPORT"); reportPath != "" {
		defer func() {
			// The launcher may own the Codex window it is testing, which can make
			// a terminal transport disappear before Go prints its final PASS/FAIL
			// line. This opt-in, local-only breadcrumb makes the last completed
			// stage inspectable without collecting user data or altering the test
			// profile. The test result itself remains authoritative.
			payload, err := json.Marshal(struct {
				Stage              string                         `json:"stage"`
				Verification       engine.ThemeVerificationResult `json:"verification"`
				SwitchVerification engine.ThemeVerificationResult `json:"switchVerification"`
				VerificationErr    string                         `json:"verificationError,omitempty"`
			}{
				Stage:              stage,
				Verification:       verification,
				SwitchVerification: switchVerification,
				VerificationErr:    verificationErr,
			})
			if err == nil {
				_ = os.WriteFile(reportPath, append(payload, '\n'), 0o600)
			}
		}()
	}

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
	if err := live.Apply(ctx, session, compiled); err != nil {
		t.Fatal(err)
	}
	stage = "style_applied"

	verification, err = live.WaitForThemeVerification(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(verification.Report, compiled) {
		if err != nil {
			verificationErr = err.Error()
		}
		t.Fatalf("isolated renderer did not reach a verified themed state: %#v err=%v", verification, err)
	}
	stage = "theme_verified"

	// The live process remains open only to prove the one-shot style is still
	// present shortly after the Helper would have exited. It deliberately does
	// not exercise a background controller, watcher, or heartbeat.
	time.Sleep(3 * time.Second)
	report, err := live.Verify(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(report, compiled) {
		t.Fatalf("isolated renderer lost its verified style: %#v err=%v", report, err)
	}
	stage = "theme_stable"

	// Switch in the same owned renderer and verify that the root marker is
	// replaced, rather than relying on a Restore between ordinary switches.
	// The source package remains the verified fixture; this variant only gives
	// the renderer a distinct public ID and a harmless marker token.
	switched := compiled
	switched.ThemePublicID = "100002"
	switched.ThemeVersion = "1.0.1"
	switched.StyleText += "\n:root[data-codex-skin=\"active\"] { --cs-isolated-switch: 1; }\n"
	if err := live.Apply(ctx, session, switched); err != nil {
		t.Fatal(err)
	}
	stage = "switch_style_applied"
	switchVerification, err = live.WaitForThemeVerification(ctx, session, switched)
	if err != nil || !engine.ReportAllowsTheme(switchVerification.Report, switched) {
		if err != nil {
			verificationErr = err.Error()
		}
		t.Fatalf("isolated renderer did not verify a direct theme switch: %#v err=%v", switchVerification, err)
	}
	stage = "theme_switched"

	if err := live.RestoreOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	stage = "official_restored"
	if err := live.StopOwned(ctx, session); err != nil {
		t.Fatal(err)
	}
	stopped = true
	stage = "pass"
}
