package adapter

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/appearance"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cdp"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

const (
	appearanceUITriggerWait  = 30 * time.Second
	appearanceUIMenuWait     = 6 * time.Second
	appearanceUINavWait      = 10 * time.Second
	appearanceUISettleWait   = 12 * time.Second
	appearanceUIRollbackWait = 45 * time.Second
	appearanceUIPoll         = 100 * time.Millisecond
)

var errAppearanceUIUnavailable = errors.New("verified Codex Appearance UI is unavailable")

type appearanceUIState struct {
	TrustedOrigin     bool    `json:"trustedOrigin"`
	BridgeAvailable   bool    `json:"bridgeAvailable"`
	Route             string  `json:"route"`
	AppearanceEntries int     `json:"appearanceEntries"`
	RadioCount        int     `json:"radioCount"`
	VisibleRadios     int     `json:"visibleRadios"`
	Checked           []bool  `json:"checked"`
	BackLinks         int     `json:"backLinks"`
	SystemVariant     string  `json:"systemVariant"`
	DarkMedia         bool    `json:"darkMedia"`
	ColorScheme       string  `json:"colorScheme"`
	BackgroundSurface string  `json:"backgroundSurface"`
	TextForeground    string  `json:"textForeground"`
	BodyColor         string  `json:"bodyColor"`
	TimeOrigin        float64 `json:"timeOrigin"`
}

type appearanceUIActionResult struct {
	Count int `json:"count"`
}

type appearanceUIHostResult struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	Value  string `json:"value"`
}

const appearanceUIStateFunction = `function () {
  const visible = (node) => {
    if (!node) return false;
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const radios = [...document.querySelectorAll(
    'input[name="appearance-theme"][type="radio"]'
  )];
  const entries = [...document.querySelectorAll(
    '[data-settings-panel-slug="appearance"]'
  )].filter(visible);
  const backLinks = [...document.querySelectorAll(
    'nav [role="link"][tabindex="0"]'
  )].filter((node) => visible(node) &&
    node.classList.contains("sidebar-item") &&
    node.classList.contains("group") &&
    node.classList.contains("relative") &&
    node.classList.contains("mb-2") &&
    node.classList.contains("w-full") &&
    node.classList.contains("shrink-0") &&
    node.querySelectorAll("svg.icon-xs").length === 1);
  const bridge = globalThis.electronBridge;
  const rootStyle = getComputedStyle(document.documentElement);
  const bodyStyle = getComputedStyle(document.body);
  return {
    trustedOrigin: location.protocol === "app:",
    bridgeAvailable: Boolean(bridge &&
      typeof bridge.sendMessageFromView === "function" &&
      typeof bridge.getSystemThemeVariant === "function"),
    route: location.pathname + location.search + location.hash,
    appearanceEntries: entries.length,
    radioCount: radios.length,
    visibleRadios: radios.filter((radio) => visible(radio.closest("label"))).length,
    checked: radios.map((radio) => radio.checked === true),
    backLinks: backLinks.length,
    systemVariant: typeof bridge?.getSystemThemeVariant === "function"
      ? bridge.getSystemThemeVariant() : "",
    darkMedia: matchMedia("(prefers-color-scheme: dark)").matches,
    colorScheme: rootStyle.colorScheme,
    backgroundSurface: rootStyle.getPropertyValue("--color-background-surface").trim(),
    textForeground: rootStyle.getPropertyValue("--color-text-foreground").trim(),
    bodyColor: bodyStyle.color,
    timeOrigin: performance.timeOrigin
  };
}`

const appearanceUIHostSettingFunction = `async function () {
  const bridge = globalThis.electronBridge;
  if (location.protocol !== "app:" ||
      !bridge || typeof bridge.sendMessageFromView !== "function") {
    return { ok: false, status: 0, value: "" };
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
      if (!response || response.type !== "fetch-response" ||
          response.requestId !== requestId) return;
      if (response.responseType !== "success") {
        finish({ ok: false, status: response.status || 0, value: "" });
        return;
      }
      try {
        const body = JSON.parse(response.bodyJsonString || "{}");
        finish({
          ok: response.status >= 200 && response.status < 300,
          status: response.status,
          value: typeof body.value === "string" ? body.value : ""
        });
      } catch {
        finish({ ok: false, status: response.status || 0, value: "" });
      }
    };
    window.addEventListener("message", onMessage);
    timer = setTimeout(() => finish({ ok: false, status: 0, value: "" }), 5000);
    Promise.resolve(bridge.sendMessageFromView({
      type: "fetch",
      requestId,
      method: "POST",
      url: "vscode://codex/get-setting",
      body: JSON.stringify({ key: "appearanceTheme" }),
      reportUploadProgress: false
    })).catch(() => finish({ ok: false, status: 0, value: "" }));
  });
}`

