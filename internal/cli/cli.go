package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/browseropen"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/credentials"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/deviceauth"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/protocol"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/sessionflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/themeapi"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/userflow"
)

const (
	exitSuccess     = 0
	exitAuthorize   = 31
	exitAccess      = 32
	exitRestore     = 41
	exitTheme       = 42
	exitApply       = 43
	exitRestart     = 44
	exitLocalUnsafe = 50
	exitInternal    = 80
)

type ApplyFlow interface {
	Apply(context.Context, string) (userflow.ApplyResult, error)
}

type Runtime struct {
	GOOS         string
	GOARCH       string
	GoVersion    string
	Root         string
	PluginCache  string
	Home         string
	LocalAppData string
	Adapter      engine.Adapter
	Context      context.Context
	HTTPClient   *http.Client
	ApplyFlow    ApplyFlow
	Executable   string
	StartWorker  func(string, string) error
	StartSession func(string, string) error
	// SessionActivated runs once after the detached runtime has verified the
	// visible renderer and restored the user's native appearance bytes. The v2
	// restart worker uses it to commit restart success before serving the rest
	// of the Codex session in the same process.
	SessionActivated func() error
	// EnableLiveSessionController is set only by the shipped Helper entrypoint.
	// Tests are safe by default and must provide a fake StartSession to opt in.
	EnableLiveSessionController bool
	// currentSessionIdentity is test-only dependency injection. Production always
	// verifies the currently controlled Codex process through the platform probe.
	currentSessionIdentity func(context.Context) (engine.Identity, error)
	// The remaining unexported session hooks keep the detached controller's real
	// state machine deterministic under unit tests. Production leaves them nil.
	sessionAdapterFactory   func(string) (themeSessionAdapter, error)
	sessionThemeLoader      func(*engine.Store) (*engine.CompiledTheme, *engine.DesiredTheme, error)
	sessionStartTimeout     time.Duration
	sessionStopTimeout      time.Duration
	sessionWaitInterval     time.Duration
	sessionControlPoll      time.Duration
	sessionHealthInterval   time.Duration
	sessionHealthTimeout    time.Duration
	sessionAppearanceSettle time.Duration
	RestartDelay            time.Duration
}

type versionData struct {
	Command          string `json:"command"`
	HelperVersion    string `json:"helperVersion"`
	PluginVersion    string `json:"pluginVersion"`
	HelperReleaseTag string `json:"helperReleaseTag"`
	APIOrigin        string `json:"apiOrigin"`
	ProtocolVersion  int    `json:"protocolVersion"`
	GoVersion        string `json:"goVersion"`
	BuildCommit      string `json:"buildCommit"`
	BuiltAt          string `json:"builtAt"`
}

