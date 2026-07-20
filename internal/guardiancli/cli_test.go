package guardiancli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVersionAndFixedRunContracts(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"version", "--json"}, &stdout, &stderr); code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("version failed: %d, %q", code, stderr.String())
	}
	var version versionResult
	if err := json.Unmarshal(stdout.Bytes(), &version); err != nil || version.Component != "codex-skin-guardian" || version.Status != "ready" {
		t.Fatalf("unexpected version contract: %#v, %v", version, err)
	}

	for reason := range allowedReasons {
		stdout.Reset()
		stderr.Reset()
		args := []string{"run", "--reason", reason, "--json", "--internal-spike"}
		if code := Run(args, &stdout, &stderr); code != exitSuccess || stderr.Len() != 0 {
			t.Fatalf("fixed reason %s failed: %d, %q", reason, code, stderr.String())
		}
		var result runResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Reason != reason || result.ReconcileImplemented || result.NetworkListener || result.ArbitraryCommand {
			t.Fatalf("unsafe or invalid run result: %#v, %v", result, err)
		}
	}
}

func TestArbitraryInputsAreRejected(t *testing.T) {
	tests := [][]string{
		nil,
		{"run", "--reason", "process", "--json"},
		{"run", "--reason", "https://example.com", "--json", "--internal-spike"},
		{"run", "--reason", "process", "--json", "--internal-spike", "--command", "whoami"},
		{"shell", "whoami"},
		{"version"},
	}
	for _, args := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != exitUsage || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("unsafe input accepted: %#v, code=%d, stdout=%q, stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}
