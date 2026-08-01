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
)

const controllerStateKey = "__CODEX_SKIN_RENDERER_CONTROLLER_V1__"

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
	arguments := []any{
		compiled.StyleText,
		compiled.BackgroundDataURL,
		compiled.ThemePublicID,
		compiled.ThemeVersion,
		compiled.TemplateVersion,
		compiled.AppearanceMode,
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return engine.ErrConfiguration
	}
	source := `(() => { const args = ` + string(encoded) + `; (` + applyFunction + `)(...args); })();`
	var registered struct {
		Identifier string `json:"identifier"`
	}
	if err := live.client.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": source,
	}, &registered); err != nil || !controllerIdentifier.MatchString(registered.Identifier) {
		return engine.ErrApplyFailed
	}
	removeRegistered := true
	defer func() {
		if removeRegistered {
			_ = live.client.Call(context.WithoutCancel(ctx), "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
				"identifier": registered.Identifier,
			}, nil)
		}
	}()
	var applied bool
	if err := callFunction(ctx, live.client, applyFunction, arguments, &applied); err != nil || !applied {
		return errors.Join(engine.ErrApplyFailed, err)
	}
	if err := adapter.writeControllerRecord(controllerRecord{
		SchemaVersion: 1, TargetID: live.targetID, Identifier: registered.Identifier,
	}); err != nil {
		return err
	}
	removeRegistered = false
	return nil
}

func (adapter *Live) removeControllerBootstrap(ctx context.Context, live *liveSession) error {
	record, found, err := adapter.readControllerRecord()
	if err != nil || !found {
		return err
	}
	if record.TargetID == live.targetID {
		if err := live.client.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
			"identifier": record.Identifier,
		}, nil); err != nil {
			return err
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

func (adapter *Live) writeControllerRecord(record controllerRecord) error {
	if !controllerIdentifier.MatchString(record.TargetID) ||
		!controllerIdentifier.MatchString(record.Identifier) {
		return engine.ErrStateUnsafe
	}
	path := adapter.controllerRecordPath()
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return engine.ErrStateUnsafe
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return engine.ErrStateUnsafe
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".renderer-controller-")
	if err != nil {
		return engine.ErrStateUnsafe
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return engine.ErrStateUnsafe
	}
	_, writeErr := temporary.Write(raw)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return engine.ErrStateUnsafe
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return engine.ErrStateUnsafe
	}
	return nil
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

