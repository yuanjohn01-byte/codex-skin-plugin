package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/renderer"
)

const controllerStateKey = "__CODEX_SKIN_RENDERER_CONTROLLER_V2__"

var controllerIdentifier = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

type controllerRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	TargetID      string `json:"targetId"`
	Identifier    string `json:"identifier"`
}

func (adapter *Live) installController(
	ctx context.Context,
	live *liveSession,
	compiled engine.CompiledTheme,
) error {
	if compiled.AppearanceMode != "dark" && compiled.AppearanceMode != "light" {
		return engine.ErrConfiguration
	}
	if err := adapter.removeControllerBootstrap(ctx, live); err != nil {
		return err
	}
	selectors, err := renderer.SelectorMap()
	if err != nil {
		return engine.ErrConfiguration
	}
	arguments := []any{
		compiled.StyleText,
		compiled.BackgroundDataURL,
		compiled.ThemePublicID,
		compiled.ThemeVersion,
		compiled.TemplateVersion,
		compiled.AppearanceMode,
		selectors,
	}
	var applied bool
	if err := callFunction(ctx, live.client, applyFunction, arguments, &applied); err != nil || !applied {
		return errors.Join(engine.ErrApplyFailed, err)
	}
	return nil
}

// removeControllerBootstrap is a migration cleanup for pre-on-demand helpers.
// New helpers never register a Page bootstrap or persist a controller record.
func (adapter *Live) removeControllerBootstrap(ctx context.Context, live *liveSession) error {
	record, found, err := adapter.readControllerRecord()
	if err != nil || !found {
		return err
	}
	if record.TargetID == live.targetID {
		// A retired Page identifier means the old bootstrap is no longer
		// addressable through this CDP session. We still remove its current
		// document style below; a new on-demand transaction must never install a
		// replacement bootstrap merely to neutralize a stale one.
		_ = live.client.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
			"identifier": record.Identifier,
		}, nil)
		var restored bool
		if err := callFunction(ctx, live.client, restoreFunction, nil, &restored); err != nil || !restored {
			return errors.Join(engine.ErrRestoreFailed, err)
		}
	}
	return adapter.clearControllerRecord()
}

func (adapter *Live) controllerRecordPath() string {
	return filepath.Join(adapter.root, "state", "renderer-controller.json")
}

func (adapter *Live) readControllerRecord() (controllerRecord, bool, error) {
	path := adapter.controllerRecordPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return controllerRecord{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return controllerRecord{}, false, engine.ErrStateUnsafe
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return controllerRecord{}, false, engine.ErrStateUnsafe
	}
	var record controllerRecord
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != 1 ||
		!controllerIdentifier.MatchString(record.TargetID) ||
		!controllerIdentifier.MatchString(record.Identifier) {
		return controllerRecord{}, false, engine.ErrStateUnsafe
	}
	return record, true, nil
}

func (adapter *Live) clearControllerRecord() error {
	path := adapter.controllerRecordPath()
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return engine.ErrStateUnsafe
		}
	} else if errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return engine.ErrStateUnsafe
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: clear renderer controller", engine.ErrStateUnsafe)
	}
	return nil
}