const appearanceUIProfileTriggerFunction = `function () {
  if (location.protocol !== "app:") return { count: 0 };
  const visible = (node) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const hasClass = (node, name) => node?.classList?.contains(name) === true;
  const triggers = [...document.querySelectorAll('button[aria-haspopup="menu"]')]
    .filter((button) => {
      const parent = button.parentElement;
      return visible(button) && hasClass(button, "sidebar-item") &&
        hasClass(button, "min-w-0") && hasClass(button, "flex-1") &&
        hasClass(button, "text-start") &&
        hasClass(parent, "sidebar-item") && hasClass(parent, "min-w-0") &&
        hasClass(parent, "flex-1") && hasClass(parent, "items-center") &&
        hasClass(parent, "gap-0");
    });
  if (triggers.length === 1) setTimeout(() => {
    triggers[0].dispatchEvent(new PointerEvent("pointerdown", {
      bubbles: true,
      cancelable: true,
      button: 0,
      buttons: 1,
      isPrimary: true,
      pointerId: 1,
      pointerType: "mouse"
    }));
  }, 100);
  return { count: triggers.length };
}`

const appearanceUISettingsMenuItemFunction = `function () {
  if (location.protocol !== "app:") return { count: 0 };
  const visible = (node) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const shortcut = ".ms-2.shrink-0.text-xs.text-codex-description";
  const items = [...document.querySelectorAll('[role="menuitem"]')]
    .filter((item) => visible(item) &&
      item.querySelectorAll(shortcut).length === 1);
  if (items.length === 1) setTimeout(() => items[0].click(), 100);
  return { count: items.length };
}`

const appearanceUINavigationFunction = `function () {
  if (location.protocol !== "app:") return { count: 0 };
  const visible = (node) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const entries = [...document.querySelectorAll(
    '[data-settings-panel-slug="appearance"]'
  )].filter(visible);
  if (entries.length === 1) setTimeout(() => entries[0].click(), 100);
  return { count: entries.length };
}`

const appearanceUISelectFunction = `function (mode) {
  if (location.protocol !== "app:") return { count: 0 };
  const index = { system: 0, light: 1, dark: 2 }[mode];
  if (index === undefined) return { count: 0 };
  const visible = (node) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const radios = [...document.querySelectorAll(
    'input[name="appearance-theme"][type="radio"]'
  )];
  const valid = radios.length === 3 &&
    radios.every((radio) => visible(radio.closest("label"))) &&
    radios.filter((radio) => radio.checked === true).length === 1;
  if (valid) setTimeout(() => radios[index].click(), 100);
  return { count: valid ? radios.length : 0 };
}`

const appearanceUIBackFunction = `function () {
  if (location.protocol !== "app:") return { count: 0 };
  const visible = (node) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const links = [...document.querySelectorAll(
    'nav [role="link"][tabindex="0"]'
  )].filter((node) => visible(node) &&
    node.classList.contains("sidebar-item") &&
    node.classList.contains("group") &&
    node.classList.contains("relative") &&
    node.classList.contains("mb-2") &&
    node.classList.contains("w-full") &&
    node.classList.contains("shrink-0") &&
    node.querySelectorAll("svg.icon-xs").length === 1);
  if (links.length === 1) setTimeout(() => links[0].click(), 100);
  return { count: links.length };
}`

