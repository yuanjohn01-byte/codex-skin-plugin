//go:build darwin && realcodex

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

func TestCurrentOfficialCodexLoopbackSession(t *testing.T) {
	if os.Getenv("CODEX_SKIN_REAL_CODEX") != "1" {
		t.Skip("set CODEX_SKIN_REAL_CODEX=1 for the local Gate B CDP probe")
	}
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if qaRoot := os.Getenv("CODEX_SKIN_QA_ROOT"); qaRoot != "" {
		if err := os.MkdirAll(qaRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		created, makeErr := os.MkdirTemp(qaRoot, "controlled-profile-")
		if makeErr != nil {
			t.Fatal(makeErr)
		}
		root = created
	}
	if _, err := engine.OpenStore(root, filepath.Join(t.TempDir(), "plugin-cache")); err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{Root: root, LaunchWait: 45 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := live.OpenVerifiedSession(ctx)
	if err != nil {
		t.Fatalf("OpenVerifiedSession() error = %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer stopCancel()
		if err := live.StopOwned(stopCtx, session); err != nil {
			t.Errorf("StopOwned() error = %v", err)
		}
	}()
	var report engine.RegionReport
	for attempts := 0; attempts < 80; attempts++ {
		report, err = live.Probe(ctx, session)
		if err == nil &&
			report.Regions["home"] == engine.RegionPass &&
			report.Regions["sidebar"] == engine.RegionPass &&
			report.Regions["mainBoundary"] == engine.RegionPass &&
			report.Regions["composer"] == engine.RegionPass &&
			report.Regions["composerUtilityBar"] == engine.RegionPass {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if session.Identity.Platform != "macos" ||
		session.Identity.AppIdentifier != "com.openai.codex" ||
		session.Identity.Publisher != "2DC432GLL2" ||
		session.Identity.ProcessID < 1 {
		t.Fatalf("session identity = %#v", session.Identity)
	}
	if report.Regions["home"] != engine.RegionPass ||
		report.Regions["sidebar"] != engine.RegionPass ||
		report.Regions["mainBoundary"] != engine.RegionPass ||
		report.Regions["composer"] != engine.RegionPass ||
		report.Regions["composerUtilityBar"] != engine.RegionPass ||
		report.Regions["topFade"] != engine.RegionPass {
		live.mu.Lock()
		active := live.sessions[session.OpaqueID]
		live.mu.Unlock()
		if active != nil {
			var diagnostics map[string]any
			_ = callFunction(ctx, active.client, `function () {
			  return {
			    location: location.href,
			    readyState: document.readyState,
			    title: document.title,
			    bodyChildren: document.body ? document.body.children.length : -1,
			    bodyTextLength: document.body ? document.body.innerText.length : -1
			  };
			}`, nil, &diagnostics)
			t.Logf("renderer diagnostics=%v", diagnostics)
		}
		if screenshotPath := os.Getenv("CODEX_SKIN_SCREENSHOT_PATH"); screenshotPath != "" {
			if content, captureErr := live.CapturePNG(ctx, session); captureErr == nil {
				if writeErr := os.WriteFile(screenshotPath, content, 0o600); writeErr != nil {
					t.Logf("screenshot write error=%v", writeErr)
				}
			} else {
				t.Logf("screenshot capture error=%v", captureErr)
			}
		}
		t.Fatalf("core capability report = %#v", report)
	}
	if os.Getenv("CODEX_SKIN_DOM_DIAGNOSTICS") == "1" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(15 * time.Second):
		}
		live.mu.Lock()
		active := live.sessions[session.OpaqueID]
		live.mu.Unlock()
		if active == nil {
			t.Fatal("verified session disappeared before DOM diagnostics")
		}
		var diagnostics map[string]any
		if err := callFunction(ctx, active.client, `function () {
		  const visible = (node) => {
		    if (!node) return false;
		    const box = node.getBoundingClientRect();
		    return box.width > 1 && box.height > 1 && getComputedStyle(node).display !== "none";
		  };
		  const describe = (node) => {
		    if (!node) return null;
		    const style = getComputedStyle(node);
		    const box = node.getBoundingClientRect();
		    return {
		      tag: node.tagName.toLowerCase(),
		      classes: typeof node.className === "string" ? node.className : "",
		      role: node.getAttribute("role") || "",
		      testId: node.getAttribute("data-testid") || "",
		      color: style.color,
		      background: style.backgroundColor,
		      fontSize: style.fontSize,
		      backdropFilter: style.backdropFilter,
		      filter: style.filter,
		      boxShadow: style.boxShadow,
		      border: style.border,
		      rect: [Math.round(box.x), Math.round(box.y), Math.round(box.width), Math.round(box.height)]
		    };
		  };
		  const describePseudo = (node, pseudo) => {
		    if (!node) return null;
		    const style = getComputedStyle(node, pseudo);
		    return {
		      content: style.content,
		      display: style.display,
		      background: style.backgroundColor,
		      backgroundImage: style.backgroundImage
		    };
		  };
		  const main = document.querySelector("main.main-surface");
		  const sidebar = document.querySelector("aside.app-shell-left-panel");
		  const composer = document.querySelector(".composer-surface-chrome");
		  const input = main?.querySelector('input[type="text"],textarea') || null;
		  const sticky = input?.closest("div.sticky") || null;
		  const headings = main ? [...main.querySelectorAll("h1,h2,h3,[role=heading]")].filter(visible) : [];
		  const largeText = main ? [...main.querySelectorAll("div,span,p")]
		    .filter((node) => visible(node) && node.innerText.trim() && !composer?.contains(node))
		    .sort((left, right) => parseFloat(getComputedStyle(right).fontSize) -
		      parseFloat(getComputedStyle(left).fontSize))[0] : null;
		  const sidebarLeaves = sidebar ? [...sidebar.querySelectorAll("span,p,a,button,div")]
		    .filter((node) => visible(node) && node.children.length === 0 && node.innerText.trim())
		    .slice(0, 12).map(describe) : [];
		  return {
		    heading: describe(headings[0] || null),
		    largeText: describe(largeText || null),
		    lowerButtons: main ? [...main.querySelectorAll("button")]
		      .filter((node) => visible(node) && node.getBoundingClientRect().y > innerHeight * 0.65)
		      .slice(0, 20).map(describe) : [],
		    topSurfaces: [...document.body.querySelectorAll("*")]
		      .filter((node) => {
		        if (!visible(node)) return false;
		        const box = node.getBoundingClientRect();
		        return box.width > innerWidth * 0.4 && box.y < 80 && box.bottom > 35;
		      })
		      .slice(0, 30).map(describe),
		    main: describe(main),
		    sidebar: describe(sidebar),
		    sidebarLeaves,
		    composer: describe(composer),
		    composerBefore: describePseudo(composer, "::before"),
		    composerAfter: describePseudo(composer, "::after"),
		    composerChildren: composer ? [...composer.children].slice(0, 12).map(describe) : [],
		    composerButtons: composer ? [...composer.querySelectorAll("button")]
		      .filter(visible).slice(0, 16).map(describe) : [],
		    composerLightSurfaces: composer ? [...composer.querySelectorAll("*")]
		      .filter((node) => visible(node) && /rgb\\(255, 255, 255\\)|0\\.99999/.test(
		        getComputedStyle(node).backgroundColor))
		      .slice(0, 12).map(describe) : [],
		    composerParent: describe(composer?.parentElement || null),
		    composerAncestors: composer ? [composer.parentElement, composer.parentElement?.parentElement,
		      composer.parentElement?.parentElement?.parentElement].map(describe) : [],
		    composerParentChildren: composer?.parentElement ?
		      [...composer.parentElement.children].slice(0, 8).map(describe) : [],
		    sticky: describe(sticky),
		    stickyChildren: sticky ? [...sticky.children].slice(0, 8).map(describe) : []
		  };
		}`, nil, &diagnostics); err != nil {
			t.Fatalf("DOM diagnostics error = %v", err)
		}
		t.Logf("sanitized main=%#v sidebar=%#v", diagnostics["main"], diagnostics["sidebar"])
	}
	t.Logf(
		"verified version=%s pid=%d regions=%v",
		session.Identity.Version,
		session.Identity.ProcessID,
		report.Regions,
	)
}

func TestCurrentOfficialCodexCurrentProfileCapabilities(t *testing.T) {
	if os.Getenv("CODEX_SKIN_REAL_CODEX") != "1" {
		t.Skip("set CODEX_SKIN_REAL_CODEX=1 for the local current-profile Gate B probe")
	}
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{
		Root: root, CurrentProfile: true, LaunchWait: 45 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := live.OpenVerifiedSession(ctx)
	if err != nil {
		t.Fatalf("OpenVerifiedSession() error = %v", err)
	}
	defer live.Close(context.Background(), session)
	report, err := live.WaitForCapabilities(ctx, session)
	t.Logf("current-profile capability report = %#v", report)
	if os.Getenv("CODEX_SKIN_LAYOUT_DIAGNOSTICS") == "1" {
		diagnostics, diagnosticErr := live.FixedLayoutDiagnostics(ctx, session)
		if diagnosticErr != nil {
			t.Fatalf("fixed layout diagnostics error = %v", diagnosticErr)
		}
		t.Logf(
			"sanitized fixed-layout main=%#v mainChildren=%#v mainSurfaces=%#v sidebar=%#v sidebarCandidates=%#v",
			diagnostics.Main,
			diagnostics.MainChildren,
			diagnostics.MainSurfaces,
			diagnostics.Sidebar,
			diagnostics.SidebarCandidates[:min(3, len(diagnostics.SidebarCandidates))],
		)
	}
	if os.Getenv("CODEX_SKIN_ACTIVITY_DIAGNOSTICS") == "1" {
		live.mu.Lock()
		active := live.sessions[session.OpaqueID]
		live.mu.Unlock()
		if active == nil {
			t.Fatal("verified session disappeared before activity diagnostics")
		}
		var diagnostics []map[string]any
		if err := callFunction(ctx, active.client, `function () {
		  const visible = (node) => {
		    const rect = node.getBoundingClientRect();
		    const style = getComputedStyle(node);
		    return rect.width > 1 && rect.height > 1 &&
		      style.display !== "none" && style.visibility !== "hidden";
		  };
		  const describe = (node) => {
		    const rect = node.getBoundingClientRect();
		    const style = getComputedStyle(node);
		    const parent = node.parentElement;
		    const child = [...node.children].find(visible) || null;
		    return {
		      tag: node.tagName.toLowerCase(),
		      classes: typeof node.className === "string" ? node.className : "",
		      role: node.getAttribute("role") || "",
		      ariaExpanded: node.hasAttribute("aria-expanded")
		        ? node.getAttribute("aria-expanded") : "",
		      color: style.color,
		      textShadow: style.textShadow,
		      rect: [Math.round(rect.x), Math.round(rect.y),
		        Math.round(rect.width), Math.round(rect.height)],
		      parentTag: parent ? parent.tagName.toLowerCase() : "",
		      parentClasses: parent && typeof parent.className === "string"
		        ? parent.className : "",
		      childTag: child ? child.tagName.toLowerCase() : "",
		      childClasses: child && typeof child.className === "string"
		        ? child.className : "",
		      childColor: child ? getComputedStyle(child).color : ""
		    };
		  };
		  const main = document.querySelector('main[data-codex-skin-main="true"]');
		  if (!main) return [];
		  return [...main.querySelectorAll('button,[role="button"],[aria-expanded]')]
		    .filter((node) => visible(node) && node.querySelector("svg"))
		    .slice(0, 80)
		    .map(describe);
		}`, nil, &diagnostics); err != nil {
			t.Fatalf("activity diagnostics error = %v", err)
		}
		t.Logf("sanitized activity candidates = %#v", diagnostics)
	}
	if os.Getenv("CODEX_SKIN_BACKGROUND_DIAGNOSTICS") == "1" {
		live.mu.Lock()
		active := live.sessions[session.OpaqueID]
		live.mu.Unlock()
		if active == nil {
			t.Fatal("verified session disappeared before background diagnostics")
		}
		var diagnostics struct {
			StyleMarkerCount       int  `json:"styleMarkerCount"`
			StyleRuleCount         int  `json:"styleRuleCount"`
			BackgroundRulePresent  bool `json:"backgroundRulePresent"`
			RootActive             bool `json:"rootActive"`
			BackgroundURLAttribute bool `json:"backgroundUrlAttribute"`
			ThemeAttribute         bool `json:"themeAttribute"`
			TemplateAttribute      bool `json:"templateAttribute"`
			InlineTokenPresent     bool `json:"inlineTokenPresent"`
			ComputedTokenPresent   bool `json:"computedTokenPresent"`
			MainBackgroundResolved bool `json:"mainBackgroundResolved"`
			BodyTokenInherited     bool `json:"bodyTokenInherited"`
			BodyGradientPresent    bool `json:"bodyGradientPresent"`
			BodyBackgroundResolved bool `json:"bodyBackgroundResolved"`
			ShorthandResolves      bool `json:"shorthandResolves"`
			LonghandResolves       bool `json:"longhandResolves"`
		}
		if err := callFunction(ctx, active.client, `function () {
		  const root = document.documentElement;
		  const style = document.querySelector("#codex-skin-theme-v1");
		  const rules = style?.sheet ? [...style.sheet.cssRules] : [];
		  const main = document.querySelector('main[data-codex-skin-main="true"]');
		  const body = document.body;
		  const testBackground = (longhand) => {
		    const node = document.createElement("div");
		    node.style.position = "fixed";
		    node.style.width = "1px";
		    node.style.height = "1px";
		    node.style.visibility = "hidden";
		    if (longhand) {
		      node.style.backgroundImage =
		        "linear-gradient(rgb(0 0 0 / .2), rgb(0 0 0 / .2)), var(--cs-background-image)";
		      node.style.backgroundPosition = "0 0, center";
		      node.style.backgroundSize = "auto, cover";
		      node.style.backgroundAttachment = "scroll, fixed";
		    } else {
		      node.style.background =
		        "linear-gradient(rgb(0 0 0 / .2), rgb(0 0 0 / .2)), " +
		        "var(--cs-background-image) center / cover fixed";
		    }
		    body.appendChild(node);
		    const resolved = getComputedStyle(node).backgroundImage.includes("blob:");
		    node.remove();
		    return resolved;
		  };
		  return {
		    styleMarkerCount: document.querySelectorAll("#codex-skin-theme-v1").length,
		    styleRuleCount: rules.length,
		    backgroundRulePresent: rules.some((rule) =>
		      rule.cssText.includes("--cs-background-image") && rule.cssText.includes("blob:")),
		    rootActive: root.getAttribute("data-codex-skin") === "active",
		    backgroundUrlAttribute: String(
		      root.getAttribute("data-codex-skin-background-url") || ""
		    ).startsWith("blob:"),
		    themeAttribute: /^\d{6}$/.test(root.getAttribute("data-codex-skin-theme") || ""),
		    templateAttribute: /^\d+$/.test(root.getAttribute("data-codex-skin-template") || ""),
		    inlineTokenPresent: root.style.getPropertyValue("--cs-background-image").includes("blob:"),
		    computedTokenPresent: getComputedStyle(root)
		      .getPropertyValue("--cs-background-image").includes("blob:"),
		    mainBackgroundResolved: Boolean(main && getComputedStyle(main)
		      .backgroundImage.includes("blob:")),
		    bodyTokenInherited: Boolean(body && getComputedStyle(body)
		      .getPropertyValue("--cs-background-image").includes("blob:")),
		    bodyGradientPresent: Boolean(body && getComputedStyle(body)
		      .backgroundImage.includes("linear-gradient")),
		    bodyBackgroundResolved: Boolean(body && getComputedStyle(body)
		      .backgroundImage.includes("blob:")),
		    shorthandResolves: testBackground(false),
		    longhandResolves: testBackground(true)
		  };
		}`, nil, &diagnostics); err != nil {
			t.Fatalf("background diagnostics error = %v", err)
		}
		t.Logf("background diagnostics = %#v", diagnostics)
		var appliedReport engine.RegionReport
		if err := callFunction(
			ctx,
			active.client,
			verifyFunction,
			nil,
			&appliedReport,
		); err != nil {
			t.Fatalf("applied theme verification error = %v", err)
		}
		t.Logf("applied theme verification report = %#v", appliedReport)
		if !appliedReport.BackgroundLoaded ||
			!appliedReport.BackgroundTokenSet ||
			!appliedReport.BodyBackgroundSet {
			t.Fatalf("applied theme background did not remain resolved")
		}
	}
	if err != nil {
		t.Fatalf("WaitForCapabilities() error = %v", err)
	}
	if !engine.CapabilitiesAllowApply(report) {
		t.Fatalf("current-profile capability report is not eligible for apply")
	}
}

func TestCurrentOfficialCodexCachedThemeCandidate(t *testing.T) {
	cachedRoot := os.Getenv("CODEX_SKIN_REAL_CODEX_APPLY_CACHED_ROOT")
	if cachedRoot == "" {
		t.Skip("set CODEX_SKIN_REAL_CODEX_APPLY_CACHED_ROOT for the local candidate probe")
	}
	verified, err := theme.VerifyCached(cachedRoot)
	if err != nil {
		t.Fatalf("VerifyCached() error = %v", err)
	}
	compiled, err := engine.CompileTheme(verified, cachedRoot)
	if err != nil {
		t.Fatalf("CompileTheme() error = %v", err)
	}
	root := filepath.Join(t.TempDir(), "CodexSkin")
	live, err := NewLive(Config{Root: root, CurrentProfile: true, LaunchWait: 45 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := live.OpenVerifiedSession(ctx)
	if err != nil {
		t.Fatalf("OpenVerifiedSession() error = %v", err)
	}
	defer live.Close(context.Background(), session)
	if err := live.Prime(ctx, session, compiled); err != nil {
		t.Fatalf("Prime() error = %v", err)
	}
	snapshot, err := live.Capture(ctx, session)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	restore := true
	defer func() {
		if restore {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer restoreCancel()
			if restoreErr := live.Restore(restoreCtx, session, snapshot); restoreErr != nil {
				t.Errorf("Restore() error = %v", restoreErr)
			}
		}
	}()
	if err := live.Apply(ctx, session, compiled); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	report, err := live.Verify(ctx, session, compiled)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	t.Logf("candidate report = %#v", report)
	if report.StyleMarkerCount != 1 ||
		report.TemplateVersion != engine.TemplateVersion ||
		report.ThemePublicID != compiled.ThemePublicID ||
		!report.BackgroundLoaded ||
		!engine.CapabilitiesAllowApply(report) {
		t.Fatalf("candidate report failed closed = %#v", report)
	}
	restore = false
}
