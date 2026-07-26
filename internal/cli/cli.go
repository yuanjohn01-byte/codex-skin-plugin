package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/protocol"
)

const (
	exitSuccess     = 0
	exitRestore     = 41
	exitLocalUnsafe = 50
	exitInternal    = 80
)

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
		if (len(args) != 2 && len(args) != 3) ||
			args[1] != "restore" ||
			(len(args) == 3 && args[2] != "--json") {
			return usageFailure(stdout, stderr, jsonMode)
		}
		return runThemeRestore(stdout, stderr, jsonMode, environment)
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
	root := environment.Root
	if root == "" {
		home := environment.Home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		localAppData := environment.LocalAppData
		if localAppData == "" {
			localAppData = os.Getenv("LOCALAPPDATA")
		}
		var err error
		root, err = engine.DefaultRoot(goos, home, localAppData)
		if err != nil {
			return writeRestoreFailure(stdout, stderr, jsonMode, "CS-RESTORE-ROOT-001", err)
		}
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
		fmt.Fprintln(stderr, "usage: codex-skin <version|doctor> [--json] | codex-skin theme restore [--json]")
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
