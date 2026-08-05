package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/sessionflow"
)

type gatedThemeSessionAdapter struct {
	identity      engine.Identity
	opened        chan struct{}
	releaseOpen   chan struct{}
	afterApply    chan struct{}
	releaseVerify chan struct{}
	verifyGate    sync.Once

	mu                     sync.Mutex
	ordinaryOpenCount      int
	boundOpenCount         int
	applyCount             int
	verifyCount            int
	healthCount            int
	restoreCount           int
	appearanceRestoreCount int
	themed                 bool
}

func (adapter *gatedThemeSessionAdapter) OpenVerifiedThemeSession(
	ctx context.Context,
	_ engine.CompiledTheme,
) (engine.Session, error) {
	adapter.mu.Lock()
	adapter.ordinaryOpenCount++
	adapter.mu.Unlock()
	return adapter.open(ctx)
}

func (adapter *gatedThemeSessionAdapter) OpenVerifiedBoundThemeSession(
	ctx context.Context,
	_ engine.CompiledTheme,
	expected engine.Identity,
) (engine.Session, error) {
	adapter.mu.Lock()
	adapter.boundOpenCount++
	adapter.mu.Unlock()
	if !sameIdentity(adapter.identity, expected) {
		return engine.Session{}, engine.ErrStateUnsafe
	}
	return adapter.open(ctx)
}

func (adapter *gatedThemeSessionAdapter) open(ctx context.Context) (engine.Session, error) {
	if adapter.opened != nil {
		close(adapter.opened)
	}
	if adapter.releaseOpen != nil {
		select {
		case <-ctx.Done():
			return engine.Session{}, ctx.Err()
		case <-adapter.releaseOpen:
		}
	}
	return engine.Session{Identity: adapter.identity, OpaqueID: "gated-session"}, nil
}

func (adapter *gatedThemeSessionAdapter) Apply(
	_ context.Context,
	_ engine.Session,
	_ engine.CompiledTheme,
) error {
	adapter.mu.Lock()
	adapter.applyCount++
	adapter.themed = true
	adapter.mu.Unlock()
	return nil
}

func (adapter *gatedThemeSessionAdapter) Verify(
	ctx context.Context,
	_ engine.Session,
	compiled engine.CompiledTheme,
) (engine.RegionReport, error) {
	adapter.mu.Lock()
	adapter.verifyCount++
	adapter.mu.Unlock()
	if adapter.afterApply != nil {
		adapter.verifyGate.Do(func() {
			close(adapter.afterApply)
			select {
			case <-ctx.Done():
			case <-adapter.releaseVerify:
			}
		})
	}
	return passingThemeReport(compiled), nil
}

func (adapter *gatedThemeSessionAdapter) ThemeSessionHealthy(
	context.Context,
	engine.Session,
	engine.CompiledTheme,
) (bool, error) {
	adapter.mu.Lock()
	adapter.healthCount++
	adapter.mu.Unlock()
	return true, nil
}

func (adapter *gatedThemeSessionAdapter) RestoreOfficial(context.Context, engine.Session) error {
	adapter.mu.Lock()
	adapter.restoreCount++
	adapter.themed = false
	adapter.mu.Unlock()
	return nil
}

func (adapter *gatedThemeSessionAdapter) VerifyOfficial(context.Context, engine.Session) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.themed {
		return engine.ErrRestoreFailed
	}
	return nil
}

func (*gatedThemeSessionAdapter) Close(context.Context, engine.Session) error { return nil }

func (adapter *gatedThemeSessionAdapter) RestoreNativeAppearanceBackup() error {
	adapter.mu.Lock()
	adapter.appearanceRestoreCount++
	adapter.mu.Unlock()
	return nil
}

func (adapter *gatedThemeSessionAdapter) state() (int, int, int, bool) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.applyCount, adapter.restoreCount, adapter.appearanceRestoreCount, adapter.themed
}

func (adapter *gatedThemeSessionAdapter) metrics() (int, int, int, int) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.ordinaryOpenCount, adapter.boundOpenCount, adapter.verifyCount, adapter.healthCount
}

