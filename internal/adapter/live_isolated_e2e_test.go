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

// TestIsolatedCodexRendererLifecycleAfterLegacyV8VerificationResidue covers
// the upgrade path that a clean-profile smoke cannot see: a superseded v8
// verifier has already left a failed, running verification journal behind.
// The test first asks the current engine to retire only that exact residue,
// then uses the same temporary profile for a real Apply -> visible verification
// -> Restore cycle. It never opens, reads, or changes the Founder's profile.
func TestIsolatedCodexRendererLifecycleAfterLegacyV8VerificationResidue(t *testing.T) {
	if os.Getenv("CODEX_SKIN_ISOLATED_E2E") != "1" {
		t.Skip("set CODEX_SKIN_ISOLATED_E2E=1 to launch an isolated Codex profile")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the current isolated live-app smoke runs on macOS only")
	}

	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
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

	// Seed only the exact durable shape emitted by Template v8 after its
	// bounded verification had already failed. The recovery point remains in
	// place; migration records a terminal history rather than deleting it.
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	recoveryID, err := store.NewRecoveryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRecoveryPoint(engine.RecoveryPoint{
		RecoveryID: recoveryID, OperationID: operationID, CapturedAt: time.Now().UTC().Format(time.RFC3339),
	}, engine.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJournal(engine.Journal{
		OperationID: operationID, Kind: "apply", Stage: "verify", Status: "running",
		ThemePublicID: compiled.ThemePublicID, ThemeVersion: compiled.ThemeVersion, RecoveryID: recoveryID,
		ErrorCode: "CS-VERIFY-001",
		Verification: &engine.VerificationSummary{
			Attempts: 66, ReapplyAttempted: true, ProbeCompleted: true,
			Scope: "shell", RuntimeVersion: 2, StyleMarkerCount: 1,
			TemplateVersion: engine.TemplateVersion - 1, ThemePublicID: compiled.ThemePublicID,
			BackgroundLoaded: true,
			Regions: map[string]engine.RegionStatus{
				"mainBoundary":  engine.RegionFail,
				"templateScope": engine.RegionFail,
				"topFade":       engine.RegionFail,
			},
		},
	}); err != nil {
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
	instance, err := engine.New(store, live)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := instance.RecoverInterrupted(ctx); err != nil {
		t.Fatalf("legacy migration recovery error = %v", err)
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 0 {
		t.Fatalf("legacy journal remains running: %#v err=%v", running, err)
	}
	if _, _, err := store.ReadRecoveryPoint(recoveryID); err != nil {
		t.Fatalf("legacy recovery point was removed: %v", err)
	}

	session, err := live.OpenVerifiedThemeSession(ctx, compiled)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = live.RestoreOfficial(cleanupCtx, session)
		_ = live.StopOwned(cleanupCtx, session)
	})
	if _, err := live.WaitForCapabilities(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.Apply(ctx, session, compiled); err != nil {
		t.Fatal(err)
	}
	verification, err := live.WaitForThemeVerification(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(verification.Report, compiled) {
		t.Fatalf("dirty-profile renderer did not reach a verified themed state: %#v err=%v", verification, err)
	}
	if err := live.RestoreOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
}
