package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/runtimebudget"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/userflow"
)

type restoreAdapter struct {
	themed  bool
	fail    bool
	openErr error
}

type fakeApplyFlow struct {
	result userflow.ApplyResult
	err    error
	id     string
}

type restartApplyAdapter struct {
	identity engine.Identity
	current  engine.CompiledTheme
	applies  int
}

func passingThemeReport(compiled engine.CompiledTheme) engine.RegionReport {
	regions := map[string]engine.RegionStatus{}
	for _, name := range []string{
		"home", "mainBoundary", "sidebar", "composer", "topFade", "bottomFade",
		"templateScope", "themeContrast",
	} {
		regions[name] = engine.RegionPass
	}
	for _, name := range []string{
		"composerUtilityBar", "conversationActivity", "conversationDiffResource",
		"suggestionCards", "projectPicker",
	} {
		regions[name] = engine.RegionNotPresent
	}
	return engine.RegionReport{
		StyleMarkerCount: 1, TemplateVersion: compiled.TemplateVersion,
		ThemePublicID: compiled.ThemePublicID, BackgroundLoaded: true, Regions: regions,
	}
}

func (adapter *restartApplyAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	return engine.Session{OpaqueID: "restart-apply", Identity: adapter.identity}, nil
}

func (adapter *restartApplyAdapter) Probe(context.Context, engine.Session) (engine.RegionReport, error) {
	report := passingThemeReport(adapter.current)
	if adapter.current.ThemePublicID == "" {
		report.StyleMarkerCount = 0
		report.TemplateVersion = 0
		report.ThemePublicID = ""
		report.BackgroundLoaded = false
	}
	return report, nil
}

func (adapter *restartApplyAdapter) Capture(context.Context, engine.Session) (engine.Snapshot, error) {
	return engine.Snapshot{}, nil
}

func (adapter *restartApplyAdapter) Apply(
	_ context.Context,
	_ engine.Session,
	compiled engine.CompiledTheme,
) error {
	adapter.current = compiled
	adapter.applies++
	return nil
}

func (adapter *restartApplyAdapter) Verify(
	_ context.Context,
	_ engine.Session,
	compiled engine.CompiledTheme,
) (engine.RegionReport, error) {
	return passingThemeReport(compiled), nil
}

func (adapter *restartApplyAdapter) Restore(
	context.Context,
	engine.Session,
	engine.Snapshot,
) error {
	adapter.current = engine.CompiledTheme{}
	return nil
}

func (adapter *restartApplyAdapter) RestoreOfficial(context.Context, engine.Session) error {
	adapter.current = engine.CompiledTheme{}
	return nil
}

func (adapter *restartApplyAdapter) VerifyOfficial(context.Context, engine.Session) error {
	if adapter.current.ThemePublicID != "" {
		return engine.ErrRestoreFailed
	}
	return nil
}

func (*restartApplyAdapter) Close(context.Context, engine.Session) error { return nil }

func (flow *fakeApplyFlow) Apply(_ context.Context, themePublicID string) (userflow.ApplyResult, error) {
	flow.id = themePublicID
	return flow.result, flow.err
}

