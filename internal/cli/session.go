package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/sessionflow"
)

const (
	sessionReconnectWindow   = 15 * time.Second
	sessionPollInterval      = 2 * time.Second
	sessionHeartbeatInterval = 10 * time.Second
	sessionHeartbeatMaxAge   = 30 * time.Second
	sessionAppearanceSettle  = 500 * time.Millisecond
)

// runThemeSession owns no scheduler and never launches Codex. It only keeps
// one previously approved, controlled Codex process themed until that process
// exits, Restore asks it to stop, or its identity becomes unsafe.
func runThemeSession(sessionID string, environment Runtime) int {
	goos, _, _ := environment.values()
	root, err := resolveRoot(goos, environment)
	if err != nil {
		return exitInternal
	}
	store, err := engine.OpenStore(root, environment.PluginCache)
	if err != nil {
		return exitInternal
	}
	sessions, err := sessionflow.New(store.Root())
	if err != nil {
		return exitInternal
	}
	record, found, err := sessions.Current()
	if err != nil || !found || record.SessionID != sessionID || record.Status != sessionflow.StatusStarting {
		return exitInternal
	}
	if _, err := sessions.Claim(sessionID, os.Getpid()); err != nil {
		return exitInternal
	}
	compiled, desired, err := engine.LoadDesiredCompiled(store)
	if err != nil || desired.ThemePublicID != record.ThemePublicID ||
		desired.ThemeVersion != record.ThemeVersion || desired.PackageSHA256 != record.PackageSHA256 {
		_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "cached_theme_invalid")
		return exitApply
	}

	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	lastReady := time.Time{}
	for {
		stopRequested, stopErr := sessions.StopRequested(sessionID)
		if stopErr != nil {
			return exitInternal
		}
		if stopRequested {
			_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
			return exitSuccess
		}

		currentDesired, desiredFound, desiredErr := store.ReadDesired()
		if desiredErr != nil || !desiredFound || currentDesired != *desired {
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "desired_theme_changed")
			return exitApply
		}

		live, liveErr := adapter.NewLive(adapter.Config{Root: store.Root(), CurrentProfile: true})
		if liveErr != nil {
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_configuration")
			return exitApply
		}
		sessionCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		liveSession, openErr := live.OpenVerifiedThemeSession(sessionCtx, *compiled)
		cancel()
		if openErr != nil {
			if sessionCodexExited(ctx) {
				_ = live.RestoreNativeAppearanceBackup()
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "codex_exited")
				return exitSuccess
			}
			if !lastReady.IsZero() && time.Since(lastReady) < sessionReconnectWindow {
				if !waitSession(ctx, sessionPollInterval) {
					return exitInternal
				}
				continue
			}
			_ = live.RestoreNativeAppearanceBackup()
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_identity_lost")
			return exitApply
		}
		if !sameIdentity(record.Codex, liveSession.Identity) {
			_ = live.Close(context.Background(), liveSession)
			_ = live.RestoreNativeAppearanceBackup()
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_identity_lost")
			return exitApply
		}

		applyCtx, applyCancel := context.WithTimeout(ctx, 12*time.Second)
		applyErr := live.Apply(applyCtx, liveSession, *compiled)
		var report engine.RegionReport
		if applyErr == nil {
			report, applyErr = live.Verify(applyCtx, liveSession, *compiled)
		}
		applyCancel()
		if applyErr != nil || !engine.ReportAllowsTheme(report, *compiled) {
			if !lastReady.IsZero() && time.Since(lastReady) < sessionReconnectWindow {
				_ = live.Close(context.Background(), liveSession)
				if !waitSession(ctx, sessionPollInterval) {
					return exitInternal
				}
				continue
			}
			restoreControlledSession(live, liveSession)
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_verify_failed")
			return exitApply
		}
		if lastReady.IsZero() {
			// Native light/dark pinning is needed only to start the controlled
			// process. Restore the user's original settings on disk before
			// claiming success, then prove the live renderer remains themed.
			if err := live.RestoreNativeAppearanceBackup(); err != nil {
				restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "appearance_restore_failed")
				return exitApply
			}
			if !waitSession(ctx, sessionAppearanceSettle) {
				restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_cancelled")
				return exitApply
			}
			settleCtx, settleCancel := context.WithTimeout(ctx, 6*time.Second)
			settledReport, settleErr := live.Verify(settleCtx, liveSession, *compiled)
			settleCancel()
			if settleErr != nil || !engine.ReportAllowsTheme(settledReport, *compiled) {
				restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "appearance_restore_changed_renderer")
				return exitApply
			}
		}
		if _, err := sessions.Activate(sessionID, os.Getpid()); err != nil {
			_ = live.Close(context.Background(), liveSession)
			if requested, stopErr := sessions.StopRequested(sessionID); stopErr == nil && requested {
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
				return exitSuccess
			}
			return exitInternal
		}
		lastReady = time.Now()
		lastHeartbeat := lastReady

		for {
			stopRequested, stopErr = sessions.StopRequested(sessionID)
			if stopErr != nil {
				_ = live.Close(context.Background(), liveSession)
				return exitInternal
			}
			if stopRequested {
				_ = live.Close(context.Background(), liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
				return exitSuccess
			}
			currentDesired, desiredFound, desiredErr = store.ReadDesired()
			if desiredErr != nil || !desiredFound || currentDesired != *desired {
				_ = live.Close(context.Background(), liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "desired_theme_changed")
				return exitApply
			}
			verifyCtx, verifyCancel := context.WithTimeout(ctx, 6*time.Second)
			report, verifyErr := live.Verify(verifyCtx, liveSession, *compiled)
			verifyCancel()
			if verifyErr == nil && engine.ReportAllowsTheme(report, *compiled) {
				if time.Since(lastHeartbeat) >= sessionHeartbeatInterval {
					if _, err := sessions.Heartbeat(sessionID, os.Getpid()); err != nil {
						_ = live.Close(context.Background(), liveSession)
						if requested, stopErr := sessions.StopRequested(sessionID); stopErr == nil && requested {
							_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
							return exitSuccess
						}
						return exitInternal
					}
					lastHeartbeat = time.Now()
				}
				if !waitSession(ctx, sessionPollInterval) {
					_ = live.Close(context.Background(), liveSession)
					return exitInternal
				}
				continue
			}
			_ = live.Close(context.Background(), liveSession)
			break
		}
	}
}