func TestActiveSessionUsesBoundIdentityAndLightweightHealthChecks(t *testing.T) {
	store, identity, compiled, desired, executable := newSessionControllerTestState(t)
	adapter := &gatedThemeSessionAdapter{identity: identity}
	controllerDone := make(chan int, 1)
	parent := Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Executable: executable,
		currentSessionIdentity: func(context.Context) (engine.Identity, error) { return identity, nil },
		sessionStartTimeout:    time.Second,
		sessionStopTimeout:     time.Second,
		sessionWaitInterval:    time.Millisecond,
	}
	parent.StartSession = func(_ string, sessionID string) error {
		go func() {
			controllerDone <- runThemeSession(sessionID, Runtime{
				GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Context: context.Background(),
				sessionControlPoll: time.Millisecond,
				// Keep the health cadence short enough to prove that foreground
				// transactions exclude probes, without phase-locking a 3 ms probe
				// interval against the test's 1 ms stop-poll interval. Production
				// health checks run every 10 seconds.
				sessionHealthInterval:   20 * time.Millisecond,
				sessionHealthTimeout:    20 * time.Millisecond,
				sessionAppearanceSettle: time.Millisecond,
				sessionAdapterFactory:   func(string) (themeSessionAdapter, error) { return adapter, nil },
				sessionThemeLoader: func(*engine.Store) (*engine.CompiledTheme, *engine.DesiredTheme, error) {
					copyCompiled, copyDesired := compiled, desired
					return &copyCompiled, &copyDesired, nil
				},
			})
		}()
		return nil
	}

	if err := startThemeSession(store, desired.ThemePublicID, desired.ThemeVersion, parent); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, _, _, healthCount := adapter.metrics()
		if healthCount >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ordinaryOpenCount, boundOpenCount, verifyCount, healthCount := adapter.metrics()
	if ordinaryOpenCount != 0 || boundOpenCount != 1 {
		t.Fatalf("session opens: ordinary=%d bound=%d", ordinaryOpenCount, boundOpenCount)
	}
	if verifyCount != 2 || healthCount < 2 {
		t.Fatalf("session checks: full_verify=%d lightweight_health=%d", verifyCount, healthCount)
	}
	var releaseOperation func() error
	var lockErr error
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		releaseOperation, lockErr = store.Lock()
		if lockErr == nil {
			break
		}
		if !errors.Is(lockErr, engine.ErrBusy) {
			t.Fatal(lockErr)
		}
		time.Sleep(time.Millisecond)
	}
	if releaseOperation == nil {
		t.Fatal("foreground apply transaction could not acquire the operation lock")
	}
	_, _, _, healthBeforeLockWait := adapter.metrics()
	// Hold the foreground transaction across more than one health interval.
	time.Sleep(50 * time.Millisecond)
	_, _, _, healthDuringLock := adapter.metrics()
	if healthDuringLock != healthBeforeLockWait {
		t.Fatalf(
			"health check raced with foreground apply transaction: before=%d during=%d",
			healthBeforeLockWait,
			healthDuringLock,
		)
	}
	if err := releaseOperation(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, _, _, healthAfterUnlock := adapter.metrics()
		if healthAfterUnlock > healthDuringLock {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_, _, _, healthAfterUnlock := adapter.metrics()
	if healthAfterUnlock <= healthDuringLock {
		t.Fatalf("health checks did not resume after transaction unlock: %d", healthAfterUnlock)
	}
	sessions, err := sessionflow.New(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := sessions.Current()
	if err != nil || !found {
		t.Fatalf("active session missing: found=%t err=%v", found, err)
	}
	if err := requestStopAndWait(
		sessions,
		record.SessionID,
		context.Background(),
		3*time.Second,
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	if code := awaitCode(t, controllerDone); code != exitSuccess {
		t.Fatalf("controller code = %d", code)
	}
}

func TestActiveRuntimeSwitchesThemeInPlaceWithoutLaunchingAnotherProcess(t *testing.T) {
	store, identity, firstCompiled, firstDesired, executable := newSessionControllerTestState(t)
	secondDesired := firstDesired
	secondDesired.ThemePublicID = "100002"
	secondDesired.ThemeVersion = "2.0.0"
	secondDesired.PackageSHA256 = strings.Repeat("b", 64)
	secondDesired.AppliedAt = "2026-08-05T08:00:00Z"
	secondCompiled := firstCompiled
	secondCompiled.ThemePublicID = secondDesired.ThemePublicID
	secondCompiled.ThemeVersion = secondDesired.ThemeVersion
	secondCompiled.StyleText = "second-theme"
	adapter := &gatedThemeSessionAdapter{identity: identity}
	controllerDone := make(chan int, 1)
	load := func(current *engine.Store) (*engine.CompiledTheme, *engine.DesiredTheme, error) {
		desired, found, err := current.ReadDesired()
		if err != nil || !found {
			return nil, nil, engine.ErrStateUnsafe
		}
		compiled := firstCompiled
		if desired.ThemePublicID == secondDesired.ThemePublicID {
			compiled = secondCompiled
		}
		return &compiled, &desired, nil
	}
	parent := Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Executable: executable,
		currentSessionIdentity: func(context.Context) (engine.Identity, error) { return identity, nil },
		sessionStartTimeout:    time.Second,
		sessionStopTimeout:     time.Second,
		sessionWaitInterval:    time.Millisecond,
	}
	parent.StartSession = func(_ string, sessionID string) error {
		go func() {
			controllerDone <- runThemeSession(sessionID, Runtime{
				GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Context: context.Background(),
				sessionControlPoll:      time.Millisecond,
				sessionHealthInterval:   5 * time.Millisecond,
				sessionHealthTimeout:    20 * time.Millisecond,
				sessionAppearanceSettle: time.Millisecond,
				sessionAdapterFactory:   func(string) (themeSessionAdapter, error) { return adapter, nil },
				sessionThemeLoader:      load,
			})
		}()
		return nil
	}
	if err := startThemeSession(store, firstDesired.ThemePublicID, firstDesired.ThemeVersion, parent); err != nil {
		t.Fatal(err)
	}
	var unlock func() error
	var lockErr error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		unlock, lockErr = store.Lock()
		if lockErr == nil {
			break
		}
		if !errors.Is(lockErr, engine.ErrBusy) {
			t.Fatal(lockErr)
		}
		time.Sleep(time.Millisecond)
	}
	if unlock == nil {
		t.Fatal("switch transaction did not acquire operation lock")
	}
	if err := store.WriteDesired(secondDesired); err != nil {
		_ = unlock()
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	sessions, err := sessionflow.New(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	var current sessionflow.Record
	for time.Now().Before(deadline) {
		current, _, err = sessions.Current()
		if err == nil && current.Status == sessionflow.StatusActive &&
			current.ThemePublicID == secondDesired.ThemePublicID {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || current.Status != sessionflow.StatusActive ||
		current.ThemePublicID != secondDesired.ThemePublicID ||
		current.ControllerPID == 0 || current.Codex.ProcessID != identity.ProcessID {
		t.Fatalf("switched runtime = %#v, err=%v", current, err)
	}
	ordinaryOpenCount, boundOpenCount, verifyCount, _ := adapter.metrics()
	applyCount, _, appearanceRestores, themed := adapter.state()
	if ordinaryOpenCount != 0 || boundOpenCount != 2 || verifyCount != 3 ||
		applyCount != 2 || appearanceRestores != 1 || !themed {
		t.Fatalf(
			"in-place switch opens=%d/%d verify=%d apply=%d appearance=%d themed=%t",
			ordinaryOpenCount,
			boundOpenCount,
			verifyCount,
			applyCount,
			appearanceRestores,
			themed,
		)
	}
	if err := requestStopAndWait(
		sessions,
		current.SessionID,
		context.Background(),
		3*time.Second,
		time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	if code := awaitCode(t, controllerDone); code != exitSuccess {
		t.Fatalf("controller code = %d", code)
	}
}

func TestSessionStartTimeoutWaitsForPausedControllerBeforeRollback(t *testing.T) {
	store, identity, compiled, desired, executable := newSessionControllerTestState(t)
	adapter := &gatedThemeSessionAdapter{
		identity: identity, opened: make(chan struct{}), releaseOpen: make(chan struct{}),
	}
	controllerDone := make(chan int, 1)
	parent := Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Executable: executable,
		currentSessionIdentity: func(context.Context) (engine.Identity, error) { return identity, nil },
		// Keep this above normal CI scheduling jitter. The test still exercises
		// the timeout path because the controller remains blocked until the
		// explicit release below.
		sessionStartTimeout: 100 * time.Millisecond,
		sessionStopTimeout:  3 * time.Second,
		sessionWaitInterval: 2 * time.Millisecond,
	}
	parent.StartSession = func(_ string, sessionID string) error {
		go func() {
			controllerDone <- runThemeSession(sessionID, Runtime{
				GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Context: context.Background(),
				sessionAdapterFactory: func(string) (themeSessionAdapter, error) { return adapter, nil },
				sessionThemeLoader: func(*engine.Store) (*engine.CompiledTheme, *engine.DesiredTheme, error) {
					copyCompiled, copyDesired := compiled, desired
					return &copyCompiled, &copyDesired, nil
				},
			})
		}()
		return nil
	}

	result := make(chan error, 1)
	go func() { result <- startThemeSession(store, desired.ThemePublicID, desired.ThemeVersion, parent) }()
	awaitControllerGate(t, adapter.opened, "controller open", result, controllerDone)
	awaitSessionStatus(t, store.Root(), sessionflow.StatusStopping)
	close(adapter.releaseOpen)

	err := awaitError(t, result)
	if !errors.Is(err, engine.ErrApplyFailed) || errors.Is(err, errSessionStopUnconfirmed) {
		t.Fatalf("start error = %v", err)
	}
	if code := awaitCode(t, controllerDone); code != exitSuccess {
		t.Fatalf("controller code = %d", code)
	}
	assertTerminalSession(t, store.Root(), sessionflow.StatusEnded)
	applyCount, restoreCount, appearanceRestoreCount, themed := adapter.state()
	if applyCount != 0 || restoreCount != 0 || appearanceRestoreCount != 1 || themed {
		t.Fatalf(
			"late mutation: apply=%d restore=%d appearance_restore=%d themed=%t",
			applyCount, restoreCount, appearanceRestoreCount, themed,
		)
	}
}

func TestSessionStartCancellationRestoresPausedPostApplyBeforeReturning(t *testing.T) {
	store, identity, compiled, desired, executable := newSessionControllerTestState(t)
	adapter := &gatedThemeSessionAdapter{
		identity: identity, afterApply: make(chan struct{}), releaseVerify: make(chan struct{}),
	}
	controllerDone := make(chan int, 1)
	parentCtx, cancelParent := context.WithCancel(context.Background())
	parent := Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Executable: executable,
		Context:                parentCtx,
		currentSessionIdentity: func(context.Context) (engine.Identity, error) { return identity, nil },
		sessionStartTimeout:    time.Second,
		sessionStopTimeout:     3 * time.Second,
		sessionWaitInterval:    2 * time.Millisecond,
	}
	parent.StartSession = func(_ string, sessionID string) error {
		go func() {
			controllerDone <- runThemeSession(sessionID, Runtime{
				GOOS: "darwin", GOARCH: "arm64", Root: store.Root(), Context: context.Background(),
				sessionAdapterFactory: func(string) (themeSessionAdapter, error) { return adapter, nil },
				sessionThemeLoader: func(*engine.Store) (*engine.CompiledTheme, *engine.DesiredTheme, error) {
					copyCompiled, copyDesired := compiled, desired
					return &copyCompiled, &copyDesired, nil
				},
			})
		}()
		return nil
	}

	result := make(chan error, 1)
	go func() { result <- startThemeSession(store, desired.ThemePublicID, desired.ThemeVersion, parent) }()
	awaitControllerGate(t, adapter.afterApply, "post-apply verification", result, controllerDone)
	cancelParent()
	awaitSessionStatus(t, store.Root(), sessionflow.StatusStopping)
	close(adapter.releaseVerify)

	err := awaitError(t, result)
	if !errors.Is(err, context.Canceled) || errors.Is(err, errSessionStopUnconfirmed) {
		t.Fatalf("start error = %v", err)
	}
	if code := awaitCode(t, controllerDone); code != exitSuccess {
		t.Fatalf("controller code = %d", code)
	}
	assertTerminalSession(t, store.Root(), sessionflow.StatusEnded)
	applyCount, restoreCount, appearanceRestoreCount, themed := adapter.state()
	if applyCount != 1 || restoreCount != 1 || appearanceRestoreCount != 1 || themed {
		t.Fatalf(
			"unsafe post-apply state: apply=%d restore=%d appearance_restore=%d themed=%t",
			applyCount, restoreCount, appearanceRestoreCount, themed,
		)
	}
}

func newSessionControllerTestState(
	t *testing.T,
) (*engine.Store, engine.Identity, engine.CompiledTheme, engine.DesiredTheme, string) {
	t.Helper()
	store, err := engine.OpenStore(filepath.Join(t.TempDir(), "CodexSkin"), "")
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	desired := engine.DesiredTheme{
		SchemaVersion: engine.StateSchemaVersion,
		ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: digest,
		TemplateVersion: engine.TemplateVersion, AppliedAt: "2026-08-04T08:00:00Z",
	}
	if err := store.WriteDesired(desired); err != nil {
		t.Fatal(err)
	}
	compiled := engine.CompiledTheme{
		ThemePublicID: desired.ThemePublicID, ThemeVersion: desired.ThemeVersion,
		TemplateVersion: engine.TemplateVersion, AppearanceMode: "dark",
		StyleText: "theme", BackgroundDataURL: "data:image/png;base64,iVBORw0KGgo=",
	}
	identity := engine.Identity{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: "26.727.0", ExecutableHash: digest, ProcessID: 4312, ProcessStartID: "start-4312",
	}
	executable := filepath.Join(store.Root(), "recovery", "engine", "codex-skin")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	return store, identity, compiled, desired, executable
}

func passingThemeReport(compiled engine.CompiledTheme) engine.RegionReport {
	regions := map[string]engine.RegionStatus{}
	for _, name := range []string{
		"home", "mainBoundary", "sidebar", "composer", "topFade", "bottomFade",
		"templateScope", "themeContrast",
	} {
		regions[name] = engine.RegionPass
	}
	for _, name := range []string{
		"composerUtilityBar", "conversationActivity", "conversationDiffResource",
		"suggestionCards", "projectPicker",
	} {
		regions[name] = engine.RegionNotPresent
	}
	return engine.RegionReport{
		StyleMarkerCount: 1, TemplateVersion: compiled.TemplateVersion,
		ThemePublicID: compiled.ThemePublicID, BackgroundLoaded: true, Regions: regions,
	}
}

func awaitControllerGate(
	t *testing.T,
	signal <-chan struct{},
	name string,
	startResult <-chan error,
	controllerResult <-chan int,
) {
	t.Helper()
	select {
	case <-signal:
	case err := <-startResult:
		t.Fatalf("start returned before %s: %v", name, err)
	case code := <-controllerResult:
		t.Fatalf("controller returned before %s: %d", name, code)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitSessionStatus(t *testing.T, root string, wanted sessionflow.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sessions, err := sessionflow.New(root)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := sessions.Current()
		if err == nil && found && record.Status == wanted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for session status %s", wanted)
}

func assertTerminalSession(t *testing.T, root string, wanted sessionflow.Status) {
	t.Helper()
	sessions, err := sessionflow.New(root)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := sessions.Current()
	if err != nil || !found || record.Status != wanted {
		t.Fatalf("session=%#v found=%t err=%v", record, found, err)
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for start result")
		return nil
	}
}

func awaitCode(t *testing.T, result <-chan int) int {
	t.Helper()
	select {
	case code := <-result:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for controller result")
		return -1
	}
}