const applyFunction = `function (styleText, backgroundDataURL, themeId, themeVersion, templateVersion, appearanceMode, selectors) {
  if (!/^[0-9]{6}$/.test(themeId) || !themeVersion ||
      !Number.isInteger(templateVersion) || templateVersion < 1 ||
      (appearanceMode !== "dark" && appearanceMode !== "light") ||
      !selectors || typeof selectors !== "object") return false;
  const STATE_KEY = "__CODEX_SKIN_RENDERER_CONTROLLER_V2__";
  const STYLE_REGISTRY_KEY = "__CODEX_SKIN_RENDERER_STYLE_SHEETS_V1__";
  const STYLE_ID = "codex-skin-theme-v1";
  const ROOT_ATTRIBUTES = [
    "data-codex-skin", "data-codex-skin-theme",
    "data-codex-skin-theme-version", "data-codex-skin-template",
    "data-codex-skin-appearance", "data-codex-skin-background-url",
    "data-codex-skin-runtime"
  ];
  const previous = globalThis[STATE_KEY];
  if (typeof previous?.cleanup === "function") previous.cleanup();

  const existingStyleRegistry = globalThis[STYLE_REGISTRY_KEY];
  const styleRegistry = existingStyleRegistry instanceof Set ? existingStyleRegistry : new Set();
  globalThis[STYLE_REGISTRY_KEY] = styleRegistry;

  const comma = backgroundDataURL.indexOf(",");
  const mediaType = backgroundDataURL.slice(5, backgroundDataURL.indexOf(";base64,"));
  if (comma < 1 || !/^image\/(?:png|jpeg|webp)$/.test(mediaType)) return false;
  const binary = atob(backgroundDataURL.slice(comma + 1));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  const backgroundURL = URL.createObjectURL(new Blob([bytes], { type: mediaType }));
  const ownedCSS = styleText + '\n:root[data-codex-skin="active"] {' +
    ' --cs-background-image: url("' + backgroundURL + '") !important; }\n';
  let styleMode = "fallback";
  let styleSheet = null;
  let styleNode = null;
  let currentMain = null;
  let stopped = false;

  const selector = (key) => typeof selectors[key] === "string" ? selectors[key] : "";
  const MAIN_SELECTOR = selector("shell-main");
  if (!MAIN_SELECTOR) return false;
  const mainSurface = () => document.querySelector(MAIN_SELECTOR);
  const settingsScope = () => Boolean(
    document.querySelector(selector("settings-panel")) ||
    document.querySelector(selector("appearance-radio"))
  );
  const removeMainMarkers = (keep = null) => {
    for (const node of document.querySelectorAll('main[data-codex-skin-main="true"]')) {
      if (node !== keep) {
        node.removeAttribute("data-codex-skin-main");
        node.removeAttribute("data-codex-skin-scope");
      }
    }
  };
  const routeScope = () => {
    if (settingsScope()) return "settings";
    if (document.querySelector(selector("home-route")) ||
        document.querySelector(selector("home-icon")) ||
        document.querySelector(selector("home-suggestions"))) return "home";
    if (document.querySelector(selector("thread-surface"))) return "thread";
    // Codex can replace the old thread scroll container on an otherwise
    // verified shell. The Home and Settings routes above have positive
    // anchors; every remaining normal shell is a task route for the signed
    // template's limited surface treatment.
    return "thread";
  };
  const setAttribute = (node, name, value) => {
    if (node.getAttribute(name) !== value) node.setAttribute(name, value);
  };
  const removeSheet = () => {
    if (styleSheet && "adoptedStyleSheets" in document) {
      try {
        document.adoptedStyleSheets = [...document.adoptedStyleSheets]
          .filter((sheet) => sheet !== styleSheet);
      } catch {}
      styleRegistry.delete(styleSheet);
    }
    if (styleNode) styleNode.disabled = true;
  };
  const installStyle = () => {
    const existing = document.getElementById(STYLE_ID);
    if (existing && existing !== styleNode) existing.remove();
    if (!styleNode || !styleNode.isConnected) {
      styleNode = document.createElement("style");
      styleNode.id = STYLE_ID;
      styleNode.type = "text/css";
      styleNode.setAttribute("data-codex-skin-owned", "true");
      (document.head || document.documentElement).appendChild(styleNode);
    }
    if (!styleSheet && "adoptedStyleSheets" in document &&
        typeof CSSStyleSheet === "function" &&
        typeof CSSStyleSheet.prototype?.replaceSync === "function") {
      try {
        styleSheet = new CSSStyleSheet();
        styleSheet.replaceSync(ownedCSS);
        const retained = [...document.adoptedStyleSheets]
          .filter((candidate) => !styleRegistry.has(candidate));
        document.adoptedStyleSheets = [...retained, styleSheet];
        styleRegistry.clear();
        styleRegistry.add(styleSheet);
        styleMode = "adopted";
      } catch {
        styleSheet = null;
        styleMode = "fallback";
      }
    }
    if (styleMode === "adopted") {
      styleNode.textContent = styleText;
      styleNode.disabled = true;
      const current = [...document.adoptedStyleSheets];
      if (!current.includes(styleSheet)) {
        document.adoptedStyleSheets = [...current, styleSheet];
        styleRegistry.add(styleSheet);
      }
    } else {
      if (styleNode.textContent !== ownedCSS) styleNode.textContent = ownedCSS;
      styleNode.disabled = false;
    }
  };
  const deactivate = () => {
    removeSheet();
    removeMainMarkers();
    currentMain = null;
    const root = document.documentElement;
    if (root) {
      for (const name of ROOT_ATTRIBUTES) root.removeAttribute(name);
      root.style.removeProperty("--cs-background-image");
    }
  };
  const activateRoot = () => {
    const root = document.documentElement;
    installStyle();
    root.style.removeProperty("--cs-background-image");
    setAttribute(root, "data-codex-skin", "active");
    setAttribute(root, "data-codex-skin-theme", themeId);
    setAttribute(root, "data-codex-skin-theme-version", themeVersion);
    setAttribute(root, "data-codex-skin-template", String(templateVersion));
    setAttribute(root, "data-codex-skin-appearance", appearanceMode);
    setAttribute(root, "data-codex-skin-runtime", "2");
    setAttribute(root, "data-codex-skin-background-url", backgroundURL);
    return true;
  };
  const activate = () => {
    const main = mainSurface();
    if (!main) return settingsScope() ? activateRoot() : false;
    const root = document.documentElement;
    const styleInstalled = styleMode === "adopted"
      ? Boolean(styleSheet && document.adoptedStyleSheets.includes(styleSheet))
      : Boolean(styleNode && styleNode.isConnected && !styleNode.disabled);
    const scope = routeScope();
    if (currentMain === main && main.getAttribute("data-codex-skin-main") === "true" &&
        main.getAttribute("data-codex-skin-scope") === scope &&
        root.getAttribute("data-codex-skin") === "active" &&
        root.getAttribute("data-codex-skin-theme") === themeId &&
        root.getAttribute("data-codex-skin-theme-version") === themeVersion &&
        Number(root.getAttribute("data-codex-skin-template") || 0) === templateVersion &&
        styleInstalled &&
        document.querySelectorAll("#codex-skin-theme-v1").length === 1) return true;
    activateRoot();
    removeMainMarkers(main);
    currentMain = main;
    setAttribute(main, "data-codex-skin-main", "true");
    setAttribute(main, "data-codex-skin-scope", scope);
    return true;
  };
  const cleanup = () => {
    if (stopped) return;
    stopped = true;
    deactivate();
    styleNode?.remove();
    if (styleRegistry.size === 0) delete globalThis[STYLE_REGISTRY_KEY];
    URL.revokeObjectURL(backgroundURL);
    if (globalThis[STATE_KEY]?.cleanup === cleanup) delete globalThis[STATE_KEY];
  };
  globalThis[STATE_KEY] = {
    cleanup, styleText, backgroundURL, themeId, themeVersion,
    templateVersion, appearanceMode, runtimeVersion: 3,
    get styleMode() { return styleMode; },
    get installed() {
      return styleMode === "adopted"
        ? Boolean(styleSheet && document.adoptedStyleSheets.includes(styleSheet))
        : Boolean(styleNode && styleNode.isConnected && !styleNode.disabled);
    },
    get active() { return document.documentElement?.getAttribute("data-codex-skin") === "active"; }
  };
  return activate();
}`
