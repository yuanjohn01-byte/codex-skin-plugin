// Package adapter implements the official Codex Desktop loopback CDP adapter.
package adapter

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/appearance"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cdp"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/renderer"
)

const (
	defaultLaunchWait   = 25 * time.Second
	openRollbackTimeout = 45 * time.Second
	// Codex can still be attaching its first shell and Blob background after a
	// controlled launch, so one on-demand apply uses a bounded verification
	// window rather than a single immediate snapshot.
	themeVerifyInitialWait = 15 * time.Second
	themeVerifyRepairWait  = 12 * time.Second
	themeVerifyPoll        = 250 * time.Millisecond
)

var sixDigitID = regexp.MustCompile(`^[0-9]{6}$`)

// transitionRecoveryThemeKey carries only the already verified, previously
// committed package through the short open transaction. It is never persisted
// and lets an early cross-mode launch failure return the user to that prior
// skin instead of silently falling back to an unskinned ordinary Codex window.
type transitionRecoveryThemeKey struct{}

type Live struct {
	root            string
	profile         string
	port            int
	launchWait      time.Duration
	currentProfile  bool
	restartApproved bool
	appearance      *appearance.Manager
	mu              sync.Mutex
	sessions        map[string]*liveSession
}

type liveSession struct {
	client         *cdp.Client
	installation   codex.Installation
	process        codex.ProcessIdentity
	port           int
	profile        string
	current        *engine.CompiledTheme
	appearanceMode string
	targetID       string
}

type Config struct {
	Root            string
	Profile         string
	Port            int
	LaunchWait      time.Duration
	CurrentProfile  bool
	RestartApproved bool
	UserHome        string
}

type remoteObject struct {
	Type     string          `json:"type"`
	ObjectID string          `json:"objectId"`
	Value    json.RawMessage `json:"value"`
}

type evaluateResult struct {
	Result           remoteObject `json:"result"`
	ExceptionDetails any          `json:"exceptionDetails,omitempty"`
}

type callResult struct {
	Result           remoteObject `json:"result"`
	ExceptionDetails any          `json:"exceptionDetails,omitempty"`
}

type SurfaceDiagnostic struct {
	Tag             string    `json:"tag"`
	Classes         string    `json:"classes"`
	BackgroundColor string    `json:"backgroundColor"`
	BackgroundImage string    `json:"backgroundImage"`
	BoxShadow       string    `json:"boxShadow"`
	Border          string    `json:"border"`
	Display         string    `json:"display"`
	Position        string    `json:"position"`
	Margin          string    `json:"margin"`
	Padding         string    `json:"padding"`
	Gap             string    `json:"gap"`
	Rect            []float64 `json:"rect"`
}

type PseudoDiagnostic struct {
	Tag             string    `json:"tag"`
	Classes         string    `json:"classes"`
	Pseudo          string    `json:"pseudo"`
	Generated       bool      `json:"generated"`
	BackgroundColor string    `json:"backgroundColor"`
	BackgroundImage string    `json:"backgroundImage"`
	BoxShadow       string    `json:"boxShadow"`
	BackdropFilter  string    `json:"backdropFilter"`
	Filter          string    `json:"filter"`
	MaskImage       string    `json:"maskImage"`
	Position        string    `json:"position"`
	Inset           string    `json:"inset"`
	Width           string    `json:"width"`
	Height          string    `json:"height"`
	Rect            []float64 `json:"rect"`
}

type LayoutDiagnostics struct {
	Main              *SurfaceDiagnostic  `json:"main"`
	MainAncestors     []SurfaceDiagnostic `json:"mainAncestors"`
	MainChildren      []SurfaceDiagnostic `json:"mainChildren"`
	MainSurfaces      []SurfaceDiagnostic `json:"mainSurfaces"`
	Sidebar           *SurfaceDiagnostic  `json:"sidebar"`
	SidebarAncestors  []SurfaceDiagnostic `json:"sidebarAncestors"`
	SidebarCandidates []SurfaceDiagnostic `json:"sidebarCandidates"`
	BoundarySurfaces  []SurfaceDiagnostic `json:"boundarySurfaces"`
	BoundaryPseudos   []PseudoDiagnostic  `json:"boundaryPseudos"`
	Composer          *SurfaceDiagnostic  `json:"composer"`
	ComposerAncestors []SurfaceDiagnostic `json:"composerAncestors"`
	ComposerSurfaces  []SurfaceDiagnostic `json:"composerSurfaces"`
	ProjectCandidates []SurfaceDiagnostic `json:"projectCandidates"`
}

func NewLive(config Config) (*Live, error) {
	if config.Root == "" {
		return nil, engine.ErrConfiguration
	}
	root, err := filepath.Abs(config.Root)
	if err != nil || root != filepath.Clean(config.Root) {
		return nil, engine.ErrConfiguration
	}
	profile := config.Profile
	if config.CurrentProfile && profile != "" {
		return nil, engine.ErrConfiguration
	}
	if !config.CurrentProfile && profile == "" {
		profile = filepath.Join(root, "state", "codex-profile")
	}
	if profile != "" {
		profile, err = filepath.Abs(profile)
		if err != nil {
			return nil, engine.ErrConfiguration
		}
		relative, err := filepath.Rel(root, profile)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
			len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
			return nil, engine.ErrConfiguration
		}
	}
	wait := config.LaunchWait
	if wait <= 0 {
		wait = defaultLaunchWait
	}
	live := &Live{
		root: root, profile: profile, port: config.Port, launchWait: wait,
		currentProfile: config.CurrentProfile, restartApproved: config.RestartApproved,
		sessions: map[string]*liveSession{},
	}
	if config.CurrentProfile {
		home := config.UserHome
		if home == "" {
			home, err = os.UserHomeDir()
		}
		if err != nil || home == "" {
			return nil, engine.ErrConfiguration
		}
		live.appearance, err = appearance.New(
			filepath.Join(home, ".codex", "config.toml"),
			filepath.Join(root, "recovery", "appearance.json"),
			runtime.GOOS,
		)
		if err != nil {
			return nil, engine.ErrConfiguration
		}
	}
	return live, nil
}

func (adapter *Live) OpenVerifiedSession(ctx context.Context) (engine.Session, error) {
	return adapter.openVerifiedSession(ctx, "", false)
}

func (adapter *Live) OpenVerifiedThemeSession(
	ctx context.Context,
	compiled engine.CompiledTheme,
) (engine.Session, error) {
	return adapter.OpenVerifiedThemeTransitionSession(ctx, compiled, nil)
}

func (adapter *Live) OpenVerifiedThemeTransitionSession(
	ctx context.Context,
	compiled engine.CompiledTheme,
	previous *engine.CompiledTheme,
) (engine.Session, error) {
	if compiled.AppearanceMode != "dark" && compiled.AppearanceMode != "light" {
		return engine.Session{}, engine.ErrConfiguration
	}
	// Interrupted-transaction recovery only needs the target native mode before
	// the engine restores its already-durable renderer snapshot. That recovery
	// intentionally passes a mode-only CompiledTheme, so do not require package
	// fields here. A normal apply still reaches this method only after the engine
	// has verified and compiled the full signed package; the optional previous
	// theme below is the only value used for adapter-side early rollback and must
	// remain fully validated.
	if previous != nil {
		if !validTransitionTheme(*previous) {
			return engine.Session{}, engine.ErrConfiguration
		}
		copy := *previous
		ctx = context.WithValue(ctx, transitionRecoveryThemeKey{}, &copy)
	}
	return adapter.openVerifiedSession(ctx, compiled.AppearanceMode, false)
}

func validTransitionTheme(compiled engine.CompiledTheme) bool {
	return sixDigitID.MatchString(compiled.ThemePublicID) &&
		compiled.ThemeVersion != "" &&
		compiled.TemplateVersion >= engine.MinimumTemplateVersion &&
		compiled.TemplateVersion <= engine.TemplateVersion &&
		(compiled.AppearanceMode == "dark" || compiled.AppearanceMode == "light") &&
		compiled.StyleText != "" && validBackgroundDataURL(compiled.BackgroundDataURL)
}

func (adapter *Live) OpenVerifiedOfficialSession(ctx context.Context) (engine.Session, error) {
	return adapter.openVerifiedSession(ctx, "", true)
}

