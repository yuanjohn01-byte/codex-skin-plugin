// Package adapter implements the official Codex Desktop loopback CDP adapter.
package adapter

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cdp"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

const defaultLaunchWait = 25 * time.Second

var sixDigitID = regexp.MustCompile(`^[0-9]{6}$`)

type Live struct {
	root       string
	profile    string
	port       int
	launchWait time.Duration
	mu         sync.Mutex
	sessions   map[string]*liveSession
}

type liveSession struct {
	client       *cdp.Client
	installation codex.Installation
	process      codex.ProcessIdentity
	port         int
	profile      string
	current      *engine.CompiledTheme
}

type Config struct {
	Root       string
	Profile    string
	Port       int
	LaunchWait time.Duration
}

type remoteObject struct {
	Type     string          `json:"type"`
	ObjectID string          `json:"objectId"`
	Value    json.RawMessage `json:"value"`
}

type evaluateResult struct {
	Result           remoteObject `json:"result"`
	ExceptionDetails any          `json:"exceptionDetails,omitempty"`
}

type callResult struct {
	Result           remoteObject `json:"result"`
	ExceptionDetails any          `json:"exceptionDetails,omitempty"`
}

type SurfaceDiagnostic struct {
	Tag             string    `json:"tag"`
	Classes         string    `json:"classes"`
	BackgroundColor string    `json:"backgroundColor"`
	BackgroundImage string    `json:"backgroundImage"`
	BoxShadow       string    `json:"boxShadow"`
	Border          string    `json:"border"`
	Display         string    `json:"display"`
	Position        string    `json:"position"`
	Margin          string    `json:"margin"`
	Padding         string    `json:"padding"`
	Gap             string    `json:"gap"`
	Rect            []float64 `json:"rect"`
}

type PseudoDiagnostic struct {
	Tag             string    `json:"tag"`
	Classes         string    `json:"classes"`
	Pseudo          string    `json:"pseudo"`
	Generated       bool      `json:"generated"`
	BackgroundColor string    `json:"backgroundColor"`
	BackgroundImage string    `json:"backgroundImage"`
	BoxShadow       string    `json:"boxShadow"`
	BackdropFilter  string    `json:"backdropFilter"`
	Filter          string    `json:"filter"`
	MaskImage       string    `json:"maskImage"`
	Position        string    `json:"position"`
	Inset           string    `json:"inset"`
	Width           string    `json:"width"`
	Height          string    `json:"height"`
	Rect            []float64 `json:"rect"`
}

type LayoutDiagnostics struct {
	Main              *SurfaceDiagnostic  `json:"main"`
	MainAncestors     []SurfaceDiagnostic `json:"mainAncestors"`
	Sidebar           *SurfaceDiagnostic  `json:"sidebar"`
	SidebarAncestors  []SurfaceDiagnostic `json:"sidebarAncestors"`
	BoundarySurfaces  []SurfaceDiagnostic `json:"boundarySurfaces"`
	BoundaryPseudos   []PseudoDiagnostic  `json:"boundaryPseudos"`
	Composer          *SurfaceDiagnostic  `json:"composer"`
	ComposerAncestors []SurfaceDiagnostic `json:"composerAncestors"`
	ComposerSurfaces  []SurfaceDiagnostic `json:"composerSurfaces"`
	ProjectCandidates []SurfaceDiagnostic `json:"projectCandidates"`
}

func NewLive(config Config) (*Live, error) {
	if config.Root == "" {
		return nil, engine.ErrConfiguration
	}
	root, err := filepath.Abs(config.Root)
	if err != nil || root != filepath.Clean(config.Root) {
		return nil, engine.ErrConfiguration
	}
	profile := config.Profile
	if profile == "" {
		profile = filepath.Join(root, "state", "codex-profile")
	}
	profile, err = filepath.Abs(profile)
	if err != nil {
		return nil, engine.ErrConfiguration
	}
	relative, err := filepath.Rel(root, profile)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return nil, engine.ErrConfiguration
	}
	wait := config.LaunchWait
	if wait <= 0 {
		wait = defaultLaunchWait
	}
	return &Live{
		root: root, profile: profile, port: config.Port, launchWait: wait,
		sessions: map[string]*liveSession{},
	}, nil
}