type doctorData struct {
	Command       string        `json:"command"`
	HelperVersion string        `json:"helperVersion"`
	Platform      string        `json:"platform"`
	Architecture  string        `json:"architecture"`
	Runtime       string        `json:"runtime"`
	NodeRequired  bool          `json:"nodeRequired"`
	Checks        []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type restoreData struct {
	Command        string `json:"command"`
	OperationID    string `json:"operationId"`
	Platform       string `json:"platform"`
	CodexVersion   string `json:"codexVersion"`
	WasThemed      bool   `json:"wasThemed"`
	NetworkUsed    bool   `json:"networkUsed"`
	LoginRequired  bool   `json:"loginRequired"`
	PluginRequired bool   `json:"pluginRequired"`
}

type applyData struct {
	Command       string `json:"command"`
	OperationID   string `json:"operationId"`
	ThemePublicID string `json:"themePublicId"`
	ThemeVersion  string `json:"themeVersion"`
	Authorized    bool   `json:"authorizedDuringCommand"`
	PurchaseShown bool   `json:"purchaseShownDuringCommand"`
	SessionStatus string `json:"sessionStatus,omitempty"`
	RuntimeStatus string `json:"runtimeStatus,omitempty"`
}

type restartData struct {
	Command         string `json:"command"`
	RestartAccepted bool   `json:"restartAccepted"`
	Kind            string `json:"kind"`
	ThemePublicID   string `json:"themePublicId,omitempty"`
	RuntimePhase    string `json:"runtimePhase,omitempty"`
}

type statusData struct {
	Command              string `json:"command"`
	DeviceLinked         bool   `json:"deviceLinked"`
	PendingThemePublicID string `json:"pendingThemePublicId,omitempty"`
	AppliedThemePublicID string `json:"appliedThemePublicId,omitempty"`
	AppliedThemeVersion  string `json:"appliedThemeVersion,omitempty"`
	RestartKind          string `json:"restartKind,omitempty"`
	RestartStatus        string `json:"restartStatus,omitempty"`
	RestartThemePublicID string `json:"restartThemePublicId,omitempty"`
	RestartErrorCode     string `json:"restartErrorCode,omitempty"`
	SessionStatus        string `json:"sessionStatus,omitempty"`
	SessionThemePublicID string `json:"sessionThemePublicId,omitempty"`
	SessionErrorCode     string `json:"sessionErrorCode,omitempty"`
	RuntimeStatus        string `json:"runtimeStatus,omitempty"`
	RuntimeThemePublicID string `json:"runtimeThemePublicId,omitempty"`
	RuntimeErrorCode     string `json:"runtimeErrorCode,omitempty"`
}

func (r Runtime) values() (string, string, string) {
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := r.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	goVersion := r.GoVersion
	if goVersion == "" {
		goVersion = runtime.Version()
	}
	return goos, goarch, goVersion
}

func Run(args []string, stdout, stderr io.Writer, environment Runtime) int {
	jsonMode := contains(args, "--json")
	if len(args) == 0 {
		return usageFailure(stdout, stderr, jsonMode)
	}
	if args[0] == "__restart-worker" {
		if len(args) != 2 {
			return exitInternal
		}
		return runRestartWorker(args[1], environment)
	}
	if args[0] == "__theme-session" {
		if len(args) != 2 {
			return exitInternal
		}
		if !environment.EnableLiveSessionController && environment.StartSession == nil {
			return exitInternal
		}
		return runThemeSession(args[1], environment)
	}
	if args[0] == "theme" {
		if len(args) >= 2 && args[1] == "restore" {
			if (len(args) != 2 && len(args) != 3) ||
				(len(args) == 3 && args[2] != "--json") {
				return usageFailure(stdout, stderr, jsonMode)
			}
			return runThemeRestore(stdout, stderr, jsonMode, environment)
		}
		if len(args) >= 2 && args[1] == "apply" {
			if (len(args) != 3 && len(args) != 4) ||
				(len(args) == 4 && args[3] != "--json") {
				return usageFailure(stdout, stderr, jsonMode)
			}
			return runThemeApply(args[2], stdout, stderr, jsonMode, environment)
		}
		if len(args) >= 2 && (args[1] == "launch" || args[1] == "continue") {
			if (len(args) != 2 && len(args) != 3) ||
				(len(args) == 3 && args[2] != "--json") {
				return usageFailure(stdout, stderr, jsonMode)
			}
			return runThemeContinue(args[1], stdout, stderr, jsonMode, environment)
		}
		return usageFailure(stdout, stderr, jsonMode)
	}
	if args[0] == "status" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return usageFailure(stdout, stderr, jsonMode)
		}
		return runStatus(stdout, stderr, jsonMode, environment)
	}
	if len(args) > 2 || (len(args) == 2 && args[1] != "--json") || args[0] == "--json" {
		return usageFailure(stdout, stderr, jsonMode)
	}

	goos, goarch, goVersion := environment.values()
	switch args[0] {
	case "version":
		data := versionData{
			Command:          "version",
			HelperVersion:    buildinfo.Version,
			PluginVersion:    buildinfo.PluginVersion,
			HelperReleaseTag: buildinfo.HelperReleaseTag,
			APIOrigin:        buildinfo.APIBaseURL,
			ProtocolVersion:  buildinfo.ProtocolVersion,
			GoVersion:        goVersion,
			BuildCommit:      buildinfo.Commit,
			BuiltAt:          buildinfo.BuiltAt,
		}
		if jsonMode {
			return writeJSON(stdout, stderr, protocol.Success(data))
		}
		fmt.Fprintf(stdout, "Codex Skin Helper %s (protocol %d, %s)\n", buildinfo.Version, buildinfo.ProtocolVersion, goVersion)
		return exitSuccess
	case "doctor":
		platform, architecture, supported := normalizedPlatform(goos, goarch)
		if !supported {
			failure := protocol.Failure("CS-LOCAL-PLATFORM-001", "use_supported_platform", false)
			if jsonMode {
				if writeJSON(stdout, stderr, failure) != exitSuccess {
					return exitInternal
				}
			} else {
				fmt.Fprintln(stderr, "Codex Skin Helper supports macOS arm64/x64 and Windows x64 in this spike.")
			}
			return exitLocalUnsafe
		}
		data := doctorData{
			Command:       "doctor",
			HelperVersion: buildinfo.Version,
			Platform:      platform,
			Architecture:  architecture,
			Runtime:       "self-contained-go",
			NodeRequired:  false,
			Checks: []doctorCheck{
				{Name: "helper_runtime", Status: "pass"},
				{Name: "supported_platform", Status: "pass"},
			},
		}
		if jsonMode {
			return writeJSON(stdout, stderr, protocol.Success(data))
		}
		fmt.Fprintf(stdout, "Codex Skin Helper runtime ready on %s/%s; Node is not required.\n", platform, architecture)
		return exitSuccess
	default:
		return usageFailure(stdout, stderr, jsonMode)
	}
}

