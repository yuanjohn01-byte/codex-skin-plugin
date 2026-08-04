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
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/sessionflow"
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
		case args[0] == "__theme-session":
			t.Fatal("unit tests must not start a real theme session controller")
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

func TestInjectedApplyFlowCannotFallThroughToRealSessionController(t *testing.T) {
	flow := &fakeApplyFlow{}
	if sessionControllerEnabled(Runtime{ApplyFlow: flow}) {
		t.Fatal("injected ApplyFlow unexpectedly enabled the real session controller")
	}
	if sessionControllerEnabled(Runtime{Adapter: &restoreAdapter{}}) {
		t.Fatal("test Adapter unexpectedly enabled the real session controller")
	}
	if sessionControllerEnabled(Runtime{}) {
		t.Fatal("default runtime must be test-safe")
	}
	if !sessionControllerEnabled(Runtime{EnableLiveSessionController: true}) {
		t.Fatal("shipped Helper opt-in must enable the session controller")
	}
	if !sessionControllerEnabled(Runtime{ApplyFlow: flow, StartSession: func(string, string) error { return nil }}) {
		t.Fatal("explicit fake StartSession must enable controller orchestration")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(
		[]string{"__theme-session", "ses_0123456789abcdef0123456789abcdef"},
		&stdout,
		&stderr,
		Runtime{},
	)
	if code != exitInternal || stdout.String() != "" || stderr.String() != "" {
		t.Fatalf(
			"default runtime entered internal live session: code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestThemeApplyReportsSuccessOnlyAfterSessionControllerIsActive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	digest := strings.Repeat("a", 64)
	if err := store.WriteDesired(engine.DesiredTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: digest,
		TemplateVersion: engine.TemplateVersion, AppliedAt: "2026-08-04T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "recovery", "engine", "codex-skin")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	identity := engine.Identity{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: "26.727.0", ExecutableHash: digest, ProcessID: 4312, ProcessStartID: "start-4312",
	}
	flow := &fakeApplyFlow{result: userflow.ApplyResult{
		OperationID: "op_flow", ThemePublicID: "100001", ThemeVersion: "1.0.0",
	}}
	started := false
	code, stdout, stderr := run(t, []string{"theme", "apply", "100001", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, Executable: executable, ApplyFlow: flow,
		currentSessionIdentity: func(context.Context) (engine.Identity, error) { return identity, nil },
		StartSession: func(worker, sessionID string) error {
			if worker != executable {
				t.Fatalf("session worker = %q, want %q", worker, executable)
			}
			sessions, err := sessionflow.New(root)
			if err != nil {
				return err
			}
			if _, err := sessions.Claim(sessionID, 9981); err != nil {
				return err
			}
			if _, err := sessions.Activate(sessionID, 9981); err != nil {
				return err
			}
			started = true
			return nil
		},
	})
	if code != exitSuccess || stderr != "" || !started {
		t.Fatalf("apply code=%d stderr=%q started=%t", code, stderr, started)
	}
	result := decodeSingleResult(t, stdout)
	var data applyData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatal(err)
	}
	if !result.OK || data.SessionStatus != string(sessionflow.StatusActive) {
		t.Fatalf("apply result=%+v data=%+v", result, data)
	}

	code, stdout, stderr = run(t, []string{"status", "--json"}, Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root,
	})
	if code != exitSuccess || stderr != "" {
		t.Fatalf("status code=%d stderr=%q", code, stderr)
	}
	status := decodeSingleResult(t, stdout)
	var currentStatus statusData
	if err := json.Unmarshal(status.Data, &currentStatus); err != nil {
		t.Fatal(err)
	}
	if currentStatus.SessionStatus != string(sessionflow.StatusActive) ||
		currentStatus.SessionThemePublicID != "100001" {
		t.Fatalf("status data=%+v", currentStatus)
	}
}

func TestStatusNeverReportsStaleControllerHeartbeatAsActive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	digest := strings.Repeat("a", 64)
	sessions, err := sessionflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := sessions.Start("100001", "1.0.0", digest, engine.Identity{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: "26.727.0", ExecutableHash: digest, ProcessID: 4312, ProcessStartID: "start-4312",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Claim(record.SessionID, 9981); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Activate(record.SessionID, 9981); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "session", "current.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var stale sessionflow.Record
	if err := json.Unmarshal(raw, &stale); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stale.CreatedAt = now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	stale.UpdatedAt = now.Add(-time.Minute).Format(time.RFC3339Nano)
	raw, err = json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
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
	if data.SessionStatus != string(sessionflow.StatusFailed) ||
		data.SessionErrorCode != "CS-FLOW-SESSION-001" {
		t.Fatalf("stale session was reported as live: %#v", data)
	}
}

func TestStopThemeSessionEndsUnclaimedControllerWithoutWaiting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	root = store.Root()
	digest := strings.Repeat("a", 64)
	sessions, err := sessionflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := sessions.Start("100001", "1.0.0", digest, engine.Identity{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: "26.727.0", ExecutableHash: digest, ProcessID: 4312, ProcessStartID: "start-4312",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stopThemeSession(root, ctx); err != nil {
		t.Fatalf("stopThemeSession() error = %v", err)
	}
	current, found, err := sessions.Current()
	if err != nil || !found || current.SessionID != record.SessionID ||
		current.Status != sessionflow.StatusEnded || current.EndedReason != "stop_before_claim" {
		t.Fatalf("session after stop = %#v, found=%t, err=%v", current, found, err)
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
	code, stdout, stderr := run(t, []string{"theme", "continue", "--json"}, Runtime{
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
	if data.Command != "theme continue" ||
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
		data.RestartStatus != string(restartflow.StatusCompleted) {
		t.Fatalf("status data = %#v", data)
	}
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

func TestFailedRestartIsHiddenAfterSameThemeAppliesDirectly(t *testing.T) {
	request := restartflow.Request{
		Kind: "apply", Status: restartflow.StatusFailed,
		ThemePublicID: "100002", ThemeVersion: "1.0.0",
	}
	desired := engine.DesiredTheme{
		ThemePublicID: "100002", ThemeVersion: "1.0.0",
	}
	if !failedRestartSupersededByAppliedTheme(request, desired, true, "") {
		t.Fatal("same-theme direct apply did not supersede the failed restart")
	}
	if failedRestartSupersededByAppliedTheme(request, desired, true, "100002") {
		t.Fatal("pending theme incorrectly superseded the failed restart")
	}
	desired.ThemePublicID = "100003"
	if failedRestartSupersededByAppliedTheme(request, desired, true, "") {
		t.Fatal("different applied theme incorrectly superseded the failed restart")
	}
}
