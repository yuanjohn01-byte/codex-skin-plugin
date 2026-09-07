package adapter

import (
	"context"
	"errors"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/appearance"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/codex"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

// Keep macOS's already accepted Restore route unchanged. Windows uses the same
// native controls as Apply but holds recovery until VerifyOfficial succeeds.
func supportsInAppRestore(platform string) bool { return platform == "windows" }

func canPrepareAppearanceInPlace(platform, target string, restore bool) bool {
	if restore {
		return supportsInAppRestore(platform)
	}
	return target != "" && supportsInAppAppearance(platform)
}

type appearanceRestoreUI interface {
	readVerifiedAppearance(context.Context, *liveSession) (appearanceUIState, string, error)
	switchAppearanceWithTransaction(context.Context, *liveSession, string, appearanceModeTransaction) error
	restoreOfficialRenderer(context.Context, *liveSession) error
}

type pendingAppearanceRestore struct {
	transaction    *appearance.LiveRestore
	baseline       appearanceUIState
	originalMode   string
	ui             appearanceRestoreUI
	removalStarted bool
}

func prepareAppearanceRestore(
	ctx context.Context,
	manager *appearance.Manager,
	live *liveSession,
	ui appearanceRestoreUI,
) (result *pendingAppearanceRestore, returnErr error) {
	baseline, hostMode, err := ui.readVerifiedAppearance(ctx, live)
	if errors.Is(err, codex.ErrListenerUntrusted) {
		return nil, errors.Join(engine.ErrStateUnsafe, err)
	}
	if err != nil || !baseline.TrustedOrigin || !baseline.BridgeAvailable ||
		baseline.Route == "" || baseline.TimeOrigin == 0 || !validAppearanceSetting(hostMode) {
		return nil, errAppearanceUIUnavailable
	}
	transaction, err := manager.BeginLiveRestore(hostMode)
	if errors.Is(err, appearance.ErrNoLiveRestorePoint) {
		return nil, nil
	}
	if errors.Is(err, appearance.ErrLiveRestoreUnavailable) {
		return nil, errAppearanceUIUnavailable
	}
	if err != nil {
		return nil, errors.Join(engine.ErrStateUnsafe, err)
	}
	ready := false
	changedMode := false
	defer func() {
		if !ready {
			if changedMode {
				returnErr = rollbackPreparedAppearanceRestore(ctx, live, ui, transaction, baseline, hostMode, returnErr)
			}
			transaction.Close()
		}
	}()
	if hostMode != transaction.Mode() {
		if err := ui.switchAppearanceWithTransaction(ctx, live, transaction.Mode(), transaction); err != nil {
			return nil, err
		}
		changedMode = true
	}
	pending := &pendingAppearanceRestore{
		transaction: transaction, baseline: baseline, originalMode: hostMode, ui: ui,
	}
	if err := pending.verifyLive(ctx, live, ui); err != nil {
		// After a UI change no restart fallback may hide an uncertain result.
		return nil, errors.Join(engine.ErrStateUnsafe, err)
	}
	if err := transaction.Stage(); err != nil {
		return nil, errors.Join(engine.ErrStateUnsafe, err)
	}
	ready = true
	return pending, nil
}

func (pending *pendingAppearanceRestore) verifyLive(ctx context.Context, live *liveSession, ui appearanceRestoreUI) error {
	state, hostMode, err := ui.readVerifiedAppearance(ctx, live)
	if err != nil {
		return err
	}
	mode := pending.transaction.Mode()
	if !restoreAppearanceStateMatches(state, hostMode, mode, pending.baseline) {
		return engine.ErrVerifyFailed
	}
	return pending.transaction.VerifyMode(mode)
}

func restoreAppearanceStateMatches(state appearanceUIState, hostMode, mode string, baseline appearanceUIState) bool {
	effective := mode
	if mode == "system" {
		effective = state.SystemVariant
	}
	return state.TrustedOrigin && state.BridgeAvailable && hostMode == mode &&
		state.Route == baseline.Route && state.TimeOrigin == baseline.TimeOrigin &&
		currentAppearanceEffectiveState(state, effective)
}

func rollbackPreparedAppearanceRestore(
	ctx context.Context, live *liveSession, ui appearanceRestoreUI,
	transaction *appearance.LiveRestore, baseline appearanceUIState, oldMode string, cause error,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appearanceUIRollbackWait)
	defer cancel()
	// Even a verified rollback is a failed Restore, not permission to restart.
	return errors.Join(engine.ErrStateUnsafe, cause,
		restorePriorAppearance(rollbackCtx, live, ui, transaction, baseline, oldMode))
}