func runThemeRestore(stdout, stderr io.Writer, jsonMode bool, environment Runtime) int {
	goos, _, _ := environment.values()
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-ROOT-001", err)
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-ROOT-001", err)
	}
	if sessionControllerEnabled(environment) {
		if err := stopThemeSession(store.Root(), environment.Context); err != nil {
			return writeRestoreFailure(stdout, stderr, jsonMode, "CS-FLOW-SESSION-001", err)
		}
	}
	runtimeAdapter := environment.Adapter
	if runtimeAdapter == nil {
		runtimeAdapter, err = adapter.NewLive(adapter.Config{
			Root: store.Root(), CurrentProfile: true,
		})
		if err != nil {
			return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-ROOT-001", err)
		}
	}
	instance, err := engine.New(store, runtimeAdapter)
	if err != nil {
		return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-001", err)
	}
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := instance.RestoreOfficial(ctx)
	if err != nil {
		if errors.Is(err, engine.ErrRestartConsent) {
			restartStore, restartErr := restartflow.New(store.Root())
			if restartErr == nil {
				_, restartErr = restartStore.StageRestore()
			}
			if restartErr == nil {
				return writeFlowFailure(
					stdout,
					stderr,
					jsonMode,
					"CS-FLOW-RESTART-001",
					"confirm_restart",
					exitRestart,
				)
			}
		}
		return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-001", err)
	}
	data := restoreData{
		Command: "theme restore", OperationID: result.OperationID,
		Platform: result.Identity.Platform, CodexVersion: result.Identity.Version,
		WasThemed: result.WasThemed, NetworkUsed: false, LoginRequired: false, PluginRequired: false,
	}
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.SuccessWithOperation(result.OperationID, data)) != exitSuccess {
			return exitInternal
		}
	} else if result.WasThemed {
		fmt.Fprintln(stdout, "Official Codex appearance restored. No network, login, or Plugin access was used.")
	} else {
		fmt.Fprintln(stdout, "Official Codex appearance is already active.")
	}
	return exitSuccess
}

