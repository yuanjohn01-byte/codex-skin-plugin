package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

type restoreAdapter struct {
	themed bool
	fail   bool
}

func (adapter *restoreAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	if adapter.fail {
		return engine.Session{}, errors.New("identity failed")
	}
	return engine.Session{
		OpaqueID: "cli-test",
		Identity: engine.Identity{Platform: "macos", Version: "26.721.41059", AppIdentifier: "com.openai.codex"},
	}, nil
}

func (adapter *restoreAdapter) Probe(context.Context, engine.Session) (engine.RegionReport, error) {
	count := 0
	if adapter.themed {
		count = 1
	}
	return engine.RegionReport{StyleMarkerCount: count}, nil
}

func (adapter *restoreAdapter) Capture(context.Context, engine.Session) (engine.Snapshot, error) {
	return engine.Snapshot{StylePresent: adapter.themed, ThemePublicID: "100001", ThemeVersion: "1.0.0", TemplateVersion: 1}, nil
}

func (*restoreAdapter) Apply(context.Context, engine.Session, engine.CompiledTheme) error {
	return nil
}

func (*restoreAdapter) Verify(context.Context, engine.Session, engine.CompiledTheme) (engine.RegionReport, error) {
	return engine.RegionReport{}, nil
}

func (adapter *restoreAdapter) Restore(context.Context, engine.Session, engine.Snapshot) error {
	adapter.themed = true
	return nil
}

func (adapter *restoreAdapter) RestoreOfficial(context.Context, engine.Session) error {
	adapter.themed = false
	return nil
}

func (adapter *restoreAdapter) VerifyOfficial(context.Context, engine.Session) error {
	if adapter.themed {
		return errors.New("marker remains")
	}
	return nil
}

func (*restoreAdapter) Close(context.Context, engine.Session) error {
	return nil
}

type resultEnvelope struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion"`
	OperationID     *string         `json:"operationId"`
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

func TestThemeRestoreJSONIsOfflineAndPluginIndependent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	fake := &restoreAdapter{themed: true}
	code, stdout, stderr := run(t, []string{"theme", "restore", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Adapter: fake,
	})
	if code != 0 || stderr != "" {
		t.Fatalf("restore failed: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if !result.OK || fake.themed {
		t.Fatalf("restore result=%+v themed=%v", result, fake.themed)
	}
	var data restoreData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.NetworkUsed || data.LoginRequired || data.PluginRequired || !data.WasThemed {
		t.Fatalf("restore data = %#v", data)
	}
	if result.OperationID == nil || *result.OperationID != data.OperationID {
		t.Fatalf("restore operation ids do not match: envelope=%v data=%s", result.OperationID, data.OperationID)
	}
}

func TestThemeRestoreFailureUsesStableCodeAndExit(t *testing.T) {
	code, stdout, stderr := run(t, []string{"theme", "restore", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: filepath.Join(t.TempDir(), "CodexSkin"),
		Adapter: &restoreAdapter{fail: true},
	})
	if code != exitRestore || stderr != "" {
		t.Fatalf("restore failure: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if result.OK || !bytes.Contains(result.Error, []byte(`"code":"CS-RESTORE-001"`)) {
		t.Fatalf("restore failure result = %+v", result)
	}
}
