//go:build isolatede2e

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/appearance"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cdp"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
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
	var previewDiagnostics map[string]any
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
			PreviewDiagnostics     map[string]any                 `json:"previewDiagnostics,omitempty"`
		}{
			Stage:                  stage,
			Verification:           verification,
			SwitchVerification:     switchVerification,
			SwitchBackVerification: switchBackVerification,
			VerificationErr:        verificationErr,
			PreviewDiagnostics:     previewDiagnostics,
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
	alternateDarkVerified := verified
	alternateDarkVerified.Manifest.ThemePublicID = "100002"
	alternateDarkVerified.Manifest.ThemeVersion = "1.0.1"
	alternateDarkVerified.Manifest.Design.Tokens.Accent = "#A855F7"
	alternateDarkCompiled, err := engine.CompileTheme(alternateDarkVerified, stagedTheme)
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
		finalReport, finalErr := live.Verify(ctx, session, compiled)
		selectorDiagnostic, selectorErr := isolatedV11SelectorDiagnostic(ctx, live, session)
		verificationErr = fmt.Sprintf(
			"wait=%v; finalVerify=%#v; finalVerifyErr=%v; selectorDiagnostic=%#v; selectorDiagnosticErr=%v",
			err, finalReport, finalErr, selectorDiagnostic, selectorErr,
		)
		writeReport()
		t.Fatalf("isolated renderer did not reach a verified themed state: %#v err=%v", verification, err)
	}
	if err := assertCurrentWorkspaceVisualFallback(ctx, live, session); err != nil {
		t.Fatal(err)
	}
	previewDiagnostics, err = isolatedComposerDiagnostics(ctx, live, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeIsolatedPreview(ctx, live, session, os.Getenv("CODEX_SKIN_ISOLATED_E2E_DARK_PREVIEW_PATH")); err != nil {
		t.Fatal(err)
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

	// A temporary profile deliberately does not touch the Founder's native
	// Codex preference, so it can prove only the no-restart contract for a
	// same-mode skin replacement. Production dark-to-light and light-to-dark
	// transitions pin the native appearance and use the controlled reload path;
	// they must never be represented here as a direct CSS-only switch.
	switched := alternateDarkCompiled
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
	if err := writeIsolatedPreview(ctx, live, session, os.Getenv("CODEX_SKIN_ISOLATED_E2E_LIGHT_PREVIEW_PATH")); err != nil {
		t.Fatal(err)
	}
	stage = "same_appearance_theme_switched"
	writeReport()

	// Complete the reverse same-mode replacement before Restore. This continues
	// to assert that a verified native dark renderer does not need a reload when
	// only dark skin data changes.
	switchedBack := compiled
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
	stage = "same_appearance_theme_returned"
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

// TestIsolatedCodexAppearanceUISwitchesWithoutRestart is an opt-in proof of
// concept for exercising the production in-app Appearance transaction before
// applying a skin. It is deliberately isolated from the Founder's current
// Codex profile.
func TestIsolatedCodexAppearanceUISwitchesWithoutRestart(t *testing.T) {
	if os.Getenv("CODEX_SKIN_ISOLATED_E2E") != "1" {
		t.Skip("set CODEX_SKIN_ISOLATED_E2E=1 to launch an isolated Codex profile")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the current isolated live-app smoke runs on macOS only")
	}

	type transitionEvidence struct {
		Mode               string                         `json:"mode"`
		ThemePublicID      string                         `json:"themePublicId"`
		Setting            string                         `json:"setting"`
		Probe              isolatedAppearanceProbe        `json:"probe"`
		Capability         engine.RegionReport            `json:"capability"`
		ThemeVerification  engine.ThemeVerificationResult `json:"themeVerification"`
		ProcessID          int                            `json:"processId"`
		ProcessStartID     string                         `json:"processStartId"`
		TargetID           string                         `json:"targetId"`
		RendererRecreated  bool                           `json:"rendererRecreated"`
		ElapsedMS          int64                          `json:"elapsedMs"`
		DiskNeedsPin       bool                           `json:"diskNeedsPin"`
		AppearancePrepared bool                           `json:"appearancePrepared"`
		AppearanceVerified bool                           `json:"appearanceVerified"`
		SkinVerified       bool                           `json:"skinVerified"`
	}
	type experimentEvidence struct {
		Stage           string                  `json:"stage"`
		OriginalSetting string                  `json:"originalSetting,omitempty"`
		OriginalRoute   string                  `json:"originalRoute,omitempty"`
		Baseline        isolatedAppearanceProbe `json:"baseline"`
		Transitions     []transitionEvidence    `json:"transitions,omitempty"`
		RestoredSetting string                  `json:"restoredSetting,omitempty"`
		Error           string                  `json:"error,omitempty"`
	}

	evidence := experimentEvidence{Stage: "created"}
	reportPath := os.Getenv("CODEX_SKIN_LIVE_APPEARANCE_E2E_REPORT")
	writeEvidence := func() {
		if reportPath == "" {
			return
		}
		payload, err := json.Marshal(evidence)
		if err == nil {
			_ = os.WriteFile(reportPath, append(payload, '\n'), 0o600)
		}
	}
	defer writeEvidence()

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
	darkCompiled, err := engine.CompileTheme(verified, stagedTheme)
	if err != nil {
		t.Fatal(err)
	}
	lightVerified := verified
	lightVerified.Manifest.ThemePublicID = "100002"
	lightVerified.Manifest.ThemeVersion = "1.0.1"
	lightVerified.Manifest.Name = "Synthetic Daylight"
	lightVerified.Manifest.Design.Mode = "light"
	lightVerified.Manifest.Design.Tokens.BackgroundOverlay = 0.12
	lightVerified.Manifest.Design.Tokens.TextPrimary = "#16181D"
	lightVerified.Manifest.Design.Tokens.TextSecondary = "#4B5563"
	lightVerified.Manifest.Design.Tokens.Accent = "#2563EB"
	lightVerified.Manifest.Design.Tokens.Border = "#00000024"
	lightCompiled, err := engine.CompileTheme(lightVerified, stagedTheme)
	if err != nil {
		t.Fatal(err)
	}

	attachedProfile := os.Getenv("CODEX_SKIN_ATTACHED_PROFILE")
	reuseProfile := os.Getenv("CODEX_SKIN_ISOLATED_REUSE_PROFILE")
	attachedPort := 0
	attachedPID := 0
	if attachedProfile != "" {
		if _, err := fmt.Sscanf(os.Getenv("CODEX_SKIN_ATTACHED_PORT"), "%d", &attachedPort); err != nil || attachedPort == 0 {
			t.Fatal("CODEX_SKIN_ATTACHED_PORT must be a valid port")
		}
		if _, err := fmt.Sscanf(os.Getenv("CODEX_SKIN_ATTACHED_PID"), "%d", &attachedPID); err != nil || attachedPID == 0 {
			t.Fatal("CODEX_SKIN_ATTACHED_PID must be a valid process ID")
		}
	}
	liveRoot := root
	if attachedProfile != "" {
		liveRoot = filepath.Dir(attachedProfile)
	} else if reuseProfile != "" {
		liveRoot = filepath.Dir(reuseProfile)
	}
	live, err := NewLive(Config{
		Root:            liveRoot,
		Profile:         firstNonEmpty(attachedProfile, reuseProfile),
		Port:            attachedPort,
		CurrentProfile:  false,
		RestartApproved: true,
		LaunchWait:      40 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var session engine.Session
	if attachedProfile != "" {
		session, err = isolatedAttachVerifiedSession(ctx, live, attachedPID)
	} else {
		session, err = live.OpenVerifiedThemeSession(ctx, darkCompiled)
	}
	if err != nil {
		t.Fatal(err)
	}
	originalSetting := ""
	var appearanceManager *appearance.Manager
	appearanceRecoveryDir := ""
	t.Cleanup(func() {
		cleanupFailed := false
		runCleanup := func(run func(context.Context) error) error {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cleanupCancel()
			return run(cleanupCtx)
		}
		if err := runCleanup(func(cleanupCtx context.Context) error {
			return live.RestoreOfficial(cleanupCtx, session)
		}); err != nil {
			cleanupFailed = true
			t.Errorf("cleanup official Restore failed: %v", err)
		}
		if originalSetting != "" {
			if err := runCleanup(func(cleanupCtx context.Context) error {
				return isolatedSelectAppearanceViaUI(cleanupCtx, live, session, originalSetting)
			}); err != nil {
				cleanupFailed = true
				t.Errorf("cleanup native appearance restore failed: %v", err)
			}
		}
		if appearanceManager != nil {
			if _, err := appearanceManager.Restore(); err != nil {
				cleanupFailed = true
				t.Errorf("cleanup config restore failed: %v", err)
			}
		}
		if attachedProfile != "" {
			if err := runCleanup(func(cleanupCtx context.Context) error {
				return live.Close(cleanupCtx, session)
			}); err != nil {
				cleanupFailed = true
				t.Errorf("cleanup attached session close failed: %v", err)
			}
		} else {
			live.mu.Lock()
			owned := live.sessions[session.OpaqueID]
			var ownedSnapshot liveSession
			if owned != nil {
				ownedSnapshot = *owned
			}
			live.mu.Unlock()
			stopErr := runCleanup(func(cleanupCtx context.Context) error {
				return live.StopOwned(cleanupCtx, session)
			})
			if stopErr != nil {
				// The isolated Electron process can outlive its exact verified
				// SIGTERM. Never leave it behind: re-verify the same executable,
				// start identity, flags, port, and temporary profile before the
				// test-only force stop, then positively observe that PID gone.
				forceErr := runCleanup(func(cleanupCtx context.Context) error {
					if owned == nil {
						return fmt.Errorf("isolated owned session disappeared before cleanup")
					}
					return isolatedForceStopOwned(cleanupCtx, ownedSnapshot)
				})
				if forceErr != nil {
					cleanupFailed = true
					t.Errorf(
						"cleanup isolated Codex stop failed: stop=%v forceStop=%v",
						stopErr, forceErr,
					)
				}
			}
		}
		if !cleanupFailed && appearanceRecoveryDir != "" {
			if err := os.Remove(appearanceRecoveryDir); err != nil && !os.IsNotExist(err) {
				t.Errorf("cleanup durable appearance recovery directory failed: %v", err)
			}
		}
	})

	originalSetting, err = isolatedGetAppearance(ctx, live, session)
	if err != nil {
		t.Fatal(err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	appearanceRecoveryDir, err = os.MkdirTemp(
		filepath.Join(userHome, ".codex"), ".codex-skin-appearance-e2e-",
	)
	if err != nil {
		t.Fatal(err)
	}
	appearanceManager, err = appearance.New(
		filepath.Join(userHome, ".codex", "config.toml"),
		filepath.Join(appearanceRecoveryDir, "appearance.json"),
		"darwin",
	)
	if err != nil {
		t.Fatal(err)
	}
	live.appearance = appearanceManager
	originalRoute, err := isolatedGetRoute(ctx, live, session)
	if err != nil {
		t.Fatal(err)
	}
	evidence.OriginalSetting = originalSetting
	evidence.OriginalRoute = originalRoute
	evidence.Baseline, err = isolatedReadAppearanceProbe(ctx, live, session)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Stage = "baseline_ready"
	writeEvidence()

	baselineProcessID := session.Identity.ProcessID
	baselineProcessStartID := session.Identity.ProcessStartID
	previousTimeOrigin := evidence.Baseline.TimeOrigin

	transition := func(mode string, compiled engine.CompiledTheme) {
		t.Helper()
		started := time.Now()
		diskNeedsPin, err := appearanceManager.NeedsPin(mode)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := live.verifiedLiveSession(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		appearancePrepared, restartRequired, err := live.reconcileCurrentAppearance(
			ctx, verified, mode, diskNeedsPin,
		)
		if err != nil {
			evidence.Error = err.Error()
			writeEvidence()
			t.Fatal(err)
		}
		if restartRequired {
			evidence.Error = "production appearance scheduler requested restart"
			writeEvidence()
			t.Fatal("production appearance scheduler did not use the verified macOS UI path")
		}
		if !appearancePrepared {
			if _, err := appearanceManager.Pin(mode); err != nil {
				evidence.Error = "same-mode recovery point: " + err.Error()
				writeEvidence()
				t.Fatal(err)
			}
		}
		sameModeReuse := !diskNeedsPin && !appearancePrepared
		readSettledAppearance := func() (isolatedAppearanceProbe, string, error) {
			if !sameModeReuse {
				return isolatedWaitForAppearance(ctx, live, session, mode)
			}
			verifiedState, hostMode, err := live.readVerifiedAppearance(ctx, verified)
			if err != nil || !currentAppearanceMatchesTarget(verifiedState, hostMode, mode) {
				return isolatedAppearanceProbe{}, hostMode, engine.ErrVerifyFailed
			}
			probe, err := isolatedReadAppearanceProbe(ctx, live, session)
			return probe, hostMode, err
		}
		probe, setting, err := readSettledAppearance()
		if err != nil {
			evidence.Error = err.Error()
			writeEvidence()
			t.Fatal(err)
		}
		currentRoute, err := isolatedGetRoute(ctx, live, session)
		if err != nil || currentRoute != originalRoute {
			t.Fatalf("Appearance fast path did not return to the original route")
		}
		controlsVisible, err := isolatedAppearanceControlsVisible(ctx, live, session)
		if err != nil || controlsVisible {
			t.Fatalf("Appearance controls remained visible after route return")
		}
		verifiedSession, err := live.verifiedLiveSession(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if verifiedSession.process.ProcessID != baselineProcessID ||
			verifiedSession.process.ProcessStartID != baselineProcessStartID {
			t.Fatalf(
				"appearance transition restarted the isolated Codex process: pid=%d/%d start=%q/%q",
				baselineProcessID,
				verifiedSession.process.ProcessID,
				baselineProcessStartID,
				verifiedSession.process.ProcessStartID,
			)
		}
		// The Appearance action commits both a host setting and a React cache
		// update. Wait for that in-document rerender to settle before asking the
		// skin controller to inspect the Settings surface.
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		probe, setting, err = readSettledAppearance()
		if err != nil {
			t.Fatal(err)
		}
		capability, err := live.WaitForCapabilities(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		evidence.Transitions = append(evidence.Transitions, transitionEvidence{
			Mode:               mode,
			ThemePublicID:      compiled.ThemePublicID,
			Setting:            setting,
			Probe:              probe,
			Capability:         capability,
			ProcessID:          verifiedSession.process.ProcessID,
			ProcessStartID:     verifiedSession.process.ProcessStartID,
			TargetID:           verifiedSession.targetID,
			RendererRecreated:  probe.TimeOrigin != previousTimeOrigin,
			ElapsedMS:          time.Since(started).Milliseconds(),
			DiskNeedsPin:       diskNeedsPin,
			AppearancePrepared: appearancePrepared,
			AppearanceVerified: true,
		})
		previousTimeOrigin = probe.TimeOrigin
		transitionIndex := len(evidence.Transitions) - 1
		evidence.Stage = "transition_" + mode + "_appearance_verified"
		writeEvidence()
		if err := live.Apply(ctx, session, compiled); err != nil {
			evidence.Error = "apply: " + err.Error()
			writeEvidence()
			t.Fatal(err)
		}
		verification, err := live.WaitForThemeVerification(ctx, session, compiled)
		evidence.Transitions[transitionIndex].ThemeVerification = verification
		if err != nil || !engine.ReportAllowsTheme(verification.Report, compiled) {
			evidence.Error = fmt.Sprintf("verify: %v", err)
			writeEvidence()
			t.Fatalf("%s theme did not visibly verify after live appearance switch: %#v err=%v", mode, verification, err)
		}
		evidence.Transitions[transitionIndex].ElapsedMS = time.Since(started).Milliseconds()
		evidence.Transitions[transitionIndex].SkinVerified = true
		evidence.Stage = "transition_" + mode + "_verified"
		writeEvidence()
	}

	sameModeOffset := 0
	switch originalSetting {
	case "light":
		transition("light", lightCompiled)
		sameModeOffset = 1
	case "dark":
		transition("dark", darkCompiled)
		sameModeOffset = 1
	}
	transition("dark", darkCompiled)
	transition("light", lightCompiled)
	transition("dark", darkCompiled)
	firstDark := evidence.Transitions[sameModeOffset].Probe
	light := evidence.Transitions[sameModeOffset+1].Probe
	secondDark := evidence.Transitions[sameModeOffset+2].Probe
	if firstDark.BackgroundPrimary == light.BackgroundPrimary &&
		firstDark.BackgroundSecondary == light.BackgroundSecondary &&
		firstDark.BackgroundSurface == light.BackgroundSurface &&
		firstDark.TextPrimary == light.TextPrimary &&
		firstDark.TextForeground == light.TextForeground {
		t.Fatalf("Codex native palette did not change between dark and light: dark=%+v light=%+v", firstDark, light)
	}
	if firstDark.BackgroundPrimary != secondDark.BackgroundPrimary ||
		firstDark.BackgroundSecondary != secondDark.BackgroundSecondary ||
		firstDark.BackgroundSurface != secondDark.BackgroundSurface ||
		firstDark.TextPrimary != secondDark.TextPrimary ||
		firstDark.TextForeground != secondDark.TextForeground {
		t.Fatalf("Codex native dark palette did not return after light switch: first=%+v second=%+v", firstDark, secondDark)
	}

	if err := live.RestoreOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	controlsWereVisible, err := isolatedAppearanceControlsVisible(ctx, live, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := isolatedSelectAppearanceViaUI(ctx, live, session, originalSetting); err != nil {
		t.Fatal(err)
	}
	restoredSetting, err := isolatedWaitForSetting(ctx, live, session, originalSetting)
	if err != nil {
		t.Fatal(err)
	}
	if restoredSetting != originalSetting {
		t.Fatalf("isolated appearance setting restored to %q, want %q", restoredSetting, originalSetting)
	}
	if !controlsWereVisible {
		if err := isolatedReturnFromSettings(ctx, live, session, originalRoute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := appearanceManager.Restore(); err != nil {
		t.Fatal(err)
	}
	evidence.RestoredSetting = restoredSetting
	evidence.Stage = "pass"
	writeEvidence()

	t.Logf("live appearance experiment passed: original=%s transitions=%+v", originalSetting, evidence.Transitions)
}

func isolatedWaitForProcessExit(ctx context.Context, processID int) error {
	if processID <= 0 {
		return fmt.Errorf("invalid isolated Codex process ID")
	}
	for {
		exists, err := isolatedProcessExists(ctx, processID)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func isolatedForceStopOwned(ctx context.Context, owned liveSession) error {
	exists, err := isolatedProcessExists(ctx, owned.process.ProcessID)
	if err != nil || !exists {
		return err
	}
	verified, err := codex.VerifyProcess(
		ctx, owned.installation, owned.process.ProcessID, owned.port, owned.profile,
	)
	if err != nil {
		return fmt.Errorf("re-verify isolated Codex before force stop: %w", err)
	}
	if verified.ProcessStartID != owned.process.ProcessStartID ||
		verified.ExecutableSHA256 != owned.process.ExecutableSHA256 {
		return fmt.Errorf("isolated Codex process identity changed before force stop")
	}
	process, err := os.FindProcess(owned.process.ProcessID)
	if err != nil {
		return fmt.Errorf("find isolated Codex process: %w", err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("force stop isolated Codex process: %w", err)
	}
	return isolatedWaitForProcessExit(ctx, owned.process.ProcessID)
}

func isolatedProcessExists(ctx context.Context, processID int) (bool, error) {
	output, err := exec.CommandContext(
		ctx, "/bin/ps", "-p", strconv.Itoa(processID), "-o", "pid=,stat=",
	).Output()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		var processMissing *exec.ExitError
		if errors.As(err, &processMissing) && processMissing.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("query isolated Codex process: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return false, nil
	}
	if len(fields) != 2 {
		return false, fmt.Errorf("unexpected isolated Codex process status")
	}
	// A zombie has already exited and cannot execute or retain the temporary
	// profile; its parent/system reaps the PID after this test returns.
	return !strings.HasPrefix(fields[1], "Z"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type isolatedAppearanceProbe struct {
	BridgeAvailable     bool    `json:"bridgeAvailable"`
	SystemVariant       string  `json:"systemVariant"`
	DarkMedia           bool    `json:"darkMedia"`
	ColorScheme         string  `json:"colorScheme"`
	BackgroundPrimary   string  `json:"backgroundPrimary"`
	BackgroundSecondary string  `json:"backgroundSecondary"`
	BackgroundSurface   string  `json:"backgroundSurface"`
	TextPrimary         string  `json:"textPrimary"`
	TextForeground      string  `json:"textForeground"`
	BodyBackground      string  `json:"bodyBackground"`
	BodyColor           string  `json:"bodyColor"`
	TimeOrigin          float64 `json:"timeOrigin"`
}

type isolatedHostResponse struct {
	OK     bool           `json:"ok"`
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
	Error  string         `json:"error,omitempty"`
}

const isolatedHostRequestFunction = `async function (endpoint, params) {
  const bridge = globalThis.electronBridge;
  if (!bridge || typeof bridge.sendMessageFromView !== "function") {
    return { ok: false, status: 0, body: {}, error: "electron_bridge_missing" };
  }
  const requestId = crypto.randomUUID();
  return await new Promise((resolve) => {
    let settled = false;
    let timer = 0;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      window.removeEventListener("message", onMessage);
      resolve(value);
    };
    const onMessage = (event) => {
      const response = event.data;
      if (!response || response.type !== "fetch-response" || response.requestId !== requestId) return;
      if (response.responseType !== "success") {
        finish({ ok: false, status: response.status || 0, body: {}, error: response.error || "host_request_failed" });
        return;
      }
      let body = {};
      try {
        body = JSON.parse(response.bodyJsonString || "{}");
      } catch {
        finish({ ok: false, status: response.status || 0, body: {}, error: "invalid_host_response" });
        return;
      }
      finish({ ok: response.status >= 200 && response.status < 300, status: response.status, body });
    };
    window.addEventListener("message", onMessage);
    timer = setTimeout(() => finish({ ok: false, status: 0, body: {}, error: "host_request_timeout" }), 5000);
    Promise.resolve(bridge.sendMessageFromView({
      type: "fetch",
      requestId,
      method: "POST",
      url: "vscode://codex/" + endpoint,
      body: JSON.stringify(params),
      reportUploadProgress: false
    })).catch((error) => finish({
      ok: false,
      status: 0,
      body: {},
      error: error instanceof Error ? error.message : String(error)
    }));
  });
}`

const isolatedAppearanceUIFunction = `function (mode) {
  const index = { system: 0, light: 1, dark: 2 }[mode];
  if (index === undefined) throw new Error("unsupported_appearance_mode");
  const radios = [...document.querySelectorAll('input[name="appearance-theme"][type="radio"]')];
  if (radios.length !== 3) throw new Error("appearance_controls_missing");
  // The native Appearance action can replace the renderer context. Schedule
  // the real DOM click only after this CDP request has returned, then let Go
  // reacquire and verify the post-action renderer state independently.
  setTimeout(() => radios[index].click(), 100);
  return { selected: mode, radioCount: radios.length };
}`

func isolatedCallAsyncFunction(
	ctx context.Context,
	live *Live,
	session engine.Session,
	function string,
	arguments []any,
	output any,
) error {
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var global evaluateResult
	if err := verified.client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": "globalThis", "returnByValue": false,
	}, &global); err != nil {
		return fmt.Errorf("isolated Runtime.evaluate failed: %w", err)
	}
	if global.ExceptionDetails != nil || global.Result.ObjectID == "" {
		return fmt.Errorf("isolated Runtime.evaluate returned no global object: exception=%v", global.ExceptionDetails)
	}
	callArguments := make([]map[string]any, 0, len(arguments))
	for _, value := range arguments {
		callArguments = append(callArguments, map[string]any{"value": value})
	}
	var result callResult
	if err := verified.client.Call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": global.Result.ObjectID, "functionDeclaration": function,
		"arguments": callArguments, "returnByValue": true, "awaitPromise": true,
	}, &result); err != nil {
		return fmt.Errorf("isolated Runtime.callFunctionOn failed: %w", err)
	}
	if result.ExceptionDetails != nil || len(result.Result.Value) == 0 {
		return fmt.Errorf(
			"isolated Runtime.callFunctionOn returned no value: exception=%v",
			result.ExceptionDetails,
		)
	}
	if err := jsonUnmarshal(result.Result.Value, output); err != nil {
		return engine.ErrVerifyFailed
	}
	return nil
}

func isolatedHostRequest(
	ctx context.Context,
	live *Live,
	session engine.Session,
	endpoint string,
	params map[string]any,
) (isolatedHostResponse, error) {
	var response isolatedHostResponse
	err := isolatedCallAsyncFunction(
		ctx,
		live,
		session,
		isolatedHostRequestFunction,
		[]any{endpoint, params},
		&response,
	)
	if err != nil {
		return response, err
	}
	if !response.OK {
		return response, fmt.Errorf("Codex host %s failed: status=%d error=%s", endpoint, response.Status, response.Error)
	}
	return response, nil
}

func isolatedGetAppearance(ctx context.Context, live *Live, session engine.Session) (string, error) {
	response, err := isolatedHostRequest(ctx, live, session, "get-setting", map[string]any{
		"key": "appearanceTheme",
	})
	if err != nil {
		return "", err
	}
	value, ok := response.Body["value"].(string)
	if !ok || (value != "system" && value != "light" && value != "dark") {
		return "", fmt.Errorf("Codex returned invalid appearanceTheme: %#v", response.Body["value"])
	}
	return value, nil
}

func isolatedSelectAppearanceViaUI(
	ctx context.Context,
	live *Live,
	session engine.Session,
	mode string,
) error {
	if mode != "system" && mode != "light" && mode != "dark" {
		return fmt.Errorf("unsupported isolated appearance mode %q", mode)
	}
	controlsVisible, err := isolatedAppearanceControlsVisible(ctx, live, session)
	if err != nil {
		return err
	}
	if !controlsVisible {
		if err := isolatedOpenAppearanceViaSettingsCommand(ctx, live, session); err != nil {
			return err
		}
	}
	return isolatedClickVisibleAppearanceViaUI(ctx, live, session, mode)
}

func isolatedOpenAppearanceViaSettingsCommand(
	ctx context.Context,
	live *Live,
	session engine.Session,
) error {
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	_, _, err = live.openAppearanceControls(ctx, verified)
	return err
}

func isolatedClickVisibleAppearanceViaUI(
	ctx context.Context,
	live *Live,
	session engine.Session,
	mode string,
) error {
	if mode != "system" && mode != "light" && mode != "dark" {
		return fmt.Errorf("unsupported isolated appearance mode %q", mode)
	}
	if err := isolatedWaitForAppearanceControls(ctx, live, session); err != nil {
		return err
	}
	var result struct {
		Selected   string `json:"selected"`
		RadioCount int    `json:"radioCount"`
	}
	if err := isolatedCallAsyncFunction(
		ctx,
		live,
		session,
		isolatedAppearanceUIFunction,
		[]any{mode},
		&result,
	); err != nil {
		return err
	}
	if result.Selected != mode || result.RadioCount != 3 {
		return fmt.Errorf("Codex Appearance UI did not select %s: %#v", mode, result)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(350 * time.Millisecond):
	}
	return isolatedReconnectOwnedRenderer(ctx, live, session)
}

func isolatedAppearanceControlsVisible(
	ctx context.Context,
	live *Live,
	session engine.Session,
) (bool, error) {
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return false, err
	}
	var count int
	if err := callFunction(ctx, verified.client, `function () {
      return document.querySelectorAll(
        'input[name="appearance-theme"][type="radio"]'
      ).length;
    }`, nil, &count); err != nil {
		return false, err
	}
	return count == 3, nil
}

func isolatedGetRoute(ctx context.Context, live *Live, session engine.Session) (string, error) {
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return "", err
	}
	var route string
	if err := callFunction(ctx, verified.client, `function () {
      return location.pathname + location.search + location.hash;
    }`, nil, &route); err != nil || route == "" {
		return "", engine.ErrVerifyFailed
	}
	return route, nil
}

func isolatedReturnFromSettings(
	ctx context.Context,
	live *Live,
	session engine.Session,
	route string,
) error {
	if route == "" {
		return fmt.Errorf("isolated return route is empty")
	}
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	return live.returnFromAppearance(ctx, verified, route)
}

func isolatedReconnectOwnedRenderer(ctx context.Context, live *Live, session engine.Session) error {
	live.mu.Lock()
	active := live.sessions[session.OpaqueID]
	if active == nil {
		live.mu.Unlock()
		return engine.ErrVerifyFailed
	}
	installation := active.installation
	process := active.process
	port := active.port
	profile := active.profile
	oldClient := active.client
	live.mu.Unlock()

	deadline := time.Now().Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		reconnectedProcess, target, client, err := live.connectControlled(
			ctx,
			installation,
			process.ProcessID,
			port,
			profile,
		)
		if err == nil {
			if reconnectedProcess.ProcessID != process.ProcessID ||
				reconnectedProcess.ProcessStartID != process.ProcessStartID {
				_ = client.Close()
				return fmt.Errorf(
					"isolated renderer reconnect changed Codex process: pid=%d/%d start=%q/%q",
					process.ProcessID,
					reconnectedProcess.ProcessID,
					process.ProcessStartID,
					reconnectedProcess.ProcessStartID,
				)
			}

			live.mu.Lock()
			current := live.sessions[session.OpaqueID]
			if current != active {
				live.mu.Unlock()
				_ = client.Close()
				return engine.ErrVerifyFailed
			}
			active.process = reconnectedProcess
			active.targetID = target.ID
			active.client = client
			live.mu.Unlock()
			if oldClient != nil {
				_ = oldClient.Close()
			}
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("isolated renderer did not return: %w", lastErr)
}

func isolatedAttachVerifiedSession(
	ctx context.Context,
	live *Live,
	processID int,
) (engine.Session, error) {
	installation, err := codex.DiscoverInstallation(ctx)
	if err != nil {
		return engine.Session{}, err
	}
	process, err := codex.VerifyListener(
		ctx,
		installation,
		processID,
		live.port,
		live.profile,
	)
	if err != nil {
		return engine.Session{}, err
	}
	targets, err := cdp.Discover(ctx, live.port)
	if err != nil {
		return engine.Session{}, err
	}
	target, err := cdp.SelectPage(targets)
	if err != nil {
		return engine.Session{}, err
	}
	client, err := cdp.Dial(ctx, target, live.port)
	if err != nil {
		return engine.Session{}, err
	}
	if err := client.Call(ctx, "Runtime.enable", map[string]any{}, nil); err != nil {
		_ = client.Close()
		return engine.Session{}, err
	}
	if err := client.Call(ctx, "Page.enable", map[string]any{}, nil); err != nil {
		_ = client.Close()
		return engine.Session{}, err
	}
	opaqueID, err := randomSessionID()
	if err != nil {
		_ = client.Close()
		return engine.Session{}, err
	}
	live.mu.Lock()
	live.sessions[opaqueID] = &liveSession{
		client: client, installation: installation, process: process,
		port: live.port, profile: live.profile, targetID: target.ID,
	}
	live.mu.Unlock()
	return engine.Session{
		OpaqueID: opaqueID,
		Identity: engine.Identity{
			Platform: installation.Platform, AppIdentifier: installation.AppIdentifier,
			Publisher: installation.Publisher, Version: installation.Version,
			ExecutableHash: installation.ExecutableSHA256, ProcessID: process.ProcessID,
			ProcessStartID: process.ProcessStartID,
		},
	}, nil
}

func isolatedWaitForAppearanceControls(ctx context.Context, live *Live, session engine.Session) error {
	deadline := time.Now().Add(10 * time.Second)
	lastRoute := ""
	lastCount := 0
	var lastErr error
	for time.Now().Before(deadline) {
		verified, err := live.verifiedLiveSession(ctx, session)
		if err == nil {
			var state struct {
				Route      string `json:"route"`
				RadioCount int    `json:"radioCount"`
			}
			err = callFunction(ctx, verified.client, `function () {
          return {
            route: location.pathname + location.search + location.hash,
            radioCount: document.querySelectorAll(
              'input[name="appearance-theme"][type="radio"]'
            ).length
          };
        }`, nil, &state)
			lastRoute = state.Route
			lastCount = state.RadioCount
		}
		lastErr = err
		if err == nil && lastCount == 3 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf(
		"Codex Appearance controls did not settle: route=%q radios=%d error=%v",
		lastRoute,
		lastCount,
		lastErr,
	)
}

func isolatedWaitForSetting(
	ctx context.Context,
	live *Live,
	session engine.Session,
	want string,
) (string, error) {
	deadline := time.Now().Add(12 * time.Second)
	last := ""
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = isolatedGetAppearance(ctx, live, session)
		if lastErr == nil && last == want {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return last, fmt.Errorf("appearance setting did not settle to %q: got=%q error=%v", want, last, lastErr)
}

func isolatedReadAppearanceProbe(
	ctx context.Context,
	live *Live,
	session engine.Session,
) (isolatedAppearanceProbe, error) {
	verified, err := live.verifiedLiveSession(ctx, session)
	if err != nil {
		return isolatedAppearanceProbe{}, err
	}
	var probe isolatedAppearanceProbe
	err = callFunction(ctx, verified.client, `function () {
      const bridge = globalThis.electronBridge;
      const rootStyle = getComputedStyle(document.documentElement);
      const bodyStyle = getComputedStyle(document.body);
      return {
        bridgeAvailable: Boolean(bridge && typeof bridge.sendMessageFromView === "function"),
        systemVariant: typeof bridge?.getSystemThemeVariant === "function" ? bridge.getSystemThemeVariant() : "",
        darkMedia: matchMedia("(prefers-color-scheme: dark)").matches,
        colorScheme: rootStyle.colorScheme,
        backgroundPrimary: rootStyle.getPropertyValue("--color-background-primary").trim(),
        backgroundSecondary: rootStyle.getPropertyValue("--color-background-secondary").trim(),
        backgroundSurface: rootStyle.getPropertyValue("--color-background-surface").trim(),
        textPrimary: rootStyle.getPropertyValue("--color-text-primary").trim(),
        textForeground: rootStyle.getPropertyValue("--color-text-foreground").trim(),
        bodyBackground: bodyStyle.backgroundColor,
        bodyColor: bodyStyle.color,
        timeOrigin: performance.timeOrigin
      };
    }`, nil, &probe)
	if err != nil {
		return probe, err
	}
	if !probe.BridgeAvailable || probe.TimeOrigin == 0 {
		return probe, engine.ErrCapabilityBlocked
	}
	return probe, nil
}

func isolatedWaitForAppearance(
	ctx context.Context,
	live *Live,
	session engine.Session,
	mode string,
) (isolatedAppearanceProbe, string, error) {
	deadline := time.Now().Add(12 * time.Second)
	var lastProbe isolatedAppearanceProbe
	lastSetting := ""
	var lastErr error
	for time.Now().Before(deadline) {
		lastSetting, lastErr = isolatedGetAppearance(ctx, live, session)
		if lastErr == nil {
			lastProbe, lastErr = isolatedReadAppearanceProbe(ctx, live, session)
		}
		variantSettled := mode == "system" || lastProbe.SystemVariant == mode
		schemeSettled := mode == "system" || lastProbe.ColorScheme == mode
		if lastErr == nil && lastSetting == mode && variantSettled && schemeSettled {
			return lastProbe, lastSetting, nil
		}
		select {
		case <-ctx.Done():
			return lastProbe, lastSetting, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return lastProbe, lastSetting, fmt.Errorf(
		"appearance %s did not settle: setting=%q probe=%+v lastError=%v",
		mode,
		lastSetting,
		lastProbe,
		lastErr,
	)
}

// writeIsolatedPreview captures only the CDP-owned temporary renderer used by
// this opt-in test. It never reads, captures or modifies the Founder's active
// Codex profile; callers opt in by supplying a local artifact path.
func writeIsolatedPreview(ctx context.Context, live *Live, session engine.Session, outputPath string) error {
	if outputPath == "" {
		return nil
	}
	content, err := live.CapturePNG(ctx, session)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, content, 0o600)
}

// isolatedV11SelectorDiagnostic contains only renderer structure and computed
// style values. It is recorded on an opt-in isolated-test failure to calibrate
// a new Codex build without inspecting any Founder profile or conversation.
func isolatedV11SelectorDiagnostic(ctx context.Context, live *Live, session engine.Session) (map[string]any, error) {
	live.mu.Lock()
	active := live.sessions[session.OpaqueID]
	live.mu.Unlock()
	if active == nil {
		return nil, engine.ErrVerifyFailed
	}
	var diagnostic map[string]any
	err := callFunction(ctx, active.client, `function () {
	  const style = document.querySelector("#codex-skin-theme-v1");
	  const main = document.querySelector('main[data-codex-skin-main="true"]');
	  const workspace = 'main[data-codex-skin-main="true"]:is([data-codex-skin-scope="home"], [data-codex-skin-scope="thread"])';
	  const header = document.querySelector('header:is(.app-header-tint, [data-app-shell-header-edge-scroll], [class*="_Header_"])');
	  const rules = style?.sheet ? [...style.sheet.cssRules].map((rule) => rule.cssText) : [];
	  return {
	    scope: main?.getAttribute("data-codex-skin-scope") || "",
	    mainMatchesWorkspace: Boolean(main?.matches(workspace)),
	    mainBackgroundImage: main ? getComputedStyle(main).backgroundImage : "",
	    mainBorderInlineStart: main ? getComputedStyle(main).borderInlineStartWidth : "",
	    headerFound: Boolean(header),
	    headerColor: header ? getComputedStyle(header).color : "",
	    styleRules: rules.length,
	    workspaceRule: rules.find((rule) => rule.includes("border-inline-start")) || "",
	    headerRule: rules.find((rule) => rule.includes("header:is(")) || "",
	    fadeRule: rules.find((rule) => rule.includes("_MainContentBottomFade_")) || ""
	  };
	}`, nil, &diagnostic)
	return diagnostic, err
}

// isolatedComposerDiagnostics returns only structure and computed paint values
// for visible composer containers in the temporary profile. It intentionally
// excludes text, values, attributes and URLs.
func isolatedComposerDiagnostics(ctx context.Context, live *Live, session engine.Session) (map[string]any, error) {
	live.mu.Lock()
	active := live.sessions[session.OpaqueID]
	live.mu.Unlock()
	if active == nil {
		return nil, engine.ErrVerifyFailed
	}
	var diagnostic map[string]any
	err := callFunction(ctx, active.client, `function () {
	  const root = document.querySelector(':is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])');
	  const visible = (node) => {
	    if (!node) return false;
	    const box = node.getBoundingClientRect();
	    const style = getComputedStyle(node);
	    return box.width > 1 && box.height > 1 && style.display !== "none" && style.visibility !== "hidden";
	  };
	  const describe = (node) => {
	    const box = node.getBoundingClientRect();
	    const style = getComputedStyle(node);
	    return {
	      tag: node.tagName.toLowerCase(),
	      classes: typeof node.className === "string" ? node.className : "",
	      backgroundColor: style.backgroundColor,
	      backgroundImage: style.backgroundImage,
	      color: style.color,
	      border: style.border,
	      position: style.position,
	      rect: [Math.round(box.x), Math.round(box.y), Math.round(box.width), Math.round(box.height)]
	    };
	  };
	  return {
	    root: root ? describe(root) : null,
	    descendants: root ? [...root.querySelectorAll("div, form, section, button, label, span, svg, textarea, [contenteditable], [role=textbox]")]
	      .filter(visible).slice(0, 32).map(describe) : []
	  };
	}`, nil, &diagnostic)
	return diagnostic, err
}

// assertCurrentWorkspaceVisualFallback exercises the current signed CSS in a
// real isolated renderer without reading a conversation. The fixture mimics
// the safe semantic shapes that replaced the legacy thread-scroll container:
// token foreground, token surface, a sticky composer, and the lower fade.
func assertCurrentWorkspaceVisualFallback(ctx context.Context, live *Live, session engine.Session) error {
	live.mu.Lock()
	active := live.sessions[session.OpaqueID]
	live.mu.Unlock()
	if active == nil {
		return engine.ErrVerifyFailed
	}
	var result struct {
		MainMarked                  bool     `json:"mainMarked"`
		SurfaceReadable             bool     `json:"surfaceReadable"`
		TextReadable                bool     `json:"textReadable"`
		ComposerReadable            bool     `json:"composerReadable"`
		BottomCleared               bool     `json:"bottomCleared"`
		NativeReadable              bool     `json:"nativeReadable"`
		ComposerLayersReadable      bool     `json:"composerLayersReadable"`
		ComposerControlsReadable    bool     `json:"composerControlsReadable"`
		ComposerControlColors       []string `json:"composerControlColors"`
		HomeHeadingReadable         bool     `json:"homeHeadingReadable"`
		ComposerPlaceholderReadable bool     `json:"composerPlaceholderReadable"`
	}
	if err := callFunction(ctx, active.client, `function () {
	  const main = document.querySelector('main[data-codex-skin-main="true"]');
	  if (!main) return { mainMarked: false };
	  const originalScope = main.getAttribute("data-codex-skin-scope") || "";
	  // Exercise the conversation-only foreground contract independently from
	  // the real blank Home route used by this renderer session.
	  main.setAttribute("data-codex-skin-scope", "thread");
	  const visible = (node) => {
	    if (!node) return false;
	    const box = node.getBoundingClientRect();
	    const style = getComputedStyle(node);
	    return box.width > 1 && box.height > 1 && style.display !== "none" && style.visibility !== "hidden";
	  };
	  const fixture = document.createElement("section");
	  fixture.setAttribute("data-codex-skin-visual-fixture", "true");
	  fixture.innerHTML = [
	    '<section data-message-author-role="assistant"><div class="bg-token-main-surface-primary"><p class="text-token-text-primary">fixture</p></div></section>',
	    '<div class="_ComposerLayoutRoot_fixture" data-codex-skin-composer="true"><div contenteditable="true"></div></div>',
	    '<div class="bg-gradient-to-t from-surface via-surface"></div>'
	  ].join("");
	  main.appendChild(fixture);
	  const surface = fixture.firstElementChild.firstElementChild;
	  const label = surface.querySelector("p");
	  const composer = fixture.children[1];
	  const fade = fixture.children[2];
	  const readable = (value) => value && value !== "rgba(0, 0, 0, 0)" && value !== "transparent";
	  const expected = document.createElement("span");
	  expected.style.color = "var(--cs-text-primary)";
	  fixture.appendChild(expected);
	  const expectedSecondary = document.createElement("span");
	  expectedSecondary.style.color = "var(--cs-text-secondary)";
	  fixture.appendChild(expectedSecondary);
	  const nativeSide = document.createElement("aside");
	  nativeSide.setAttribute("data-codex-skin-native-fixture", "true");
	  nativeSide.style.background = "rgb(255, 255, 255)";
	  nativeSide.innerHTML = '<p class="text-default" style="color: rgb(26, 28, 31)">native</p>';
	  main.appendChild(nativeSide);
	  const nativeLabel = nativeSide.firstElementChild;
	  const parse = (value) => (String(value).match(/[0-9.]+/g) || []).slice(0, 3).map(Number);
	  const luminance = (rgb) => {
	    const channel = (value) => {
	      value /= 255;
	      return value <= .04045 ? value / 12.92 : Math.pow((value + .055) / 1.055, 2.4);
	    };
	    return .2126 * channel(rgb[0]) + .7152 * channel(rgb[1]) + .0722 * channel(rgb[2]);
	  };
	  const nativeForeground = parse(getComputedStyle(nativeLabel).color);
	  const nativeBackground = parse(getComputedStyle(nativeSide).backgroundColor);
	  const nativeContrast = nativeForeground.length === 3 && nativeBackground.length === 3
	    ? (Math.max(luminance(nativeForeground), luminance(nativeBackground)) + .05) /
	      (Math.min(luminance(nativeForeground), luminance(nativeBackground)) + .05)
	    : 0;
	  const realComposer = document.querySelector(':is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])');
	  const realComposerBody = realComposer?.querySelector('[class*="_ComposerLayoutBody_"]');
	  const realComposerFooter = realComposer?.querySelector(':is([class*="_ComposerFooter_"], [class*="_ComposerHomeUtilityBar_"])');
	  const richInput = realComposer?.querySelector('[class*="_RichTextInput_"]');
	  const realComposerStyle = realComposer ? getComputedStyle(realComposer) : null;
	  const composerLayersReadable = Boolean(realComposerStyle && realComposerBody && realComposerFooter && richInput &&
	    getComputedStyle(realComposerBody).backgroundColor === realComposerStyle.backgroundColor &&
	    getComputedStyle(realComposerFooter).backgroundColor === realComposerStyle.backgroundColor &&
	    getComputedStyle(richInput).color === realComposerStyle.color);
	  const composerControls = realComposer
	    ? [...realComposer.querySelectorAll(':is(button, [role="button"])')].filter(visible)
	    : [];
	  const composerControlLeaves = composerControls.flatMap((control) => [
	    control,
	    ...control.querySelectorAll("*")
	  ]).filter(visible);
	  const composerControlsReadable = Boolean(realComposerStyle && composerControlLeaves.length > 0 &&
	    composerControlLeaves.every((control) => getComputedStyle(control).color === realComposerStyle.color));
	  const textReadable = getComputedStyle(label).color === getComputedStyle(expected).color;
	  main.setAttribute("data-codex-skin-scope", originalScope);
	  const homeHeading = main.querySelector('[class~="heading-xl"]');
	  const homeHeadingReadable = Boolean(homeHeading &&
	    getComputedStyle(homeHeading).color === getComputedStyle(expected).color);
	  const placeholderTarget = realComposer?.querySelector(
	    '[data-placeholder], .ProseMirror p.is-editor-empty:first-child'
	  );
	  const placeholderStyle = placeholderTarget
	    ? getComputedStyle(placeholderTarget, "::before")
	    : null;
	  const composerPlaceholderReadable = Boolean(placeholderStyle &&
	    placeholderStyle.color === getComputedStyle(expectedSecondary).color &&
	    placeholderStyle.opacity === "1");
	  const result = {
	    mainMarked: originalScope === "home" || originalScope === "thread",
	    surfaceReadable: readable(getComputedStyle(surface).backgroundColor),
	    textReadable,
	    composerReadable: readable(getComputedStyle(composer).backgroundColor) &&
	      getComputedStyle(composer).backgroundImage === "none",
	    bottomCleared: getComputedStyle(fade).display === "none",
	    nativeReadable: nativeContrast >= 4.5,
	    composerLayersReadable,
	    composerControlsReadable,
	    composerControlColors: composerControlLeaves.map((control) => getComputedStyle(control).color),
	    homeHeadingReadable,
	    composerPlaceholderReadable
	  };
	  nativeSide.remove();
	  fixture.remove();
	  return result;
	}`, nil, &result); err != nil {
		return err
	}
	if !result.MainMarked || !result.SurfaceReadable || !result.TextReadable ||
		!result.ComposerReadable || !result.BottomCleared || !result.NativeReadable ||
		!result.ComposerLayersReadable || !result.ComposerControlsReadable ||
		!result.HomeHeadingReadable || !result.ComposerPlaceholderReadable {
		return fmt.Errorf("%w: current workspace fixture %#v", engine.ErrVerifyFailed, result)
	}
	return nil
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
			TemplateVersion: 8, ThemePublicID: compiled.ThemePublicID,
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
