//go:build staginge2e

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/buildinfo"
	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

func TestStagingReleaseInstallInFreshIsolatedRoot(t *testing.T) {
	if os.Getenv("CODEX_SKIN_STAGING_RELEASE_E2E") != "1" {
		t.Skip("set CODEX_SKIN_STAGING_RELEASE_E2E=1 to verify the live Staging release")
	}
	if runtime.GOOS != "darwin" || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		t.Skip("live Staging isolation is exercised on supported macOS")
	}

	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-root")
	pluginCache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(pluginCache, 0o700); err != nil {
		t.Fatal(err)
	}
	resolvedCache, err := secureAbsolute(pluginCache)
	if err != nil {
		t.Fatal(err)
	}
	keyset, err := releasecontract.TrustedVerificationKeyset()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	config := Config{
		Root: root, PluginCache: pluginCache, ReleaseTag: buildinfo.HelperReleaseTag,
		RuntimeGOOS: runtime.GOOS, RuntimeGOARCH: runtime.GOARCH,
		Source: bootstrapHTTPSourceForStagingE2E(), TrustedKeyset: &keyset,
		SelfTester: CommandSelfTester{}, SelfTestTimeout: 12 * time.Second,
	}
	first, err := Install(ctx, config)
	if err != nil {
		t.Fatalf("fresh signed Staging install: %v", err)
	}
	if first.Reused || first.HelperVersion != buildinfo.Version || first.HelperSHA256 == "" ||
		first.RecoverySHA256 != first.HelperSHA256 || first.RecoveryEntry == "" ||
		!pathContains(first.Root, first.Executable) || pathContains(resolvedCache, first.Executable) {
		t.Fatalf("fresh isolated install result = %#v", first)
	}
	if info, err := os.Lstat(first.RecoveryEntry); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("recovery entry is not a regular isolated file: info=%#v err=%v", info, err)
	}
	second, err := Install(ctx, config)
	if err != nil {
		t.Fatalf("reused signed Staging install: %v", err)
	}
	if !second.Reused || second.Executable != first.Executable || second.HelperSHA256 != first.HelperSHA256 ||
		second.RecoverySHA256 != first.RecoverySHA256 {
		t.Fatalf("reused isolated install result = %#v, first = %#v", second, first)
	}
}

func bootstrapHTTPSourceForStagingE2E() *HTTPReleaseSource {
	return NewHTTPReleaseSource()
}