func (adapter *Live) openVerifiedSession(
	ctx context.Context,
	targetAppearance string,
	restoreAppearance bool,
) (engine.Session, error) {
	installation, err := codex.DiscoverInstallation(ctx)
	if err != nil {
		return engine.Session{}, err
	}
	profile := adapter.profile
	port := adapter.port
	launchedPID := 0
	appearanceChanged := false
	appearanceRestart := false
	mutated := false
	var process codex.ProcessIdentity
	if adapter.currentProfile && adapter.appearance != nil {
		switch {
		case restoreAppearance:
			appearanceRestart, err = adapter.appearance.NeedsRestore()
		case targetAppearance != "":
			appearanceRestart, err = adapter.appearance.NeedsPin(targetAppearance)
		default:
			// Compatibility entry points that do not declare a target mode must
			// still consume an old native-appearance backup through a controlled
			// reload. Writing config.toml while reusing an existing renderer is
			// not a valid restore: Codex may keep the old native palette alive.
			appearanceRestart, err = adapter.appearance.NeedsRestore()
			// Restore must also consume an already-matching backup. It does not
			// need a reload in that case, but retaining it would let a later
			// install restore stale pre-theme state.
			restoreAppearance = true
		}
		if err != nil {
			return engine.Session{}, errors.Join(engine.ErrStateUnsafe, err)
		}
	}

	if adapter.currentProfile {
		current, currentErr := codex.DiscoverCurrentInstance(ctx, installation)
		switch {
		case currentErr == nil && current.ControlledPort > 0 && !appearanceRestart:
			profile = current.Profile
			port = current.ControlledPort
			process = current.Process
		case currentErr == nil && !adapter.restartApproved:
			return engine.Session{}, engine.ErrRestartConsent
		case currentErr == nil:
			profile = current.Profile
			if err := codex.StopCurrentInstance(ctx, installation, current); err != nil {
				return engine.Session{}, err
			}
			mutated = true
		case errors.Is(currentErr, codex.ErrCurrentMissing):
			profile, err = codex.DefaultUserProfile(installation)
			if err != nil {
				return engine.Session{}, err
			}
		default:
			return engine.Session{}, currentErr
		}
	} else if err := ensureProfile(profile); err != nil {
		return engine.Session{}, err
	}

	// Capture or consume the native appearance backup before a no-reload reuse.
	// Pin returns true when it created the recovery point even if appearanceTheme
	// was already correct; that durable state is still an operation mutation and
	// must be cleaned up if opening the verified session later fails.
	if adapter.currentProfile && adapter.appearance != nil && !appearanceRestart {
		switch {
		case targetAppearance != "":
			appearanceChanged, err = adapter.appearance.Pin(targetAppearance)
		case restoreAppearance:
			appearanceChanged, err = adapter.appearance.Restore()
		}
		if err != nil {
			return engine.Session{}, adapter.recoverOpenFailure(
				ctx, installation, launchedPID, port, profile, process,
				mutated, errors.Join(engine.ErrStateUnsafe, err),
			)
		}
		mutated = mutated || appearanceChanged
	}

	if process.ProcessID == 0 {
		if adapter.currentProfile {
			// The official app may update its on-disk bundle while the old
			// process is still open. Once that process has been stopped, never
			// launch with the cached pre-stop identity: rediscover the complete
			// signed installation and require it to settle first.
			installation, err = codex.DiscoverStableInstallation(ctx)
			if err != nil {
				return engine.Session{}, adapter.recoverOpenFailure(
					ctx, installation, launchedPID, port, profile, process, mutated, err,
				)
			}
			profile, err = codex.DefaultUserProfile(installation)
			if err != nil {
				return engine.Session{}, adapter.recoverOpenFailure(
					ctx, installation, launchedPID, port, profile, process, mutated, err,
				)
			}
		}
		if adapter.currentProfile && adapter.appearance != nil && appearanceRestart {
			// Pin/Restore persists or consumes the exact pre-theme backup before
			// it returns. Treat that as a mutation even if the visible TOML value
			// happened to be the requested value, so every later launch failure
			// restores the user's original native preference.
			mutated = true
			if restoreAppearance {
				appearanceChanged, err = adapter.appearance.Restore()
			} else {
				appearanceChanged, err = adapter.appearance.Pin(targetAppearance)
			}
			if err != nil {
				return engine.Session{}, adapter.recoverOpenFailure(
					ctx, installation, launchedPID, port, profile, process,
					mutated, errors.Join(engine.ErrStateUnsafe, err),
				)
			}
			mutated = mutated || appearanceChanged
		}
		if port == 0 {
			port, err = reserveLoopbackPort()
			if err != nil {
				return engine.Session{}, adapter.recoverOpenFailure(
					ctx, installation, launchedPID, port, profile, process, mutated, err,
				)
			}
		}
		launchedPID, err = codex.LaunchControlled(ctx, installation, profile, port)
		if err != nil {
			return engine.Session{}, adapter.recoverOpenFailure(
				ctx, installation, launchedPID, port, profile, process, true, err,
			)
		}
		mutated = true
	}
	deadline := time.Now().Add(adapter.launchWait)
	var targets []cdp.Target
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return engine.Session{}, adapter.recoverOpenFailure(
				ctx, installation, launchedPID, port, profile, process, mutated, ctx.Err(),
			)
		}
		if launchedPID > 0 {
			process, err = codex.VerifyListener(ctx, installation, launchedPID, port, profile)
		} else {
			process, err = codex.VerifyListener(
				ctx,
				installation,
				process.ProcessID,
				port,
				profile,
			)
		}
		if err == nil {
			targets, err = cdp.Discover(ctx, port)
			if err == nil {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		err = errors.Join(codex.ErrListenerUntrusted, err)
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	target, err := cdp.SelectPage(targets)
	if err != nil {
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	client, err := cdp.Dial(ctx, target, port)
	if err != nil {
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	if err := client.Call(ctx, "Runtime.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	if err := client.Call(ctx, "Page.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	opaqueID, err := randomSessionID()
	if err != nil {
		client.Close()
		return engine.Session{}, adapter.recoverOpenFailure(
			ctx, installation, launchedPID, port, profile, process, mutated, err,
		)
	}
	adapter.mu.Lock()
	adapter.sessions[opaqueID] = &liveSession{
		client: client, installation: installation, process: process, port: port, profile: profile,
		appearanceMode: targetAppearance, targetID: target.ID,
	}
	adapter.mu.Unlock()
	return engine.Session{
		OpaqueID: opaqueID,
		Identity: engine.Identity{
			Platform: installation.Platform, AppIdentifier: installation.AppIdentifier,
			Publisher: installation.Publisher, Version: installation.Version,
			ExecutableHash: installation.ExecutableSHA256, ProcessID: process.ProcessID,
			ProcessStartID: process.ProcessStartID,
		},
	}, nil
}

// restoreLegacyAppearanceIfNeeded is retained only to consume a backup written
// by the pre-Runtime-v2 native-appearance migration. Current transactions pin
// the native mode deliberately and restore it only through their controlled
// Restore or rollback paths.
func (adapter *Live) restoreLegacyAppearanceIfNeeded() error {
	if !adapter.currentProfile || adapter.appearance == nil {
		return nil
	}
	needed, err := adapter.appearance.NeedsRestore()
	if err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	if !needed {
		return nil
	}
	if _, err := adapter.appearance.Restore(); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	return nil
}

func (adapter *Live) recoverOpenFailure(
	ctx context.Context,
	installation codex.Installation,
	launchedPID int,
	port int,
	profile string,
	process codex.ProcessIdentity,
	mutated bool,
	cause error,
) error {
	if !mutated {
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openRollbackTimeout)
	defer cancel()
	if process.ProcessID == 0 && launchedPID > 0 {
		verified, verifyErr := codex.VerifyListener(
			cleanupCtx, installation, launchedPID, port, profile,
		)
		if verifyErr == nil {
			process = verified
		} else {
			current, currentErr := codex.DiscoverCurrentInstance(cleanupCtx, installation)
			if currentErr == nil && current.Process.ProcessID == launchedPID &&
				current.ControlledPort == port && current.Profile == profile {
				cause = errors.Join(cause, codex.StopCurrentInstance(cleanupCtx, installation, current))
				launchedPID = 0
			} else {
				cause = errors.Join(cause, verifyErr, currentErr)
			}
		}
	}
	if process.ProcessID > 0 {
		cause = errors.Join(cause, codex.StopOwnedProcess(
			cleanupCtx, installation, process, port, profile,
		))
	}
	if previous, ok := ctx.Value(transitionRecoveryThemeKey{}).(*engine.CompiledTheme); ok &&
		previous != nil && adapter.currentProfile && adapter.appearance != nil {
		// The new controlled renderer was never returned to the engine, so the
		// engine cannot invoke its normal snapshot rollback. Recover the exact
		// previously committed skin here before considering an ordinary fallback.
		// This is only reached after the failed target process has been stopped.
		if recoveryErr := adapter.recoverPriorTheme(cleanupCtx, port, *previous); recoveryErr == nil {
			return cause
		} else {
			cause = errors.Join(cause, recoveryErr)
		}
	}
	if adapter.appearance != nil {
		_, restoreErr := adapter.appearance.Restore()
		cause = errors.Join(cause, restoreErr)
	}
	return reopenOrdinaryIfMissing(cleanupCtx, installation, cause)
}

// recoverPriorTheme repairs the narrow gap before openVerifiedSession has
// produced an engine.Session. It deliberately reuses only an already
// revalidated prior package supplied by the engine; it never reads arbitrary
// skin data from the renderer or from a live page.
func (adapter *Live) recoverPriorTheme(
	ctx context.Context,
	port int,
	previous engine.CompiledTheme,
) error {
	if !validTransitionTheme(previous) || adapter.appearance == nil {
		return engine.ErrConfiguration
	}
	installation, err := codex.DiscoverStableInstallation(ctx)
	if err != nil {
		return err
	}
	profile, err := codex.DefaultUserProfile(installation)
	if err != nil {
		return err
	}
	if port == 0 {
		port, err = reserveLoopbackPort()
		if err != nil {
			return err
		}
	}
	if _, err := adapter.appearance.Pin(previous.AppearanceMode); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	launchedPID, err := codex.LaunchControlled(ctx, installation, profile, port)
	if err != nil {
		return err
	}
	process, target, client, err := adapter.connectControlled(
		ctx, installation, launchedPID, port, profile,
	)
	if err != nil {
		return errors.Join(err, adapter.stopRecoveredProcess(ctx, installation, launchedPID, port, profile))
	}
	defer client.Close()
	live := &liveSession{
		client: client, installation: installation, process: process, port: port,
		profile: profile, appearanceMode: previous.AppearanceMode, targetID: target.ID,
	}
	if err := adapter.installController(ctx, live, previous); err != nil {
		return errors.Join(err, codex.StopOwnedProcess(ctx, installation, process, port, profile))
	}
	selectors, err := renderer.SelectorMap()
	if err != nil {
		return errors.Join(engine.ErrConfiguration, codex.StopOwnedProcess(ctx, installation, process, port, profile))
	}
	verification, err := waitForThemeVerificationWithRepair(
		ctx, themeVerifyInitialWait, themeVerifyRepairWait, themeVerifyPoll,
		func(checkCtx context.Context) (engine.RegionReport, error) {
			var report engine.RegionReport
			err := callFunction(
				checkCtx, client, verifyFunction,
				[]any{previous.TemplateVersion, selectors}, &report,
			)
			return report, err
		},
		func(applyCtx context.Context) error {
			return adapter.installController(applyCtx, live, previous)
		},
		previous,
	)
	if err != nil {
		return errors.Join(err, codex.StopOwnedProcess(ctx, installation, process, port, profile))
	}
	if !engine.ReportAllowsTheme(verification.Report, previous) {
		return errors.Join(engine.ErrVerifyFailed, codex.StopOwnedProcess(ctx, installation, process, port, profile))
	}
	copy := previous
	live.current = &copy
	return nil
}

func (adapter *Live) stopRecoveredProcess(
	ctx context.Context,
	installation codex.Installation,
	launchedPID int,
	port int,
	profile string,
) error {
	process, err := codex.VerifyListener(ctx, installation, launchedPID, port, profile)
	if err == nil {
		return codex.StopOwnedProcess(ctx, installation, process, port, profile)
	}
	current, currentErr := codex.DiscoverCurrentInstance(ctx, installation)
	if currentErr == nil && current.Process.ProcessID == launchedPID &&
		current.ControlledPort == port && current.Profile == profile {
		return errors.Join(err, codex.StopCurrentInstance(ctx, installation, current))
	}
	return errors.Join(err, currentErr)
}

func reopenOrdinaryIfMissing(
	ctx context.Context,
	installation codex.Installation,
	cause error,
) error {
	return reopenOrdinaryIfMissingWith(ctx, cause, codexRecoveryOperations{
		discoverStableInstallation: codex.DiscoverStableInstallation,
		discoverCurrentInstance:    codex.DiscoverCurrentInstance,
		launchOrdinary:             codex.LaunchOrdinary,
		waitForCurrentInstance:     codex.WaitForCurrentInstance,
	})
}

type codexRecoveryOperations struct {
	discoverStableInstallation func(context.Context) (codex.Installation, error)
	discoverCurrentInstance    func(context.Context, codex.Installation) (codex.CurrentInstance, error)
	launchOrdinary             func(context.Context, codex.Installation) error
	waitForCurrentInstance     func(context.Context, codex.Installation) (codex.CurrentInstance, error)
}

func reopenOrdinaryIfMissingWith(
	ctx context.Context,
	cause error,
	operations codexRecoveryOperations,
) error {
	_, err := ensureOrdinaryInstanceWith(ctx, operations)
	return errors.Join(cause, err)
}

// ensureOrdinaryInstanceWith proves the postcondition used by a recovery:
// an ordinary, non-CDP Codex process exists and stays stable. It deliberately
// returns the observed instance so final rollback can distinguish a harmless
// controlled-process shutdown race from a failed return to the official app.
func ensureOrdinaryInstanceWith(
	ctx context.Context,
	operations codexRecoveryOperations,
) (codex.CurrentInstance, error) {
	if ctx == nil ||
		operations.discoverStableInstallation == nil ||
		operations.discoverCurrentInstance == nil ||
		operations.launchOrdinary == nil ||
		operations.waitForCurrentInstance == nil {
		return codex.CurrentInstance{}, codex.ErrIdentityUntrusted
	}
	// Always reacquire the official installation here. The caller's identity
	// may be the exact reason the controlled launch failed (for example, an
	// in-place Codex update between user consent and relaunch).
	fresh, err := operations.discoverStableInstallation(ctx)
	if err != nil {
		return codex.CurrentInstance{}, err
	}
	current, currentErr := operations.discoverCurrentInstance(ctx, fresh)
	switch {
	case currentErr == nil:
		if current.ControlledPort != 0 {
			return codex.CurrentInstance{}, codex.ErrCurrentUnsafe
		}
		return current, nil
	case !errors.Is(currentErr, codex.ErrCurrentMissing):
		return codex.CurrentInstance{}, currentErr
	}
	if err := operations.launchOrdinary(ctx, fresh); err != nil {
		return codex.CurrentInstance{}, err
	}
	current, err = operations.waitForCurrentInstance(ctx, fresh)
	if err != nil {
		return codex.CurrentInstance{}, err
	}
	if current.ControlledPort != 0 {
		return codex.CurrentInstance{}, codex.ErrCurrentUnsafe
	}
	return current, nil
}

func (adapter *Live) Probe(ctx context.Context, session engine.Session) (engine.RegionReport, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.RegionReport{}, err
	}
	selectors, err := renderer.SelectorMap()
	if err != nil {
		return engine.RegionReport{}, engine.ErrConfiguration
	}
	var report engine.RegionReport
	if err := callFunction(ctx, live.client, probeFunction, []any{selectors}, &report); err != nil {
		return engine.RegionReport{}, err
	}
	return report, nil
}

func (adapter *Live) WaitForCapabilities(ctx context.Context, session engine.Session) (engine.RegionReport, error) {
	deadline := time.Now().Add(adapter.launchWait)
	var last engine.RegionReport
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = adapter.Probe(ctx, session)
		if lastErr == nil && engine.CapabilitiesAllowApply(last) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return engine.RegionReport{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return engine.RegionReport{}, lastErr
	}
	return last, engine.ErrCapabilityBlocked
}

func (adapter *Live) Capture(ctx context.Context, session engine.Session) (engine.Snapshot, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.Snapshot{}, err
	}
	var snapshot engine.Snapshot
	if err := callFunction(ctx, live.client, captureFunction, nil, &snapshot); err != nil {
		return engine.Snapshot{}, err
	}
	if snapshot.StylePresent &&
		(live.current == nil ||
			!sixDigitID.MatchString(snapshot.ThemePublicID) ||
			snapshot.TemplateVersion != live.current.TemplateVersion ||
			snapshot.ThemeVersion == "" ||
			snapshot.ThemePublicID != live.current.ThemePublicID ||
			snapshot.ThemeVersion != live.current.ThemeVersion ||
			snapshot.StyleText != live.current.StyleText) {
		return engine.Snapshot{}, engine.ErrCapabilityBlocked
	}
	if snapshot.StylePresent {
		snapshot.StyleText = live.current.StyleText
		snapshot.BackgroundDataURL = live.current.BackgroundDataURL
		snapshot.AppearanceMode = live.current.AppearanceMode
	}
	return snapshot, nil
}

func (adapter *Live) Prime(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) error {
	if !sixDigitID.MatchString(compiled.ThemePublicID) ||
		compiled.ThemeVersion == "" ||
		compiled.TemplateVersion != engine.TemplateVersion ||
		(compiled.AppearanceMode != "dark" && compiled.AppearanceMode != "light") ||
		compiled.StyleText == "" ||
		!validBackgroundDataURL(compiled.BackgroundDataURL) {
		return engine.ErrConfiguration
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var snapshot engine.Snapshot
	if err := callFunction(ctx, live.client, captureFunction, nil, &snapshot); err != nil {
		return err
	}
	if !snapshot.StylePresent {
		if err := adapter.installController(ctx, live, compiled); err != nil {
			return err
		}
		copy := compiled
		live.current = &copy
		return nil
	}
	primed, err := selectPrimedTheme(snapshot, compiled)
	if err != nil {
		return err
	}
	live.current = primed
	return adapter.installController(ctx, live, *primed)
}

func selectPrimedTheme(
	snapshot engine.Snapshot,
	compiled engine.CompiledTheme,
) (*engine.CompiledTheme, error) {
	if snapshot.ThemePublicID != compiled.ThemePublicID ||
		snapshot.ThemeVersion != compiled.ThemeVersion ||
		snapshot.AppearanceMode != compiled.AppearanceMode {
		return nil, engine.ErrCapabilityBlocked
	}
	copy := compiled
	switch {
	case snapshot.TemplateVersion == compiled.TemplateVersion &&
		snapshot.StyleText == compiled.StyleText:
	case snapshot.TemplateVersion == engine.TemplateVersion-1 &&
		compiled.PreviousStyleText != "" &&
		snapshot.StyleText == compiled.PreviousStyleText:
		copy.TemplateVersion = engine.TemplateVersion - 1
		copy.StyleText = compiled.PreviousStyleText
		copy.PreviousStyleText = ""
		copy.MigrationStyleText = ""
		copy.LegacyStyleText = ""
	case snapshot.TemplateVersion == engine.TemplateVersion-2 &&
		compiled.MigrationStyleText != "" &&
		snapshot.StyleText == compiled.MigrationStyleText:
		copy.TemplateVersion = engine.TemplateVersion - 2
		copy.StyleText = compiled.MigrationStyleText
		copy.PreviousStyleText = ""
		copy.MigrationStyleText = ""
		copy.LegacyStyleText = ""
	case snapshot.TemplateVersion == engine.MinimumTemplateVersion &&
		compiled.LegacyStyleText != "" &&
		snapshot.StyleText == compiled.LegacyStyleText:
		copy.TemplateVersion = engine.MinimumTemplateVersion
		copy.StyleText = compiled.LegacyStyleText
		copy.PreviousStyleText = ""
		copy.MigrationStyleText = ""
		copy.LegacyStyleText = ""
	default:
		return nil, engine.ErrCapabilityBlocked
	}
	return &copy, nil
}

func (adapter *Live) Apply(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) error {
	if !sixDigitID.MatchString(compiled.ThemePublicID) ||
		compiled.ThemeVersion == "" ||
		compiled.TemplateVersion != engine.TemplateVersion ||
		(compiled.AppearanceMode != "dark" && compiled.AppearanceMode != "light") ||
		compiled.StyleText == "" ||
		!validBackgroundDataURL(compiled.BackgroundDataURL) {
		return engine.ErrConfiguration
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	if err := adapter.installController(ctx, live, compiled); err != nil {
		return err
	}
	copy := compiled
	live.current = &copy
	return nil
}

func (adapter *Live) Verify(ctx context.Context, session engine.Session, compiled engine.CompiledTheme) (engine.RegionReport, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return engine.RegionReport{}, err
	}
	selectors, err := renderer.SelectorMap()
	if err != nil {
		return engine.RegionReport{}, engine.ErrConfiguration
	}
	var report engine.RegionReport
	if err := callFunction(
		ctx,
		live.client,
		verifyFunction,
		[]any{compiled.TemplateVersion, selectors},
		&report,
	); err != nil {
		return engine.RegionReport{}, err
	}
	return report, nil
}

// WaitForThemeVerification follows the same fail-closed contract as Verify,
// but lets a newly started renderer settle before it is judged. A first
// bounded wait is followed by one idempotent controller replacement and a
// second bounded wait. It never treats a timeout as success and never retries
// an untrusted session or a malformed theme.
func (adapter *Live) WaitForThemeVerification(
	ctx context.Context,
	session engine.Session,
	compiled engine.CompiledTheme,
) (engine.ThemeVerificationResult, error) {
	return waitForThemeVerificationWithRepair(
		ctx, themeVerifyInitialWait, themeVerifyRepairWait, themeVerifyPoll,
		func(checkCtx context.Context) (engine.RegionReport, error) {
			return adapter.Verify(checkCtx, session, compiled)
		},
		func(applyCtx context.Context) error { return adapter.Apply(applyCtx, session, compiled) },
		compiled,
	)
}

func waitForThemeVerificationWithRepair(
	ctx context.Context,
	initialWait time.Duration,
	repairWait time.Duration,
	poll time.Duration,
	verify func(context.Context) (engine.RegionReport, error),
	reapply func(context.Context) error,
	compiled engine.CompiledTheme,
) (engine.ThemeVerificationResult, error) {
	initial, initialErr := waitForThemeVerificationPass(ctx, initialWait, poll, verify, compiled)
	if initialErr == nil {
		return initial, nil
	}
	if !retryableThemeVerificationError(initialErr) || reapply == nil {
		return initial, initialErr
	}

	initial.ReapplyAttempted = true
	if err := reapply(ctx); err != nil {
		return initial, err
	}
	repaired, repairedErr := waitForThemeVerificationPass(ctx, repairWait, poll, verify, compiled)
	repaired.Attempts += initial.Attempts
	repaired.ReapplyAttempted = true
	if !repaired.ProbeCompleted && initial.ProbeCompleted {
		repaired.Report = initial.Report
	}
	return repaired, repairedErr
}

func retryableThemeVerificationError(err error) bool {
	return err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, engine.ErrConfiguration) &&
		!errors.Is(err, codex.ErrListenerUntrusted)
}

func waitForThemeVerificationPass(
	ctx context.Context,
	wait time.Duration,
	poll time.Duration,
	verify func(context.Context) (engine.RegionReport, error),
	compiled engine.CompiledTheme,
) (engine.ThemeVerificationResult, error) {
	if wait <= 0 || poll <= 0 || verify == nil {
		return engine.ThemeVerificationResult{}, engine.ErrConfiguration
	}
	deadline := time.Now().Add(wait)
	result := engine.ThemeVerificationResult{}
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		report, err := verify(ctx)
		result.Attempts++
		if err == nil {
			result.Report = report
			result.ProbeCompleted = true
			if engine.ReportAllowsTheme(report, compiled) {
				return result, nil
			}
			lastErr = engine.ErrVerifyFailed
		} else {
			result.ProbeCompleted = false
			lastErr = err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if remaining > poll {
			remaining = poll
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = engine.ErrVerifyFailed
	}
	return result, lastErr
}

func (adapter *Live) Restore(ctx context.Context, session engine.Session, snapshot engine.Snapshot) error {
	if !snapshot.StylePresent {
		return adapter.RestoreOfficial(ctx, session)
	}
	if !sixDigitID.MatchString(snapshot.ThemePublicID) ||
		snapshot.ThemeVersion == "" ||
		snapshot.TemplateVersion < engine.MinimumTemplateVersion ||
		snapshot.TemplateVersion > engine.TemplateVersion ||
		(snapshot.AppearanceMode != "dark" && snapshot.AppearanceMode != "light") ||
		snapshot.StyleText == "" ||
		!validBackgroundDataURL(snapshot.BackgroundDataURL) {
		return engine.ErrRollbackFailed
	}
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	// A failed cross-mode switch must return to both the prior skin and its
	// matching native Codex palette. Reinstalling a dark skin into a light
	// native renderer (or the reverse) recreates the unreadable white-card
	// failure that the transaction is intended to prevent.
	if adapter.currentProfile && adapter.appearance != nil &&
		live.appearanceMode != snapshot.AppearanceMode {
		if err := adapter.restartSessionAppearance(ctx, live, snapshot.AppearanceMode); err != nil {
			return errors.Join(engine.ErrRollbackFailed, err)
		}
	}
	restoredTheme := engine.CompiledTheme{
		ThemePublicID: snapshot.ThemePublicID, ThemeVersion: snapshot.ThemeVersion,
		TemplateVersion: snapshot.TemplateVersion, StyleText: snapshot.StyleText,
		BackgroundDataURL: snapshot.BackgroundDataURL, AppearanceMode: snapshot.AppearanceMode,
	}
	if err := adapter.installController(ctx, live, restoredTheme); err != nil {
		return errors.Join(engine.ErrRollbackFailed, err)
	}
	live.current = &restoredTheme
	return nil
}

// restartSessionAppearance changes only the controlled renderer's native
// light/dark setting. It is used exclusively for a rollback to a prior skin
// whose appearance mode differs from the failed target. Any failed restart is
// fail-closed: recoverOrdinaryAppearance restores the user's exact pre-skin
// choice and reopens only an ordinary, non-CDP Codex process.
func (adapter *Live) restartSessionAppearance(
	ctx context.Context,
	live *liveSession,
	mode string,
) error {
	if mode != "dark" && mode != "light" {
		return engine.ErrConfiguration
	}
	if !adapter.restartApproved || adapter.appearance == nil {
		return engine.ErrRestartConsent
	}
	if err := adapter.removeControllerBootstrap(ctx, live); err != nil {
		return err
	}
	var cleaned bool
	_ = callFunction(ctx, live.client, restoreFunction, nil, &cleaned)
	if err := codex.StopOwnedProcess(
		ctx, live.installation, live.process, live.port, live.profile,
	); err != nil {
		return err
	}
	_ = live.client.Close()
	live.client = nil
	live.current = nil

	if _, err := adapter.appearance.Pin(mode); err != nil {
		return adapter.recoverOrdinaryAppearance(ctx, live.installation, err)
	}
	launchedPID, err := codex.LaunchControlled(ctx, live.installation, live.profile, live.port)
	if err != nil {
		return adapter.recoverOrdinaryAppearance(ctx, live.installation, err)
	}
	process, target, client, err := adapter.connectControlled(
		ctx, live.installation, launchedPID, live.port, live.profile,
	)
	if err != nil {
		return adapter.recoverFailedControlled(
			ctx, live.installation, launchedPID, live.port, live.profile, err,
		)
	}
	if err := adapter.clearControllerRecord(); err != nil {
		_ = client.Close()
		_ = codex.StopOwnedProcess(ctx, live.installation, process, live.port, live.profile)
		return adapter.recoverOrdinaryAppearance(ctx, live.installation, err)
	}
	live.process = process
	live.targetID = target.ID
	live.client = client
	live.appearanceMode = mode
	return nil
}

func (adapter *Live) connectControlled(
	ctx context.Context,
	installation codex.Installation,
	launchedPID int,
	port int,
	profile string,
) (codex.ProcessIdentity, cdp.Target, *cdp.Client, error) {
	deadline := time.Now().Add(adapter.launchWait)
	var process codex.ProcessIdentity
	var targets []cdp.Target
	var err error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return codex.ProcessIdentity{}, cdp.Target{}, nil, ctx.Err()
		}
		process, err = codex.VerifyListener(ctx, installation, launchedPID, port, profile)
		if err == nil {
			targets, err = cdp.Discover(ctx, port)
			if err == nil {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err == nil && process.ProcessID == 0 {
		err = context.DeadlineExceeded
	}
	if err != nil {
		return codex.ProcessIdentity{}, cdp.Target{}, nil, errors.Join(codex.ErrListenerUntrusted, err)
	}
	target, err := cdp.SelectPage(targets)
	if err != nil {
		return codex.ProcessIdentity{}, cdp.Target{}, nil, err
	}
	client, err := cdp.Dial(ctx, target, port)
	if err != nil {
		return codex.ProcessIdentity{}, cdp.Target{}, nil, err
	}
	if err := client.Call(ctx, "Runtime.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return codex.ProcessIdentity{}, cdp.Target{}, nil, err
	}
	if err := client.Call(ctx, "Page.enable", map[string]any{}, nil); err != nil {
		client.Close()
		return codex.ProcessIdentity{}, cdp.Target{}, nil, err
	}
	return process, target, client, nil
}

func (adapter *Live) recoverOrdinaryAppearance(
	ctx context.Context,
	installation codex.Installation,
	cause error,
) error {
	if adapter.appearance != nil {
		_, restoreErr := adapter.appearance.Restore()
		cause = errors.Join(cause, restoreErr)
	}
	return reopenOrdinaryIfMissing(ctx, installation, cause)
}

func (adapter *Live) recoverFailedControlled(
	ctx context.Context,
	installation codex.Installation,
	launchedPID int,
	port int,
	profile string,
	cause error,
) error {
	if process, err := codex.VerifyListener(ctx, installation, launchedPID, port, profile); err == nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		cause = errors.Join(cause, codex.StopOwnedProcess(
			stopCtx, installation, process, port, profile,
		))
		cancel()
	}
	return adapter.recoverOrdinaryAppearance(ctx, installation, cause)
}

func (adapter *Live) RestoreOfficial(ctx context.Context, session engine.Session) error {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	if err := adapter.removeControllerBootstrap(ctx, live); err != nil {
		return err
	}
	var restored bool
	if err := callFunction(ctx, live.client, restoreFunction, nil, &restored); err != nil {
		return err
	}
	if !restored {
		return engine.ErrRestoreFailed
	}
	live.current = nil
	return nil
}

func (adapter *Live) VerifyOfficial(ctx context.Context, session engine.Session) error {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return err
	}
	var official bool
	if err := callFunction(ctx, live.client, officialFunction, nil, &official); err != nil {
		return err
	}
	if !official {
		return engine.ErrRestoreFailed
	}
	return nil
}

func (adapter *Live) CapturePNG(ctx context.Context, session engine.Session) ([]byte, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := live.client.Call(ctx, "Page.captureScreenshot", map[string]any{
		"format": "png", "fromSurface": true, "captureBeyondViewport": false,
	}, &response); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.Strict().DecodeString(response.Data)
	if err != nil || len(content) < 8 || len(content) > 25*1024*1024 {
		return nil, engine.ErrVerifyFailed
	}
	return content, nil
}

// FixedLayoutDiagnostics returns style and geometry only for the fixed theme
// surfaces. It deliberately excludes text, attributes, URLs, and user content.
func (adapter *Live) FixedLayoutDiagnostics(ctx context.Context, session engine.Session) (LayoutDiagnostics, error) {
	live, err := adapter.verifiedLiveSession(ctx, session)
	if err != nil {
		return LayoutDiagnostics{}, err
	}
	var diagnostics LayoutDiagnostics
	if err := callFunction(ctx, live.client, fixedLayoutDiagnosticsFunction, nil, &diagnostics); err != nil {
		return LayoutDiagnostics{}, err
	}
	return diagnostics, nil
}

func (adapter *Live) Close(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	delete(adapter.sessions, session.OpaqueID)
	adapter.mu.Unlock()
	if live == nil {
		return nil
	}
	return live.client.Close()
}

// FinalizeOfficialRollback completes a failed first-theme transaction. The
// renderer has already been verified official by the engine; this method then
// stops only the exact controlled process, consumes any legacy native-
// appearance backup left by an earlier Alpha, and reopens Codex without the
// loopback debugging launch flags.
func (adapter *Live) FinalizeOfficialRollback(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	delete(adapter.sessions, session.OpaqueID)
	adapter.mu.Unlock()
	if live == nil {
		return codex.ErrListenerUntrusted
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openRollbackTimeout)
	defer cancel()
	closeErr := live.client.Close()
	stopErr := codex.StopOwnedProcess(
		cleanupCtx, live.installation, live.process, live.port, live.profile,
	)
	var appearanceErr error
	if adapter.appearance != nil {
		_, appearanceErr = adapter.appearance.Restore()
	}
	// A controlled renderer can legitimately disappear while the restore code
	// is closing its CDP client. Do not call that a rollback failure when the
	// native appearance restore succeeded and a stable ordinary Codex process
	// is positively observed afterwards. Other recovery paths still retain and
	// report their original causes through reopenOrdinaryIfMissing.
	_, ordinaryErr := ensureOrdinaryInstanceWith(cleanupCtx, codexRecoveryOperations{
		discoverStableInstallation: codex.DiscoverStableInstallation,
		discoverCurrentInstance:    codex.DiscoverCurrentInstance,
		launchOrdinary:             codex.LaunchOrdinary,
		waitForCurrentInstance:     codex.WaitForCurrentInstance,
	})
	if appearanceErr != nil || ordinaryErr != nil {
		return errors.Join(closeErr, stopErr, appearanceErr, ordinaryErr)
	}
	return nil
}

// StopOwned closes and terminates only the exact controlled process created for
// this session. It is intended for bounded platform QA and never falls back to a
// process-name match.
func (adapter *Live) StopOwned(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	delete(adapter.sessions, session.OpaqueID)
	adapter.mu.Unlock()
	if live == nil {
		return codex.ErrListenerUntrusted
	}
	stopErr := codex.StopOwnedProcess(
		ctx,
		live.installation,
		live.process,
		live.port,
		live.profile,
	)
	closeErr := live.client.Close()
	return errors.Join(closeErr, stopErr)
}

func (adapter *Live) verifiedLiveSession(ctx context.Context, session engine.Session) (*liveSession, error) {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	adapter.mu.Unlock()
	if live == nil {
		return nil, codex.ErrListenerUntrusted
	}
	var process codex.ProcessIdentity
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		process, err = codex.VerifyListener(
			ctx, live.installation, live.process.ProcessID, live.port, live.profile,
		)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil ||
		process.ProcessID != live.process.ProcessID ||
		process.ProcessStartID != live.process.ProcessStartID ||
		process.ExecutableSHA256 != live.process.ExecutableSHA256 {
		return nil, errors.Join(codex.ErrListenerUntrusted, err)
	}
	return live, nil
}

func callFunction(ctx context.Context, client *cdp.Client, function string, arguments []any, output any) error {
	var global evaluateResult
	if err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": "globalThis", "returnByValue": false,
	}, &global); err != nil || global.ExceptionDetails != nil || global.Result.ObjectID == "" {
		return engine.ErrVerifyFailed
	}
	callArguments := make([]map[string]any, 0, len(arguments))
	for _, value := range arguments {
		callArguments = append(callArguments, map[string]any{"value": value})
	}
	var result callResult
	if err := client.Call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": global.Result.ObjectID, "functionDeclaration": function,
		"arguments": callArguments, "returnByValue": true, "awaitPromise": false,
	}, &result); err != nil || result.ExceptionDetails != nil {
		return engine.ErrVerifyFailed
	}
	if len(result.Result.Value) == 0 {
		return engine.ErrVerifyFailed
	}
	if err := jsonUnmarshal(result.Result.Value, output); err != nil {
		return engine.ErrVerifyFailed
	}
	return nil
}