func (adapter *Live) switchAppearanceInPlace(
	ctx context.Context,
	live *liveSession,
	targetMode string,
) error {
	if !supportsInAppAppearance(runtime.GOOS) || adapter.appearance == nil ||
		(targetMode != "dark" && targetMode != "light") {
		return errAppearanceUIUnavailable
	}
	entryState, entryMode, err := adapter.readVerifiedAppearance(ctx, live)
	if err != nil {
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return errors.Join(engine.ErrStateUnsafe, err)
		}
		return errAppearanceUIUnavailable
	}
	if !entryState.TrustedOrigin || !entryState.BridgeAvailable ||
		entryState.Route == "" || entryState.TimeOrigin == 0 ||
		!validAppearanceSetting(entryMode) {
		return errAppearanceUIUnavailable
	}
	originalRoute, openedSettings, err := adapter.openAppearanceControls(ctx, live)
	if err != nil {
		if openedSettings {
			_ = adapter.returnFromAppearance(ctx, live, originalRoute)
		}
		return err
	}
	baseline, oldMode, err := adapter.readVerifiedAppearance(ctx, live)
	if err != nil {
		if openedSettings {
			_ = adapter.returnFromAppearance(ctx, live, originalRoute)
		}
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return errors.Join(engine.ErrStateUnsafe, err)
		}
		return errAppearanceUIUnavailable
	}
	if !appearanceControlsReady(baseline, oldMode) || oldMode == targetMode ||
		oldMode != entryMode || originalRoute != entryState.Route ||
		baseline.TimeOrigin != entryState.TimeOrigin {
		if openedSettings {
			_ = adapter.returnFromAppearance(ctx, live, originalRoute)
		}
		return errAppearanceUIUnavailable
	}
	transaction, err := adapter.appearance.BeginLiveSwitch(oldMode)
	if err != nil {
		if openedSettings {
			_ = adapter.returnFromAppearance(ctx, live, originalRoute)
		}
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	defer transaction.Close()

	selectionStarted, err := adapter.selectAppearanceMode(ctx, live, targetMode)
	if err != nil {
		if !selectionStarted {
			if openedSettings {
				_ = adapter.returnFromAppearance(ctx, live, originalRoute)
			}
			if errors.Is(err, codex.ErrListenerUntrusted) {
				return errors.Join(engine.ErrStateUnsafe, err)
			}
			return errAppearanceUIUnavailable
		}
		return adapter.rollbackAppearanceUI(
			ctx, live, transaction, baseline, oldMode, originalRoute, openedSettings, err,
		)
	}
	targetEffective := targetMode
	if _, err := adapter.waitForAppearanceMode(
		ctx, live, transaction, targetMode, targetEffective, baseline, false,
	); err != nil {
		return adapter.rollbackAppearanceUI(
			ctx, live, transaction, baseline, oldMode, originalRoute, openedSettings, err,
		)
	}
	if openedSettings {
		if err := adapter.returnFromAppearance(ctx, live, originalRoute); err != nil {
			return adapter.rollbackAppearanceUI(
				ctx, live, transaction, baseline, oldMode, originalRoute, true, err,
			)
		}
		if err := adapter.verifyReturnedAppearance(
			ctx, live, transaction, targetMode, targetEffective, baseline, originalRoute,
		); err != nil {
			return adapter.rollbackAppearanceUI(
				ctx, live, transaction, baseline, oldMode, originalRoute, true, err,
			)
		}
	}
	live.appearanceMode = targetMode
	return nil
}

func (adapter *Live) rollbackAppearanceUI(
	ctx context.Context,
	live *liveSession,
	transaction *appearance.LiveSwitch,
	baseline appearanceUIState,
	oldMode string,
	originalRoute string,
	openedSettings bool,
	cause error,
) error {
	rollbackCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), appearanceUIRollbackWait,
	)
	defer cancel()
	ctx = rollbackCtx

	_, rollbackOpened, err := adapter.openAppearanceControls(ctx, live)
	if err != nil {
		return errors.Join(engine.ErrStateUnsafe, cause, err)
	}
	if _, err := adapter.selectAppearanceMode(ctx, live, oldMode); err != nil {
		return errors.Join(engine.ErrStateUnsafe, cause, err)
	}
	oldEffective := oldMode
	if oldMode == "system" {
		oldEffective = baseline.SystemVariant
	}
	if _, err := adapter.waitForAppearanceMode(
		ctx, live, transaction, oldMode, oldEffective, baseline, true,
	); err != nil {
		return errors.Join(engine.ErrStateUnsafe, cause, err)
	}
	if openedSettings || rollbackOpened {
		if err := adapter.returnFromAppearance(ctx, live, originalRoute); err != nil {
			return errors.Join(engine.ErrStateUnsafe, cause, err)
		}
	}
	if oldMode == "dark" || oldMode == "light" {
		live.appearanceMode = oldMode
	}
	return errors.Join(errAppearanceUIUnavailable, cause)
}