func runThemeApply(themePublicID string, stdout, stderr io.Writer, jsonMode bool, environment Runtime) int {
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	flow := environment.ApplyFlow
	if flow == nil {
		var err error
		flow, err = buildApplyFlow(environment)
		if err != nil {
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-CONFIG-001", "contact_support", exitInternal)
		}
	}
	result, err := flow.Apply(ctx, themePublicID)
	if err != nil {
		switch {
		case errors.Is(err, userflow.ErrAuthorization):
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-AUTH-001", "reauthorize", exitAuthorize)
		case errors.Is(err, userflow.ErrAccess):
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-ACCESS-001", "finish_purchase", exitAccess)
		case errors.Is(err, userflow.ErrRestart):
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-001", "confirm_restart", exitRestart)
		case errors.Is(err, userflow.ErrApply):
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-APPLY-001", "use_offline_restore_entry", exitApply)
		default:
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-THEME-001", "choose_available_theme", exitTheme)
		}
	}
	data := applyData{
		Command:       "theme apply",
		OperationID:   result.OperationID,
		ThemePublicID: result.ThemePublicID,
		ThemeVersion:  result.ThemeVersion,
		Authorized:    result.Authorized,
		PurchaseShown: result.PurchaseShown,
	}
	if sessionControllerEnabled(environment) {
		goos, _, _ := environment.values()
		root, rootErr := resolveRoot(goos, environment)
		if rootErr != nil {
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-SESSION-001", "use_offline_restore_entry", exitApply)
		}
		store, storeErr := engine.OpenStore(root, environment.PluginCache)
		var sessionErr error
		if storeErr == nil {
			sessionErr = ensureThemeSession(
				store,
				result.ThemePublicID,
				result.ThemeVersion,
				environment,
			)
		}
		if storeErr != nil || sessionErr != nil {
			if storeErr == nil && sessionRollbackSafe(sessionErr) {
				_ = rollbackAfterSessionStartFailure(store, environment)
			}
			return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-SESSION-001", "use_offline_restore_entry", exitApply)
		}
		data.SessionStatus = string(sessionflow.StatusActive)
		data.RuntimeStatus = string(sessionflow.StatusActive)
	}
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.SuccessWithOperation(result.OperationID, data)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "Theme %s v%s is active for this Codex session. Closing Codex or restarting your computer ends this skin session; apply the theme again through Codex Skin next time.\n", result.ThemePublicID, result.ThemeVersion)
	}
	return exitSuccess
}

func buildApplyFlow(environment Runtime) (ApplyFlow, error) {
	goos, goarch, _ := environment.values()
	platform, _, supported := normalizedPlatform(goos, goarch)
	if !supported || buildinfo.APIBaseURL == "" {
		return nil, userflow.ErrConfiguration
	}
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return nil, err
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return nil, err
	}
	state, err := flowstate.New(store.Root())
	if err != nil {
		return nil, err
	}
	credentialStore, err := credentials.New()
	if err != nil {
		return nil, err
	}
	authClient, err := deviceauth.NewClient(buildinfo.APIBaseURL, environment.HTTPClient, credentialStore)
	if err != nil {
		return nil, err
	}
	themeClient, err := themeapi.NewClient(buildinfo.APIBaseURL, environment.HTTPClient)
	if err != nil {
		return nil, err
	}
	runtimeAdapter := environment.Adapter
	if runtimeAdapter == nil {
		var boundIdentity *engine.Identity
		if sessions, sessionErr := sessionflow.New(store.Root()); sessionErr == nil {
			if current, found, readErr := sessions.Current(); readErr == nil && found &&
				current.Status == sessionflow.StatusActive &&
				current.Fresh(time.Now(), sessionHeartbeatMaxAge) {
				copy := current.Codex
				boundIdentity = &copy
			}
		}
		runtimeAdapter, err = adapter.NewLive(adapter.Config{
			Root: store.Root(), CurrentProfile: true, BoundIdentity: boundIdentity,
		})
		if err != nil {
			return nil, err
		}
	}
	restartStore, err := restartflow.New(store.Root())
	if err != nil {
		return nil, err
	}
	instance, err := engine.New(store, runtimeAdapter)
	if err != nil {
		return nil, err
	}
	applier := userflow.EngineApplier{Engine: instance, Restart: restartStore}
	return userflow.New(userflow.Config{
		Root:              store.Root(),
		BaseURL:           buildinfo.APIBaseURL,
		Auth:              authClient,
		Themes:            themeClient,
		State:             state,
		Applier:           applier,
		OpenURL:           browseropen.Open,
		DeviceDisplayName: "Codex Skin on " + platform,
		Platform:          platform,
		PluginVersion:     buildinfo.PluginVersion,
		EngineVersion:     engine.CurrentEngineVersion,
	})
}