func jsonUnmarshal(raw []byte, output any) error {
	return json.Unmarshal(raw, output)
}

func ensureProfile(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return engine.ErrStateUnsafe
		}
		return os.Chmod(path, 0o700)
	} else if !errors.Is(err, os.ErrNotExist) {
		return engine.ErrStateUnsafe
	}
	return os.MkdirAll(path, 0o700)
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("%w: reserve loopback port", codex.ErrLaunchFailed)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(raw), nil
}

func validBackgroundDataURL(value string) bool {
	for _, prefix := range []string{
		"data:image/png;base64,",
		"data:image/jpeg;base64,",
		"data:image/webp;base64,",
	} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		encoded := strings.TrimPrefix(value, prefix)
		if len(encoded) < 4 || len(encoded) > 20*1024*1024 {
			return false
		}
		_, err := base64.StdEncoding.Strict().DecodeString(encoded)
		return err == nil
	}
	return false
}

const probeFunction = `function (selectors) {
  const status = (node, optional) => node ? "pass" : (optional ? "not_present" : "fail");
  const query = (key) => typeof selectors?.[key] === "string"
    ? document.querySelector(selectors[key]) : null;
  const style = document.querySelectorAll("#codex-skin-theme-v1");
  const root = document.documentElement;
  const suggestions = query("home-suggestions");
  const topFade = query("main-content-top-fade");
  const main = query("shell-main");
  const sidebar = query("left-panel");
  const header = query("header-tint");
  const composer = query("composer-chrome");
  const home = query("home-icon") || query("home-route");
  const thread = query("thread-surface");
  const settings = query("settings-panel") || query("appearance-radio");
  // The legacy thread container is an optional L2 probe. A verified normal
  // shell that is neither Home nor Settings is a Codex task/conversation route.
  const scope = settings ? "settings" : home ? "home" : "thread";
  const activityHeader = document.querySelector(
    ".thread-scroll-container button.group\\/activity-header"
  );
	const diffResource = document.querySelector(
		'.thread-scroll-container ' +
		'[class~="[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"]'
	);
  const composerUtilityBar = main?.querySelector('[class*="_homeUtilityBar_"]') || null;
  const project = main?.querySelector('button[class*="_utilityBarLabel_"]') ||
    main?.querySelector('div.sticky:has(input[type="text"],textarea)') ||
    document.querySelector('[data-testid*="project" i]');
  return {
    scope,
    runtimeVersion: Number(root.getAttribute("data-codex-skin-runtime") || 0),
    styleMarkerCount: style.length,
    templateVersion: Number(root.getAttribute("data-codex-skin-template") || 0),
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    backgroundLoaded: false,
    regions: {
      home: status(main, false),
      shellMain: status(main, false),
      mainBoundary: status(main, false),
      sidebar: status(sidebar, false),
      headerTint: status(header, false),
      composerUtilityBar: status(composerUtilityBar, true),
      topFade: status(topFade, true),
      bottomFade: "not_present",
      templateScope: status(main, false),
      themeContrast: "pass",
      conversationActivity: status(activityHeader, true),
		conversationDiffResource: status(diffResource, true),
      suggestionCards: status(suggestions, true),
      projectPicker: status(project, true),
      composer: status(composer, true)
    }
  };
}`

