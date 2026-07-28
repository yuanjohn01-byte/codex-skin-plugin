// free_theme_live_canary verifies one signed package in an identity-checked,
// loopback-only Codex session, then exercises transactional offline Restore.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

type pinnedAdapter struct {
	live    *adapter.Live
	session engine.Session
}

func (pinned *pinnedAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	return pinned.session, nil
}
func (pinned *pinnedAdapter) Probe(ctx context.Context, _ engine.Session) (engine.RegionReport, error) {
	return pinned.live.Probe(ctx, pinned.session)
}
func (pinned *pinnedAdapter) Capture(ctx context.Context, _ engine.Session) (engine.Snapshot, error) {
	return pinned.live.Capture(ctx, pinned.session)
}
func (pinned *pinnedAdapter) Apply(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) error {
	return pinned.live.Apply(ctx, pinned.session, compiled)
}
func (pinned *pinnedAdapter) Verify(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) (engine.RegionReport, error) {
	return pinned.live.Verify(ctx, pinned.session, compiled)
}
func (pinned *pinnedAdapter) Restore(ctx context.Context, _ engine.Session, snapshot engine.Snapshot) error {
	return pinned.live.Restore(ctx, pinned.session, snapshot)
}
func (pinned *pinnedAdapter) RestoreOfficial(ctx context.Context, _ engine.Session) error {
	return pinned.live.RestoreOfficial(ctx, pinned.session)
}
func (pinned *pinnedAdapter) VerifyOfficial(ctx context.Context, _ engine.Session) error {
	return pinned.live.VerifyOfficial(ctx, pinned.session)
}
func (*pinnedAdapter) Close(context.Context, engine.Session) error {
	return nil
}
func (pinned *pinnedAdapter) Prime(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) error {
	return pinned.live.Prime(ctx, pinned.session, compiled)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Free theme live canary failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) (returnErr error) {
	if len(args) != 8 {
		return errors.New("expected --package --descriptor --signature --root")
	}
	values := map[string]string{}
	for index := 0; index < len(args); index += 2 {
		name := args[index]
		if name != "--package" && name != "--descriptor" &&
			name != "--signature" && name != "--root" {
			return errors.New("unknown argument")
		}
		value := filepath.Clean(args[index+1])
		if !filepath.IsAbs(value) || values[name] != "" {
			return errors.New("all paths must be unique and absolute")
		}
		values[name] = value
	}
	descriptor, err := os.ReadFile(values["--descriptor"])
	if err != nil || len(descriptor) > 64*1024 {
		return errors.New("release descriptor is unavailable or oversized")
	}
	signature, err := os.ReadFile(values["--signature"])
	if err != nil || len(signature) > 1024 {
		return errors.New("release signature is unavailable or oversized")
	}
	verified, err := theme.Verify(values["--package"], descriptor, signature)
	if err != nil {
		return err
	}
	store, err := engine.OpenStore(
		values["--root"],
		filepath.Join(filepath.Dir(values["--root"]), "synthetic-plugin-cache"),
	)
	if err != nil {
		return err
	}
	live, err := adapter.NewLive(adapter.Config{
		Root:       store.Root(),
		LaunchWait: 45 * time.Second,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := live.OpenVerifiedSession(ctx)
	if err != nil {
		return err
	}
	stopped := false
	defer func() {
		if stopped {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		returnErr = errors.Join(returnErr, live.RestoreOfficial(cleanupCtx, session))
		returnErr = errors.Join(returnErr, live.StopOwned(cleanupCtx, session))
	}()
	if err := waitForCapabilities(ctx, live, session); err != nil {
		return err
	}
	instance, err := engine.New(store, &pinnedAdapter{live: live, session: session})
	if err != nil {
		return err
	}
	applied, err := instance.ApplyVerified(ctx, verified)
	if err != nil {
		return err
	}
	restored, err := instance.RestoreOfficial(ctx)
	if err != nil || !restored.WasThemed {
		return fmt.Errorf("offline restore did not restore a themed session: %v", err)
	}
	if err := live.VerifyOfficial(ctx, session); err != nil {
		return err
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCancel()
	if err := live.StopOwned(stopCtx, session); err != nil {
		return err
	}
	stopped = true
	fmt.Printf(
		"{\"command\":\"free-theme-live-canary\",\"themePublicId\":%q,\"themeVersion\":%q,\"packageSha256\":%q,\"codexVersion\":%q,\"offlineRestore\":true,\"officialRestored\":true}\n",
		applied.ThemePublicID,
		applied.ThemeVersion,
		verified.PackageSHA256,
		applied.Identity.Version,
	)
	return nil
}

func waitForCapabilities(ctx context.Context, live *adapter.Live, session engine.Session) error {
	for attempt := 0; attempt < 120; attempt++ {
		report, err := live.Probe(ctx, session)
		if err == nil &&
			report.Regions["home"] == engine.RegionPass &&
			report.Regions["sidebar"] == engine.RegionPass &&
			report.Regions["composer"] == engine.RegionPass &&
			report.Regions["composerUtilityBar"] == engine.RegionPass {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("Codex capabilities did not stabilize")
}