func (adapter *Live) OpenVerifiedSession(ctx context.Context) (engine.Session, error) {
	installation, err := codex.DiscoverInstallation(ctx)
	if err != nil {
		return engine.Session{}, err
	}
	if err := ensureProfile(adapter.profile); err != nil {
		return engine.Session{}, err
	}
	port := adapter.port
	if port == 0 {
		port, err = reserveLoopbackPort()
		if err != nil {
			return engine.Session{}, err
		}
	}
	launchedPID, err := codex.LaunchControlled(ctx, installation, adapter.profile, port)
	if err != nil {
		return engine.Session{}, err
	}
	deadline := time.Now().Add(adapter.launchWait)
	var process codex.ProcessIdentity
	var targets []cdp.Target
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return engine.Session{}, ctx.Err()
		}
		process, err = codex.VerifyListener(ctx, installation, launchedPID, port, adapter.profile)
		if err == nil {
			targets, err = cdp.Discover(ctx, port)
			if err == nil {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		return engine.Session{}, errors.Join(codex.ErrListenerUntrusted, err)
	}
	target, err := cdp.SelectPage(targets)
	if err != nil {
		return engine.Session{}, err
	}
	client, err := cdp.Dial(ctx, target, port)
	if err != nil {
		return engine.Session{}, err
	}
	if err := client.Call(ctx, "Runtime.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return engine.Session{}, err
	}
	if err := client.Call(ctx, "Page.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return engine.Session{}, err
	}
	opaqueID, err := randomSessionID()
	if err != nil {
		client.Close()
		return engine.Session{}, err
	}
	adapter.mu.Lock()
	adapter.sessions[opaqueID] = &liveSession{
		client: client, installation: installation, process: process, port: port, profile: adapter.profile,
	}
	adapter.mu.Unlock()
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

func (adapter *Live) Probe(ctx context.Context, session engine.Session) (engine.RegionReport, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.RegionReport{}, err
	}
	var report engine.RegionReport
	if err := callFunction(ctx, live.client, probeFunction, nil, &report); err != nil {
		return engine.RegionReport{}, err
	}
	return report, nil
}

func (adapter *Live) Capture(ctx context.Context, session engine.Session) (engine.Snapshot, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.Snapshot{}, err
	}
	var snapshot engine.Snapshot
	if err := callFunction(ctx, live.client, captureFunction, nil, &snapshot); err != nil {
		return engine.Snapshot{}, err
	}
	if snapshot.StylePresent &&
		(live.current == nil ||
			!sixDigitID.MatchString(snapshot.ThemePublicID) ||
			snapshot.TemplateVersion != engine.TemplateVersion ||
			snapshot.ThemeVersion == "" ||
			snapshot.ThemePublicID != live.current.ThemePublicID ||
			snapshot.ThemeVersion != live.current.ThemeVersion ||
			snapshot.StyleText != live.current.StyleText) {
		return engine.Snapshot{}, engine.ErrCapabilityBlocked
	}
	if snapshot.StylePresent {
		snapshot.StyleText = live.current.StyleText
		snapshot.BackgroundDataURL = live.current.BackgroundDataURL
	}
	return snapshot, nil
}