const captureFunction = `function () {
  const styles = document.querySelectorAll("#codex-skin-theme-v1");
  if (styles.length > 1) throw new Error("invalid marker count");
  const root = document.documentElement;
  const style = styles[0] || null;
  const state = globalThis["__CODEX_SKIN_RENDERER_CONTROLLER_V2__"];
  return {
	stylePresent: Boolean(style && state),
	styleText: state?.styleText || "",
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    themeVersion: root.getAttribute("data-codex-skin-theme-version") || "",
	templateVersion: Number(state?.templateVersion || root.getAttribute("data-codex-skin-template") || 0),
	appearanceMode: state?.appearanceMode || root.getAttribute("data-codex-skin-appearance") || ""
  };
}`

const verifyFunction = `function (expectedTemplateVersion, selectors) {
  const visible = (node) => {
    if (!node) return false;
    const box = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return box.width > 1 && box.height > 1 && style.display !== "none" && style.visibility !== "hidden";
  };
  const optional = (node, pass) => !node ? "not_present" : (pass ? "pass" : "fail");
  const color = (value, surface) => {
    if (!value || !/^#[0-9A-F]{6}(?:[0-9A-F]{2})?$/.test(value)) return null;
    const channel = (start) => Number.parseInt(value.slice(start, start + 2), 16);
    const alpha = value.length === 9 ? channel(7) / 255 : 1;
    return [
      channel(1) * alpha + surface[0] * (1 - alpha),
      channel(3) * alpha + surface[1] * (1 - alpha),
      channel(5) * alpha + surface[2] * (1 - alpha)
    ];
  };
  const luminance = (rgb) => {
    const channel = (value) => {
      value /= 255;
      return value <= 0.04045 ? value / 12.92 : Math.pow((value + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * channel(rgb[0]) + 0.7152 * channel(rgb[1]) + 0.0722 * channel(rgb[2]);
  };
  const contrast = (left, right) => {
    const high = Math.max(luminance(left), luminance(right));
    const low = Math.min(luminance(left), luminance(right));
    return (high + 0.05) / (low + 0.05);
  };
	const parsedComputedColor = (value) => {
		const serialized = String(value || "").trim();
		const rgb = serialized.match(
			/rgba?\(\s*([0-9.]+)[,\s]+([0-9.]+)[,\s]+([0-9.]+)(?:\s*[,/]\s*([0-9.]+)%?)?/
		);
		if (rgb) {
			const alpha = rgb[4] == null
				? 1
				: Math.max(0, Math.min(1, Number(rgb[4]) / (serialized.includes("%") ? 100 : 1)));
			return { rgb: [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])], alpha };
		}
		const oklab = serialized.match(
			/^oklab\(\s*([+-]?[0-9.]+)(%)?\s+([+-]?[0-9.]+)(%)?\s+([+-]?[0-9.]+)(%)?(?:\s*\/\s*([0-9.]+)(%)?)?\s*\)$/
		);
		if (!oklab) return null;
		const lightness = Number(oklab[1]) / (oklab[2] ? 100 : 1);
		const a = Number(oklab[3]) * (oklab[4] ? 0.004 : 1);
		const b = Number(oklab[5]) * (oklab[6] ? 0.004 : 1);
		const l = Math.pow(lightness + 0.3963377774 * a + 0.2158037573 * b, 3);
		const m = Math.pow(lightness - 0.1055613458 * a - 0.0638541728 * b, 3);
		const s = Math.pow(lightness - 0.0894841775 * a - 1.291485548 * b, 3);
		const linear = [
			4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
			-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
			-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s
		];
		const gamma = (channel) => {
			const value = channel <= 0.0031308
				? 12.92 * channel
				: 1.055 * Math.pow(channel, 1 / 2.4) - 0.055;
			return Math.max(0, Math.min(255, value * 255));
		};
		const alpha = oklab[7] == null
			? 1
			: Math.max(0, Math.min(1, Number(oklab[7]) / (oklab[8] ? 100 : 1)));
		return { rgb: linear.map(gamma), alpha };
	};
	const computedColor = (value) => parsedComputedColor(value)?.rgb || null;
	const effectiveBackground = (node, fallback) => {
		const layers = [];
		let base = fallback;
		for (let current = node; current instanceof Element; current = current.parentElement) {
			const computed = getComputedStyle(current);
			const layer = parsedComputedColor(computed.backgroundColor);
			if (layer && layer.alpha > 0) {
				layers.push(layer);
				if (layer.alpha >= 0.995) {
					base = layer.rgb;
					break;
				}
			}
			if (computed.backgroundImage && computed.backgroundImage !== "none") {
				base = fallback;
				break;
			}
		}
		return layers.reverse().reduce((under, layer) => [
			layer.rgb[0] * layer.alpha + under[0] * (1 - layer.alpha),
			layer.rgb[1] * layer.alpha + under[1] * (1 - layer.alpha),
			layer.rgb[2] * layer.alpha + under[2] * (1 - layer.alpha)
		], base);
	};
	  const root = document.documentElement;
	  const query = (key) => typeof selectors?.[key] === "string"
	    ? document.querySelector(selectors[key]) : null;
	  const inMain = (key) => {
	    if (!main || typeof selectors?.[key] !== "string") return null;
	    try {
	      return main.matches(selectors[key]) ? main : main.querySelector(selectors[key]);
	    } catch {
	      return null;
	    }
	  };
	  const styles = document.querySelectorAll("#codex-skin-theme-v1");
	  const style = styles[0] || null;
	  const shellMain = query("shell-main");
	  const main = shellMain?.getAttribute("data-codex-skin-main") === "true" ? shellMain : null;
	  const sidebar = query("left-panel");
	  const header = query("header-tint");
	  const composer = document.querySelector('[data-codex-skin-composer="true"]') ||
	    query("composer-chrome");
	  const suggestions = query("home-suggestions");
	  const home = query("home-icon") || query("home-route");
	  const thread = query("thread-surface");
	  const settings = query("settings-panel") || query("appearance-radio");
	  const injectedScope = main?.getAttribute("data-codex-skin-scope") || "";
	  const scope = settings ? "settings" : injectedScope || (home ? "home" : thread ? "thread" : "shell");
	const activityHeaders = [...document.querySelectorAll(
		'.thread-scroll-container :is(button.group\\/activity-header, ' +
		'button[class~="group/activity-header"])'
	)].filter(visible);
	const diffResourceSelector = '.thread-scroll-container ' +
		'[class~="[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"]';
	const diffResourceCards = [...document.querySelectorAll(diffResourceSelector)].filter(visible);
	const diffResourceControls = diffResourceCards.flatMap((card) =>
		[...card.querySelectorAll(
			':is(button, a, [role="button"])[class~="text-token-text-primary"], ' +
			'[class~="text-token-text-secondary"], [class~="text-token-text-tertiary"]'
		)].filter(visible)
	);
  const legacyTopFade = document.querySelector(".app-shell-main-content-top-fade");
	  const topFades = typeof selectors?.["main-content-top-fade"] === "string"
	    ? [...document.querySelectorAll(selectors["main-content-top-fade"])] : [];
	  const composerUtilityBar = query("home-utility");
  const cards = suggestions ? [...suggestions.querySelectorAll("button")].filter(visible) : [];
  const project = main?.querySelector('button[class*="_utilityBarLabel_"]') ||
    main?.querySelector('div.sticky:has(input[type="text"],textarea)') ||
    document.querySelector('[data-testid*="project" i]');
  const background = getComputedStyle(document.body).backgroundImage || "";
  const rootBackground = getComputedStyle(root).getPropertyValue("--cs-background-image") || "";
  const routeScope = main?.getAttribute("data-codex-skin-scope") || "";
	  const nativeUtilitySignals = Boolean(main && inMain("native-utility-route"));
	  const homeSignals = Boolean(main && !nativeUtilitySignals && (
	    inMain("home-route") || inMain("home-icon") || inMain("home-suggestions") ||
	    (inMain("home-title") && composer && main.contains(composer))
	  ));
	  const threadSignals = Boolean(main && (
	    inMain("thread-surface") || inMain("message") || inMain("markdown")
	  ));
	  const workspaceSignals = (routeScope === "home" && homeSignals) ||
	    (routeScope === "thread" && threadSignals);
  const scopedMain = (routeScope === "home" || routeScope === "thread") && workspaceSignals;
  const scopeContractSafe = Boolean(style &&
    style.textContent.includes(expectedTemplateVersion < 9
      ? "--cs-scope-contract: 8"
      : expectedTemplateVersion < 11
        ? "--cs-scope-contract: 9"
        : expectedTemplateVersion < 12
          ? "--cs-scope-contract: 11"
          : "--cs-scope-contract: 12") &&
    style.textContent.includes('data-codex-skin-scope="home"') &&
    style.textContent.includes('data-codex-skin-scope="thread"'));
  const workspaceContractSafe = expectedTemplateVersion < 10 || Boolean(style &&
    (expectedTemplateVersion < 11
      ? style.textContent.includes("--cs-workspace-contract: 10") &&
        style.textContent.includes("--color-background-primary") &&
        style.textContent.includes("--color-token-main-surface-primary")
      : expectedTemplateVersion < 12
        ? style.textContent.includes("--cs-workspace-contract: 11") &&
        style.textContent.includes("Scoped workspace contract v11") &&
        style.textContent.includes("App token names are never reassigned here")
	        : style.textContent.includes("--cs-workspace-contract: 12") &&
	          style.textContent.includes("Focused conversation contract v12") &&
	          style.textContent.includes("data-codex-skin-composer") &&
	          style.textContent.includes('class~="from-surface"') &&
	          !style.textContent.includes(':root[data-codex-skin="active"] body {')) &&
    (expectedTemplateVersion < 12
      ? style.textContent.includes("_ComposerLayoutRoot_")
      : style.textContent.includes("data-codex-skin-composer-boundary")) &&
    style.textContent.includes("_MainContentBottomFade_"));
  const topFadeContractSafe = expectedTemplateVersion < 6 || (expectedTemplateVersion < 8
    ? Boolean(style && style.textContent.includes("--cs-top-fade-contract: 6") &&
        style.textContent.includes('[class*="_MainContentTopFade_"]'))
    : (scopeContractSafe && workspaceContractSafe));
  const shellEdgeContractSafe = expectedTemplateVersion < 7 || (expectedTemplateVersion < 8
    ? Boolean(style && style.textContent.includes("--cs-shell-edge-contract: 7") &&
        style.textContent.includes('[data-app-shell-header-edge-scroll]') &&
        style.textContent.includes('[class*="_Header_"]') &&
        style.textContent.includes('[data-app-shell-main-content-top-fade]'))
    : (scopeContractSafe && workspaceContractSafe));
  const topFadeNeutralized = expectedTemplateVersion < 6
    ? (!legacyTopFade || Boolean(getComputedStyle(legacyTopFade) &&
        getComputedStyle(legacyTopFade).backgroundImage === "none" &&
        getComputedStyle(legacyTopFade).backdropFilter === "none" &&
        Number(getComputedStyle(legacyTopFade).opacity) === 0))
    : (topFadeContractSafe && topFades.every((fade) => {
        const computed = getComputedStyle(fade);
        return computed.display === "none" ||
          (computed.backgroundImage === "none" &&
            computed.backdropFilter === "none" &&
            Number(computed.opacity) === 0);
      }));
  const mainRect = main?.getBoundingClientRect() || null;
  const sidebarRect = sidebar?.getBoundingClientRect() || null;
  const sidebarAfterStyle = sidebar ? getComputedStyle(sidebar, "::after") : null;
  const mainBoundaryNeutralized = Boolean(main && mainRect && sidebarRect && sidebarAfterStyle &&
    getComputedStyle(main).boxShadow === "none" &&
    getComputedStyle(main).borderInlineStartWidth === "0px" &&
    Math.abs(sidebarRect.right - mainRect.left) <= 1 &&
    (sidebarAfterStyle.content === "none" || sidebarAfterStyle.display === "none"));
  const composerUtilityStyle = composerUtilityBar ? getComputedStyle(composerUtilityBar) : null;
  const composerUtilityNeutralized = Boolean(composerUtilityStyle &&
    composerUtilityStyle.backgroundColor !== "rgb(246, 246, 246)" &&
    composerUtilityStyle.borderTopWidth !== "0px");
  const mainStyle = main ? getComputedStyle(main) : null;
  const templateScopeSafe = expectedTemplateVersion < 8
    ? Boolean(style && mainStyle?.backgroundImage.includes("linear-gradient"))
    : Boolean(style && scopedMain && scopeContractSafe && workspaceContractSafe &&
        mainStyle?.backgroundImage.includes("linear-gradient"));
  const bottomFadeSelector = typeof selectors?.["conversation-bottom-fade"] === "string"
    ? selectors["conversation-bottom-fade"]
    : ':is(.bg-gradient-to-t.from-token-main-surface-primary, ' +
      '[class*="_MainContentBottomFade_"], [class*="_MainContentBottomGradient_"])';
  const bottomFades = main
    ? [...main.querySelectorAll(bottomFadeSelector)].filter(visible) : [];
  const bottomFadeNeutralized = bottomFades.every((fade) => {
    const computed = getComputedStyle(fade);
    return computed.display === "none" ||
      (computed.backgroundImage === "none" &&
        (computed.backgroundColor === "rgba(0, 0, 0, 0)" ||
          computed.backgroundColor === "transparent"));
  });
  const rootStyle = getComputedStyle(root);
  const surface = rootStyle.getPropertyValue("--cs-surface-rgb").trim()
    .split(/\s+/).map(Number);
  const primary = color(rootStyle.getPropertyValue("--cs-text-primary").trim(), surface);
  const secondary = color(rootStyle.getPropertyValue("--cs-text-secondary").trim(), surface);
  const accent = color(rootStyle.getPropertyValue("--cs-accent").trim(), surface);
  const themeContrastSafe = surface.length === 3 && surface.every(Number.isFinite) &&
    primary && secondary && accent &&
    contrast(primary, surface) >= 4.5 &&
    contrast(secondary, surface) >= 4.5 &&
    contrast(accent, surface) >= 3;
	const activityContractSafe = Boolean(style &&
		style.textContent.includes("--cs-activity-contract: 3") &&
		style.textContent.includes('button[class~="group/activity-header"]') &&
		style.textContent.includes("text-shadow: none !important"));
	const conversationActivitySafe = activityContractSafe && activityHeaders.length > 0 && activityHeaders.every((header) => {
		const label = header.querySelector("[class~='text-token-conversation-body']") || header;
		const foreground = computedColor(getComputedStyle(label).color);
		const background = effectiveBackground(label, surface);
		return foreground && contrast(foreground, background) >= 4.5;
  });
	const diffResourceContractSafe = Boolean(style &&
		style.textContent.includes("--cs-diff-resource-contract: 4") &&
		style.textContent.includes(
			"[--codex-diffs-header-padding-x:var(--thread-resource-card-row-padding-x)]"
		) &&
		style.textContent.includes("color: var(--cs-text-primary) !important") &&
		style.textContent.includes("text-shadow: none !important"));
	const conversationDiffResourceSafe = diffResourceContractSafe &&
		diffResourceCards.length > 0 && diffResourceControls.length > 0 &&
			diffResourceControls.every((control) => {
					const foreground = computedColor(getComputedStyle(control).color);
					const background = effectiveBackground(control, surface);
					return foreground && contrast(foreground, background) >= 4.5;
				});
	  return {
	    scope,
	    runtimeVersion: Number(root.getAttribute("data-codex-skin-runtime") || 0),
	    styleMarkerCount: styles.length,
    templateVersion: Number(root.getAttribute("data-codex-skin-template") || 0),
    themePublicId: root.getAttribute("data-codex-skin-theme") || "",
    backgroundLoaded: Boolean(style && style.sheet && style.sheet.cssRules.length > 0 &&
      rootBackground.includes("blob:") &&
      root.getAttribute("data-codex-skin-background-url")?.startsWith("blob:")),
    backgroundTokenSet: rootBackground.includes("blob:"),
    bodyBackgroundSet: background.includes("blob:"),
    regions: {
	      home: visible(main) ? "pass" : "fail",
	      shellMain: visible(main) ? "pass" : "fail",
	      mainBoundary: mainBoundaryNeutralized ? "pass" : "fail",
	      sidebar: visible(sidebar) ? "pass" : "fail",
	      // Newer renderers keep an inactive header node alongside the visible
	      // shell header. A hidden match has no visual surface to validate.
	      headerTint: !visible(header) || shellEdgeContractSafe ? "pass" : "fail",
	      composerUtilityBar: optional(composerUtilityBar, composerUtilityNeutralized),
	      topFade: topFades.length === 0 ? "not_present" : (topFadeNeutralized ? "pass" : "fail"),
      bottomFade: bottomFades.length === 0 ? "not_present" : (bottomFadeNeutralized ? "pass" : "fail"),
      templateScope: templateScopeSafe ? "pass" : "fail",
      themeContrast: themeContrastSafe ? "pass" : "fail",
		conversationActivity: activityHeaders.length === 0
			? "not_present"
			: (conversationActivitySafe ? "pass" : "fail"),
		conversationDiffResource: diffResourceCards.length === 0
			? "not_present"
			: (conversationDiffResourceSafe ? "pass" : "fail"),
      suggestionCards: optional(suggestions, cards.length > 0),
      projectPicker: optional(project, visible(project)),
	      composer: optional(composer, visible(composer))
    }
  };
}`