func (adapter *Live) openAppearanceControls(
	ctx context.Context,
	live *liveSession,
) (string, bool, error) {
	state, err := adapter.readAppearanceUIStateWithReconnect(ctx, live)
	if err != nil {
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return "", false, errors.Join(engine.ErrStateUnsafe, err)
		}
		return "", false, errAppearanceUIUnavailable
	}
	if !state.TrustedOrigin || !state.BridgeAvailable || state.Route == "" {
		return "", false, errAppearanceUIUnavailable
	}
	if state.RadioCount == 3 {
		return state.Route, false, nil
	}
	// Do not redirect a user who is already editing a different Settings panel.
	// The verified fast path is limited to either an already-visible Appearance
	// page or the normal app -> Settings -> Appearance route it owns end to end.
	if state.AppearanceEntries != 0 || state.BackLinks != 0 {
		return state.Route, false, errAppearanceUIUnavailable
	}
	originalRoute := state.Route

	// A menu may already be open. Select its unique shortcut-bearing Settings
	// item first; otherwise open the unique profile-footer menu.
	selected, _ := adapter.performAppearanceUIAction(ctx, live, appearanceUISettingsMenuItemFunction, nil)
	if selected != 1 {
		if err := adapter.waitForAppearanceUIAction(
			ctx, live, appearanceUIProfileTriggerFunction, nil, appearanceUITriggerWait,
		); err != nil {
			return originalRoute, false, err
		}
		if err := adapter.waitForAppearanceUIAction(
			ctx, live, appearanceUISettingsMenuItemFunction, nil, appearanceUIMenuWait,
		); err != nil {
			return originalRoute, false, err
		}
	}
	if err := adapter.waitForAppearanceUIState(ctx, live, appearanceUINavWait, func(state appearanceUIState) bool {
		return state.TrustedOrigin && state.AppearanceEntries == 1
	}); err != nil {
		return originalRoute, true, err
	}
	if err := adapter.waitForAppearanceUIAction(
		ctx, live, appearanceUINavigationFunction, nil, appearanceUINavWait,
	); err != nil {
		return originalRoute, true, err
	}
	if err := adapter.waitForAppearanceUIState(ctx, live, appearanceUINavWait, func(state appearanceUIState) bool {
		return state.TrustedOrigin && state.AppearanceEntries == 1 &&
			state.RadioCount == 3 && state.VisibleRadios == 3 && state.BackLinks == 1
	}); err != nil {
		return originalRoute, true, err
	}
	return originalRoute, true, nil
}