func (adapter *Live) Prime(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) error {
	if !sixDigitID.MatchString(compiled.ThemePublicID) ||
		compiled.ThemeVersion == "" ||
		compiled.TemplateVersion != engine.TemplateVersion ||
		compiled.StyleText == "" ||
		!validBackgroundDataURL(compiled.BackgroundDataURL) {
		return engine.ErrConfiguration
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var snapshot engine.Snapshot
	if err := callFunction(ctx, live.client, captureFunction, nil, &snapshot); err != nil {
		return err
	}
	if !snapshot.StylePresent {
		live.current = nil
		return nil
	}
	if snapshot.ThemePublicID != compiled.ThemePublicID ||
		snapshot.ThemeVersion != compiled.ThemeVersion ||
		snapshot.TemplateVersion != compiled.TemplateVersion ||
		snapshot.StyleText != compiled.StyleText {
		return engine.ErrCapabilityBlocked
	}
	copy := compiled
	live.current = &copy
	return nil
}

func (adapter *Live) Apply(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) error {
	if !sixDigitID.MatchString(compiled.ThemePublicID) ||
		compiled.ThemeVersion == "" ||
		compiled.TemplateVersion != engine.TemplateVersion ||
		compiled.StyleText == "" ||
		!validBackgroundDataURL(compiled.BackgroundDataURL) {
		return engine.ErrConfiguration
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var applied bool
	if err := callFunction(ctx, live.client, applyFunction, []any{
		compiled.StyleText, compiled.BackgroundDataURL, compiled.ThemePublicID,
		compiled.ThemeVersion, compiled.TemplateVersion,
	}, &applied); err != nil {
		return err
	}
	if !applied {
		return engine.ErrApplyFailed
	}
	copy := compiled
	live.current = &copy
	return nil
}

func (adapter *Live) Verify(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) (engine.RegionReport, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.RegionReport{}, err
	}
	var report engine.RegionReport
	if err := callFunction(ctx, live.client, verifyFunction, nil, &report); err != nil {
		return engine.RegionReport{}, err
	}
	return report, nil
}

func (adapter *Live) Restore(ctx context.Context, session engine.Session, snapshot engine.Snapshot) error {
	if !snapshot.StylePresent {
		return adapter.RestoreOfficial(ctx, session)
	}
	if !sixDigitID.MatchString(snapshot.ThemePublicID) ||
		snapshot.ThemeVersion == "" ||
		snapshot.TemplateVersion != engine.TemplateVersion ||
		snapshot.StyleText == "" ||
		!validBackgroundDataURL(snapshot.BackgroundDataURL) {
		return engine.ErrRollbackFailed
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var restored bool
	if err := callFunction(ctx, live.client, applyFunction, []any{
		snapshot.StyleText, snapshot.BackgroundDataURL, snapshot.ThemePublicID,
		snapshot.ThemeVersion, snapshot.TemplateVersion,
	}, &restored); err != nil {
		return err
	}
	if !restored {
		return engine.ErrRollbackFailed
	}
	live.current = &engine.CompiledTheme{
		ThemePublicID: snapshot.ThemePublicID, ThemeVersion: snapshot.ThemeVersion,
		TemplateVersion: snapshot.TemplateVersion, StyleText: snapshot.StyleText,
		BackgroundDataURL: snapshot.BackgroundDataURL,
	}
	return nil
}

func (adapter *Live) RestoreOfficial(ctx context.Context, session engine.Session) error {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var restored bool
	if err := callFunction(ctx, live.client, restoreFunction, nil, &restored); err != nil {
		return err
	}
	if !restored {
		return engine.ErrRestoreFailed
	}
	live.current = nil
	return nil
}

func (adapter *Live) VerifyOfficial(ctx context.Context, session engine.Session) error {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var official bool
	if err := callFunction(ctx, live.client, officialFunction, nil, &official); err != nil {
		return err
	}
	if !official {
		return engine.ErrRestoreFailed
	}
	return nil
}

func (adapter *Live) CapturePNG(ctx context.Context, session engine.Session) ([]byte, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := live.client.Call(ctx, "Page.captureScreenshot", map[string]any{
		"format": "png", "fromSurface": true, "captureBeyondViewport": false,
	}, &response); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.Strict().DecodeString(response.Data)
	if err != nil || len(content) < 8 || len(content) > 25*1024*1024 {
		return nil, engine.ErrVerifyFailed
	}
	return content, nil
}

// FixedLayoutDiagnostics returns style and geometry only for the fixed theme
// surfaces. It deliberately excludes text, attributes, URLs, and user content.
func (adapter *Live) FixedLayoutDiagnostics(ctx context.Context, session engine.Session) (LayoutDiagnostics, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return LayoutDiagnostics{}, err
	}
	var diagnostics LayoutDiagnostics
	if err := callFunction(ctx, live.client, fixedLayoutDiagnosticsFunction, nil, &diagnostics); err != nil {
		return LayoutDiagnostics{}, err
	}
	return diagnostics, nil
}

func (adapter *Live) Close(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	delete(adapter.sessions, session.OpaqueID)
	adapter.mu.Unlock()
	if live == nil {
		return nil
	}
	return live.client.Close()
}

// StopOwned closes and terminates only the exact controlled process created for
// this session. It is intended for bounded platform QA and never falls back to a
// process-name match.
func (adapter *Live) StopOwned(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	delete(adapter.sessions, session.OpaqueID)
	adapter.mu.Unlock()
	if live == nil {
		return codex.ErrListenerUntrusted
	}
	stopErr := codex.StopOwnedProcess(
		ctx,
		live.installation,
		live.process,
		live.port,
		live.profile,
	)
	closeErr := live.client.Close()
	return errors.Join(closeErr, stopErr)
}

func (adapter *Live) verifiedLiveSession(ctx context.Context, session engine.Session) (*liveSession, error) {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	adapter.mu.Unlock()
	if live == nil {
		return nil, codex.ErrListenerUntrusted
	}
	var process codex.ProcessIdentity
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		process, err = codex.VerifyListener(
			ctx, live.installation, live.process.ProcessID, live.port, live.profile,
		)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil ||
		process.ProcessID != live.process.ProcessID ||
		process.ProcessStartID != live.process.ProcessStartID ||
		process.ExecutableSHA256 != live.process.ExecutableSHA256 {
		return nil, errors.Join(codex.ErrListenerUntrusted, err)
	}
	return live, nil
}

func callFunction(ctx context.Context, client *cdp.Client, function string, arguments []any, output any) error {
	var global evaluateResult
	if err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": "globalThis", "returnByValue": false,
	}, &global); err != nil || global.ExceptionDetails != nil || global.Result.ObjectID == "" {
		return engine.ErrVerifyFailed
	}
	callArguments := make([]map[string]any, 0, len(arguments))
	for _, value := range arguments {
		callArguments = append(callArguments, map[string]any{"value": value})
	}
	var result callResult
	if err := client.Call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": global.Result.ObjectID, "functionDeclaration": function,
		"arguments": callArguments, "returnByValue": true, "awaitPromise": false,
	}, &result); err != nil || result.ExceptionDetails != nil {
		return engine.ErrVerifyFailed
	}
	if len(result.Result.Value) == 0 {
		return engine.ErrVerifyFailed
	}
	if err := jsonUnmarshal(result.Result.Value, output); err != nil {
		return engine.ErrVerifyFailed
	}
	return nil
}