const restoreFunction = `function () {
	const state = globalThis["__CODEX_SKIN_RENDERER_CONTROLLER_V2__"];
	if (typeof state?.cleanup === "function") state.cleanup();
	delete globalThis["__CODEX_SKIN_RENDERER_CONTROLLER_V2__"];
  for (const style of document.querySelectorAll("#codex-skin-theme-v1")) style.remove();
  for (const main of document.querySelectorAll(
    'main[data-codex-skin-main="true"], main[data-codex-skin-scope]'
  )) {
    main.removeAttribute("data-codex-skin-main");
    main.removeAttribute("data-codex-skin-scope");
  }
  for (const node of document.querySelectorAll(
    '[data-codex-skin-composer], [data-codex-skin-composer-boundary]'
  )) {
    node.removeAttribute("data-codex-skin-composer");
    node.removeAttribute("data-codex-skin-composer-boundary");
  }
  const root = document.documentElement;
  const backgroundURL = root.getAttribute("data-codex-skin-background-url");
  if (backgroundURL && backgroundURL.startsWith("blob:")) URL.revokeObjectURL(backgroundURL);
  root.removeAttribute("data-codex-skin");
  root.removeAttribute("data-codex-skin-theme");
  root.removeAttribute("data-codex-skin-theme-version");
	root.removeAttribute("data-codex-skin-template");
	root.removeAttribute("data-codex-skin-appearance");
	root.removeAttribute("data-codex-skin-runtime");
  root.removeAttribute("data-codex-skin-background-url");
  root.style.removeProperty("--cs-background-image");
  return document.querySelectorAll("#codex-skin-theme-v1").length === 0;
}`

