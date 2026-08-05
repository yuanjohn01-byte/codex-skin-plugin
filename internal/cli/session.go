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
	sessionReconnectPoll     = 500 * time.Millisecond
	sessionControlPoll       = 500 * time.Millisecond
	sessionHealthInterval    = 10 * time.Second
	sessionHealthTimeout     = 3 * time.Second
	sessionHeartbeatInterval = 10 * time.Second
	sessionHeartbeatMaxAge   = 30 * time.Second
	sessionAppearanceSettle  = 500 * time.Millisecond
	sessionVerificationWait  = 35 * time.Second
)

var errSessionStopUnconfirmed = errors.New("theme session controller stop is unconfirmed")

type themeSessionAdapter interface {
	OpenVerifiedThemeSession(context.Context, engine.CompiledTheme) (engine.Session, error)
	OpenVerifiedBoundThemeSession(context.Context, engine.CompiledTheme, engine.Identity) (engine.Session, error)
	Apply(context.Context, engine.Session, engine.CompiledTheme) error
	Verify(context.Context, engine.Session, engine.CompiledTheme) (engine.RegionReport, error)
	ThemeSessionHealthy(context.Context, engine.Session, engine.CompiledTheme) (bool, error)
	RestoreOfficial(context.Context, engine.Session) error
	VerifyOfficial(context.Context, engine.Session) error
	Close(context.Context, engine.Session) error
	RestoreNativeAppearanceBackup() error
}