func jsonUnmarshal(raw []byte, output any) error {
	return json.Unmarshal(raw, output)
}

func ensureProfile(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return engine.ErrStateUnsafe
		}
		return os.Chmod(path, 0o700)
	} else if !errors.Is(err, os.ErrNotExist) {
		return engine.ErrStateUnsafe
	}
	return os.MkdirAll(path, 0o700)
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("%w: reserve loopback port", codex.ErrLaunchFailed)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(raw), nil
}

func validBackgroundDataURL(value string) bool {
	for _, prefix := range []string{
		"data:image/png;base64,",
		"data:image/jpeg;base64,",
		"data:image/webp;base64,",
	} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		encoded := strings.TrimPrefix(value, prefix)
		if len(encoded) < 4 || len(encoded) > 20*1024*1024 {
			return false
		}
		_, err := base64.StdEncoding.Strict().DecodeString(encoded)
		return err == nil
	}
	return false
}

const probeFunction = `function () {
  const status = (node, optional) => node ? "pass" : (optional ? "not_present" : "fail");
  const style = document.querySelectorAll("#codex-skin-theme-v1");
  const root = document.documentElement;
  const suggestions = document.querySelector(".group\\/home-suggestions");
  const topFade = document.querySelector(".app-shell-main-content-top-fade");
  const main = document.querySelector("main.main-surface");
  const composerUtilityBar = main?.querySelector('[class*="_homeUtilityBar_"]') || null;
  const project = document.querySelector('main.main-surface button[class*="_utilityBarLabel_"]') ||
    document.querySelector('main.main-surface div.sticky:has(input[type="text"],textarea)') ||
    document.querySelector('[data-testid*="project" i]');
  return {
    styleMarkerCount: style.length,
    templateVersion: Number(root.getAttribute("data-codex-skin-template") || 0),
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    backgroundLoaded: false,
    regions: {
      home: status(main, false),
      mainBoundary: status(main, false),
      sidebar: status(document.querySelector("aside.app-shell-left-panel"), false),
      composerUtilityBar: status(composerUtilityBar, false),
      topFade: status(topFade, false),
      suggestionCards: status(suggestions, true),
      projectPicker: status(project, true),
      composer: status(document.querySelector(".composer-surface-chrome"), false)
    }
  };
}`