const officialFunction = `function () {
  const root = document.documentElement;
	return !globalThis["__CODEX_SKIN_RENDERER_CONTROLLER_V2__"] &&
    document.querySelectorAll("#codex-skin-theme-v1").length === 0 &&
    document.querySelectorAll('main[data-codex-skin-main="true"]').length === 0 &&
    document.querySelectorAll('main[data-codex-skin-scope]').length === 0 &&
    document.querySelectorAll('[data-codex-skin-composer]').length === 0 &&
    document.querySelectorAll('[data-codex-skin-composer-boundary]').length === 0 &&
    !root.hasAttribute("data-codex-skin") &&
    !root.hasAttribute("data-codex-skin-theme") &&
    !root.hasAttribute("data-codex-skin-theme-version") &&
		!root.hasAttribute("data-codex-skin-template") &&
		!root.hasAttribute("data-codex-skin-appearance") &&
		!root.hasAttribute("data-codex-skin-runtime") &&
    !root.hasAttribute("data-codex-skin-background-url") &&
    !root.style.getPropertyValue("--cs-background-image");
}`

const fixedLayoutDiagnosticsFunction = `function () {
  const visible = (node) => {
    if (!node) return false;
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return rect.width > 1 && rect.height > 1 &&
      style.display !== "none" && style.visibility !== "hidden";
  };
  const describe = (node) => {
    if (!node) return null;
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    return {
      tag: node.tagName.toLowerCase(),
      classes: typeof node.className === "string" ? node.className : "",
      backgroundColor: style.backgroundColor,
      backgroundImage: style.backgroundImage,
      boxShadow: style.boxShadow,
      border: style.border,
      display: style.display,
      position: style.position,
      margin: style.margin,
      padding: style.padding,
      gap: style.gap,
      rect: [rect.x, rect.y, rect.width, rect.height]
    };
  };
  const describePseudo = (node, pseudo) => {
    const rect = node.getBoundingClientRect();
    const style = getComputedStyle(node, pseudo);
    const value = (property) => String(property || "");
    return {
      tag: node.tagName.toLowerCase(),
      classes: typeof node.className === "string" ? node.className : "",
      pseudo,
      generated: value(style.content) !== "none",
      backgroundColor: value(style.backgroundColor),
      backgroundImage: value(style.backgroundImage),
      boxShadow: value(style.boxShadow),
      backdropFilter: value(style.backdropFilter),
      filter: value(style.filter),
      maskImage: value(style.maskImage),
      position: value(style.position),
      inset: value(style.inset),
      width: value(style.width),
      height: value(style.height),
      rect: [rect.x, rect.y, rect.width, rect.height]
    };
  };
  const main = document.querySelector(
    'main[data-codex-skin-main="true"], main.main-surface, main[class*="_MainContentSurface_"]'
  );
  const sidebar = document.querySelector("aside.app-shell-left-panel");
  const sidebarCandidates = [...document.querySelectorAll(
    ':is(aside, nav, [role="navigation"], [data-testid*="sidebar" i], ' +
    '[class*="sidebar" i], [class*="left-panel" i], [class*="_Sidebar_"])'
  )].filter(visible).slice(0, 24).map(describe);
  const composer = document.querySelector(".composer-surface-chrome");
  const mainChildren = main ? [...main.children].filter(visible).slice(0, 24).map(describe) : [];
  const mainSurfaces = main ? [...main.querySelectorAll("div,section,form,footer")]
    .filter((node) => {
      if (!visible(node)) return false;
      const rect = node.getBoundingClientRect();
      const style = getComputedStyle(node);
      return rect.width > innerWidth * .35 && rect.height > 24 &&
        style.backgroundColor !== "rgba(0, 0, 0, 0)";
    }).slice(0, 40).map(describe) : [];
  const composerRect = composer?.getBoundingClientRect() || null;
  const composerSurfaces = main && composerRect ? [...main.querySelectorAll("div,form,section")]
    .filter((node) => {
      if (!visible(node) || node === composer || composer.contains(node)) return false;
      const rect = node.getBoundingClientRect();
      const style = getComputedStyle(node);
      return rect.width > composerRect.width * 0.5 &&
        rect.bottom >= composerRect.top - 140 &&
        rect.top <= composerRect.bottom &&
        style.backgroundColor !== "rgba(0, 0, 0, 0)";
    })
    .slice(0, 24).map(describe) : [];
  const projectCandidates = main ? [...main.querySelectorAll("button")]
    .filter((node) => visible(node) &&
      (String(node.className).includes("utility") ||
        (composerRect && node.getBoundingClientRect().bottom >= composerRect.top - 140 &&
          node.getBoundingClientRect().top <= composerRect.top + 40)))
    .slice(0, 16).map(describe) : [];
  const ancestors = [];
  let current = composer?.parentElement || null;
  for (let index = 0; current && index < 6; index += 1, current = current.parentElement) {
    ancestors.push(describe(current));
  }
  const ancestorChain = (node) => {
    const result = [];
    let current = node?.parentElement || null;
    for (let index = 0; current && index < 8; index += 1, current = current.parentElement) {
      result.push(describe(current));
    }
    return result;
  };
  const boundarySurfaces = [];
  const boundaryPseudos = [];
  const seen = new Set();
  const mainRect = main?.getBoundingClientRect() || null;
  const sidebarRect = sidebar?.getBoundingClientRect() || null;
  if (mainRect && sidebarRect) {
    const sampleY = Math.min(window.innerHeight - 2, Math.max(2, window.innerHeight / 2));
    for (const offset of [-64, -48, -32, -24, -16, -8, -1, 1]) {
      const x = Math.min(window.innerWidth - 1, Math.max(1, sidebarRect.right + offset));
      for (const node of document.elementsFromPoint(x, sampleY)) {
        if (seen.has(node)) continue;
        seen.add(node);
        boundarySurfaces.push(describe(node));
        for (const pseudo of ["::before", "::after"]) {
          const item = describePseudo(node, pseudo);
          const active = (value) => value !== "" && value !== "none";
          if (item.generated || item.backgroundColor !== "rgba(0, 0, 0, 0)" ||
              active(item.backgroundImage) || active(item.boxShadow) ||
              active(item.filter) || active(item.backdropFilter) ||
              active(item.maskImage)) {
            boundaryPseudos.push(item);
          }
        }
      }
    }
  }
  return {
    main: describe(main),
    mainAncestors: ancestorChain(main),
	mainChildren,
	mainSurfaces,
    sidebar: describe(sidebar),
    sidebarAncestors: ancestorChain(sidebar),
    sidebarCandidates,
    boundarySurfaces,
    boundaryPseudos,
    composer: describe(composer),
    composerAncestors: ancestors,
    composerSurfaces,
    projectCandidates
  };
}`