func (adapter *Live) selectAppearanceMode(
	ctx context.Context,
	live *liveSession,
	mode string,
) (bool, error) {
	if !validAppearanceSetting(mode) {
		return false, engine.ErrConfiguration
	}
	if err := verifyAppearanceUIProcess(ctx, live); err != nil {
		return false, err
	}
	var result appearanceUIActionResult
	// Do not automatically retry this mutating call. A lost CDP response can
	// occur after the renderer scheduled the click, so any transport error is
	// treated as an uncertain mutation and must enter the verified rollback.
	if err := callFunction(
		ctx, live.client, appearanceUISelectFunction, []any{mode}, &result,
	); err != nil {
		return true, err
	}
	if result.Count != 3 {
		return false, errAppearanceUIUnavailable
	}
	if err := waitAppearanceUIPoll(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func (adapter *Live) returnFromAppearance(
	ctx context.Context,
	live *liveSession,
	originalRoute string,
) error {
	if originalRoute == "" {
		return errAppearanceUIUnavailable
	}
	if err := adapter.waitForAppearanceUIAction(
		ctx, live, appearanceUIBackFunction, nil, appearanceUINavWait,
	); err != nil {
		return err
	}
	return adapter.waitForAppearanceUIState(ctx, live, appearanceUINavWait, func(state appearanceUIState) bool {
		return state.TrustedOrigin && state.Route == originalRoute &&
			state.AppearanceEntries == 0 && state.RadioCount == 0 && state.BackLinks == 0
	})
}

func (adapter *Live) waitForAppearanceMode(
	ctx context.Context,
	live *liveSession,
	transaction *appearance.LiveSwitch,
	settingMode string,
	effectiveMode string,
	baseline appearanceUIState,
	requireBaselinePalette bool,
) (appearanceUIState, error) {
	deadline := time.Now().Add(appearanceUISettleWait)
	var last appearanceUIState
	for time.Now().Before(deadline) {
		state, hostMode, err := adapter.readVerifiedAppearance(ctx, live)
		if err == nil {
			last = state
			diskOK := transaction.VerifyMode(settingMode) == nil
			settled := diskOK && hostMode == settingMode &&
				appearanceStateMatches(state, settingMode, effectiveMode, baseline)
			if settled && (!requireBaselinePalette || sameAppearancePalette(state, baseline)) {
				return state, nil
			}
		}
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return last, err
		}
		if err := waitAppearanceUIPoll(ctx); err != nil {
			return last, err
		}
	}
	return last, engine.ErrVerifyFailed
}

func (adapter *Live) readVerifiedAppearance(
	ctx context.Context,
	live *liveSession,
) (appearanceUIState, string, error) {
	if err := verifyAppearanceUIProcess(ctx, live); err != nil {
		return appearanceUIState{}, "", err
	}
	state, err := adapter.readAppearanceUIStateWithReconnect(ctx, live)
	if err != nil {
		return state, "", err
	}
	hostMode, err := adapter.readAppearanceHostMode(ctx, live)
	return state, hostMode, err
}

func (adapter *Live) verifyReturnedAppearance(
	ctx context.Context,
	live *liveSession,
	transaction *appearance.LiveSwitch,
	settingMode string,
	effectiveMode string,
	baseline appearanceUIState,
	originalRoute string,
) error {
	state, hostMode, err := adapter.readVerifiedAppearance(ctx, live)
	if err != nil {
		return err
	}
	if transaction.VerifyMode(settingMode) != nil || hostMode != settingMode ||
		!returnedAppearanceMatches(state, effectiveMode, baseline, originalRoute) {
		return engine.ErrVerifyFailed
	}
	return nil
}

func verifyAppearanceUIProcess(ctx context.Context, live *liveSession) error {
	process, err := codex.VerifyListener(
		ctx, live.installation, live.process.ProcessID, live.port, live.profile,
	)
	if err != nil || process.ProcessID != live.process.ProcessID ||
		process.ProcessStartID != live.process.ProcessStartID ||
		process.ExecutableSHA256 != live.process.ExecutableSHA256 {
		return errors.Join(codex.ErrListenerUntrusted, err)
	}
	return nil
}

func (adapter *Live) readAppearanceUIStateWithReconnect(
	ctx context.Context,
	live *liveSession,
) (appearanceUIState, error) {
	var state appearanceUIState
	if err := callFunction(ctx, live.client, appearanceUIStateFunction, nil, &state); err == nil {
		return state, nil
	}
	if err := adapter.reconnectAppearanceUI(ctx, live); err != nil {
		return state, err
	}
	if err := callFunction(ctx, live.client, appearanceUIStateFunction, nil, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (adapter *Live) readAppearanceHostMode(
	ctx context.Context,
	live *liveSession,
) (string, error) {
	var result appearanceUIHostResult
	if err := callAsyncFunction(ctx, live.client, appearanceUIHostSettingFunction, nil, &result); err != nil {
		if reconnectErr := adapter.reconnectAppearanceUI(ctx, live); reconnectErr != nil {
			return "", reconnectErr
		}
		if err := callAsyncFunction(ctx, live.client, appearanceUIHostSettingFunction, nil, &result); err != nil {
			return "", err
		}
	}
	if !result.OK || result.Status < 200 || result.Status >= 300 ||
		!validAppearanceSetting(result.Value) {
		return "", engine.ErrVerifyFailed
	}
	return result.Value, nil
}

func (adapter *Live) reconnectAppearanceUI(ctx context.Context, live *liveSession) error {
	process, target, client, err := adapter.connectControlled(
		ctx, live.installation, live.process.ProcessID, live.port, live.profile,
	)
	if err != nil {
		return err
	}
	if process.ProcessID != live.process.ProcessID ||
		process.ProcessStartID != live.process.ProcessStartID ||
		process.ExecutableSHA256 != live.process.ExecutableSHA256 {
		_ = client.Close()
		return codex.ErrListenerUntrusted
	}
	oldClient := live.client
	live.process = process
	live.targetID = target.ID
	live.client = client
	if oldClient != nil {
		_ = oldClient.Close()
	}
	return nil
}

func (adapter *Live) waitForAppearanceUIAction(
	ctx context.Context,
	live *liveSession,
	function string,
	arguments []any,
	wait time.Duration,
) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		count, err := adapter.performAppearanceUIAction(ctx, live, function, arguments)
		if err == nil && count == 1 {
			return waitAppearanceUIPoll(ctx)
		}
		if err == nil && count > 1 {
			return errAppearanceUIUnavailable
		}
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return err
		}
		if err := waitAppearanceUIPoll(ctx); err != nil {
			return err
		}
	}
	return errAppearanceUIUnavailable
}

