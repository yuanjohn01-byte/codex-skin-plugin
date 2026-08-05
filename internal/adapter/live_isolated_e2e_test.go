//go:build isolatede2e

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

// TestIsolatedCodexRendererSessionLifecycle is an opt-in real-app smoke test.
// It launches Codex only with a temporary user-data directory rooted below
// t.TempDir; it never discovers, stops, or attaches to the Founder's current
// Codex profile. Keep this out of ordinary CI and run it only before a Founder
// runtime candidate is handed over for local acceptance.
func TestIsolatedCodexRendererSessionLifecycle(t *testing.T) {
	if os.Getenv("CODEX_SKIN_ISOLATED_E2E") != "1" {
		t.Skip("set CODEX_SKIN_ISOLATED_E2E=1 to launch an isolated Codex profile")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the current isolated live-app smoke runs on macOS only")
	}

	root := filepath.Join(t.TempDir(), "CodexSkin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := NewLive(Config{
		Root:            root,
		CurrentProfile:  false,
		RestartApproved: true,
		LaunchWait:      40 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled := engine.CompiledTheme{
		ThemePublicID:     "100005",
		ThemeVersion:      "1.0.1",
		TemplateVersion:   engine.TemplateVersion,
		AppearanceMode:    "dark",
		StyleText:         ":root { --cs-test-runtime: 1; }\n",
		BackgroundDataURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLZ7wAAAABJRU5ErkJggg==",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	session, err := live.OpenVerifiedThemeSession(ctx, compiled)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = live.RestoreOfficial(cleanupCtx, session)
		_ = live.StopOwned(cleanupCtx, session)
	})

	if _, err := live.WaitForCapabilities(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.Apply(ctx, session, compiled); err != nil {
		t.Fatal(err)
	}

	verification, err := live.WaitForThemeVerification(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(verification.Report, compiled) {
		t.Fatalf("isolated renderer did not reach a verified themed state: %#v err=%v", verification, err)
	}

	// The live process remains open only to prove the one-shot style is still
	// present shortly after the Helper would have exited. It deliberately does
	// not exercise a background controller, watcher, or heartbeat.
	time.Sleep(3 * time.Second)
	report, err := live.Verify(ctx, session, compiled)
	if err != nil || !engine.ReportAllowsTheme(report, compiled) {
		t.Fatalf("isolated renderer lost its verified style: %#v err=%v", report, err)
	}

	if err := live.RestoreOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := live.StopOwned(ctx, session); err != nil {
		t.Fatal(err)
	}
	stopped = true
}
