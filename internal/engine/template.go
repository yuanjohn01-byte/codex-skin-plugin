package engine

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

func CompileTheme(verified theme.Verified, stagedRoot string) (CompiledTheme, error) {
	manifest := verified.Manifest
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
	style := fmt.Sprintf(`:root[%s="active"] {
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
:root[%s="active"] main.main-surface,
:root[%s="active"] aside.app-shell-left-panel {
  background: transparent !important;
  color: var(--cs-text-primary) !important;
}
:root[%s="active"] main.main-surface {
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
:root[%s="active"] main.main-surface div.sticky:has(input[type="text"]) {
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
:root[%s="active"] main.main-surface [class~="text-token-text-primary"],
:root[%s="active"] main.main-surface [class~="text-token-foreground"],
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
:root[%s="active"] main.main-surface div.sticky:has(input[type="text"]) button {
  color: var(--cs-text-primary) !important;
  background: transparent !important;
}
:root[%s="active"] main.main-surface [class*="_homeUtilityBar_"] {
  color: var(--cs-text-primary) !important;
  background: rgb(%s / var(--cs-surface-opacity)) !important;
  border: 1px solid var(--cs-border) !important;
  border-radius: calc(16px * var(--cs-radius-scale)) !important;
  backdrop-filter: blur(var(--cs-surface-blur)) saturate(108%%);
}
:root[%s="active"] main.main-surface button[class*="_utilityBarLabel_"] {
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
	return CompiledTheme{
		ThemePublicID:     manifest.ThemePublicID,
		ThemeVersion:      manifest.ThemeVersion,
		TemplateVersion:   TemplateVersion,
		StyleText:         style,
		BackgroundDataURL: imageURL,
	}, nil
}