// runThemeSession is the v2 session Runtime Supervisor. On the restart path it
// runs inside the same detached process that launched and themed Codex; it does
// not hand ownership to a second keeper process. It remains alive only until
// Codex exits, Restore asks it to stop, or identity becomes unsafe.
func runThemeSession(sessionID string, environment Runtime) int {
	controlPoll, healthInterval, healthTimeout, appearanceSettle := sessionControllerTimings(environment)
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
	loadTheme := environment.sessionThemeLoader
	if loadTheme == nil {
		loadTheme = engine.LoadDesiredCompiled
	}
	compiled, desired, err := loadTheme(store)
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
		if desiredErr != nil || !desiredFound {
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "desired_theme_missing")
			return exitApply
		}
		if currentDesired != *desired {
			nextCompiled, nextDesired, loadErr := loadTheme(store)
			if loadErr != nil || nextDesired == nil || nextCompiled == nil || *nextDesired != currentDesired {
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "cached_theme_invalid")
				return exitApply
			}
			updated, switchErr := sessions.Switch(
				sessionID,
				nextDesired.ThemePublicID,
				nextDesired.ThemeVersion,
				nextDesired.PackageSHA256,
			)
			if switchErr != nil {
				return exitInternal
			}
			record = updated
			compiled, desired = nextCompiled, nextDesired
		}

		newLive := environment.sessionAdapterFactory
		if newLive == nil {
			newLive = func(root string) (themeSessionAdapter, error) {
				return adapter.NewLive(adapter.Config{Root: root, CurrentProfile: true})
			}
		}
		live, liveErr := newLive(store.Root())
		if liveErr != nil {
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_configuration")
			return exitApply
		}
		sessionCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		liveSession, openErr := live.OpenVerifiedBoundThemeSession(sessionCtx, *compiled, record.Codex)
		cancel()
		if openErr != nil {
			if requested, stopErr := sessions.StopRequested(sessionID); stopErr == nil && requested {
				if appearanceErr := live.RestoreNativeAppearanceBackup(); appearanceErr != nil {
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_restore_failed")
					return exitApply
				}
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
				return exitSuccess
			}
			if sessionCodexExited(ctx) {
				_ = live.RestoreNativeAppearanceBackup()
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "codex_exited")
				return exitSuccess
			}
			if !lastReady.IsZero() && time.Since(lastReady) < sessionReconnectWindow {
				if !waitSession(ctx, sessionReconnectPoll) {
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
		stopRequested, stopErr = sessions.StopRequested(sessionID)
		if stopErr != nil {
			_ = live.Close(context.Background(), liveSession)
			return exitInternal
		}
		if stopRequested {
			closeErr := live.Close(context.Background(), liveSession)
			appearanceErr := live.RestoreNativeAppearanceBackup()
			if errors.Join(closeErr, appearanceErr) != nil {
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_restore_failed")
				return exitApply
			}
			_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
			return exitSuccess
		}

		applyCtx, applyCancel := context.WithTimeout(ctx, sessionVerificationWait)
		applyErr := live.Apply(applyCtx, liveSession, *compiled)
		var report engine.RegionReport
		if applyErr == nil {
			report, applyErr = waitForSessionThemeVerification(applyCtx, live, liveSession, *compiled)
		}
		applyCancel()
		stopRequested, stopErr = sessions.StopRequested(sessionID)
		if stopErr != nil || stopRequested {
			if restoreErr := restoreControlledSession(live, liveSession); restoreErr != nil {
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_restore_failed")
				return exitApply
			}
			if stopErr != nil {
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_state_lost")
				return exitInternal
			}
			_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
			return exitSuccess
		}
		if applyErr != nil || !engine.ReportAllowsTheme(report, *compiled) {
			if !lastReady.IsZero() && time.Since(lastReady) < sessionReconnectWindow {
				_ = live.Close(context.Background(), liveSession)
				if !waitSession(ctx, sessionReconnectPoll) {
					return exitInternal
				}
				continue
			}
			_ = restoreControlledSession(live, liveSession)
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_verify_failed")
			return exitApply
		}
		if lastReady.IsZero() {
			// Native light/dark pinning is needed only to start the controlled
			// process. Restore the user's original settings on disk before
			// claiming success, then prove the live renderer remains themed.
			if err := live.RestoreNativeAppearanceBackup(); err != nil {
				_ = restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "appearance_restore_failed")
				return exitApply
			}
			if !waitSession(ctx, appearanceSettle) {
				_ = restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_cancelled")
				return exitApply
			}
			settleCtx, settleCancel := context.WithTimeout(ctx, sessionVerificationWait)
			settledReport, settleErr := waitForSessionThemeVerification(
				settleCtx, live, liveSession, *compiled,
			)
			settleCancel()
			if settleErr != nil || !engine.ReportAllowsTheme(settledReport, *compiled) {
				_ = restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "appearance_restore_changed_renderer")
				return exitApply
			}
			stopRequested, stopErr = sessions.StopRequested(sessionID)
			if stopErr != nil || stopRequested {
				if restoreErr := restoreControlledSession(live, liveSession); restoreErr != nil {
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_restore_failed")
					return exitApply
				}
				if stopErr != nil {
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_state_lost")
					return exitInternal
				}
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
				return exitSuccess
			}
		}
		if _, err := sessions.Activate(sessionID, os.Getpid()); err != nil {
			restoreErr := restoreControlledSession(live, liveSession)
			if requested, stopErr := sessions.StopRequested(sessionID); stopErr == nil && requested {
				if restoreErr != nil {
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_restore_failed")
					return exitApply
				}
				_, _ = sessions.Finish(sessionID, sessionflow.StatusEnded, "restore_requested")
				return exitSuccess
			}
			_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_activate_failed")
			return exitInternal
		}
		if lastReady.IsZero() && environment.SessionActivated != nil {
			if err := environment.SessionActivated(); err != nil {
				_ = restoreControlledSession(live, liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "runtime_commit_failed")
				return exitInternal
			}
			environment.SessionActivated = nil
		}
		lastReady = time.Now()
		lastHeartbeat := lastReady
		nextHealth := lastReady.Add(healthInterval)

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
			if desiredErr != nil || !desiredFound {
				_ = live.Close(context.Background(), liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "desired_theme_missing")
				return exitApply
			}
			now := time.Now()
			if currentDesired == *desired && now.Before(nextHealth) {
				wait := controlPoll
				if remaining := time.Until(nextHealth); remaining < wait {
					wait = remaining
				}
				if !waitSession(ctx, wait) {
					_ = live.Close(context.Background(), liveSession)
					return exitInternal
				}
				continue
			}

			// The foreground Plugin process holds this lock while applying and
			// committing an in-place switch. Acquire it only when a desired-theme
			// change is visible or a health probe is due. Taking it on every
			// control tick can starve Restore's stop request on fast renderers.
			releaseOperation, lockErr := store.Lock()
			if errors.Is(lockErr, engine.ErrBusy) {
				if !waitSession(ctx, controlPoll) {
					_ = live.Close(context.Background(), liveSession)
					return exitInternal
				}
				continue
			}
			if lockErr != nil {
				_ = live.Close(context.Background(), liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_state_lost")
				return exitInternal
			}
			currentDesired, desiredFound, desiredErr = store.ReadDesired()
			if desiredErr != nil || !desiredFound {
				_ = releaseOperation()
				_ = live.Close(context.Background(), liveSession)
				_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "desired_theme_missing")
				return exitApply
			}
			if currentDesired != *desired {
				if err := releaseOperation(); err != nil {
					_ = live.Close(context.Background(), liveSession)
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_state_lost")
					return exitInternal
				}
				nextCompiled, nextDesired, loadErr := loadTheme(store)
				if loadErr != nil || nextDesired == nil || nextCompiled == nil || *nextDesired != currentDesired {
					_ = live.Close(context.Background(), liveSession)
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "cached_theme_invalid")
					return exitApply
				}
				updated, switchErr := sessions.Switch(
					sessionID,
					nextDesired.ThemePublicID,
					nextDesired.ThemeVersion,
					nextDesired.PackageSHA256,
				)
				_ = live.Close(context.Background(), liveSession)
				if switchErr != nil {
					return exitInternal
				}
				record = updated
				compiled, desired = nextCompiled, nextDesired
				break
			}
			now = time.Now()
			if now.Before(nextHealth) {
				if err := releaseOperation(); err != nil {
					_ = live.Close(context.Background(), liveSession)
					_, _ = sessions.Finish(sessionID, sessionflow.StatusFailed, "controller_state_lost")
					return exitInternal
				}
				wait := controlPoll
				if remaining := time.Until(nextHealth); remaining < wait {
					wait = remaining
				}
				if !waitSession(ctx, wait) {
					_ = live.Close(context.Background(), liveSession)
					return exitInternal
				}
				continue
			}
			healthCtx, healthCancel := context.WithTimeout(ctx, healthTimeout)
			healthy, healthErr := live.ThemeSessionHealthy(healthCtx, liveSession, *compiled)
			healthCancel()
			unlockErr := releaseOperation()
			if healthErr != nil || unlockErr != nil || !healthy {
				_ = live.Close(context.Background(), liveSession)
				break
			}
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
			nextHealth = time.Now().Add(healthInterval)
		}
	}
}

