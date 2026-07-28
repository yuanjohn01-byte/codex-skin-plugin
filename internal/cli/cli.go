package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/browseropen"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/credentials"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/deviceauth"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/protocol"
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
}

type versionData struct {
	Command         string `json:"command"`
	HelperVersion   string `json:"helperVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
	GoVersion       string `json:"goVersion"`
	BuildCommit     string `json:"buildCommit"`
	BuiltAt         string `json:"builtAt"`
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
}

type statusData struct {
	Command              string `json:"command"`
	DeviceLinked         bool   `json:"deviceLinked"`
	PendingThemePublicID string `json:"pendingThemePublicId,omitempty"`
	AppliedThemePublicID string `json:"appliedThemePublicId,omitempty"`
	AppliedThemeVersion  string `json:"appliedThemeVersion,omitempty"`
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
			Command:         "version",
			HelperVersion:   buildinfo.Version,
			ProtocolVersion: buildinfo.ProtocolVersion,
			GoVersion:       goVersion,
			BuildCommit:     buildinfo.Commit,
			BuiltAt:         buildinfo.BuiltAt,
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
	runtimeAdapter := environment.Adapter
	if runtimeAdapter == nil {
		runtimeAdapter, err = adapter.NewLive(adapter.Config{Root: store.Root()})
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
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.SuccessWithOperation(result.OperationID, data)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "Theme %s v%s applied and verified.\n", result.ThemePublicID, result.ThemeVersion)
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
		runtimeAdapter, err = adapter.NewLive(adapter.Config{Root: store.Root()})
		if err != nil {
			return nil, err
		}
	}
	instance, err := engine.New(store, runtimeAdapter)
	if err != nil {
		return nil, err
	}
	return userflow.New(userflow.Config{
		Root:              store.Root(),
		BaseURL:           buildinfo.APIBaseURL,
		Auth:              authClient,
		Themes:            themeClient,
		State:             state,
		Applier:           userflow.EngineApplier{Engine: instance},
		OpenURL:           browseropen.Open,
		DeviceDisplayName: "Codex Skin on " + platform,
		Platform:          platform,
		PluginVersion:     buildinfo.PluginVersion,
		EngineVersion:     engine.CurrentEngineVersion,
	})
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
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Success(data)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "Device linked: %t; applied theme: %s %s; pending theme: %s.\n", data.DeviceLinked, data.AppliedThemePublicID, data.AppliedThemeVersion, data.PendingThemePublicID)
	}
	return exitSuccess
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
		fmt.Fprintln(stderr, "usage: codex-skin <version|doctor|status> [--json] | codex-skin theme <apply THEME_ID|restore> [--json]")
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
