// Package guardiancli exposes only the fixed Guardian spike command surface.
package guardiancli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
)

const (
	exitSuccess = 0
	exitUsage   = 80
)

var allowedReasons = map[string]bool{
	"renderer":     true,
	"process":      true,
	"version":      true,
	"rule-refresh": true,
	"manual":       true,
}

type versionResult struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Component       string `json:"component"`
	GuardianVersion string `json:"guardianVersion"`
	Status          string `json:"status"`
}

type runResult struct {
	SchemaVersion        int    `json:"schemaVersion"`
	Component            string `json:"component"`
	GuardianVersion      string `json:"guardianVersion"`
	Status               string `json:"status"`
	Reason               string `json:"reason"`
	ReconcileImplemented bool   `json:"reconcileImplemented"`
	NetworkListener      bool   `json:"networkListener"`
	ArbitraryCommand     bool   `json:"arbitraryCommand"`
}

func Run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "version" && args[1] == "--json":
		return writeJSON(stdout, stderr, versionResult{
			SchemaVersion:   1,
			Component:       "codex-skin-guardian",
			GuardianVersion: buildinfo.Version,
			Status:          "ready",
		})
	case len(args) == 5 && args[0] == "run" && args[1] == "--reason" && allowedReasons[args[2]] && args[3] == "--json" && args[4] == "--internal-spike":
		return writeJSON(stdout, stderr, runResult{
			SchemaVersion:        1,
			Component:            "codex-skin-guardian",
			GuardianVersion:      buildinfo.Version,
			Status:               "trigger-validated",
			Reason:               args[2],
			ReconcileImplemented: false,
			NetworkListener:      false,
			ArbitraryCommand:     false,
		})
	default:
		fmt.Fprintln(stderr, "Guardian accepts only version --json or the fixed internal lifecycle trigger.")
		return exitUsage
	}
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(stderr, "failed to encode Guardian result")
		return exitUsage
	}
	return exitSuccess
}
