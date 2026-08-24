package renderer

import (
	"strings"
	"testing"
)

func TestSelectorContractIsVersionedAndComplete(t *testing.T) {
	contract, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if contract.Schema != Schema || contract.Source.Release != "v1.5.11" ||
		contract.Source.Commit != "1c7a859ed51bcfe4923f6483a5625a6e63f97657" {
		t.Fatalf("unexpected contract source: %#v", contract)
	}
	selectors, err := SelectorMap()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"shell-main", "left-panel", "header-tint", "main-content-top-fade",
		"home-route-css", "home-banners", "home-title", "native-utility-route",
		"markdown", "conversation-bottom-fade", "overlay-menu", "overlay-popper",
	} {
		if selectors[key] == "" {
			t.Fatalf("missing selector %q", key)
		}
	}
	if selectors["shell-main"] == "main.main-surface" {
		t.Fatal("v2 shell selector must include stable data/CSS-module fallbacks")
	}
	if selectors["home-route"] != `[role="main"]:has([data-testid="home-icon"])` ||
		selectors["home-route-css"] != `[role="main"]` {
		t.Fatal("v2 home route must preserve separate DOM and CSS-safe selectors")
	}
	if selectors["home-title"] != `[class~="heading-xl"][class~="text-default"]` {
		t.Fatal("v2 home title must identify the current home greeting")
	}
	if selectors["native-utility-route"] != `:is(#appgen-site-search, #sites-page-search, #scheduled-page-search, #plugins-store-page-search)` {
		t.Fatal("v2 native utility route must exclude current Sites, Scheduled and Plugins pages")
	}
	if !strings.Contains(selectors["conversation-bottom-fade"], `[class~="from-surface"][class~="via-surface"]`) {
		t.Fatal("v2 conversation bottom fade must include the current native surface gradient")
	}
}