func runThemeContinue(command string, stdout, stderr io.Writer, jsonMode bool, environment Runtime) int {
	if command != "launch" && command != "continue" {
		return usageFailure(stdout, stderr, jsonMode)
	}
	goos, _, _ := environment.values()
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-002", "run_theme_again", exitRestart)
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-002", "run_theme_again", exitRestart)
	}
	restartStore, err := restartflow.New(store.Root())
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-002", "run_theme_again", exitRestart)
	}
	request, found, err := restartStore.Current()
	if err != nil || !found {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-002", "run_theme_again", exitRestart)
	}
	request, err = restartStore.Approve(request.RequestID)
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-002", "run_theme_again", exitRestart)
	}
	executable, err := recoveryExecutable(store.Root(), goos, environment.Executable)
	if err != nil {
		_, _ = restartStore.Fail(request.RequestID, "CS-FLOW-RESTART-003")
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-003", "run_theme_again", exitRestart)
	}
	startWorker := environment.StartWorker
	if startWorker == nil {
		startWorker = restartflow.StartWorker
	}
	if err := startWorker(executable, request.RequestID); err != nil {
		_, _ = restartStore.Fail(request.RequestID, "CS-FLOW-RESTART-003")
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-FLOW-RESTART-003", "run_theme_again", exitRestart)
	}
	data := restartData{
		Command: "theme " + command, RestartAccepted: true,
		Kind: request.Kind, ThemePublicID: request.ThemePublicID, RuntimePhase: "launching",
	}
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Success(data)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintln(stdout, "Runtime launch accepted. Codex will reopen once; the same external runtime will verify and keep the skin active for this session.")
	}
	return exitSuccess
}