const captureFunction = `function () {
  const styles = document.querySelectorAll("#codex-skin-theme-v1");
  if (styles.length > 1) throw new Error("invalid marker count");
  const root = document.documentElement;
  const style = styles[0] || null;
  return {
    stylePresent: Boolean(style),
    styleText: style ? style.textContent : "",
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    themeVersion: root.getAttribute("data-codex-skin-theme-version") || "",
    templateVersion: Number(root.getAttribute("data-codex-skin-template") || 0)
  };
}`

const applyFunction = `function (styleText, backgroundDataURL, themeId, themeVersion, templateVersion) {
  for (const old of document.querySelectorAll("#codex-skin-theme-v1")) old.remove();
  const style = document.createElement("style");
  style.id = "codex-skin-theme-v1";
  style.type = "text/css";
  style.textContent = styleText;
  (document.head || document.documentElement).appendChild(style);
  const root = document.documentElement;
  const previousURL = root.getAttribute("data-codex-skin-background-url");
  if (previousURL && previousURL.startsWith("blob:")) URL.revokeObjectURL(previousURL);
  const comma = backgroundDataURL.indexOf(",");
  const mediaType = backgroundDataURL.slice(5, backgroundDataURL.indexOf(";base64,"));
  const binary = atob(backgroundDataURL.slice(comma + 1));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  const backgroundURL = URL.createObjectURL(new Blob([bytes], { type: mediaType }));
  root.style.setProperty("--cs-background-image", 'url("' + backgroundURL + '")');
  root.setAttribute("data-codex-skin-background-url", backgroundURL);
  root.setAttribute("data-codex-skin", "active");
  root.setAttribute("data-codex-skin-theme", themeId);
  root.setAttribute("data-codex-skin-theme-version", themeVersion);
  root.setAttribute("data-codex-skin-template", String(templateVersion));
  return document.querySelectorAll("#codex-skin-theme-v1").length === 1;
}`

