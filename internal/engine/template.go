package engine

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

func CompileTheme(verified theme.Verified, stagedRoot string) (CompiledTheme, error) {
	manifest := verified.Manifest
	if !theme.EngineCompatible(CurrentEngineVersion, manifest.Compatibility.MinEngineVersion) {
		return CompiledTheme{}, fmt.Errorf(
			"%w: current=%s required=%s",
			theme.ErrEngineIncompatible,
			CurrentEngineVersion,
			manifest.Compatibility.MinEngineVersion,
		)
	}
	background := manifest.Design.Tokens.BackgroundImage
	target := filepath.Join(stagedRoot, filepath.FromSlash(background))
	if err := ensureContained(stagedRoot, target); err != nil {
		return CompiledTheme{}, err
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return CompiledTheme{}, fmt.Errorf("%w: read staged asset", ErrConfiguration)
	}
	contentType := ""
	expectedSize := int64(0)
	for _, asset := range manifest.Assets {
		if asset.Path == background {
			contentType = asset.ContentType
			expectedSize = asset.ByteSize
			break
		}
	}
	if int64(len(content)) != expectedSize {
		return CompiledTheme{}, fmt.Errorf("%w: staged asset size", ErrConfiguration)
	}
	expectedExtension := map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/webp": ".webp",
	}[contentType]
	if expectedExtension == "" || !strings.EqualFold(filepath.Ext(background), expectedExtension) {
		return CompiledTheme{}, fmt.Errorf("%w: staged asset type", ErrConfiguration)
	}
	tokens := manifest.Design.Tokens
	sidebarRGB := "14 18 24"
	surfaceRGB := "20 24 32"
	shadowAlpha := ".18"
	if manifest.Design.Mode == "light" {
		sidebarRGB = "244 248 252"
		surfaceRGB = "249 252 255"
		shadowAlpha = ".12"
	}
	imageURL := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(content)
	legacyStyle := fmt.Sprintf(`:root[%s="active"] {
  --cs-background-overlay: %.4f;
  --cs-surface-opacity: %.4f;
  --cs-surface-blur: %dpx;
  --cs-text-primary: %s;
  --cs-text-secondary: %s;
  --cs-accent: %s;
  --cs-border: %s;
  --cs-radius-scale: %.4f;
}
:root[%s="active"] body {
  background:
    linear-gradient(rgb(0 0 0 / var(--cs-background-overlay)), rgb(0 0 0 / var(--cs-background-overlay))),
    var(--cs-background-image) center / cover fixed !important;
}
:root[%s="active"] main[data-codex-skin-main="true"],
:root[%s="active"] aside.app-shell-left-panel {
  background: transparent !important;
  color: var(--cs-text-primary) !important;
}
:root[%s="active"] main[data-codex-skin-main="true"] {
  box-shadow: none !important;
}
:root[%s="active"] .app-shell-main-content-top-fade {
  background: none !important;
  background-image: none !important;
  backdrop-filter: none !important;
  box-shadow: none !important;
  opacity: 0 !important;
  transition: none !important;
}
:root[%s="active"] aside.app-shell-left-panel {
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%%);
  background: rgb(%s / var(--cs-surface-opacity)) !important;
  border-right: 1px solid var(--cs-border);
}
:root[%s="active"] aside.app-shell-left-panel::after {
  content: none !important;
  display: none !important;
  background: none !important;
  width: 0 !important;
}
:root[%s="active"] .composer-surface-chrome,
:root[%s="active"] .group\/home-suggestions button,
:root[%s="active"] main[data-codex-skin-main="true"] div.sticky:has(input[type="text"]) {
  color: var(--cs-text-primary) !important;
  background: rgb(%s / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%%);
  box-shadow: 0 18px 50px rgb(0 0 0 / %s);
}
:root[%s="active"] .group\/home-suggestions button:hover,
:root[%s="active"] .composer-surface-chrome button:hover {
  border-color: var(--cs-accent) !important;
}
:root[%s="active"] .group\/home-suggestions button svg,
:root[%s="active"] .composer-surface-chrome svg {
  color: var(--cs-accent) !important;
}
:root[%s="active"] .group\/home-suggestions button [class~="text-token-text-primary"],
:root[%s="active"] .composer-surface-chrome p {
  color: var(--cs-text-primary) !important;
}
:root[%s="active"] main[data-codex-skin-main="true"] [class~="text-token-text-primary"],
:root[%s="active"] main[data-codex-skin-main="true"] [class~="text-token-foreground"],
:root[%s="active"] aside.app-shell-left-panel [class~="text-token-text-primary"],
:root[%s="active"] .composer-surface-chrome input,
:root[%s="active"] .composer-surface-chrome textarea {
  color: var(--cs-text-primary) !important;
}
:root[%s="active"] aside.app-shell-left-panel,
:root[%s="active"] aside.app-shell-left-panel * {
  color: var(--cs-text-primary) !important;
}
:root[%s="active"] .composer-surface-chrome [class~="text-token-text-secondary"],
:root[%s="active"] .composer-surface-chrome [class~="text-token-text-tertiary"],
:root[%s="active"] aside.app-shell-left-panel [class~="text-token-text-secondary"],
:root[%s="active"] aside.app-shell-left-panel [class~="text-token-text-tertiary"] {
  color: var(--cs-text-secondary) !important;
}
:root[%s="active"] .composer-surface-chrome input::placeholder,
:root[%s="active"] .composer-surface-chrome textarea::placeholder {
  color: var(--cs-text-secondary) !important;
}
:root[%s="active"] main[data-codex-skin-main="true"] div.sticky:has(input[type="text"]) button {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
}
:root[%s="active"] main[data-codex-skin-main="true"] [class*="_homeUtilityBar_"] {
  color: var(--cs-text-primary) !important;
  background: rgb(%s / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%%);
}
:root[%s="active"] main[data-codex-skin-main="true"] button[class*="_utilityBarLabel_"] {
  color: var(--cs-text-primary) !important;
}
`,
		RootMarkerAttribute,
		tokens.BackgroundOverlay,
		tokens.SurfaceOpacity,
		tokens.SurfaceBlurPx,
		tokens.TextPrimary,
		tokens.TextSecondary,
		tokens.Accent,
		tokens.Border,
		tokens.RadiusScale,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		sidebarRGB,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		surfaceRGB,
		shadowAlpha,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		RootMarkerAttribute,
		surfaceRGB,
		RootMarkerAttribute,
	)
	// Keep the preceding two signed templates byte-for-byte available. V11 was
	// preview-only, while the installed staging Helper can still carry V10; both
	// must move to the scoped repair without a restart or intermediate Restore.
	migrationStyle, err := compileTemplateV10(
		manifest.Design.Mode,
		tokens,
		sidebarRGB,
		surfaceRGB,
		shadowAlpha,
	)
	if err != nil {
		return CompiledTheme{}, err
	}
	previousStyle, err := compileTemplateV11(
		manifest.Design.Mode,
		tokens,
		sidebarRGB,
		surfaceRGB,
		shadowAlpha,
	)
	if err != nil {
		return CompiledTheme{}, err
	}
	style, err := compileTemplateV12(
		manifest.Design.Mode,
		tokens,
		sidebarRGB,
		surfaceRGB,
		shadowAlpha,
	)
	if err != nil {
		return CompiledTheme{}, err
	}
	return CompiledTheme{
		ThemePublicID:      manifest.ThemePublicID,
		ThemeVersion:       manifest.ThemeVersion,
		TemplateVersion:    TemplateVersion,
		AppearanceMode:     manifest.Design.Mode,
		StyleText:          style,
		PreviousStyleText:  previousStyle,
		MigrationStyleText: migrationStyle,
		LegacyStyleText:    legacyStyle,
		BackgroundDataURL:  imageURL,
	}, nil
}