func runRestartWorker(requestID string, environment Runtime) int {
	goos, _, _ := environment.values()
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return exitInternal
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return exitInternal
	}
	restartStore, err := restartflow.New(store.Root())
	if err != nil {
		return exitInternal
	}
	request, err := restartStore.Begin(requestID)
	if err != nil {
		return exitInternal
	}
	delay := environment.RestartDelay
	if delay == 0 {
		delay = 2 * time.Second
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	ctx := environment.Context
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
	}
	// Switching and Restore both take exclusive ownership from the previous
	// Scheme A controller before a new renderer transaction begins. Terminal or
	// missing session state is an idempotent no-op.
	if (request.Kind == "apply" || request.Kind == "restore") && sessionControllerEnabled(environment) {
		if err := stopThemeSession(store.Root(), ctx); err != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-SESSION-001")
			return exitRestore
		}
	}
	runtimeAdapter := environment.Adapter
	if runtimeAdapter == nil {
		runtimeAdapter, err = adapter.NewLive(adapter.Config{
			Root: store.Root(), CurrentProfile: true, RestartApproved: true,
		})
		if err != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-RESTART-004")
			return exitInternal
		}
	}
	instance, err := engine.New(store, runtimeAdapter)
	if err != nil {
		_, _ = restartStore.Fail(requestID, "CS-FLOW-RESTART-004")
		return exitInternal
	}
	operationID := ""
	resultThemeID := ""
	resultVersion := ""
	var resultIdentity engine.Identity
	var completedRelease *themeapi.Release
	applyStartedAt := time.Time{}
	switch request.Kind {
	case "apply":
		verified, verifyErr := restartStore.LoadVerified(request)
		if verifyErr != nil ||
			!themeEngineCompatible(verified.Manifest.Compatibility.MinEngineVersion) {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-RESTART-005")
			return exitTheme
		}
		applyStartedAt = time.Now()
		result, applyErr := instance.ApplyVerified(ctx, verified)
		if applyErr != nil {
			_, _ = restartStore.Fail(requestID, restartApplyFailureCode(applyErr))
			return exitApply
		}
		operationID = result.OperationID
		resultThemeID = result.ThemePublicID
		resultVersion = result.ThemeVersion
		resultIdentity = result.Identity
		completedRelease = &themeapi.Release{
			ThemePublicID:    verified.Manifest.ThemePublicID,
			ThemeVersion:     verified.Manifest.ThemeVersion,
			Descriptor:       verified.Descriptor,
			DescriptorBytes:  verified.DescriptorBytes,
			SignatureBytes:   verified.Signature,
			MinEngineVersion: verified.Manifest.Compatibility.MinEngineVersion,
		}
		if flowStore, flowErr := flowstate.New(store.Root()); flowErr == nil {
			if state, readErr := flowStore.Read(); readErr == nil &&
				state.PendingThemePublicID == resultThemeID {
				state.PendingThemePublicID = ""
				_ = flowStore.Write(state)
			}
		}
	case "restore":
		result, restoreErr := instance.RestoreOfficial(ctx)
		if restoreErr != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-RESTART-007")
			return exitRestore
		}
		operationID = result.OperationID
	default:
		_, _ = restartStore.Fail(requestID, "CS-FLOW-RESTART-004")
		return exitInternal
	}
	if completedRelease != nil && sessionControllerEnabled(environment) {
		sessions, sessionErr := sessionflow.New(store.Root())
		if sessionErr != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-SESSION-001")
			return exitApply
		}
		if _, _, sessionErr = sessions.ExpireStale(sessionHeartbeatMaxAge, "runtime_heartbeat_lost"); sessionErr != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-SESSION-001")
			return exitApply
		}
		desired, desiredFound, desiredErr := store.ReadDesired()
		if desiredErr != nil || !desiredFound || desired.ThemePublicID != resultThemeID ||
			desired.ThemeVersion != resultVersion {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-SESSION-001")
			return exitApply
		}
		record, sessionErr := sessions.Start(
			desired.ThemePublicID,
			desired.ThemeVersion,
			desired.PackageSHA256,
			resultIdentity,
		)
		if sessionErr != nil {
			_, _ = restartStore.Fail(requestID, "CS-FLOW-SESSION-001")
			return exitApply
		}
		environment.SessionActivated = func() error {
			if _, completeErr := restartStore.Complete(
				requestID,
				operationID,
				resultThemeID,
				resultVersion,
			); completeErr != nil {
				return completeErr
			}
			recordRestartApply(
				store.Root(),
				*completedRelease,
				applyStartedAt,
				time.Now(),
				environment,
			)
			return nil
		}
		// The restart worker itself is now the single session Runtime
		// Supervisor. It commits restart success through SessionActivated and
		// remains alive only until Codex exits or Restore requests a stop.
		return runThemeSession(record.SessionID, environment)
	}
	if _, err := restartStore.Complete(
		requestID,
		operationID,
		resultThemeID,
		resultVersion,
	); err != nil {
		return exitInternal
	}
	if completedRelease != nil {
		recordRestartApply(
			store.Root(),
			*completedRelease,
			applyStartedAt,
			time.Now(),
			environment,
		)
	}
	return exitSuccess
}