const verifyFunction = `function () {
  const visible = (node) => {
    if (!node) return false;
    const box = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return box.width > 1 && box.height > 1 && style.display !== "none" && style.visibility !== "hidden";
  };
  const optional = (node, pass) => !node ? "not_present" : (pass ? "pass" : "fail");
  const root = document.documentElement;
  const styles = document.querySelectorAll("#codex-skin-theme-v1");
  const style = styles[0] || null;
  const main = document.querySelector("main.main-surface");
  const sidebar = document.querySelector("aside.app-shell-left-panel");
  const composer = document.querySelector(".composer-surface-chrome");
  const suggestions = document.querySelector(".group\\/home-suggestions");
  const topFade = document.querySelector(".app-shell-main-content-top-fade");
  const composerUtilityBar = main?.querySelector('[class*="_homeUtilityBar_"]') || null;
  const cards = suggestions ? [...suggestions.querySelectorAll("button")].filter(visible) : [];
  const project = document.querySelector('main.main-surface button[class*="_utilityBarLabel_"]') ||
    document.querySelector('main.main-surface div.sticky:has(input[type="text"],textarea)') ||
    document.querySelector('[data-testid*="project" i]');
  const background = getComputedStyle(document.body).backgroundImage || "";
  const rootBackground = getComputedStyle(root).getPropertyValue("--cs-background-image") || "";
  const topFadeStyle = topFade ? getComputedStyle(topFade) : null;
  const topFadeNeutralized = Boolean(topFadeStyle &&
    topFadeStyle.backgroundImage === "none" &&
    topFadeStyle.backdropFilter === "none" &&
    Number(topFadeStyle.opacity) === 0);
  const mainRect = main?.getBoundingClientRect() || null;
  const sidebarRect = sidebar?.getBoundingClientRect() || null;
  const resizeHandle = sidebar?.querySelector('[class~="cursor-col-resize"]') || null;
  const resizeHandleRect = resizeHandle?.getBoundingClientRect() || null;
  const resizeHandleStyle = resizeHandle ? getComputedStyle(resizeHandle) : null;
  const sidebarAfterStyle = sidebar ? getComputedStyle(sidebar, "::after") : null;
  const resizeHandleIntact = Boolean(resizeHandleRect && resizeHandleStyle &&
    resizeHandleRect.width >= 8 &&
    resizeHandleRect.height >= sidebarRect.height * 0.8 &&
    Math.abs(resizeHandleRect.left + resizeHandleRect.width / 2 - sidebarRect.right) <= 4 &&
    resizeHandleStyle.cursor.includes("col-resize"));
  const mainBoundaryNeutralized = Boolean(main && mainRect && sidebarRect && sidebarAfterStyle &&
    getComputedStyle(main).boxShadow === "none" &&
    Math.abs(sidebarRect.right - mainRect.left) <= 1 &&
    (sidebarAfterStyle.content === "none" || sidebarAfterStyle.display === "none") &&
    resizeHandleIntact);
  const composerUtilityStyle = composerUtilityBar ? getComputedStyle(composerUtilityBar) : null;
  const composerUtilityNeutralized = Boolean(composerUtilityStyle &&
    composerUtilityStyle.backgroundColor !== "rgb(246, 246, 246)" &&
    composerUtilityStyle.borderTopWidth !== "0px");
  return {
    styleMarkerCount: styles.length,
    templateVersion: Number(root.getAttribute("data-codex-skin-template") || 0),
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    backgroundLoaded: Boolean(style && style.sheet && style.sheet.cssRules.length > 0 &&
      rootBackground.includes("blob:") &&
      root.getAttribute("data-codex-skin-background-url")?.startsWith("blob:")),
    backgroundTokenSet: rootBackground.includes("blob:"),
    bodyBackgroundSet: background.includes("blob:"),
    regions: {
      home: visible(main) ? "pass" : "fail",
      mainBoundary: mainBoundaryNeutralized ? "pass" : "fail",
      sidebar: visible(sidebar) ? "pass" : "fail",
      composerUtilityBar: composerUtilityNeutralized ? "pass" : "fail",
      topFade: topFadeNeutralized ? "pass" : "fail",
      suggestionCards: optional(suggestions, cards.length > 0),
      projectPicker: optional(project, visible(project)),
      composer: visible(composer) ? "pass" : "fail"
    }
  };
}`

const restoreFunction = `function () {
  for (const style of document.querySelectorAll("#codex-skin-theme-v1")) style.remove();
  const root = document.documentElement;
  const backgroundURL = root.getAttribute("data-codex-skin-background-url");
  if (backgroundURL && backgroundURL.startsWith("blob:")) URL.revokeObjectURL(backgroundURL);
  root.removeAttribute("data-codex-skin");
  root.removeAttribute("data-codex-skin-theme");
  root.removeAttribute("data-codex-skin-theme-version");
  root.removeAttribute("data-codex-skin-template");
  root.removeAttribute("data-codex-skin-background-url");
  root.style.removeProperty("--cs-background-image");
  return document.querySelectorAll("#codex-skin-theme-v1").length === 0;
}`

const officialFunction = `function () {
  const root = document.documentElement;
  return document.querySelectorAll("#codex-skin-theme-v1").length === 0 &&
    !root.hasAttribute("data-codex-skin") &&
    !root.hasAttribute("data-codex-skin-theme") &&
    !root.hasAttribute("data-codex-skin-theme-version") &&
    !root.hasAttribute("data-codex-skin-template") &&
    !root.hasAttribute("data-codex-skin-background-url") &&
    !root.style.getPropertyValue("--cs-background-image");
}`

