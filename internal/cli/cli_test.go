package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type resultEnvelope struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion"`
	OK              bool            `json:"ok"`
	Status          string          `json:"status"`
	Data            json.RawMessage `json:"data"`
	Error           json.RawMessage `json:"error"`
}

func run(t *testing.T, args []string, environment Runtime) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(args, &stdout, &stderr, environment)
	return code, stdout.String(), stderr.String()
}

func decodeSingleResult(t *testing.T, output string) resultEnvelope {
	t.Helper()
	if strings.Count(output, "\n") != 1 || !strings.HasSuffix(output, "\n") {
		t.Fatalf("JSON mode must emit exactly one JSON line, got %q", output)
	}
	var result resultEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON result: %v", err)
	}
	if result.Type != "result" || result.ProtocolVersion != 1 {
		t.Fatalf("unexpected envelope: %+v", result)
	}
	return result
}

func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := run(t, []string{"version", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", GoVersion: "go1.26.5",
	})
	if code != 0 || stderr != "" {
		t.Fatalf("version failed: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if !result.OK || result.Status != "completed" || string(result.Error) != "null" {
		t.Fatalf("unexpected version result: %+v", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["command"] != "version" || data["protocolVersion"] != float64(1) {
		t.Fatalf("unexpected version data: %#v", data)
	}
}

func TestDoctorSupportedPlatforms(t *testing.T) {
	tests := []struct {
		name         string
		goos         string
		goarch       string
		platform     string
		architecture string
	}{
		{name: "mac arm64", goos: "darwin", goarch: "arm64", platform: "macos", architecture: "arm64"},
		{name: "mac x64", goos: "darwin", goarch: "amd64", platform: "macos", architecture: "x64"},
		{name: "windows x64", goos: "windows", goarch: "amd64", platform: "windows", architecture: "x64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := run(t, []string{"doctor", "--json"}, Runtime{GOOS: test.goos, GOARCH: test.goarch})
			if code != 0 || stderr != "" {
				t.Fatalf("doctor failed: code=%d stderr=%q", code, stderr)
			}
			result := decodeSingleResult(t, stdout)
			var data map[string]any
			if err := json.Unmarshal(result.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["platform"] != test.platform || data["architecture"] != test.architecture || data["nodeRequired"] != false {
				t.Fatalf("unexpected doctor data: %#v", data)
			}
		})
	}
}

func TestDoctorRejectsUnsupportedPlatform(t *testing.T) {
	code, stdout, stderr := run(t, []string{"doctor", "--json"}, Runtime{GOOS: "linux", GOARCH: "amd64"})
	if code != 50 || stderr != "" {
		t.Fatalf("unsupported platform returned code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if result.OK || result.Status != "failed" || string(result.Data) != "null" {
		t.Fatalf("unsupported platform did not fail closed: %+v", result)
	}
	if !bytes.Contains(result.Error, []byte(`"code":"CS-LOCAL-PLATFORM-001"`)) {
		t.Fatalf("unexpected product error: %s", result.Error)
	}
}

func TestUnknownCommandJSONDoesNotPolluteStderr(t *testing.T) {
	code, stdout, stderr := run(t, []string{"unknown", "--json"}, Runtime{})
	if code != 80 || stderr != "" {
		t.Fatalf("unexpected failure channel: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if result.OK || !bytes.Contains(result.Error, []byte(`"code":"CS-HELPER-INPUT-001"`)) {
		t.Fatalf("unexpected error result: %+v", result)
	}
}

func TestHumanUsageUsesStderrOnly(t *testing.T) {
	code, stdout, stderr := run(t, nil, Runtime{})
	if code != 80 || stdout != "" || !strings.HasPrefix(stderr, "usage:") {
		t.Fatalf("unexpected human usage output: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