func restartApplyFailureCode(err error) string {
	switch {
	case errors.Is(err, engine.ErrRollbackFailed):
		return "CS-FLOW-ROLLBACK-001"
	case errors.Is(err, engine.ErrVerifyFailed):
		return "CS-FLOW-VERIFY-001"
	default:
		return "CS-FLOW-RESTART-006"
	}
}

func recordRestartApply(
	root string,
	release themeapi.Release,
	startedAt, completedAt time.Time,
	environment Runtime,
) {
	if buildinfo.APIBaseURL == "" || completedAt.Before(startedAt) {
		return
	}
	stateStore, err := flowstate.New(root)
	if err != nil {
		return
	}
	state, err := stateStore.Read()
	if err != nil || state.DeviceID == "" {
		return
	}
	credentialStore, err := credentials.New()
	if err != nil {
		return
	}
	httpClient := environment.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	authClient, err := deviceauth.NewClient(
		buildinfo.APIBaseURL,
		httpClient,
		credentialStore,
	)
	if err != nil {
		return
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	refreshed, err := authClient.Refresh(auditCtx, state.DeviceID)
	if err != nil ||
		refreshed.Outcome != deviceauth.OutcomeAuthorized ||
		refreshed.AccessToken == nil {
		return
	}
	themeClient, err := themeapi.NewClient(buildinfo.APIBaseURL, httpClient)
	if err != nil {
		return
	}
	_ = themeClient.RecordApply(
		auditCtx,
		release,
		refreshed.AccessToken.Value(),
		completedAt,
		completedAt.Sub(startedAt),
	)
}

func runStatus(stdout, stderr io.Writer, jsonMode bool, environment Runtime) int {
	goos, _, _ := environment.values()
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	stateStore, err := flowstate.New(store.Root())
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	state, err := stateStore.Read()
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	desired, found, err := store.ReadDesired()
	if err != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	data := statusData{
		Command:              "status",
		DeviceLinked:         state.DeviceID != "",
		PendingThemePublicID: state.PendingThemePublicID,
	}
	if found {
		data.AppliedThemePublicID = desired.ThemePublicID
		data.AppliedThemeVersion = desired.ThemeVersion
	}
	sessions, sessionErr := sessionflow.New(store.Root())
	if sessionErr != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	if session, sessionFound, sessionReadErr := sessions.Current(); sessionReadErr != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	} else if sessionFound {
		data.SessionStatus = string(session.Status)
		data.SessionThemePublicID = session.ThemePublicID
		data.RuntimeStatus = string(session.Status)
		data.RuntimeThemePublicID = session.ThemePublicID
		if session.InProgress() && !session.Fresh(time.Now(), sessionHeartbeatMaxAge) {
			// A hard exit or computer restart cannot write a terminal record.
			// Never repeat a stale heartbeat as a live skin session.
			data.SessionStatus = string(sessionflow.StatusFailed)
			data.RuntimeStatus = string(sessionflow.StatusFailed)
			data.SessionErrorCode = "CS-FLOW-SESSION-001"
			data.RuntimeErrorCode = "CS-FLOW-RUNTIME-001"
		} else if session.Status == sessionflow.StatusFailed {
			data.SessionErrorCode = "CS-FLOW-SESSION-001"
			data.RuntimeErrorCode = "CS-FLOW-RUNTIME-001"
		}
	}
	restartStore, restartErr := restartflow.New(store.Root())
	if restartErr != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	restartRequest, restartFound, restartErr := restartStore.Current()
	if restartErr != nil {
		return writeFlowFailure(stdout, stderr, jsonMode, "CS-STATUS-001", "run_doctor", exitLocalUnsafe)
	}
	if restartFound && !failedRestartSupersededByAppliedTheme(
		restartRequest,
		desired,
		found,
		state.PendingThemePublicID,
	) {
		data.RestartKind = restartRequest.Kind
		data.RestartStatus = string(restartRequest.Status)
		data.RestartThemePublicID = restartRequest.ThemePublicID
		data.RestartErrorCode = restartRequest.ErrorCode
	}
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Success(data)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(
			stdout,
			"Device linked: %t; applied theme: %s %s; session: %s; pending theme: %s; restart: %s %s.\n",
			data.DeviceLinked,
			data.AppliedThemePublicID,
			data.AppliedThemeVersion,
			data.SessionStatus,
			data.PendingThemePublicID,
			data.RestartKind,
			data.RestartStatus,
		)
	}
	return exitSuccess
}