const fixedLayoutDiagnosticsFunction = `function () {
  const visible = (node) => {
    if (!node) return false;
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const describe = (node) => {
    if (!node) return null;
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return {
      tag: node.tagName.toLowerCase(),
      classes: typeof node.className === "string" ? node.className : "",
      backgroundColor: style.backgroundColor,
      backgroundImage: style.backgroundImage,
      boxShadow: style.boxShadow,
      border: style.border,
      display: style.display,
      position: style.position,
      margin: style.margin,
      padding: style.padding,
      gap: style.gap,
      rect: [rect.x, rect.y, rect.width, rect.height]
    };
  };
  const describePseudo = (node, pseudo) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node, pseudo);
    const value = (property) => String(property || "");
    return {
      tag: node.tagName.toLowerCase(),
      classes: typeof node.className === "string" ? node.className : "",
      pseudo,
      generated: value(style.content) !== "none",
      backgroundColor: value(style.backgroundColor),
      backgroundImage: value(style.backgroundImage),
      boxShadow: value(style.boxShadow),
      backdropFilter: value(style.backdropFilter),
      filter: value(style.filter),
      maskImage: value(style.maskImage),
      position: value(style.position),
      inset: value(style.inset),
      width: value(style.width),
      height: value(style.height),
      rect: [rect.x, rect.y, rect.width, rect.height]
    };
  };
  const main = document.querySelector("main.main-surface");
  const sidebar = document.querySelector("aside.app-shell-left-panel");
  const composer = document.querySelector(".composer-surface-chrome");
  const composerRect = composer?.getBoundingClientRect() || null;
  const composerSurfaces = main && composerRect ? [...main.querySelectorAll("div,form,section")]
    .filter((node) => {
      if (!visible(node) || node === composer || composer.contains(node)) return false;
      const rect = node.getBoundingClientRect();
      const style = getComputedStyle(node);
      return rect.width > composerRect.width * 0.5 &&
        rect.bottom >= composerRect.top - 140 &&
        rect.top <= composerRect.bottom &&
        style.backgroundColor !== "rgba(0, 0, 0, 0)";
    })
    .slice(0, 24).map(describe) : [];
  const projectCandidates = main ? [...main.querySelectorAll("button")]
    .filter((node) => visible(node) &&
      (String(node.className).includes("utility") ||
        (composerRect && node.getBoundingClientRect().bottom >= composerRect.top - 140 &&
          node.getBoundingClientRect().top <= composerRect.top + 40)))
    .slice(0, 16).map(describe) : [];
  const ancestors = [];
  let current = composer?.parentElement || null;
  for (let index = 0; current && index < 6; index += 1, current = current.parentElement) {
    ancestors.push(describe(current));
  }
  const ancestorChain = (node) => {
    const result = [];
    let current = node?.parentElement || null;
    for (let index = 0; current && index < 8; index += 1, current = current.parentElement) {
      result.push(describe(current));
    }
    return result;
  };
  const boundarySurfaces = [];
  const boundaryPseudos = [];
  const seen = new Set();
  const mainRect = main?.getBoundingClientRect() || null;
  const sidebarRect = sidebar?.getBoundingClientRect() || null;
  if (mainRect && sidebarRect) {
    const sampleY = Math.min(window.innerHeight - 2, Math.max(2, window.innerHeight / 2));
    for (const offset of [-64, -48, -32, -24, -16, -8, -1, 1]) {
      const x = Math.min(window.innerWidth - 1, Math.max(1, sidebarRect.right + offset));
      for (const node of document.elementsFromPoint(x, sampleY)) {
        if (seen.has(node)) continue;
        seen.add(node);
        boundarySurfaces.push(describe(node));
        for (const pseudo of ["::before", "::after"]) {
          const item = describePseudo(node, pseudo);
          const active = (value) => value !== "" && value !== "none";
          if (item.generated || item.backgroundColor !== "rgba(0, 0, 0, 0)" ||
              active(item.backgroundImage) || active(item.boxShadow) ||
              active(item.filter) || active(item.backdropFilter) ||
              active(item.maskImage)) {
            boundaryPseudos.push(item);
          }
        }
      }
    }
  }
  return {
    main: describe(main),
    mainAncestors: ancestorChain(main),
    sidebar: describe(sidebar),
    sidebarAncestors: ancestorChain(sidebar),
    boundarySurfaces,
    boundaryPseudos,
    composer: describe(composer),
    composerAncestors: ancestors,
    composerSurfaces,
    projectCandidates
  };
}`
