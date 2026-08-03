package bootstrapcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/bootstrap"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/protocol"
	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

const (
	exitSuccess  = 0
	exitUsage    = 2
	exitRejected = 50
	exitInternal = 80
)

type Runtime struct {
	GOOS         string
	GOARCH       string
	Home         string
	LocalAppData string
	Root         string
	ReleaseTag   string
	Context      context.Context
	Source       bootstrap.Source
	SelfTester   bootstrap.SelfTester
	Keyset       *releasecontract.VerificationKeyset
	HTTPClient   *http.Client
}

type versionData struct {
	Command          string `json:"command"`
	BootstrapVersion string `json:"bootstrapVersion"`
	ReleaseTag       string `json:"releaseTag"`
	BuildCommit      string `json:"buildCommit"`
	BuiltAt          string `json:"builtAt"`
}

type installData struct {
	Command          string `json:"command"`
	BootstrapVersion string `json:"bootstrapVersion"`
	ReleaseTag       string `json:"releaseTag"`
	HelperVersion    string `json:"helperVersion"`
	HelperSHA256     string `json:"helperSha256"`
	RecoverySHA256   string `json:"recoverySha256"`
	PreviousVersion  string `json:"previousVersion,omitempty"`
	Reused           bool   `json:"reused"`
	RecoveryReady    bool   `json:"recoveryReady"`
}

func Run(args []string, stdout, stderr io.Writer, environment Runtime) int {
	jsonMode := contains(args, "--json")
	if len(args) == 0 {
		return usage(stdout, stderr, jsonMode)
	}
	switch args[0] {
	case "version":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return usage(stdout, stderr, jsonMode)
		}
		data := versionData{
			Command: "version", BootstrapVersion: buildinfo.BootstrapVersion,
			ReleaseTag: releaseTag(environment), BuildCommit: buildinfo.Commit, BuiltAt: buildinfo.BuiltAt,
		}
		if jsonMode {
			return writeJSON(stdout, stderr, protocol.Success(data))
		}
		fmt.Fprintf(stdout, "Codex Skin Bootstrap %s (%s)\n", data.BootstrapVersion, data.ReleaseTag)
		return exitSuccess
	case "install":
		pluginCache, ok := installArguments(args[1:])
		if !ok {
			return usage(stdout, stderr, jsonMode)
		}
		return install(pluginCache, stdout, stderr, jsonMode, environment)
	default:
		return usage(stdout, stderr, jsonMode)
	}
}

func install(pluginCache string, stdout, stderr io.Writer, jsonMode bool, environment Runtime) int {
	goos := environment.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := environment.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	root := environment.Root
	if root == "" {
		var err error
		root, err = bootstrap.DefaultRoot(goos, valueOr(environment.Home, os.Getenv("HOME")), valueOr(environment.LocalAppData, os.Getenv("LOCALAPPDATA")))
		if err != nil {
			return failure(stdout, stderr, jsonMode, "CS-BOOTSTRAP-ROOT-001", "use_supported_platform", err)
		}
	}
	cache, err := filepath.Abs(pluginCache)
	if err != nil || !filepath.IsAbs(cache) || strings.TrimSpace(pluginCache) == "" {
		return failure(stdout, stderr, jsonMode, "CS-BOOTSTRAP-PATH-001", "reinstall_plugin", bootstrap.ErrUnsafePath)
	}
	keyset := environment.Keyset
	if keyset == nil {
		trusted, err := releasecontract.TrustedVerificationKeyset()
		if err != nil {
			return failure(stdout, stderr, jsonMode, "CS-BOOTSTRAP-TRUST-001", "contact_support", err)
		}
		keyset = &trusted
	}
	source := environment.Source
	if source == nil {
		if environment.HTTPClient != nil {
			source = bootstrap.NewHTTPReleaseSourceWithClient(environment.HTTPClient)
		} else {
			source = bootstrap.NewHTTPReleaseSource()
		}
	}
	tester := environment.SelfTester
	if tester == nil {
		tester = bootstrap.CommandSelfTester{}
	}
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := bootstrap.Install(ctx, bootstrap.Config{
		Root: root, PluginCache: cache, ReleaseTag: releaseTag(environment),
		RuntimeGOOS: goos, RuntimeGOARCH: goarch, Source: source,
		TrustedKeyset: keyset, SelfTester: tester,
	})
	if err != nil {
		return failure(stdout, stderr, jsonMode, "CS-BOOTSTRAP-INSTALL-001", "retry_helper_install", err)
	}
	data := installData{
		Command: "install", BootstrapVersion: buildinfo.BootstrapVersion,
		ReleaseTag: releaseTag(environment), HelperVersion: result.HelperVersion,
		HelperSHA256: result.HelperSHA256, RecoverySHA256: result.RecoverySHA256,
		PreviousVersion: result.PreviousVersion, Reused: result.Reused, RecoveryReady: result.RecoveryEntry != "",
	}
	if jsonMode {
		return writeJSON(stdout, stderr, protocol.Success(data))
	}
	fmt.Fprintf(stdout, "Codex Skin Helper %s is verified and recovery-ready.\n", result.HelperVersion)
	return exitSuccess
}

func releaseTag(environment Runtime) string {
	if environment.ReleaseTag != "" {
		return environment.ReleaseTag
	}
	return buildinfo.HelperReleaseTag
}

func installArguments(args []string) (string, bool) {
	pluginCache := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
		case "--plugin-cache":
			if index+1 >= len(args) || pluginCache != "" {
				return "", false
			}
			index++
			pluginCache = args[index]
		default:
			return "", false
		}
	}
	return pluginCache, pluginCache != ""
}

func failure(stdout, stderr io.Writer, jsonMode bool, code, action string, cause error) int {
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Failure(code, action, true)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintf(stderr, "Codex Skin Helper installation stopped safely (%s). %v\n", code, cause)
	}
	return exitRejected
}

func usage(stdout, stderr io.Writer, jsonMode bool) int {
	if jsonMode {
		if writeJSON(stdout, stderr, protocol.Failure("CS-BOOTSTRAP-USAGE-001", "use_fixed_plugin_entry", false)) != exitSuccess {
			return exitInternal
		}
	} else {
		fmt.Fprintln(stderr, "Usage: codex-skin-bootstrap version [--json] | install --plugin-cache PATH [--json]")
	}
	return exitUsage
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(stderr, "Codex Skin Bootstrap could not write its result.")
		return exitInternal
	}
	return exitSuccess
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
