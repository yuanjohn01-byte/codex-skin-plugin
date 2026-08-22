package adapter

import (
	"strings"
	"testing"
)

func TestAppearanceUIPlatformGateKeepsWindowsOnRestartFallback(t *testing.T) {
	if !supportsInAppAppearance("darwin") {
		t.Fatal("macOS in-app Appearance path is disabled")
	}
	for _, platform := range []string{"windows", "linux", ""} {
		if supportsInAppAppearance(platform) {
			t.Fatalf("%q unexpectedly enabled the macOS Appearance path", platform)
		}
	}
}

func TestAppearanceControlsReadyRequiresExactSemanticContract(t *testing.T) {
	state := appearanceUIState{
		TrustedOrigin:     true,
		BridgeAvailable:   true,
		AppearanceEntries: 1,
		RadioCount:        3,
		VisibleRadios:     3,
		BackLinks:         1,
		Checked:           []bool{false, false, true},
		SystemVariant:     "dark",
		DarkMedia:         true,
		ColorScheme:       "dark",
		BackgroundSurface: "#181818",
		TextForeground:    "#dfdfdf",
		BodyColor:         "rgb(223, 223, 223)",
		TimeOrigin:        42,
	}
	if !appearanceControlsReady(state, "dark") {
		t.Fatal("exact dark Appearance contract was rejected")
	}

	tests := []struct {
		name   string
		mutate func(*appearanceUIState)
	}{
		{"untrusted origin", func(state *appearanceUIState) { state.TrustedOrigin = false }},
		{"ambiguous navigation", func(state *appearanceUIState) { state.AppearanceEntries = 2 }},
		{"hidden radio", func(state *appearanceUIState) { state.VisibleRadios = 2 }},
		{"ambiguous selection", func(state *appearanceUIState) { state.Checked[1] = true }},
		{"wrong system variant", func(state *appearanceUIState) { state.SystemVariant = "light" }},
		{"stale media query", func(state *appearanceUIState) { state.DarkMedia = false }},
		{"missing native palette", func(state *appearanceUIState) { state.BackgroundSurface = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := state
			candidate.Checked = append([]bool(nil), state.Checked...)
			test.mutate(&candidate)
			if appearanceControlsReady(candidate, "dark") {
				t.Fatal("invalid Appearance structure was accepted")
			}
		})
	}
}

func TestAppearanceControlsReadyAcceptsSystemWithVerifiedEffectiveMode(t *testing.T) {
	state := appearanceUIState{
		TrustedOrigin:     true,
		BridgeAvailable:   true,
		AppearanceEntries: 1,
		RadioCount:        3,
		VisibleRadios:     3,
		BackLinks:         1,
		Checked:           []bool{true, false, false},
		SystemVariant:     "light",
		ColorScheme:       "light",
		BackgroundSurface: "#ffffff",
		TextForeground:    "#1a1c1f",
		BodyColor:         "rgb(26, 28, 31)",
		TimeOrigin:        42,
	}
	if !appearanceControlsReady(state, "system") {
		t.Fatal("verified system/light Appearance state was rejected")
	}
	state.DarkMedia = true
	if appearanceControlsReady(state, "system") {
		t.Fatal("system setting accepted a stale dark media query")
	}
}

func TestAppearanceStateMatchesRejectsStalePaletteAndRendererReplacement(t *testing.T) {
	baseline := appearanceUIState{
		TrustedOrigin:     true,
		BridgeAvailable:   true,
		AppearanceEntries: 1,
		RadioCount:        3,
		VisibleRadios:     3,
		BackLinks:         1,
		Checked:           []bool{false, false, true},
		SystemVariant:     "dark",
		DarkMedia:         true,
		ColorScheme:       "dark",
		BackgroundSurface: "#181818",
		TextForeground:    "#dfdfdf",
		BodyColor:         "rgb(223, 223, 223)",
		TimeOrigin:        42,
	}
	light := baseline
	light.Checked = []bool{false, true, false}
	light.SystemVariant = "light"
	light.DarkMedia = false
	light.ColorScheme = "light"
	if appearanceStateMatches(light, "light", "light", baseline) {
		t.Fatal("light setting accepted the stale dark native palette")
	}
	light.BackgroundSurface = "#ffffff"
	light.TextForeground = "#1a1c1f"
	light.BodyColor = "rgb(26, 28, 31)"
	if !appearanceStateMatches(light, "light", "light", baseline) {
		t.Fatal("settled light state was rejected")
	}
	light.TimeOrigin++
	if appearanceStateMatches(light, "light", "light", baseline) {
		t.Fatal("replacement renderer was accepted as an in-document switch")
	}
}