func (adapter *Live) performAppearanceUIAction(
	ctx context.Context,
	live *liveSession,
	function string,
	arguments []any,
) (int, error) {
	if err := verifyAppearanceUIProcess(ctx, live); err != nil {
		return 0, err
	}
	var result appearanceUIActionResult
	err := callFunction(ctx, live.client, function, arguments, &result)
	if err != nil {
		if reconnectErr := adapter.reconnectAppearanceUI(ctx, live); reconnectErr != nil {
			return 0, reconnectErr
		}
		err = callFunction(ctx, live.client, function, arguments, &result)
	}
	return result.Count, err
}

func (adapter *Live) waitForAppearanceUIState(
	ctx context.Context,
	live *liveSession,
	wait time.Duration,
	accept func(appearanceUIState) bool,
) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		state, err := adapter.readAppearanceUIStateWithReconnect(ctx, live)
		if err == nil && accept != nil && accept(state) {
			return nil
		}
		if errors.Is(err, codex.ErrListenerUntrusted) {
			return err
		}
		if err := waitAppearanceUIPoll(ctx); err != nil {
			return err
		}
	}
	return errAppearanceUIUnavailable
}

func callAsyncFunction(
	ctx context.Context,
	client *cdp.Client,
	function string,
	arguments []any,
	output any,
) error {
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
		"arguments": callArguments, "returnByValue": true, "awaitPromise": true,
	}, &result); err != nil || result.ExceptionDetails != nil || len(result.Result.Value) == 0 {
		return engine.ErrVerifyFailed
	}
	if err := jsonUnmarshal(result.Result.Value, output); err != nil {
		return engine.ErrVerifyFailed
	}
	return nil
}

func appearanceControlsReady(state appearanceUIState, hostMode string) bool {
	if !state.TrustedOrigin || !state.BridgeAvailable || state.TimeOrigin == 0 ||
		state.AppearanceEntries != 1 || state.RadioCount != 3 || state.VisibleRadios != 3 ||
		len(state.Checked) != 3 || !validAppearanceSetting(hostMode) ||
		state.BackgroundSurface == "" || state.TextForeground == "" || state.BodyColor == "" {
		return false
	}
	checked := 0
	for _, value := range state.Checked {
		if value {
			checked++
		}
	}
	index := appearanceModeIndex(hostMode)
	if checked != 1 || index < 0 || !state.Checked[index] {
		return false
	}
	effective := hostMode
	if hostMode == "system" {
		effective = state.SystemVariant
	}
	return appearanceEffectiveState(state, effective)
}

func appearanceStateMatches(
	state appearanceUIState,
	settingMode string,
	effectiveMode string,
	baseline appearanceUIState,
) bool {
	if !appearanceControlsReady(state, settingMode) ||
		!appearanceEffectiveState(state, effectiveMode) ||
		state.TimeOrigin != baseline.TimeOrigin {
		return false
	}
	if baseline.SystemVariant != effectiveMode && sameAppearancePalette(state, baseline) {
		return false
	}
	return true
}

func returnedAppearanceMatches(
	state appearanceUIState,
	effectiveMode string,
	baseline appearanceUIState,
	originalRoute string,
) bool {
	if !state.TrustedOrigin || !state.BridgeAvailable || state.Route != originalRoute ||
		state.AppearanceEntries != 0 || state.RadioCount != 0 || state.BackLinks != 0 ||
		state.TimeOrigin != baseline.TimeOrigin || state.BackgroundSurface == "" ||
		state.TextForeground == "" || state.BodyColor == "" ||
		!appearanceEffectiveState(state, effectiveMode) {
		return false
	}
	return baseline.SystemVariant == effectiveMode || !sameAppearancePalette(state, baseline)
}

func appearanceEffectiveState(state appearanceUIState, mode string) bool {
	if mode != "dark" && mode != "light" || state.SystemVariant != mode || state.ColorScheme != mode {
		return false
	}
	return state.DarkMedia == (mode == "dark")
}

func sameAppearancePalette(left, right appearanceUIState) bool {
	return left.BackgroundSurface == right.BackgroundSurface &&
		left.TextForeground == right.TextForeground && left.BodyColor == right.BodyColor
}

func appearanceModeIndex(mode string) int {
	switch mode {
	case "system":
		return 0
	case "light":
		return 1
	case "dark":
		return 2
	default:
		return -1
	}
}

func validAppearanceSetting(mode string) bool {
	return mode == "system" || mode == "light" || mode == "dark"
}

func supportsInAppAppearance(platform string) bool {
	return platform == "darwin"
}

func waitAppearanceUIPoll(ctx context.Context) error {
	timer := time.NewTimer(appearanceUIPoll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