func compileTemplateV8(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV7(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const scopeContract = `

/* Fixed route-scope contract v8. Current Codex Home may render suggestion
   cards before (or without) the older composer surface. The renderer marks a
   verified Home or thread main element before this CSS is installed, so the
   artwork never spills into native utility routes while Home does not depend
   on one volatile child component. */
:root[__MARKER__="active"] {
  --cs-scope-contract: 8;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
    :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
  ),
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
    :is(
      .thread-scroll-container,
      [data-message-author-role],
      [data-local-conversation-user-anchor],
      [data-local-conversation-final-assistant],
      [class*="_markdown"]
    )
  ) {
  background:
    linear-gradient(
      rgb(var(--cs-surface-rgb) / .22),
      rgb(var(--cs-surface-rgb) / .22)
    ) !important;
  border: 0 !important;
  border-inline-start: 0 !important;
  box-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  > header:is(
    .app-header-tint,
    [data-app-shell-header-edge-scroll],
    [class*="_Header_"]
  ) {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
  border-bottom: 0 !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:is(
      [data-codex-skin-scope="home"],
      [data-codex-skin-scope="thread"]
    )
  )
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"]
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}
`
	return style + strings.ReplaceAll(scopeContract, "__MARKER__", RootMarkerAttribute), nil
}

func compileTemplateV9(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV8(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const taskRouteFallbackContract = `

/* Fixed task-route fallback contract v9. A current Codex task can replace the
   older thread scroll container while retaining the verified app shell. The
   renderer therefore treats a non-Home, non-Settings shell as a task route;
   this signed contract keeps the existing scoped surface, boundary and fade
   rules active without granting themes control over route selectors. */
:root[__MARKER__="active"] {
  --cs-scope-contract: 9;
}
`
	return style + strings.ReplaceAll(taskRouteFallbackContract, "__MARKER__", RootMarkerAttribute), nil
}

func compileTemplateV10(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV9(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const currentWorkspaceContract = `

/* Current-workspace visual contract v10. New Codex task pages can omit the
   legacy thread-scroll container while still containing the active main,
   composer, token text and bottom gradient. Keep the repair attached only to
   the renderer-marked Home/task main; theme packages still cannot choose
   selectors or change this signed surface policy. */
:root[__MARKER__="active"] {
  --cs-workspace-contract: 10;
  /* Current Codex shell variables. These are fixed renderer tokens, not
     theme-defined CSS: without this bridge the light native defaults can
     reappear in Scheduled, Plugins and the current ComposerLayoutRoot. */
  --color-background-primary: rgb(var(--cs-surface-rgb) / .96);
  --color-background-secondary: rgb(var(--cs-surface-rgb) / .92);
  --color-background-surface: rgb(var(--cs-surface-rgb) / .92);
  --color-background-elevated-primary: rgb(var(--cs-surface-rgb) / .94);
  --color-background-elevated-secondary: rgb(var(--cs-surface-rgb) / .90);
  --color-text-primary: var(--cs-text-primary);
  --color-text-secondary: var(--cs-text-secondary);
  --color-text-foreground: var(--cs-text-primary);
  --color-border-primary: var(--cs-border);
  --background: rgb(var(--cs-surface-rgb) / .96);
  --foreground: var(--cs-text-primary);
  --card: rgb(var(--cs-surface-rgb) / .92);
  --card-foreground: var(--cs-text-primary);
  --popover: rgb(var(--cs-surface-rgb) / .94);
  --popover-foreground: var(--cs-text-primary);
  --secondary: rgb(var(--cs-surface-rgb) / .92);
  --secondary-foreground: var(--cs-text-primary);
  --muted-foreground: var(--cs-text-secondary);
  --border: var(--cs-border);
  --input: var(--cs-border);
  --color-token-main-surface-primary: rgb(var(--cs-surface-rgb) / .96);
  --color-token-main-surface-secondary: rgb(var(--cs-surface-rgb) / .92);
  --color-token-main-surface-tertiary: rgb(var(--cs-surface-rgb) / .88);
  --color-token-foreground: var(--cs-text-primary);
  --color-token-text-primary: var(--cs-text-primary);
  --color-token-text-secondary: var(--cs-text-secondary);
  --color-token-text-tertiary: var(--cs-text-secondary);
  --color-token-muted-foreground: var(--cs-text-secondary);
  --color-token-description-foreground: var(--cs-text-secondary);
  --color-token-input-placeholder-foreground: var(--cs-text-secondary);
  --color-token-border-default: var(--cs-border);
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    [class~="bg-token-main-surface-primary"],
    [class~="bg-token-main-surface-secondary"],
    [class~="bg-token-main-surface-tertiary"]
  ) {
  color: var(--cs-text-primary) !important;
  background-color: rgb(var(--cs-surface-rgb) / .92) !important;
  background-image: none !important;
}

/* The current task composer may be a sticky input wrapper rather than the
   old composer-surface-chrome element. Give both shapes the same readable
   surface so the lower window never falls back to the native bright band. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    .composer-surface-chrome,
    [class*="_ComposerLayoutRoot_"],
    div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  ) {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    .composer-surface-chrome,
    [class*="_ComposerLayoutRoot_"],
    div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  )
  :is(input, textarea, [class~="text-token-text-primary"], [class~="text-token-foreground"]) {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    .composer-surface-chrome,
    [class*="_ComposerLayoutRoot_"],
    div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  )
  :is(input, textarea)::placeholder {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

/* Token foregrounds are stable renderer semantics rather than a theme
   supplied selector. This covers the new task page without recolouring
   unmarked native windows after the style is removed. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    [class~="text-token-text-primary"],
    [class~="text-token-foreground"],
    [class~="text-default"],
    [class~="text-token-conversation-body"],
    [data-message-author-role],
    [class*="_markdown"],
    h1, h2, h3, h4, h5, h6, p, li, [role="heading"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is([class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]) {
  color: var(--cs-text-secondary) !important;
  text-shadow: none !important;
}

/* Clear only the known app-shell/content fade primitives. They are layers,
   not content cards, and otherwise create the bright lower/top bands seen on
   the current Scheduled and Plugins routes. */
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:is(
      [data-codex-skin-scope="home"],
      [data-codex-skin-scope="thread"]
    )
  )
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"],
    [class*="_MainContentBottomFade_"],
    [class*="_MainContentBottomGradient_"],
    .bg-gradient-to-t.from-token-main-surface-primary
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
}
`
	contract := strings.ReplaceAll(currentWorkspaceContract, "__MARKER__", RootMarkerAttribute)
	contract = strings.ReplaceAll(contract, "__SHADOW_ALPHA__", shadowAlpha)
	return style + contract, nil
}

func compileTemplateV11(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	if err := validateTemplateContrast(mode, tokens); err != nil {
		return "", err
	}
	textShadowRGB := "0 0 0"
	if mode == "light" {
		textShadowRGB = "255 255 255"
	}
	// Start a new narrow baseline instead of extending v10. Earlier templates
	// wrote Codex's foreground/background tokens at :root, which let a native
	// white utility pane inherit a skin foreground. V11 owns only its --cs-
	// variables; the app's System/Light/Dark pairing remains untouched.
	const scopedWorkspaceContract = `:root[__MARKER__="active"] {
  --cs-background-overlay: __BACKGROUND_OVERLAY__;
  --cs-surface-opacity: __SURFACE_OPACITY__;
  --cs-surface-blur: __SURFACE_BLUR__px;
  --cs-surface-rgb: __SURFACE_RGB__;
  --cs-text-shadow-rgb: __TEXT_SHADOW_RGB__;
  --cs-text-primary: __TEXT_PRIMARY__;
  --cs-text-secondary: __TEXT_SECONDARY__;
  --cs-accent: __ACCENT__;
  --cs-border: __BORDER__;
  --cs-radius-scale: __RADIUS_SCALE__;
  --cs-scope-contract: 11;
  --cs-workspace-contract: 11;
}

/* Scoped workspace contract v11. A skin owns a marked Home/task workspace
   only while it contains a composer, message or Markdown signal; native utility routes, output side panels and overlays keep Codex's own paired
   System/Light/Dark background and foreground behavior. App token names are never reassigned here. */
:root[__MARKER__="active"] body {
  background:
    linear-gradient(
      rgb(0 0 0 / var(--cs-background-overlay)),
      rgb(0 0 0 / var(--cs-background-overlay))
    ),
    var(--cs-background-image) center / cover fixed !important;
}

:root[__MARKER__="active"] aside.app-shell-left-panel {
  color: var(--cs-text-primary) !important;
  background: rgb(__SIDEBAR_RGB__ / var(--cs-surface-opacity)) !important;
  border-right: 1px solid var(--cs-border);
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"] aside.app-shell-left-panel::after {
  content: none !important;
  display: none !important;
  width: 0 !important;
  background: none !important;
}

:root[__MARKER__="active"] aside.app-shell-left-panel,
:root[__MARKER__="active"] aside.app-shell-left-panel * {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"] aside.app-shell-left-panel
  :is(
    [class~="text-token-text-secondary"],
    [class~="text-token-text-tertiary"],
    [class*="text-token-input-placeholder-foreground"]
  ) {
  color: var(--cs-text-secondary) !important;
}

/* Only a marked active Home/task workspace may reveal the artwork beneath its
   main region. The controller assigns this marker only after a positive
   workspace signal; current Codex utilities remain marked native. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  ) {
  background:
    linear-gradient(
      rgb(var(--cs-surface-rgb) / .22),
      rgb(var(--cs-surface-rgb) / .22)
  ) !important;
  border: 0 !important;
  border-inline-start: 0 !important;
  box-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:is(
      [data-codex-skin-scope="home"],
      [data-codex-skin-scope="thread"]
    )
  )
  header:is(
    .app-header-tint,
    [data-app-shell-header-edge-scroll],
    [class*="_Header_"]
  ) {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
  border-bottom: 0 !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

/* The current ComposerLayoutRoot replaces the old sticky composer on newer
   Codex builds. Its surface, editable foreground, caret and placeholder are
   set together, so a light or dark skin cannot create an unreadable input. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"]) {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

/* The current ComposerLayoutRoot has independent body and utility-bar paint
   layers. Keep those layers paired with the composer shell instead of letting
   their native light fill show through a dark skin. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  :is(
    [class*="_ComposerLayoutBody_"],
    [class*="_ComposerFooter_"],
    [class*="_ComposerHomeUtilityBar_"]
  ) {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  background-image: none !important;
}

/* Footer controls can carry a native dark foreground even when their parent
   has been paired with a dark skin surface. Keep interactive labels and icons
   readable inside the scoped composer without touching native utility cards. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  :is(button, [role="button"], label, svg) {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  :is(button, [role="button"], label) * {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  [data-placeholder]::before {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  .ProseMirror p.is-editor-empty:first-child::before {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  :is(
    input, textarea, [contenteditable="true"], [role="textbox"],
    [class*="_RichTextInput_"]
  ) {
  color: var(--cs-text-primary) !important;
  caret-color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(.composer-surface-chrome, [class*="_ComposerLayoutRoot_"])
  :is(input, textarea)::placeholder {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

/* Conversation foregrounds are paired with the themed conversation surface,
   not with every token-labelled element in main. This intentionally excludes
   native Output and Sources cards that happen to sit beside a task. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

/* Current message leaves can carry their own token color. Override those
   leaves only beneath a positively identified message/Markdown container;
   native cards that reuse a token class remain outside this rule. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  )
  :is(
    p, li, h1, h2, h3, h4, h5, h6, strong, em, code,
    [class~="text-token-text-primary"],
    [class~="text-token-foreground"],
    [class~="text-token-conversation-body"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  )
  :is(
    [class~="bg-token-main-surface-primary"],
    [class~="bg-token-main-surface-secondary"],
    [class~="bg-token-main-surface-tertiary"]
  ) {
  color: var(--cs-text-primary) !important;
  background-color: rgb(var(--cs-surface-rgb) / .92) !important;
  background-image: none !important;
}

/* Home has no messages, so theme its heading and suggested actions only after
   the renderer's positive Home marker has selected this scope. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="home"]
  :is(
    .group\/home-suggestions,
    .group\/home-suggestions *,
    [class*="_homeUtilityBar_"],
    button[class*="_utilityBarLabel_"],
    [class~="heading-xl"],
    [class~="group/title"],
    [class~="group/title"] *,
    h1, h2, h3, [role="heading"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:is(
      [data-codex-skin-scope="home"],
      [data-codex-skin-scope="thread"]
    )
  )
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"],
    [class*="_MainContentBottomFade_"],
    [class*="_MainContentBottomGradient_"],
    .bg-gradient-to-t.from-token-main-surface-primary
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}
`
	replacer := strings.NewReplacer(
		"__MARKER__", RootMarkerAttribute,
		"__BACKGROUND_OVERLAY__", fmt.Sprintf("%.4f", tokens.BackgroundOverlay),
		"__SURFACE_OPACITY__", fmt.Sprintf("%.4f", tokens.SurfaceOpacity),
		"__SURFACE_BLUR__", strconv.Itoa(tokens.SurfaceBlurPx),
		"__SURFACE_RGB__", surfaceRGB,
		"__SIDEBAR_RGB__", sidebarRGB,
		"__TEXT_SHADOW_RGB__", textShadowRGB,
		"__TEXT_PRIMARY__", tokens.TextPrimary,
		"__TEXT_SECONDARY__", tokens.TextSecondary,
		"__ACCENT__", tokens.Accent,
		"__BORDER__", tokens.Border,
		"__RADIUS_SCALE__", fmt.Sprintf("%.4f", tokens.RadiusScale),
		"__SHADOW_ALPHA__", shadowAlpha,
	)
	return replacer.Replace(scopedWorkspaceContract), nil
}

func compileTemplateV12(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	if err := validateTemplateContrast(mode, tokens); err != nil {
		return "", err
	}
	textShadowRGB := "0 0 0"
	if mode == "light" {
		textShadowRGB = "255 255 255"
	}
	const focusedConversationContract = `:root[__MARKER__="active"] {
  --cs-background-overlay: __BACKGROUND_OVERLAY__;
  --cs-surface-opacity: __SURFACE_OPACITY__;
  --cs-surface-blur: __SURFACE_BLUR__px;
  --cs-surface-rgb: __SURFACE_RGB__;
  --cs-text-shadow-rgb: __TEXT_SHADOW_RGB__;
  --cs-text-primary: __TEXT_PRIMARY__;
  --cs-text-secondary: __TEXT_SECONDARY__;
  --cs-accent: __ACCENT__;
  --cs-border: __BORDER__;
  --cs-radius-scale: __RADIUS_SCALE__;
}

:root[__MARKER__="active"] aside.app-shell-left-panel {
  color: var(--cs-text-primary) !important;
  background: rgb(__SIDEBAR_RGB__ / var(--cs-surface-opacity)) !important;
  border-right: 1px solid var(--cs-border);
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"] aside.app-shell-left-panel::after {
  content: none !important;
  display: none !important;
  width: 0 !important;
  background: none !important;
}

:root[__MARKER__="active"] aside.app-shell-left-panel,
:root[__MARKER__="active"] aside.app-shell-left-panel * {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"] aside.app-shell-left-panel
  :is(
    [class~="text-token-text-secondary"],
    [class~="text-token-text-tertiary"],
    [class*="text-token-input-placeholder-foreground"]
  ) {
  color: var(--cs-text-secondary) !important;
}

/* Focused conversation contract v12. The artwork is painted only by a
   controller-marked Home/task main, never by body. Therefore Sites, Scheduled
   and Plugins retain exactly the official Codex System/Light/Dark surface and
   text pairing. The composer can be a sibling of main on current task pages;
   its marker is applied only after the same positive Home/thread route probe. */
:root[__MARKER__="active"] {
  --cs-scope-contract: 12;
  --cs-workspace-contract: 12;
}

/* The controller owns the positive route probe. Paint only its explicit
   conversation scopes; a generic main or heading can never reveal artwork. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="home"],
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"] {
  background-color: transparent !important;
  background-image:
    linear-gradient(
      rgb(var(--cs-surface-rgb) / .22),
      rgb(var(--cs-surface-rgb) / .22)
    ),
    linear-gradient(
      rgb(0 0 0 / var(--cs-background-overlay)),
      rgb(0 0 0 / var(--cs-background-overlay))
    ),
    var(--cs-background-image) !important;
  background-position: center, center, center !important;
  background-size: cover, cover, cover !important;
  background-attachment: fixed, fixed, fixed !important;
  border-inline-start: 0 !important;
  box-shadow: none !important;
}

/* Codex keeps one shell main while React swaps Home, conversations and native
   utility pages below it. The controller refreshes its local marker after
   those document mutations; CSS also fails closed when a retained marker no
   longer contains a live conversation/Home signal or exposes a known native
   utility anchor. This uses one non-nested :has() probe; it does not repeat
   V11's unsupported :has(...:has(...)) shape. */
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:has(
    :is(
      #appgen-site-search,
      #sites-page-search,
      #scheduled-page-search,
      #plugins-store-page-search
    )
  ),
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:not(:has(
    :is(
      .thread-scroll-container,
      [data-message-author-role],
      [data-local-conversation-user-anchor],
      [data-local-conversation-final-assistant],
      [class*="_markdown"],
      [data-testid="home-icon"],
      .group\/home-suggestions,
      .composer-surface-chrome,
      [class*="_ComposerLayoutRoot_"]
    )
  )) {
  /* Reset the native page to Codex's explicit surface colour. Using the
     background shorthand with a custom property is serialized by the current
     Chromium build as a transparent background-color, which exposes the skin
     layer while the sticky search region keeps its official bg-surface fill. */
  background-color: var(--color-surface) !important;
  background-image: none !important;
  background-position: initial !important;
  background-size: auto !important;
  background-attachment: scroll !important;
  border-inline-start: 1px solid var(--color-border) !important;
  box-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  ),
:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  )
  :is(
    p, li, h1, h2, h3, h4, h5, h6, strong, em, code,
    [class~="text-token-text-primary"],
    [class~="text-token-foreground"],
    [class~="text-token-conversation-body"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  )
  :is([class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]) {
  color: var(--cs-text-secondary) !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [data-local-conversation-user-anchor],
    [data-local-conversation-final-assistant],
    [class*="_markdown"]
  )
  :is(
    [class~="bg-token-main-surface-primary"],
    [class~="bg-token-main-surface-secondary"],
    [class~="bg-token-main-surface-tertiary"],
    [class*="bg-surface-elevated-secondary"]
  ) {
  color: var(--cs-text-primary) !important;
  background-color: rgb(var(--cs-surface-rgb) / .92) !important;
  background-image: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"][data-codex-skin-scope="home"]
  :is(
    .group\/home-suggestions,
    .group\/home-suggestions *,
    [class*="_homeUtilityBar_"],
    button[class*="_utilityBarLabel_"],
    [class~="heading-xl"],
    [class~="group/title"],
    [class~="group/title"] *,
    h1, h2, h3, [role="heading"]
  ) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]
  main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  )
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"],
    [class*="_MainContentBottomFade_"],
    [class*="_MainContentBottomGradient_"],
    [class~="bg-gradient-to-t"][class~="from-token-main-surface-primary"],
    [class~="bg-gradient-to-t"][class~="from-surface"][class~="via-surface"]
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}

:root[__MARKER__="active"] [data-codex-skin-composer="true"] {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  background-image: none !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"] [data-codex-skin-composer="true"]
  :is(
  [class*="_ComposerLayoutBody_"],
  [class*="_ComposerFooter_"],
  [class*="_ComposerHomeUtilityBar_"]
  ) {
  color: var(--cs-text-primary) !important;
	background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  background-image: none !important;
}

:root[__MARKER__="active"] [data-codex-skin-composer="true"]
  :is(
  button, [role="button"], label, svg,
	button *, [role="button"] *, label *,
  input, textarea, [contenteditable="true"], [role="textbox"], [class*="_RichTextInput_"]
  ) {
  color: var(--cs-text-primary) !important;
  caret-color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"] [data-codex-skin-composer="true"] [data-placeholder]::before,
:root[__MARKER__="active"] [data-codex-skin-composer="true"]
  .ProseMirror p.is-editor-empty:first-child::before,
:root[__MARKER__="active"] [data-codex-skin-composer="true"]
  :is(input, textarea)::placeholder {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

/* V11 source is retained solely as an exact migration payload. It used nested
   relational selectors that current Chromium rejects, so it must not be
   emitted as part of V12. */
/*
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"]:is(
    [data-codex-skin-scope="home"],
    [data-codex-skin-scope="thread"]
  ) {
	background-color: transparent !important;
  background-image:
    linear-gradient(
      rgb(var(--cs-surface-rgb) / .22),
      rgb(var(--cs-surface-rgb) / .22)
    ),
    linear-gradient(
      rgb(0 0 0 / var(--cs-background-overlay)),
      rgb(0 0 0 / var(--cs-background-overlay))
    ),
    var(--cs-background-image) !important;
  background-position: center, center, center !important;
  background-size: cover, cover, cover !important;
  background-attachment: fixed, fixed, fixed !important;
	border-inline-start: 0 !important;
	box-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"],
        .thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) :is(
  .app-shell-main-content-top-fade,
  [data-app-shell-main-content-top-fade],
  [class*="_MainContentTopFade_"]
  ) {
	display: none !important;
	opacity: 0 !important;
	background: none !important;
	background-image: none !important;
	box-shadow: none !important;
	backdrop-filter: none !important;
	transition: none !important;
}

V11 legacy note: apply-on-demand route branch retained only for migration text.
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"] {
	background-color: transparent !important;
	background-image:
	  linear-gradient(rgb(var(--cs-surface-rgb) / .22), rgb(var(--cs-surface-rgb) / .22)),
	  linear-gradient(
	    rgb(0 0 0 / var(--cs-background-overlay)),
	    rgb(0 0 0 / var(--cs-background-overlay))
	  ),
	  var(--cs-background-image) !important;
	background-position: center, center, center !important;
	background-size: cover, cover, cover !important;
	background-attachment: fixed, fixed, fixed !important;
	border-inline-start: 0 !important;
	box-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"]
  :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"]) {
	color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) :is(
	[data-codex-skin-composer="true"],
	.composer-surface-chrome,
	[class*="_ComposerLayoutRoot_"],
	div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  ) {
	color: var(--cs-text-primary) !important;
	background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
	background-image: none !important;
	border-color: var(--cs-border) !important;
	border-radius: calc(16px * var(--cs-radius-scale)) !important;
	box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__) !important;
	backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) :is(
	[data-codex-skin-composer="true"],
	.composer-surface-chrome,
	[class*="_ComposerLayoutRoot_"],
	div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  ) :is(
	[class*="_ComposerLayoutBody_"],
	[class*="_ComposerFooter_"],
	[class*="_ComposerHomeUtilityBar_"],
	button, [role="button"], label, svg,
	input, textarea, [contenteditable="true"], [role="textbox"], [class*="_RichTextInput_"]
  ) {
	color: var(--cs-text-primary) !important;
	caret-color: var(--cs-text-primary) !important;
	background-image: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) :is(
	[data-codex-skin-composer="true"],
	.composer-surface-chrome,
	[class*="_ComposerLayoutRoot_"],
	div.sticky:has(:is(input[type="text"], textarea, [contenteditable="true"], [role="textbox"]))
  ) :is([data-placeholder]::before, .ProseMirror p.is-editor-empty:first-child::before, input::placeholder, textarea::placeholder) {
	color: var(--cs-text-secondary) !important;
	opacity: 1 !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) :is(
	[class*="_MainContentBottomFade_"],
	[class*="_MainContentBottomGradient_"],
	.bg-gradient-to-t.from-token-main-surface-primary
  ) {
	display: none !important;
	opacity: 0 !important;
	background: none !important;
	background-image: none !important;
	box-shadow: none !important;
	backdrop-filter: none !important;
	transition: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"]
  :is(
    .thread-scroll-container,
    [data-message-author-role],
    [class*="_markdown"],
    .group\/home-suggestions,
    [data-testid="home-icon"],
    [class~="heading-xl"]
  ) {
	color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"]
  :is([class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]) {
	color: var(--cs-text-secondary) !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) main[data-codex-skin-main="true"]
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"],
    [class*="_MainContentBottomFade_"],
    [class*="_MainContentBottomGradient_"],
    .bg-gradient-to-t.from-token-main-surface-primary
  ) {
	display: none !important;
	opacity: 0 !important;
	background: none !important;
	background-image: none !important;
	box-shadow: none !important;
	backdrop-filter: none !important;
	transition: none !important;
}

V11 legacy note: composer marker branch retained only for migration text.
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"],
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer-boundary="true"] {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  background-image: none !important;
  border-color: var(--cs-border) !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"] {
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  :is(
    [class*="_ComposerLayoutBody_"],
    [class*="_ComposerFooter_"],
    [class*="_ComposerHomeUtilityBar_"]
  ) {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  background-image: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  :is(button, [role="button"], label, svg),
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  :is(button, [role="button"], label) *,
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  :is(input, textarea, [contenteditable="true"], [role="textbox"], [class*="_RichTextInput_"]) {
  color: var(--cs-text-primary) !important;
  caret-color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"] [data-placeholder]::before,
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  .ProseMirror p.is-editor-empty:first-child::before,
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer="true"]
  :is(input, textarea)::placeholder {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

V11 legacy note: local lower-fade branch retained only for migration text.
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer-boundary="true"]
  :is(
    [class*="_MainContentBottomFade_"],
    [class*="_MainContentBottomGradient_"],
    .bg-gradient-to-t.from-token-main-surface-primary
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
	transition: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer-boundary="true"]::before,
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"][data-codex-skin-scope="home"]:has(
      :is([data-testid="home-icon"], .group\/home-suggestions, [class~="heading-xl"])
    ),
    main[data-codex-skin-main="true"][data-codex-skin-scope="thread"]:has(
      :is(.thread-scroll-container, [data-message-author-role], [class*="_markdown"])
    )
  ) [data-codex-skin-composer-boundary="true"]::after {
	background: none !important;
	background-image: none !important;
	box-shadow: none !important;
}
*/
`
	replacer := strings.NewReplacer(
		"__MARKER__", RootMarkerAttribute,
		"__BACKGROUND_OVERLAY__", fmt.Sprintf("%.4f", tokens.BackgroundOverlay),
		"__SURFACE_OPACITY__", fmt.Sprintf("%.4f", tokens.SurfaceOpacity),
		"__SURFACE_BLUR__", strconv.Itoa(tokens.SurfaceBlurPx),
		"__SURFACE_RGB__", surfaceRGB,
		"__SIDEBAR_RGB__", sidebarRGB,
		"__TEXT_SHADOW_RGB__", textShadowRGB,
		"__TEXT_PRIMARY__", tokens.TextPrimary,
		"__TEXT_SECONDARY__", tokens.TextSecondary,
		"__ACCENT__", tokens.Accent,
		"__BORDER__", tokens.Border,
		"__RADIUS_SCALE__", fmt.Sprintf("%.4f", tokens.RadiusScale),
		"__SHADOW_ALPHA__", shadowAlpha,
	)
	return replacer.Replace(focusedConversationContract), nil
}

func compileTemplateV7(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV6(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const shellEdgeContract = `

/* Fixed shell-edge contract v7. Codex 26.727 exposes stable data attributes
   and CSS-module prefixes for the header and top fade. Keep all fallbacks in
   the signed Helper template; theme packages cannot provide selectors. */
:root[__MARKER__="active"] {
  --cs-shell-edge-contract: 7;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  main[data-codex-skin-main="true"]
  > header:is(
    .app-header-tint,
    [data-app-shell-header-edge-scroll],
    [class*="_Header_"]
  ) {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
  border-bottom: 0 !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  :is(
    .app-shell-main-content-top-fade,
    [data-app-shell-main-content-top-fade],
    [class*="_MainContentTopFade_"]
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}
`
	return style + strings.ReplaceAll(shellEdgeContract, "__MARKER__", RootMarkerAttribute), nil
}

func compileTemplateV6(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV5(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const topFadeContract = `

/* Fixed top-fade contract v6. Codex 26.727 moved the native top fade from
   the stable app-shell class to a CSS-module MainContentTopFade class. Both
   variants are adapter-owned fixed selectors; theme packages cannot supply
   selectors or executable CSS. */
:root[__MARKER__="active"] {
  --cs-top-fade-contract: 6;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  :is(
    .app-shell-main-content-top-fade,
    [class*="_MainContentTopFade_"]
  ) {
  display: none !important;
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}
`
	return style + strings.ReplaceAll(topFadeContract, "__MARKER__", RootMarkerAttribute), nil
}

func compileTemplateV5(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV4(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	colorScheme := "dark"
	if mode == "light" {
		colorScheme = "light"
	}
	const nativeTokenBridge = `

/* Fixed native-token bridge v5. Codex dropdown/popover primitives may begin
   in the user's native appearance. The scoped theme contract carries its own
   colour scheme, so switching a skin never rewrites that user preference. */
:root[__MARKER__="active"] {
  --cs-native-token-contract: 5;
  color-scheme: __COLOR_SCHEME__;
  --color-token-dropdown-background: rgb(var(--cs-surface-rgb) / .98);
}

:root[__MARKER__="active"] [class~="bg-token-dropdown-background"] {
  color: var(--cs-text-primary) !important;
  --color-token-foreground: var(--cs-text-primary);
  --color-token-text-secondary: var(--cs-text-secondary);
  --color-token-muted-foreground: var(--cs-text-secondary);
  --color-token-description-foreground: var(--cs-text-secondary);
  --color-token-list-hover-background: rgb(var(--cs-surface-rgb) / .92);
  --color-token-border: var(--cs-border);
  --color-token-border-default: var(--cs-border);
}

:root[__MARKER__="active"] [class~="bg-token-dropdown-background"]
  :is([class~="text-token-text-primary"], [class~="text-token-foreground"]) {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"] [class~="bg-token-dropdown-background"]
  :is([class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]) {
  color: var(--cs-text-secondary) !important;
}
`
	bridge := strings.ReplaceAll(nativeTokenBridge, "__MARKER__", RootMarkerAttribute)
	bridge = strings.ReplaceAll(bridge, "__COLOR_SCHEME__", colorScheme)
	return style + bridge, nil
}

func compileTemplateV4(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV3(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const diffResourceRule = `

/* Fixed conversation diff/resource contract v4. Codex renders file-edit
   summaries outside the activity-header component. Keep this selector tied
   to the stable diff resource-card variable token instead of recolouring the
   whole main surface or native utility routes. */
:root[__MARKER__="active"] {
  --cs-diff-resource-contract: 4;
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  [class~="[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"]
  :is(button, a, [role="button"])[class~="text-token-text-primary"],
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  [class~="[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"]
  :is(button, a, [role="button"])[class~="text-token-text-primary"]
  *,
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  [class~="[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"]
  :is([class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]) {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}`
	return style + strings.ReplaceAll(diffResourceRule, "__MARKER__", RootMarkerAttribute), nil
}

func compileTemplateV3(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	style, err := compileTemplateV2(mode, tokens, sidebarRGB, surfaceRGB, shadowAlpha)
	if err != nil {
		return "", err
	}
	const previousActivityRule = `/* Reasoning summaries and tool activity disclosures live outside Markdown
   message bodies. Scope their contrast to the dedicated activity-header
   component so resource cards and native utility surfaces keep their
   official foreground/background pairing. */
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header,
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header
  * {
  color: var(--cs-text-secondary) !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header
  svg {
  color: var(--cs-accent) !important;
}`
	const currentActivityRule = `/* Fixed activity contract v3. Reasoning summaries and tool activity
   disclosures render after apply, so the selector must remain safe for
   future nodes as well as those present during verification. */
:root[__MARKER__="active"] {
  --cs-activity-contract: 3;
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  :is(button.group\/activity-header, button[class~="group/activity-header"]),
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  :is(button.group\/activity-header, button[class~="group/activity-header"])
  * {
  color: var(--cs-text-primary) !important;
  text-shadow: none !important;
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  :is(button.group\/activity-header, button[class~="group/activity-header"])
  svg {
  color: var(--cs-accent) !important;
}`
	previous := strings.ReplaceAll(previousActivityRule, "__MARKER__", RootMarkerAttribute)
	current := strings.ReplaceAll(currentActivityRule, "__MARKER__", RootMarkerAttribute)
	if !strings.Contains(style, previous) {
		return "", fmt.Errorf("%w: template v2 activity contract", ErrConfiguration)
	}
	return strings.Replace(style, previous, current, 1), nil
}

func compileTemplateV2(
	mode string,
	tokens theme.Tokens,
	sidebarRGB string,
	surfaceRGB string,
	shadowAlpha string,
) (string, error) {
	if err := validateTemplateContrast(mode, tokens); err != nil {
		return "", err
	}
	textShadowRGB := "0 0 0"
	if mode == "light" {
		textShadowRGB = "255 255 255"
	}
	const template = `:root[__MARKER__="active"] {
  --cs-background-overlay: __BACKGROUND_OVERLAY__;
  --cs-surface-opacity: __SURFACE_OPACITY__;
  --cs-surface-blur: __SURFACE_BLUR__px;
  --cs-surface-rgb: __SURFACE_RGB__;
  --cs-text-shadow-rgb: __TEXT_SHADOW_RGB__;
  --cs-text-primary: __TEXT_PRIMARY__;
  --cs-text-secondary: __TEXT_SECONDARY__;
  --cs-accent: __ACCENT__;
  --cs-border: __BORDER__;
  --cs-radius-scale: __RADIUS_SCALE__;
}

/* The artwork belongs to the window, but native utility routes keep their
   own opaque main surface. Only Home and conversation structures opt in to
   the immersive main treatment below. */
:root[__MARKER__="active"] body {
  background:
    linear-gradient(
      rgb(0 0 0 / var(--cs-background-overlay)),
      rgb(0 0 0 / var(--cs-background-overlay))
    ),
    var(--cs-background-image) center / cover fixed !important;
}

:root[__MARKER__="active"]
  aside.app-shell-left-panel {
  color: var(--cs-text-primary) !important;
  background: rgb(__SIDEBAR_RGB__ / var(--cs-surface-opacity)) !important;
  border-right: 1px solid var(--cs-border);
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"]
  aside.app-shell-left-panel::after {
  content: none !important;
  display: none !important;
  width: 0 !important;
  background: none !important;
}

:root[__MARKER__="active"]
  aside.app-shell-left-panel,
:root[__MARKER__="active"]
  aside.app-shell-left-panel * {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  aside.app-shell-left-panel
  :is(
    [class~="text-token-text-secondary"],
    [class~="text-token-text-tertiary"],
    [class*="text-token-input-placeholder-foreground"]
  ) {
  color: var(--cs-text-secondary) !important;
}

/* Native-safe default: Pull Requests, Sites, Scheduled, Plugins and other
   utility routes do not match this structural opt-in, so their official
   main background and official text colors remain untouched. */
:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  main[data-codex-skin-main="true"] {
  background:
    linear-gradient(
      rgb(var(--cs-surface-rgb) / .22),
      rgb(var(--cs-surface-rgb) / .22)
    ) !important;
  border: 0 !important;
  box-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  main[data-codex-skin-main="true"]
  > header.app-header-tint {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
  border-bottom: 0 !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  .app-shell-main-content-top-fade {
  opacity: 0 !important;
  background: none !important;
  background-image: none !important;
  box-shadow: none !important;
  backdrop-filter: none !important;
  transition: none !important;
}

/* Codex paints another opaque gradient immediately above the composer.
   The composer owns its readable surface, so this layer must stay clear. */
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  .bg-gradient-to-t.from-token-main-surface-primary {
  background: transparent !important;
  background-image: none !important;
  box-shadow: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  :is(
    .composer-surface-chrome,
    .group\/home-suggestions button,
    main[data-codex-skin-main="true"] [class*="_homeUtilityBar_"],
    main[data-codex-skin-main="true"] div.sticky:has(input[type="text"], textarea)
  ) {
  color: var(--cs-text-primary) !important;
  background: rgb(var(--cs-surface-rgb) / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  box-shadow: 0 18px 50px rgb(0 0 0 / __SHADOW_ALPHA__);
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%);
}

:root[__MARKER__="active"]
  .composer-surface-chrome::before {
  content: none !important;
  display: none !important;
  background: none !important;
}

:root[__MARKER__="active"]:has(
    main[data-codex-skin-main="true"] :is(.composer-surface-chrome, .thread-scroll-container)
  )
  main[data-codex-skin-main="true"]
  div.sticky:has(input[type="text"], textarea)::after {
  content: none !important;
  display: none !important;
  background: none !important;
}

:root[__MARKER__="active"]
  :is(.group\/home-suggestions button, .composer-surface-chrome button):hover {
  border-color: var(--cs-accent) !important;
}

:root[__MARKER__="active"]
  :is(.group\/home-suggestions button, .composer-surface-chrome) svg {
  color: var(--cs-accent) !important;
}

:root[__MARKER__="active"]
  :is(
    .group\/home-suggestions button [class~="text-token-text-primary"],
    .composer-surface-chrome p,
    .composer-surface-chrome input,
    .composer-surface-chrome textarea,
    .composer-surface-chrome [class~="text-token-text-primary"]
  ) {
  color: var(--cs-text-primary) !important;
}

:root[__MARKER__="active"]
  :is(
    .composer-surface-chrome [class~="text-token-text-secondary"],
    .composer-surface-chrome [class~="text-token-text-tertiary"]
  ) {
  color: var(--cs-text-secondary) !important;
}

:root[__MARKER__="active"]
  .composer-surface-chrome input::placeholder,
:root[__MARKER__="active"]
  .composer-surface-chrome textarea::placeholder {
  color: var(--cs-text-secondary) !important;
  opacity: 1 !important;
}

/* Conversation text is themed only inside known message structures. Native
   Output/Sources cards, Terminal, dialogs and popovers are not descendants
   of these selectors and retain official surface/text pairing. */
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  :is([class*="_markdown"], [data-message-author-role]) {
  color: var(--cs-text-primary) !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  :is([class*="_markdown"], [data-message-author-role])
  :is(
    p,
    li,
    h1,
    h2,
    h3,
    h4,
    h5,
    h6,
    strong,
    em,
    code,
    [class~="text-token-text-primary"],
    [class~="text-token-foreground"]
  ) {
  color: inherit !important;
}

/* Reasoning summaries and tool activity disclosures live outside Markdown
   message bodies. Scope their contrast to the dedicated activity-header
   component so resource cards and native utility surfaces keep their
   official foreground/background pairing. */
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header,
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header
  * {
  color: var(--cs-text-secondary) !important;
  text-shadow:
    0 1px 2px rgb(var(--cs-text-shadow-rgb) / .82),
    0 0 10px rgb(var(--cs-text-shadow-rgb) / .56);
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .thread-scroll-container)
  .thread-scroll-container
  button.group\/activity-header
  svg {
  color: var(--cs-accent) !important;
}

:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .group\/home-suggestions)
  main[data-codex-skin-main="true"]
  [class~="text-token-text-primary"],
:root[__MARKER__="active"]:has(main[data-codex-skin-main="true"] .group\/home-suggestions)
  main[data-codex-skin-main="true"]
  button[class*="_utilityBarLabel_"] {
  color: var(--cs-text-primary) !important;
}
`
	replacer := strings.NewReplacer(
		"__MARKER__", RootMarkerAttribute,
		"__BACKGROUND_OVERLAY__", fmt.Sprintf("%.4f", tokens.BackgroundOverlay),
		"__SURFACE_OPACITY__", fmt.Sprintf("%.4f", tokens.SurfaceOpacity),
		"__SURFACE_BLUR__", strconv.Itoa(tokens.SurfaceBlurPx),
		"__SURFACE_RGB__", surfaceRGB,
		"__SIDEBAR_RGB__", sidebarRGB,
		"__TEXT_SHADOW_RGB__", textShadowRGB,
		"__TEXT_PRIMARY__", tokens.TextPrimary,
		"__TEXT_SECONDARY__", tokens.TextSecondary,
		"__ACCENT__", tokens.Accent,
		"__BORDER__", tokens.Border,
		"__RADIUS_SCALE__", fmt.Sprintf("%.4f", tokens.RadiusScale),
		"__SHADOW_ALPHA__", shadowAlpha,
	)
	return replacer.Replace(template), nil
}

type rgbColor struct {
	red   float64
	green float64
	blue  float64
}

func validateTemplateContrast(mode string, tokens theme.Tokens) error {
	surface := rgbColor{red: 20, green: 24, blue: 32}
	if mode == "light" {
		surface = rgbColor{red: 249, green: 252, blue: 255}
	}
	for name, candidate := range map[string]string{
		"textPrimary":   tokens.TextPrimary,
		"textSecondary": tokens.TextSecondary,
	} {
		color, err := parseThemeColor(candidate, surface)
		if err != nil {
			return fmt.Errorf("%w: %s color", ErrConfiguration, name)
		}
		if ratio := contrastRatio(color, surface); ratio < 4.5 {
			return fmt.Errorf("%w: %s contrast %.2f is below 4.5", ErrConfiguration, name, ratio)
		}
		luminance := relativeLuminance(color)
		switch {
		case mode == "dark" && name == "textPrimary" && luminance < 0.75:
			return fmt.Errorf("%w: dark textPrimary is not near-white", ErrConfiguration)
		case mode == "dark" && name == "textSecondary" && luminance < 0.45:
			return fmt.Errorf("%w: dark textSecondary is not light enough", ErrConfiguration)
		case mode == "light" && name == "textPrimary" && luminance > 0.08:
			return fmt.Errorf("%w: light textPrimary is not near-black", ErrConfiguration)
		case mode == "light" && name == "textSecondary" && luminance > 0.20:
			return fmt.Errorf("%w: light textSecondary is not dark enough", ErrConfiguration)
		}
	}
	accent, err := parseThemeColor(tokens.Accent, surface)
	if err != nil {
		return fmt.Errorf("%w: accent color", ErrConfiguration)
	}
	if ratio := contrastRatio(accent, surface); ratio < 3 {
		return fmt.Errorf("%w: accent contrast %.2f is below 3.0", ErrConfiguration, ratio)
	}
	return nil
}

func parseThemeColor(value string, surface rgbColor) (rgbColor, error) {
	if len(value) != 7 && len(value) != 9 || value[0] != '#' {
		return rgbColor{}, ErrConfiguration
	}
	component := func(start int) (float64, error) {
		parsed, err := strconv.ParseUint(value[start:start+2], 16, 8)
		return float64(parsed), err
	}
	red, err := component(1)
	if err != nil {
		return rgbColor{}, err
	}
	green, err := component(3)
	if err != nil {
		return rgbColor{}, err
	}
	blue, err := component(5)
	if err != nil {
		return rgbColor{}, err
	}
	alpha := 1.0
	if len(value) == 9 {
		parsed, parseErr := component(7)
		if parseErr != nil {
			return rgbColor{}, parseErr
		}
		alpha = parsed / 255
	}
	return rgbColor{
		red:   red*alpha + surface.red*(1-alpha),
		green: green*alpha + surface.green*(1-alpha),
		blue:  blue*alpha + surface.blue*(1-alpha),
	}, nil
}

func contrastRatio(left rgbColor, right rgbColor) float64 {
	leftLuminance := relativeLuminance(left)
	rightLuminance := relativeLuminance(right)
	if leftLuminance < rightLuminance {
		leftLuminance, rightLuminance = rightLuminance, leftLuminance
	}
	return (leftLuminance + 0.05) / (rightLuminance + 0.05)
}

func relativeLuminance(color rgbColor) float64 {
	channel := func(value float64) float64 {
		value /= 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(color.red) +
		0.7152*channel(color.green) +
		0.0722*channel(color.blue)
}