const applyFunction = `function (styleText, backgroundDataURL, themeId, themeVersion, templateVersion, appearanceMode) {
  if (!/^[0-9]{6}$/.test(themeId) || !themeVersion ||
      !Number.isInteger(templateVersion) || templateVersion < 1 ||
      (appearanceMode !== "dark" && appearanceMode !== "light")) return false;
  const STATE_KEY = "__CODEX_SKIN_RENDERER_CONTROLLER_V1__";
  const DISABLED_KEY = "__CODEX_SKIN_RENDERER_DISABLED_V1__";
  const STYLE_REGISTRY_KEY = "__CODEX_SKIN_RENDERER_STYLE_SHEETS_V1__";
  const STYLE_ID = "codex-skin-theme-v1";
  const ROOT_ATTRIBUTES = [
    "data-codex-skin", "data-codex-skin-theme",
    "data-codex-skin-theme-version", "data-codex-skin-template",
    "data-codex-skin-appearance", "data-codex-skin-background-url"
  ];
  const previous = globalThis[STATE_KEY];
  if (typeof previous?.cleanup === "function") previous.cleanup();
  globalThis[DISABLED_KEY] = false;

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
  let rootObserver = null;
  let partObserver = null;
  let bodyReadyHandler = null;
  let timer = null;
  let interval = null;
  let stopped = false;

  const mainSurface = () => document.querySelector(
    'main.main-surface, main[class*="_MainContentSurface_"]'
  );
  const settingsScope = () => Boolean(
    document.querySelector('input[name="appearance-theme"]') ||
    document.querySelector('[data-testid="theme-preview"]') ||
    !mainSurface()
  );
  const removeMainMarkers = (keep = null) => {
    for (const node of document.querySelectorAll('main[data-codex-skin-main="true"]')) {
      if (node !== keep) node.removeAttribute("data-codex-skin-main");
    }
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
    const root = document.documentElement;
    if (root) {
      for (const name of ROOT_ATTRIBUTES) root.removeAttribute(name);
      root.style.removeProperty("--cs-background-image");
    }
  };
  const activate = () => {
    const main = mainSurface();
    if (!main) return false;
    installStyle();
    removeMainMarkers(main);
    setAttribute(main, "data-codex-skin-main", "true");
    const root = document.documentElement;
    root.style.removeProperty("--cs-background-image");
    setAttribute(root, "data-codex-skin", "active");
    setAttribute(root, "data-codex-skin-theme", themeId);
    setAttribute(root, "data-codex-skin-theme-version", themeVersion);
    setAttribute(root, "data-codex-skin-template", String(templateVersion));
    setAttribute(root, "data-codex-skin-appearance", appearanceMode);
    setAttribute(root, "data-codex-skin-background-url", backgroundURL);
    return true;
  };
  const ensure = () => {
    timer = null;
    if (stopped || globalThis[DISABLED_KEY]) return;
    if (settingsScope()) deactivate();
    else activate();
  };
  const schedule = () => {
    if (stopped || timer !== null) return;
    timer = setTimeout(ensure, 40);
  };
  const navigationHandler = () => schedule();
  const cleanup = () => {
    if (stopped) return;
    stopped = true;
    if (timer !== null) clearTimeout(timer);
    if (interval !== null) clearInterval(interval);
    rootObserver?.disconnect();
    partObserver?.disconnect();
    if (bodyReadyHandler && typeof document.removeEventListener === "function") {
      document.removeEventListener("DOMContentLoaded", bodyReadyHandler);
    }
    try { globalThis.navigation?.removeEventListener("navigate", navigationHandler); } catch {}
    globalThis.removeEventListener("popstate", navigationHandler);
    globalThis.removeEventListener("hashchange", navigationHandler);
    deactivate();
    styleNode?.remove();
    if (styleRegistry.size === 0) delete globalThis[STYLE_REGISTRY_KEY];
    URL.revokeObjectURL(backgroundURL);
    if (globalThis[STATE_KEY]?.cleanup === cleanup) delete globalThis[STATE_KEY];
    globalThis[DISABLED_KEY] = true;
  };
  globalThis[STATE_KEY] = {
    cleanup, ensure, styleText, backgroundURL, themeId, themeVersion,
    templateVersion, appearanceMode,
    get styleMode() { return styleMode; },
    get active() { return document.documentElement?.getAttribute("data-codex-skin") === "active"; }
  };
  if (typeof MutationObserver === "function") {
    rootObserver = new MutationObserver(schedule);
    partObserver = new MutationObserver(schedule);
  }
  const observeAttributes = (node) => {
    if (!rootObserver || !node) return;
    rootObserver.observe(node, {
      attributes: true,
      attributeFilter: [
        "class", "data-theme", "data-appearance", "data-color-mode",
        "data-codex-skin", "data-codex-skin-theme", "data-codex-skin-template"
      ]
    });
  };
  const observeBody = () => {
    observeAttributes(document.documentElement);
    observeAttributes(document.body);
    partObserver?.observe(document.documentElement, { childList: true, subtree: true });
  };
  observeAttributes(document.documentElement);
  if (document.body) observeBody();
  else if (typeof document.addEventListener === "function") {
    bodyReadyHandler = () => {
      if (!globalThis[DISABLED_KEY]) {
        observeBody();
        schedule();
      }
    };
    document.addEventListener("DOMContentLoaded", bodyReadyHandler, { once: true });
  }
  try { globalThis.navigation?.addEventListener("navigate", navigationHandler); } catch {}
  globalThis.addEventListener("popstate", navigationHandler);
  globalThis.addEventListener("hashchange", navigationHandler);
  interval = setInterval(ensure, 30000);
  if (settingsScope()) {
    deactivate();
    return true;
  }
  return activate();
}`