func waitForSessionThemeVerification(
	ctx context.Context,
	live themeSessionAdapter,
	session engine.Session,
	compiled engine.CompiledTheme,
) (engine.RegionReport, error) {
	if waiter, supported := live.(engine.ThemeVerificationWaiter); supported {
		result, err := waiter.WaitForThemeVerification(ctx, session, compiled)
		return result.Report, err
	}
	return live.Verify(ctx, session, compiled)
}

func sessionControllerTimings(environment Runtime) (
	controlPoll time.Duration,
	healthInterval time.Duration,
	healthTimeout time.Duration,
	appearanceSettle time.Duration,
) {
	controlPoll = environment.sessionControlPoll
	if controlPoll <= 0 {
		controlPoll = sessionControlPoll
	}
	healthInterval = environment.sessionHealthInterval
	if healthInterval <= 0 {
		healthInterval = sessionHealthInterval
	}
	healthTimeout = environment.sessionHealthTimeout
	if healthTimeout <= 0 {
		healthTimeout = sessionHealthTimeout
	}
	appearanceSettle = environment.sessionAppearanceSettle
	if appearanceSettle <= 0 {
		appearanceSettle = sessionAppearanceSettle
	}
	return controlPoll, healthInterval, healthTimeout, appearanceSettle
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

func restoreControlledSession(live themeSessionAdapter, session engine.Session) error {
	if live == nil {
		return engine.ErrConfiguration
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	restoreErr := live.RestoreOfficial(cleanupCtx, session)
	var verifyErr error
	if restoreErr == nil {
		verifyErr = live.VerifyOfficial(cleanupCtx, session)
	}
	closeErr := live.Close(cleanupCtx, session)
	appearanceErr := live.RestoreNativeAppearanceBackup()
	return errors.Join(restoreErr, verifyErr, closeErr, appearanceErr)
}

func sessionControllerEnabled(environment Runtime) bool {
	// Production opts in explicitly from cmd/codex-skin. Tests are safe by
	// default and may exercise orchestration only with a fake StartSession.
	return environment.StartSession != nil ||
		(environment.EnableLiveSessionController && environment.ApplyFlow == nil && environment.Adapter == nil)
}

func ensureThemeSession(
	store *engine.Store,
	themePublicID, themeVersion string,
	environment Runtime,
) error {
	if store == nil {
		return engine.ErrConfiguration
	}
	sessions, err := sessionflow.New(store.Root())
	if err != nil {
		return err
	}
	current, found, err := sessions.Current()
	if err != nil {
		return err
	}
	if !found || !current.InProgress() || !current.Fresh(time.Now(), sessionHeartbeatMaxAge) {
		return startThemeSession(store, themePublicID, themeVersion, environment)
	}
	ctx := environment.Context
	if ctx == nil {
		ctx = context.Background()
	}
	startTimeout, _, waitInterval := sessionTimings(environment)
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		current, found, err = sessions.Current()
		if err != nil || !found {
			if err == nil {
				err = engine.ErrStateUnsafe
			}
			return err
		}
		if current.Status == sessionflow.StatusActive &&
			current.ThemePublicID == themePublicID && current.ThemeVersion == themeVersion {
			return nil
		}
		if current.Status == sessionflow.StatusFailed || current.Status == sessionflow.StatusEnded {
			return engine.ErrApplyFailed
		}
		if !waitSession(ctx, waitInterval) {
			return ctx.Err()
		}
	}
	return engine.ErrApplyFailed
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
	startTimeout, stopTimeout, waitInterval := sessionTimings(environment)
	deadline := time.Now().Add(startTimeout)
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
		if !waitSession(ctx, waitInterval) {
			cause := ctx.Err()
			if stopErr := requestStopAndWait(sessions, record.SessionID, ctx, stopTimeout, waitInterval); stopErr != nil {
				return errors.Join(errSessionStopUnconfirmed, cause, stopErr)
			}
			return cause
		}
	}
	if stopErr := requestStopAndWait(sessions, record.SessionID, ctx, stopTimeout, waitInterval); stopErr != nil {
		return errors.Join(errSessionStopUnconfirmed, engine.ErrApplyFailed, stopErr)
	}
	return engine.ErrApplyFailed
}

