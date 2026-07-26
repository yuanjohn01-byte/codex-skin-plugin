//go:build darwin

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
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
