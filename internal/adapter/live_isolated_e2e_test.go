//go:build isolatede2e

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
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
	lightVerified := verified
	lightVerified.Manifest.ThemePublicID = "100002"
	lightVerified.Manifest.ThemeVersion = "1.0.1"
	lightVerified.Manifest.Design.Mode = "light"
	lightVerified.Manifest.Design.Tokens.TextPrimary = "#1A1C1F"
	lightVerified.Manifest.Design.Tokens.TextSecondary = "#3D4349"
	lightVerified.Manifest.Design.Tokens.Accent = "#2563EB"
	lightVerified.Manifest.Design.Tokens.Border = "#1A1C1F24"
	lightCompiled, err := engine.CompileTheme(lightVerified, stagedTheme)
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

	// Switch directly from the fixture's dark skin to a separately compiled
	// light skin in the same owned renderer. The two templates set their own
	// workspace foreground and background together while native pages continue
	// to follow Codex's appearance. No native preference rewrite, restart or
	// restore is needed merely because the skin mode changes.
	switched := lightCompiled
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
	stage = "cross_appearance_theme_switched"
	writeReport()

	// Complete the reverse light-to-dark replacement before Restore. This is
	// intentionally another direct controller change in the same renderer: no
	// process restart, native preference change, or offline recovery is allowed.
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