func (adapter *restoreAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	if adapter.openErr != nil {
		return engine.Session{}, adapter.openErr
	}
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
	if len(args) > 0 {
		switch {
		case args[0] == "__restart-worker" && environment.Adapter == nil:
			t.Fatal("restart worker tests require an injected adapter")
		case len(args) >= 2 && args[0] == "theme" && args[1] == "restore" && environment.Adapter == nil:
			t.Fatal("restore tests require an injected adapter")
		case len(args) >= 2 && args[0] == "theme" && args[1] == "apply" &&
			environment.ApplyFlow == nil && environment.Adapter == nil:
			t.Fatal("apply tests require an injected flow or adapter")
		}
	}
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

func TestThemeApplyJSONUsesOneContinuousFlow(t *testing.T) {
	flow := &fakeApplyFlow{result: userflow.ApplyResult{
		OperationID:   "op_flow",
		ThemePublicID: "100001",
		ThemeVersion:  "1.0.0",
		Authorized:    true,
		PurchaseShown: true,
	}}
	code, stdout, stderr := run(t, []string{"theme", "apply", "100001", "--json"}, Runtime{
		ApplyFlow: flow,
	})
	if code != exitSuccess || stderr != "" || flow.id != "100001" {
		t.Fatalf("apply failed: code=%d stderr=%q id=%q", code, stderr, flow.id)
	}
	result := decodeSingleResult(t, stdout)
	if !result.OK || result.OperationID == nil || *result.OperationID != "op_flow" {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	var data applyData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if !data.Authorized || !data.PurchaseShown || data.ThemePublicID != "100001" {
		t.Fatalf("unexpected apply data: %+v", data)
	}
}

func TestThemeApplySuccessDoesNotReportABackgroundRuntime(t *testing.T) {
	flow := &fakeApplyFlow{result: userflow.ApplyResult{
		OperationID: "op_flow", ThemePublicID: "100001", ThemeVersion: "1.0.0",
	}}
	code, stdout, stderr := run(t, []string{"theme", "apply", "100001", "--json"}, Runtime{ApplyFlow: flow})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("apply code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if !result.OK || bytes.Contains(result.Data, []byte("sessionStatus")) ||
		bytes.Contains(result.Data, []byte("runtimeStatus")) {
		t.Fatalf("on-demand apply result=%+v", result)
	}
}

func TestStatusIgnoresLegacySessionJournal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	digest := strings.Repeat("a", 64)
	if err := store.WriteDesired(engine.DesiredTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: digest,
		TemplateVersion: engine.TemplateVersion, AppliedAt: "2026-08-05T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "session", "current.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{\"status\":\"failed\",\"reason\":\"controller_identity_lost\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, []string{"status", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("status code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	var data statusData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.AppliedThemePublicID != "100001" || data.AppliedThemeVersion != "1.0.0" ||
		bytes.Contains(result.Data, []byte("sessionStatus")) || bytes.Contains(result.Data, []byte("runtimeStatus")) {
		t.Fatalf("legacy session journal leaked into on-demand status: %#v", data)
	}
}

func TestThemeApplyMapsStableFailureActions(t *testing.T) {
	tests := []struct {
		name string
		err  error
		exit int
		code string
	}{
		{name: "authorization", err: userflow.ErrAuthorization, exit: exitAuthorize, code: "CS-FLOW-AUTH-001"},
		{name: "access", err: userflow.ErrAccess, exit: exitAccess, code: "CS-FLOW-ACCESS-001"},
		{name: "restart busy", err: userflow.ErrRestartBusy, exit: exitRestart, code: "CS-FLOW-RESTART-002"},
		{name: "theme", err: userflow.ErrTheme, exit: exitTheme, code: "CS-FLOW-THEME-001"},
		{name: "restart", err: userflow.ErrRestart, exit: exitRestart, code: "CS-FLOW-RESTART-001"},
		{name: "apply", err: userflow.ErrApply, exit: exitApply, code: "CS-FLOW-APPLY-001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := run(t, []string{"theme", "apply", "100001", "--json"}, Runtime{
				ApplyFlow: &fakeApplyFlow{err: test.err},
			})
			if code != test.exit || stderr != "" {
				t.Fatalf("failure code=%d stderr=%q", code, stderr)
			}
			result := decodeSingleResult(t, stdout)
			if result.OK || !bytes.Contains(result.Error, []byte(`"code":"`+test.code+`"`)) {
				t.Fatalf("unexpected failure result: %+v", result)
			}
		})
	}
}

func TestThemeRestoreStagesRestartWithoutClaimingSuccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	code, stdout, stderr := run(t, []string{"theme", "restore", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
		Adapter: &restoreAdapter{openErr: engine.ErrRestartConsent},
	})
	if code != exitRestart || stderr != "" {
		t.Fatalf("restore restart code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	if result.OK ||
		!bytes.Contains(result.Error, []byte(`"code":"CS-FLOW-RESTART-001"`)) ||
		!bytes.Contains(result.Error, []byte(`"action":"confirm_restart"`)) {
		t.Fatalf("restart result = %+v", result)
	}
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, found, err := restartStore.Current()
	if err != nil || !found || request.Kind != "restore" ||
		request.Status != restartflow.StatusPending {
		t.Fatalf("restart request = %#v, found=%t, error=%v", request, found, err)
	}
}

func TestThemeContinueApprovesAndStartsFixedRecoveryWorker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	opened, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = opened.Root()
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := restartStore.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "recovery", "engine", "codex-skin")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryExecutable(root, "darwin", executable); err != nil {
		t.Fatalf("recovery executable setup: %v", err)
	}
	var startedExecutable string
	var startedRequestID string
	code, stdout, stderr := run(t, []string{"theme", "launch", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Executable: executable,
		StartWorker: func(worker, requestID string) error {
			startedExecutable = worker
			startedRequestID = requestID
			return nil
		},
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf(
			"continue code=%d stdout=%q stderr=%q worker=%q request=%q",
			code,
			stdout,
			stderr,
			startedExecutable,
			startedRequestID,
		)
	}
	result := decodeSingleResult(t, stdout)
	if !result.OK || startedExecutable != executable ||
		startedRequestID != request.RequestID {
		t.Fatalf(
			"continue result=%+v worker=%q request=%q",
			result,
			startedExecutable,
			startedRequestID,
		)
	}
	var data restartData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Command != "theme launch" ||
		!data.RestartAccepted ||
		data.Kind != "restore" ||
		data.ThemePublicID != "" {
		t.Fatalf("restart data = %#v", data)
	}
	current, found, err := restartStore.Current()
	if err != nil || !found || current.Status != restartflow.StatusApproved {
		t.Fatalf("approved request = %#v, found=%t, error=%v", current, found, err)
	}
}

func TestThemeContinueRemainsCompatibilityAlias(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	opened, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = opened.Root()
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := restartStore.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "recovery", "engine", "codex-skin")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryExecutable(root, "darwin", executable); err != nil {
		t.Fatalf("recovery executable setup: %v", err)
	}
	code, stdout, stderr := run(t, []string{"theme", "continue", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Executable: executable,
		StartWorker: func(_ string, requestID string) error {
			if requestID != request.RequestID {
				t.Fatalf("request id = %s", requestID)
			}
			return nil
		},
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("alias code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	result := decodeSingleResult(t, stdout)
	var data restartData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Command != "theme continue" || !data.RestartAccepted {
		t.Fatalf("alias data = %#v", data)
	}
}

func TestRestartWorkerCompletesRestoreAndStatusReportsTerminalFact(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := restartStore.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartStore.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	flowStore, err := flowstate.New(root)
	if err != nil {
		t.Fatal(err)
	}
	flowState, err := flowStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	flowState.PendingThemePublicID = "100001"
	if err := flowStore.Write(flowState); err != nil {
		t.Fatal(err)
	}
	fake := &restoreAdapter{themed: true}
	code, stdout, stderr := run(t, []string{"__restart-worker", request.RequestID}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Adapter: fake,
		RestartDelay: -1,
	})
	if code != exitSuccess || stdout != "" || stderr != "" || fake.themed {
		t.Fatalf(
			"worker code=%d stdout=%q stderr=%q themed=%t",
			code,
			stdout,
			stderr,
			fake.themed,
		)
	}
	current, found, err := restartStore.Current()
	if err != nil || !found || current.Status != restartflow.StatusCompleted ||
		current.OperationID == "" {
		t.Fatalf("completed request = %#v, found=%t, error=%v", current, found, err)
	}

	code, stdout, stderr = run(t, []string{"status", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("status code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	var data statusData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.RestartKind != "restore" ||
		data.RestartStatus != string(restartflow.StatusCompleted) ||
		data.PendingThemePublicID != "" {
		t.Fatalf("status data = %#v", data)
	}
	state, err := flowStore.Read()
	if err != nil || state.PendingThemePublicID != "" {
		t.Fatalf("flow state after restore = %#v, err=%v", state, err)
	}
}

func TestRestartWorkerCompletesVerifiedApplyWithoutBackgroundSupervisor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	verified := verifiedRestartTheme(t)
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := restartStore.StageApply(verified)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartStore.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	identity := engine.Identity{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: "26.727.0", ExecutableHash: digest, ProcessID: 4312, ProcessStartID: "start-4312",
	}
	initial := &restartApplyAdapter{identity: identity}
	code, stdout, stderr := run(t, []string{"__restart-worker", request.RequestID}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Adapter: initial,
		RestartDelay: -1,
	})
	if code != exitSuccess || stdout != "" || stderr != "" {
		t.Fatalf(
			"worker code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	current, found, err := restartStore.Current()
	if err != nil || !found || current.Status != restartflow.StatusCompleted ||
		current.ResultThemeID != verified.Manifest.ThemePublicID {
		t.Fatalf("restart result=%#v found=%t err=%v", current, found, err)
	}
	if initial.applies != 1 {
		t.Fatalf("initial apply count=%d", initial.applies)
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found || desired.ThemePublicID != verified.Manifest.ThemePublicID {
		t.Fatalf("on-demand apply did not commit desired state: %#v found=%t err=%v", desired, found, err)
	}
}

func verifiedRestartTheme(t *testing.T) theme.Verified {
	t.Helper()
	fixture := filepath.Join("..", "..", "fixtures", "free-test-theme-v1", "signed-release-v1")
	descriptor, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.json"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(
		filepath.Join(fixture, "package.cskin"),
		descriptor,
		signature,
	)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func TestStatusReportsOnlyDurableLocalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	code, stdout, stderr := run(t, []string{"status", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("status failed: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	var data statusData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.DeviceLinked || data.AppliedThemePublicID != "" || data.PendingThemePublicID != "" {
		t.Fatalf("unexpected status data: %+v", data)
	}
}

func TestStatusReportsFailedRestartEvenWhenLastVerifiedThemeMatches(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	verified := verifiedRestartTheme(t)
	if err := store.WriteDesired(engine.DesiredTheme{
		ThemePublicID:   verified.Manifest.ThemePublicID,
		ThemeVersion:    verified.Manifest.ThemeVersion,
		PackageSHA256:   strings.Repeat("a", 64),
		TemplateVersion: engine.TemplateVersion,
		AppliedAt:       "2026-08-08T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	restartStore, err := restartflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := restartStore.StageApply(verified)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartStore.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := restartStore.Fail(request.RequestID, "CS-FLOW-ROLLBACK-001"); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, []string{"status", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("status failed: code=%d stderr=%q", code, stderr)
	}
	result := decodeSingleResult(t, stdout)
	var data statusData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.AppliedThemePublicID != verified.Manifest.ThemePublicID ||
		data.RestartStatus != string(restartflow.StatusFailed) ||
		data.RestartErrorCode != "CS-FLOW-ROLLBACK-001" {
		t.Fatalf("status hid a current restart failure: %#v", data)
	}
}

func TestRestartApplyFailureCodePreservesVerificationAndRollbackOutcomes(t *testing.T) {
	if got := restartApplyFailureCode(engine.ErrVerifyFailed); got != "CS-FLOW-VERIFY-001" {
		t.Fatalf("verification failure code = %q", got)
	}
	if got := restartApplyFailureCode(errors.Join(engine.ErrVerifyFailed, engine.ErrRollbackFailed)); got != "CS-FLOW-ROLLBACK-001" {
		t.Fatalf("rollback failure code = %q", got)
	}
	if got := restartApplyFailureCode(errors.New("other failure")); got != "CS-FLOW-RESTART-006" {
		t.Fatalf("fallback failure code = %q", got)
	}
}

func TestRestartWorkerDefaultTimeoutLeavesRoomForVerifiedRelaunch(t *testing.T) {
	if restartTimeout != restartflow.RestartWorkerTimeout {
		t.Fatalf(
			"restart timeout = %s, continuation contract = %s",
			restartTimeout,
			restartflow.RestartWorkerTimeout,
		)
	}
	minimumSafetyBoundary := restartTimeout +
		runtimebudget.RestartStartupDelay +
		runtimebudget.EngineRollbackTimeout +
		runtimebudget.AdapterCleanupTimeout
	if restartflow.RestartRunningLease <= minimumSafetyBoundary {
		t.Fatalf(
			"running lease = %s, want more than latest cleanup boundary %s",
			restartflow.RestartRunningLease,
			minimumSafetyBoundary,
		)
	}
}