func TestReturnedAppearanceRequiresOriginalRouteAndRenderer(t *testing.T) {
	baseline := appearanceUIState{
		TrustedOrigin:     true,
		BridgeAvailable:   true,
		Route:             "/settings/appearance",
		AppearanceEntries: 1,
		RadioCount:        3,
		VisibleRadios:     3,
		Checked:           []bool{false, false, true},
		BackLinks:         1,
		SystemVariant:     "dark",
		DarkMedia:         true,
		ColorScheme:       "dark",
		BackgroundSurface: "#181818",
		TextForeground:    "#dfdfdf",
		BodyColor:         "rgb(223, 223, 223)",
		TimeOrigin:        42,
	}
	returned := baseline
	returned.Route = "/"
	returned.AppearanceEntries = 0
	returned.RadioCount = 0
	returned.VisibleRadios = 0
	returned.Checked = nil
	returned.BackLinks = 0
	returned.SystemVariant = "light"
	returned.DarkMedia = false
	returned.ColorScheme = "light"
	returned.BackgroundSurface = "#ffffff"
	returned.TextForeground = "#1a1c1f"
	returned.BodyColor = "rgb(26, 28, 31)"
	if !returnedAppearanceMatches(returned, "light", baseline, "/", false) {
		t.Fatal("verified returned Appearance state was rejected")
	}

	tests := []struct {
		name   string
		mutate func(*appearanceUIState)
	}{
		{"wrong route", func(state *appearanceUIState) { state.Route = "/settings/appearance" }},
		{"settings controls remain", func(state *appearanceUIState) { state.RadioCount = 3 }},
		{"renderer replaced", func(state *appearanceUIState) { state.TimeOrigin++ }},
		{"stale palette", func(state *appearanceUIState) {
			state.BackgroundSurface = baseline.BackgroundSurface
			state.TextForeground = baseline.TextForeground
			state.BodyColor = baseline.BodyColor
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := returned
			test.mutate(&candidate)
			if returnedAppearanceMatches(candidate, "light", baseline, "/", false) {
				t.Fatal("invalid returned Appearance state was accepted")
			}
		})
	}
	rolledBack := baseline
	rolledBack.Route = "/"
	rolledBack.AppearanceEntries = 0
	rolledBack.RadioCount = 0
	rolledBack.VisibleRadios = 0
	rolledBack.Checked = nil
	rolledBack.BackLinks = 0
	if !returnedAppearanceMatches(rolledBack, "dark", baseline, "/", true) {
		t.Fatal("exact rollback palette was rejected after returning to the original route")
	}
	rolledBack.BodyColor = "rgb(1, 2, 3)"
	if returnedAppearanceMatches(rolledBack, "dark", baseline, "/", true) {
		t.Fatal("rollback accepted a changed native palette after Back")
	}
}

func TestCurrentAppearanceMatchRequiresLiveHostAndEffectivePalette(t *testing.T) {
	state := appearanceUIState{
		TrustedOrigin:     true,
		BridgeAvailable:   true,
		Route:             "/",
		SystemVariant:     "light",
		ColorScheme:       "light",
		BackgroundSurface: "#ffffff",
		TextForeground:    "#1a1c1f",
		BodyColor:         "rgb(26, 28, 31)",
		TimeOrigin:        42,
	}
	if !currentAppearanceMatchesTarget(state, "light", "light") {
		t.Fatal("verified current light renderer was rejected")
	}
	state.SystemVariant = "dark"
	state.DarkMedia = true
	state.ColorScheme = "dark"
	if currentAppearanceMatchesTarget(state, "light", "light") {
		t.Fatal("disk/host light incorrectly accepted a live dark renderer")
	}
	state.SystemVariant = "light"
	state.DarkMedia = false
	state.ColorScheme = "light"
	if currentAppearanceMatchesTarget(state, "dark", "light") {
		t.Fatal("mismatched live host setting was accepted")
	}
}

func TestAppearanceUIContractDoesNotUseLocalizedLabelsOrCoordinates(t *testing.T) {
	contracts := []string{
		appearanceUIProfileTriggerFunction,
		appearanceUISettingsMenuItemFunction,
		appearanceUINavigationFunction,
		appearanceUISelectFunction,
		appearanceUIBackFunction,
	}
	for _, contract := range contracts {
		for _, forbidden := range []string{
			"Settings", "Appearance", "Back to app", "elementFromPoint", "clientX", "clientY",
		} {
			if strings.Contains(contract, forbidden) {
				t.Fatalf("UI contract contains localized or coordinate selector %q", forbidden)
			}
		}
	}
	for _, required := range []string{
		`getAttribute("aria-expanded")`,
		`getAttribute("aria-controls")`,
		`visibleMenus.length !== 1`,
		`item.closest('[role="menu"]') === menu`,
	} {
		if !strings.Contains(appearanceUISettingsMenuItemFunction, required) {
			t.Fatalf("Settings selector is not scoped to the verified profile menu: missing %q", required)
		}
	}
}