func failedRestartSupersededByAppliedTheme(
	request restartflow.Request,
	desired engine.DesiredTheme,
	desiredFound bool,
	pendingThemePublicID string,
) bool {
	return request.Status == restartflow.StatusFailed &&
		request.Kind == "apply" &&
		desiredFound &&
		pendingThemePublicID == "" &&
		request.ThemePublicID == desired.ThemePublicID &&
		request.ThemeVersion == desired.ThemeVersion
}

func recoveryExecutable(root, goos, supplied string) (string, error) {
	name := "codex-skin"
	if goos == "windows" {
		name = "codex-skin.exe"
	}
	expected := filepath.Join(root, "recovery", "engine", name)
	executable := supplied
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	executable, err := filepath.Abs(executable)
	if err != nil || filepath.Clean(executable) != executable ||
		!sameFilePath(executable, expected, goos) {
		return "", errors.New("restart worker is not the fixed recovery Helper")
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("restart worker is unsafe")
	}
	return executable, nil
}

func sameFilePath(left, right, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func themeEngineCompatible(minimum string) bool {
	return theme.EngineCompatible(engine.CurrentEngineVersion, minimum)
}

func resolveRoot(goos string, environment Runtime) (string, error) {
	if environment.Root != "" {
		return environment.Root, nil
	}
	home := environment.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	localAppData := environment.LocalAppData
	if localAppData == "" {
		localAppData = os.Getenv("LOCALAPPDATA")
	}
	return engine.DefaultRoot(goos, home, localAppData)
}

func writeFlowFailure(stdout, stderr io.Writer, jsonMode bool, code, action string, exitCode int) int {
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Failure(code, action, false)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stderr, "Codex Skin flow did not complete (%s).\n", code)
	}
	return exitCode
}

func writeRestoreFailure(stdout, stderr io.Writer, jsonMode bool, code string, cause error) int {
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Failure(code, "use_offline_restore_entry", false)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stderr, "Codex Skin restore did not complete (%s).\n", code)
	}
	_ = cause
	return exitRestore
}

func normalizedPlatform(goos, goarch string) (string, string, bool) {
	platform := ""
	switch goos {
	case "darwin":
		platform = "macos"
	case "windows":
		platform = "windows"
	default:
		return "", "", false
	}
	architecture := ""
	switch goarch {
	case "arm64":
		architecture = "arm64"
	case "amd64":
		architecture = "x64"
	default:
		return "", "", false
	}
	if platform == "windows" && architecture != "x64" {
		return "", "", false
	}
	return platform, architecture, true
}

func usageFailure(stdout, stderr io.Writer, jsonMode bool) int {
	failure := protocol.Failure("CS-HELPER-INPUT-001", "check_command", false)
	if jsonMode {
		if writeJSON(stdout, stderr, failure) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintln(stderr, "usage: codex-skin <version|doctor|status> [--json] | codex-skin theme <apply THEME_ID|restore|launch> [--json]")
	}
	return exitInternal
}

func writeJSON(stdout, stderr io.Writer, value protocol.Result) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(stderr, "failed to encode Helper result")
		return exitInternal
	}
	return exitSuccess
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}