func sessionTimings(environment Runtime) (time.Duration, time.Duration, time.Duration) {
	startTimeout := environment.sessionStartTimeout
	if startTimeout <= 0 {
		startTimeout = 35 * time.Second
	}
	stopTimeout := environment.sessionStopTimeout
	if stopTimeout <= 0 {
		stopTimeout = 20 * time.Second
	}
	waitInterval := environment.sessionWaitInterval
	if waitInterval <= 0 {
		waitInterval = 150 * time.Millisecond
	}
	return startTimeout, stopTimeout, waitInterval
}

func requestStopAndWait(
	sessions *sessionflow.Store,
	sessionID string,
	parent context.Context,
	timeout, interval time.Duration,
) error {
	if sessions == nil || sessionID == "" {
		return engine.ErrConfiguration
	}
	if parent == nil {
		parent = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	requested := false
	// A health probe and a Restore request share the operation lock. Fixed
	// retry intervals can phase-lock on a fast renderer: each Restore attempt
	// arrives while the same lightweight probe owns the lock. Back off only
	// after a busy lock so the request yields a bounded, different window; a
	// successful stop request immediately returns to the caller's cadence.
	waitInterval := interval
	maxBusyWait := 25 * time.Millisecond
	if interval > maxBusyWait {
		maxBusyWait = interval
	}
	for {
		if !requested {
			record, didRequest, err := sessions.RequestStop()
			if err == nil {
				if record.SessionID != sessionID {
					return engine.ErrStateUnsafe
				}
				if record.Status == sessionflow.StatusEnded || record.Status == sessionflow.StatusFailed {
					return nil
				}
				requested = didRequest || record.Status == sessionflow.StatusStopping
				waitInterval = interval
			} else if !errors.Is(err, engine.ErrBusy) {
				return err
			} else if waitInterval < maxBusyWait {
				waitInterval *= 2
				if waitInterval > maxBusyWait {
					waitInterval = maxBusyWait
				}
			}
		}
		current, found, err := sessions.Current()
		if err != nil || !found || current.SessionID != sessionID {
			if err == nil {
				err = engine.ErrStateUnsafe
			}
			return err
		}
		if current.Status == sessionflow.StatusEnded || current.Status == sessionflow.StatusFailed {
			return nil
		}
		if !waitSession(cleanupCtx, waitInterval) {
			return cleanupCtx.Err()
		}
	}
}

func sessionRollbackSafe(err error) bool {
	return err != nil && !errors.Is(err, errSessionStopUnconfirmed)
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
	record, found, err := sessions.Current()
	if err != nil || !found || !record.InProgress() {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !record.Fresh(time.Now(), sessionHeartbeatMaxAge) {
		deadline := time.Now().Add(2 * time.Second)
		for {
			_, expired, expireErr := sessions.ExpireStale(
				sessionHeartbeatMaxAge,
				"controller_heartbeat_lost",
			)
			if expireErr == nil {
				if expired {
					return nil
				}
				break
			}
			if !errors.Is(expireErr, engine.ErrBusy) || !time.Now().Before(deadline) {
				return expireErr
			}
			if !waitSession(context.WithoutCancel(ctx), 25*time.Millisecond) {
				return context.Canceled
			}
		}
	}
	if err := requestStopAndWait(
		sessions,
		record.SessionID,
		ctx,
		12*time.Second,
		150*time.Millisecond,
	); err != nil {
		return err
	}
	current, found, err := sessions.Current()
	if err != nil || !found || current.SessionID != record.SessionID {
		if err == nil {
			err = engine.ErrStateUnsafe
		}
		return err
	}
	if current.Status == sessionflow.StatusEnded {
		return nil
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
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
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
	_, err = instance.RestoreOfficial(cleanupCtx)
	return err
}