func sessionCodexExited(ctx context.Context) bool {
	installation, err := codex.DiscoverInstallation(ctx)
	if err != nil {
		return false
	}
	_, err = codex.DiscoverCurrentInstance(ctx, installation)
	return errors.Is(err, codex.ErrCurrentMissing)
}

func sameIdentity(left, right engine.Identity) bool {
	return left.Platform == right.Platform && left.AppIdentifier == right.AppIdentifier &&
		left.Publisher == right.Publisher && left.Version == right.Version &&
		left.ExecutableHash == right.ExecutableHash && left.ProcessID == right.ProcessID &&
		left.ProcessStartID == right.ProcessStartID
}

func waitSession(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func restoreControlledSession(live *adapter.Live, session engine.Session) {
	if live == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = live.RestoreOfficial(cleanupCtx, session)
	_ = live.Close(cleanupCtx, session)
	_ = live.RestoreNativeAppearanceBackup()
}

func sessionControllerEnabled(environment Runtime) bool {
	// Production opts in explicitly from cmd/codex-skin. Tests are safe by
	// default and may exercise orchestration only with a fake StartSession.
	return environment.StartSession != nil ||
		(environment.EnableLiveSessionController && environment.ApplyFlow == nil && environment.Adapter == nil)
}

func startThemeSession(
	store *engine.Store,
	themePublicID, themeVersion string,
	environment Runtime,
) error {
	if store == nil {
		return engine.ErrConfiguration
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found || desired.ThemePublicID != themePublicID || desired.ThemeVersion != themeVersion {
		return engine.ErrStateUnsafe
	}
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	identityProbe := environment.currentSessionIdentity
	if identityProbe == nil {
		identityProbe = currentControlledIdentity
	}
	identity, err := identityProbe(ctx)
	if err != nil {
		return err
	}
	sessions, err := sessionflow.New(store.Root())
	if err != nil {
		return err
	}
	if _, _, err := sessions.ExpireStale(sessionHeartbeatMaxAge, "controller_heartbeat_lost"); err != nil {
		return err
	}
	record, err := sessions.Start(
		desired.ThemePublicID,
		desired.ThemeVersion,
		desired.PackageSHA256,
		identity,
	)
	if err != nil {
		return err
	}
	goos, _, _ := environment.values()
	executable, err := recoveryExecutable(store.Root(), goos, environment.Executable)
	if err != nil {
		_, _ = sessions.Finish(record.SessionID, sessionflow.StatusFailed, "controller_launch_failed")
		return err
	}
	start := environment.StartSession
	if start == nil {
		start = restartflow.StartSession
	}
	if err := start(executable, record.SessionID); err != nil {
		_, _ = sessions.Finish(record.SessionID, sessionflow.StatusFailed, "controller_launch_failed")
		return err
	}
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		current, found, currentErr := sessions.Current()
		if currentErr != nil || !found || current.SessionID != record.SessionID {
			return engine.ErrStateUnsafe
		}
		switch current.Status {
		case sessionflow.StatusActive:
			return nil
		case sessionflow.StatusFailed, sessionflow.StatusEnded:
			return engine.ErrApplyFailed
		}
		if !waitSession(ctx, 150*time.Millisecond) {
			return ctx.Err()
		}
	}
	_, _, _ = sessions.RequestStop()
	return engine.ErrApplyFailed
}

func currentControlledIdentity(ctx context.Context) (engine.Identity, error) {
	installation, err := codex.DiscoverInstallation(ctx)
	if err != nil {
		return engine.Identity{}, err
	}
	current, err := codex.DiscoverCurrentInstance(ctx, installation)
	if err != nil || current.ControlledPort < 1 {
		return engine.Identity{}, engine.ErrRestartConsent
	}
	return engine.Identity{
		Platform: installation.Platform, AppIdentifier: installation.AppIdentifier,
		Publisher: installation.Publisher, Version: installation.Version,
		ExecutableHash: installation.ExecutableSHA256, ProcessID: current.Process.ProcessID,
		ProcessStartID: current.Process.ProcessStartID,
	}, nil
}

func stopThemeSession(root string, ctx context.Context) error {
	sessions, err := sessionflow.New(root)
	if err != nil {
		return err
	}
	if _, expired, err := sessions.ExpireStale(sessionHeartbeatMaxAge, "controller_heartbeat_lost"); err != nil {
		return err
	} else if expired {
		return nil
	}
	record, requested, err := sessions.RequestStop()
	if err != nil || !requested {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		current, found, currentErr := sessions.Current()
		if currentErr != nil || !found || current.SessionID != record.SessionID {
			return engine.ErrStateUnsafe
		}
		if current.Status == sessionflow.StatusEnded {
			return nil
		}
		if current.Status == sessionflow.StatusFailed {
			return engine.ErrApplyFailed
		}
		if !waitSession(ctx, 150*time.Millisecond) {
			return ctx.Err()
		}
	}
	return engine.ErrApplyFailed
}

func rollbackAfterSessionStartFailure(store *engine.Store, environment Runtime) error {
	if store == nil {
		return engine.ErrConfiguration
	}
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeAdapter := environment.Adapter
	if runtimeAdapter == nil {
		live, err := adapter.NewLive(adapter.Config{
			Root: store.Root(), CurrentProfile: true, RestartApproved: true,
		})
		if err != nil {
			return err
		}
		runtimeAdapter = live
	}
	instance, err := engine.New(store, runtimeAdapter)
	if err != nil {
		return err
	}
	_, err = instance.RestoreOfficial(ctx)
	return err
}
