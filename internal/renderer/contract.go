// Package renderer owns the versioned Codex renderer compatibility contract.
package renderer

import (
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
)

const Schema = "codex-skin-selectors/2"

var (
	//go:embed assets/selectors-v2.json
	rawContract []byte

	keyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	loadOnce   sync.Once
	loaded     Contract
	loadErr    error
)

type Source struct {
	Project string `json:"project"`
	Release string `json:"release"`
	Commit  string `json:"commit"`
	License string `json:"license"`
}

type Selector struct {
	Key      string `json:"key"`
	Selector string `json:"selector"`
	Tier     string `json:"tier"`
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
}

type Contract struct {
	Schema    string     `json:"schema"`
	Source    Source     `json:"source"`
	Priority  []string   `json:"priority"`
	Selectors []Selector `json:"selectors"`
}

func Load() (Contract, error) {
	loadOnce.Do(func() {
		if err := json.Unmarshal(rawContract, &loaded); err != nil {
			loadErr = err
			return
		}
		loadErr = validate(loaded)
	})
	return loaded, loadErr
}

func SelectorMap() (map[string]string, error) {
	contract, err := Load()
	if err != nil {
		return nil, err
	}
	selectors := make(map[string]string, len(contract.Selectors))
	for _, item := range contract.Selectors {
		selectors[item.Key] = item.Selector
	}
	return selectors, nil
}

func validate(contract Contract) error {
	if contract.Schema != Schema || contract.Source.Project == "" ||
		contract.Source.Release == "" || len(contract.Source.Commit) != 40 ||
		contract.Source.License != "MIT" || len(contract.Selectors) < 8 {
		return errors.New("renderer selector contract is invalid")
	}
	seen := map[string]bool{}
	for _, item := range contract.Selectors {
		if !keyPattern.MatchString(item.Key) || seen[item.Key] || item.Selector == "" ||
			(item.Tier != "L1" && item.Tier != "L2") || item.Scope == "" ||
			(item.Required && item.Tier != "L1") {
			return errors.New("renderer selector entry is invalid")
		}
		seen[item.Key] = true
	}
	for _, required := range []string{
		"shell-main", "left-panel", "header-tint", "home-icon", "home-route", "home-route-css",
	} {
		if !seen[required] {
			return errors.New("renderer selector contract is incomplete")
		}
	}
	return nil
}