func restorePriorAppearance(
	ctx context.Context, live *liveSession, ui appearanceRestoreUI,
	transaction *appearance.LiveRestore, baseline appearanceUIState, oldMode string,
) error {
	state, hostMode, err := ui.readVerifiedAppearance(ctx, live)
	if err != nil || !state.TrustedOrigin || !state.BridgeAvailable ||
		state.TimeOrigin != baseline.TimeOrigin || state.Route != baseline.Route ||
		(hostMode != oldMode && hostMode != transaction.Mode()) {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	if hostMode != oldMode {
		if err := ui.switchAppearanceWithTransaction(ctx, live, oldMode, transaction); err != nil {
			return errors.Join(engine.ErrStateUnsafe, err)
		}
	}
	state, hostMode, err = ui.readVerifiedAppearance(ctx, live)
	if err != nil || !restoreAppearanceStateMatches(state, hostMode, oldMode, baseline) ||
		transaction.VerifyMode(oldMode) != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	return nil
}

// AbortOfficialRestore runs before the engine closes a failed Restore session.
// A pending transaction exists only for Windows in-place Restore; macOS and
// already verified/committed sessions are untouched. Recovery never restarts.
func (adapter *Live) AbortOfficialRestore(ctx context.Context, session engine.Session) error {
	adapter.mu.Lock()
	live := adapter.sessions[session.OpaqueID]
	adapter.mu.Unlock()
	if live == nil || live.pendingRestore == nil {
		return nil
	}
	pending := live.pendingRestore
	defer pending.transaction.Close()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appearanceUIRollbackWait)
	defer cancel()
	if !pending.removalStarted {
		// The adapter has not attempted to remove the previous skin. Undo only
		// the native mode, leaving both that skin and first-Apply backup intact.
		return restorePriorAppearance(cleanupCtx, live, pending.ui,
			pending.transaction, pending.baseline, pending.originalMode)
	}
	// An interrupted removal may already have removed some/all of the skin.
	// Do not pin the old mode blindly. Complete bounded official cleanup only
	// on the same trusted renderer with the restored native preference intact.
	// The original operation still fails and keeps its backup, even if cleanup
	// succeeds; only the normal VerifyOfficial path may consume that backup.
	if err := pending.verifyLive(cleanupCtx, live, pending.ui); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	if err := pending.ui.restoreOfficialRenderer(cleanupCtx, live); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	if err := pending.verifyLive(cleanupCtx, live, pending.ui); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	if err := pending.transaction.VerifyRestored(); err != nil {
		return errors.Join(engine.ErrStateUnsafe, err)
	}
	return nil
}

func (adapter *Live) restoreOfficialRenderer(ctx context.Context, live *liveSession) error {
	if err := verifyAppearanceUIProcess(ctx, live); err != nil {
		return err
	}
	if err := adapter.removeThemeRenderer(ctx, live); err != nil {
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

// Called only after the official renderer (no skin/controller) was verified.
// Any failure leaves the exact first-Apply backup available for offline Restore.
func (pending *pendingAppearanceRestore) finish(ctx context.Context, live *liveSession, ui appearanceRestoreUI) error {
	if err := pending.verifyLive(ctx, live, ui); err != nil {
		return err
	}
	return pending.transaction.Commit()
}
