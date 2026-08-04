package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

var themePublicIDPattern = regexp.MustCompile(`^[0-9]{6}$`)

const rollbackTimeout = 20 * time.Second

type Engine struct {
	store   *Store
	adapter Adapter
}

func New(store *Store, adapter Adapter) (*Engine, error) {
	if store == nil || adapter == nil {
		return nil, ErrConfiguration
	}
	return &Engine{store: store, adapter: adapter}, nil
}

func (engine *Engine) ApplyVerified(ctx context.Context, verified theme.Verified) (result ApplyResult, returnErr error) {
	if verified.Manifest.SchemaVersion != theme.SchemaVersion ||
		verified.Descriptor.ThemePublicID != verified.Manifest.ThemePublicID ||
		verified.Descriptor.ThemeVersion != verified.Manifest.ThemeVersion ||
		verified.PackageSHA256 == "" {
		return ApplyResult{}, ErrConfiguration
	}
	unlock, err := engine.store.Lock()
	if err != nil {
		return ApplyResult{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, unlock())
	}()
	if err := engine.recoverInterruptedLocked(ctx); err != nil {
		return ApplyResult{}, err
	}

	operationID, err := engine.store.NewOperationID()
	if err != nil {
		return ApplyResult{}, err
	}
	journal := Journal{
		OperationID:   operationID,
		Kind:          "apply",
		Stage:         "validate",
		Status:        "running",
		ThemePublicID: verified.Manifest.ThemePublicID,
		ThemeVersion:  verified.Manifest.ThemeVersion,
	}
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, err
	}
	stage, err := engine.store.StagingPath(operationID)
	if err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-STATE-001", err)
	}
	defer os.RemoveAll(stage)

	journal.Stage = "stage"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, err
	}
	if err := theme.Extract(verified, stage); err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-THEME-PACKAGE-001", err)
	}
	if err := theme.WriteVerificationBundle(verified, stage); err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-THEME-PACKAGE-001", err)
	}
	compiled, err := CompileTheme(verified, stage)
	if err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-THEME-COMPILE-001", err)
	}
	previousCompiled, previousDesiredPointer, err := engine.loadDesiredCompiled()
	if err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-CACHE-VERIFY-001", err)
	}
	before := Snapshot{}
	if previousCompiled != nil {
		before = snapshotFromCompiled(*previousCompiled)
	}
	recoveryID, err := engine.store.NewRecoveryID()
	if err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-STATE-001", err)
	}
	recovery := RecoveryPoint{
		RecoveryID:      recoveryID,
		OperationID:     operationID,
		CapturedAt:      canonicalNow(),
		WasThemed:       before.StylePresent,
		ThemePublicID:   before.ThemePublicID,
		ThemeVersion:    before.ThemeVersion,
		PreviousDesired: previousDesiredPointer,
	}
	if err := engine.store.WriteRecoveryPoint(recovery, before); err != nil {
		return ApplyResult{}, engine.failJournal(journal, "CS-BACKUP-001", err)
	}
	journal.RecoveryID = recoveryID
	journal.Stage = "prepare"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, err
	}

	session, err := engine.openThemeSession(ctx, compiled)
	if err != nil {
		return ApplyResult{}, engine.failRecoverableJournal(journal, "CS-CODEX-IDENTITY-001", err)
	}
	defer engine.adapter.Close(ctx, session)
	rollback := func(code string, cause error) error {
		return engine.failWithRollback(
			ctx,
			session,
			before,
			previousDesiredPointer,
			journal,
			code,
			cause,
		)
	}
	if previousCompiled != nil {
		if err := engine.primeSession(ctx, session, *previousCompiled); err != nil {
			return ApplyResult{}, rollback("CS-CACHE-VERIFY-001", err)
		}
	}

	journal.Stage = "backup"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	before, err = engine.adapter.Capture(ctx, session)
	if err != nil {
		return ApplyResult{}, rollback("CS-BACKUP-001", err)
	}

	probe, err := engine.probeCapabilities(ctx, session)
	if err != nil || !CapabilitiesAllowApply(probe) {
		if err == nil {
			err = ErrCapabilityBlocked
		}
		return ApplyResult{}, rollback("CS-COMPAT-001", err)
	}

	journal.Stage = "apply"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	if err := engine.adapter.Apply(ctx, session, compiled); err != nil {
		return ApplyResult{}, rollback("CS-APPLY-001", fmt.Errorf("%w: %v", ErrApplyFailed, err))
	}

	journal.Stage = "verify"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	report, err := engine.adapter.Verify(ctx, session, compiled)
	if err != nil || !reportAllowsCommit(report, compiled) {
		if err == nil {
			err = ErrVerifyFailed
		}
		return ApplyResult{}, rollback("CS-VERIFY-001", err)
	}

	journal.Stage = "commit"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	desired := DesiredTheme{
		ThemePublicID:   compiled.ThemePublicID,
		ThemeVersion:    compiled.ThemeVersion,
		PackageSHA256:   verified.PackageSHA256,
		TemplateVersion: compiled.TemplateVersion,
		AppliedAt:       canonicalNow(),
	}
	if _, err := engine.store.CommitTheme(stage, desired); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	if err := engine.store.WriteDesired(desired); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	journal.Stage = "complete"
	journal.Status = "completed"
	if err := engine.store.WriteJournal(journal); err != nil {
		return ApplyResult{}, rollback("CS-STATE-001", err)
	}
	return ApplyResult{
		OperationID:   operationID,
		ThemePublicID: compiled.ThemePublicID,
		ThemeVersion:  compiled.ThemeVersion,
		Identity:      session.Identity,
		Report:        report,
		RecoveryPoint: recoveryID,
	}, nil
}

func (engine *Engine) RestoreOfficial(ctx context.Context) (result RestoreResult, returnErr error) {
	unlock, err := engine.store.Lock()
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, unlock())
	}()
	operationID, err := engine.store.NewOperationID()
	if err != nil {
		return RestoreResult{}, err
	}
	journal := Journal{OperationID: operationID, Kind: "restore", Stage: "validate", Status: "running"}
	if err := engine.store.WriteJournal(journal); err != nil {
		return RestoreResult{}, err
	}
	session, err := engine.openOfficialSession(ctx)
	if err != nil {
		return RestoreResult{}, engine.failJournal(journal, "CS-CODEX-IDENTITY-001", err)
	}
	defer engine.adapter.Close(ctx, session)
	probe, err := engine.probeCapabilities(ctx, session)
	if err != nil {
		return RestoreResult{}, engine.failJournal(journal, "CS-COMPAT-001", err)
	}
	wasThemed := probe.StyleMarkerCount > 0
	journal.Stage = "restore"
	if err := engine.store.WriteJournal(journal); err != nil {
		return RestoreResult{}, err
	}
	if err := engine.adapter.RestoreOfficial(ctx, session); err != nil {
		return RestoreResult{}, engine.failJournal(journal, "CS-RESTORE-001", fmt.Errorf("%w: %v", ErrRestoreFailed, err))
	}
	if err := engine.adapter.VerifyOfficial(ctx, session); err != nil {
		return RestoreResult{}, engine.failJournal(journal, "CS-RESTORE-001", errors.Join(ErrRestoreFailed, err))
	}
	if err := engine.store.ClearDesired(); err != nil {
		return RestoreResult{}, engine.failJournal(journal, "CS-STATE-001", err)
	}
	journal.Stage = "complete"
	journal.Status = "completed"
	if err := engine.store.WriteJournal(journal); err != nil {
		return RestoreResult{}, err
	}
	if err := engine.resolveInterruptedJournals(operationID); err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{OperationID: operationID, Identity: session.Identity, WasThemed: wasThemed}, nil
}

func (engine *Engine) openThemeSession(ctx context.Context, compiled CompiledTheme) (Session, error) {
	if opener, supported := engine.adapter.(ThemeSessionOpener); supported {
		return opener.OpenVerifiedThemeSession(ctx, compiled)
	}
	return engine.adapter.OpenVerifiedSession(ctx)
}

func (engine *Engine) openOfficialSession(ctx context.Context) (Session, error) {
	if opener, supported := engine.adapter.(OfficialSessionOpener); supported {
		return opener.OpenVerifiedOfficialSession(ctx)
	}
	return engine.adapter.OpenVerifiedSession(ctx)
}

// RecoverInterrupted restores the last-known-good snapshot for an apply that
// was interrupted after renderer mutation became possible.
func (engine *Engine) RecoverInterrupted(ctx context.Context) (returnErr error) {
	unlock, err := engine.store.Lock()
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, unlock())
	}()
	return engine.recoverInterruptedLocked(ctx)
}

func (engine *Engine) recoverInterruptedLocked(ctx context.Context) error {
	journals, err := engine.store.RunningJournals()
	if err != nil {
		return err
	}
	if len(journals) == 0 {
		return nil
	}
	if len(journals) != 1 {
		return fmt.Errorf("%w: multiple running operation journals", ErrStateUnsafe)
	}
	journal := journals[0]
	if journal.Kind != "apply" {
		journal.Status = "failed"
		journal.ErrorCode = "CS-INTERRUPTED-001"
		journal.Stage = "interrupted"
		return engine.store.WriteJournal(journal)
	}
	mutatingStage := journal.Stage == "prepare" || journal.Stage == "backup" ||
		journal.Stage == "apply" || journal.Stage == "verify" || journal.Stage == "commit"
	if !mutatingStage {
		journal.Status = "failed"
		journal.ErrorCode = "CS-INTERRUPTED-001"
		journal.Stage = "interrupted"
		return engine.store.WriteJournal(journal)
	}
	if journal.RecoveryID == "" {
		return fmt.Errorf("%w: interrupted apply has no recovery point", ErrStateUnsafe)
	}
	point, snapshot, err := engine.store.ReadRecoveryPoint(journal.RecoveryID)
	if err != nil || point.OperationID != journal.OperationID {
		if err == nil {
			err = fmt.Errorf("%w: recovery operation mismatch", ErrStateUnsafe)
		}
		return err
	}
	session, err := engine.adapter.OpenVerifiedSession(ctx)
	if err != nil {
		return err
	}
	defer engine.adapter.Close(ctx, session)
	if snapshot.StylePresent {
		err = engine.adapter.Restore(ctx, session, snapshot)
	} else {
		err = engine.adapter.RestoreOfficial(ctx, session)
	}
	if err != nil {
		return fmt.Errorf("%w: interrupted apply restore: %v", ErrRollbackFailed, err)
	}
	if snapshot.StylePresent {
		captured, captureErr := engine.adapter.Capture(ctx, session)
		if captureErr != nil || captured != snapshot {
			return errors.Join(ErrRollbackFailed, captureErr)
		}
	} else if err := engine.adapter.VerifyOfficial(ctx, session); err != nil {
		return fmt.Errorf("%w: interrupted official restore: %v", ErrRollbackFailed, err)
	} else if finalizer, supported := engine.adapter.(OfficialRollbackFinalizer); supported {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		if err := finalizer.FinalizeOfficialRollback(cleanupCtx, session); err != nil {
			return fmt.Errorf("%w: interrupted official finalize: %v", ErrRollbackFailed, err)
		}
	}
	if point.PreviousDesired == nil {
		if err := engine.store.ClearDesired(); err != nil {
			return err
		}
	} else if err := engine.store.WriteDesired(*point.PreviousDesired); err != nil {
		return err
	}
	journal.Status = "failed"
	journal.ErrorCode = "CS-INTERRUPTED-001"
	journal.Stage = "rolled_back"
	return engine.store.WriteJournal(journal)
}

func (engine *Engine) resolveInterruptedJournals(currentOperationID string) error {
	journals, err := engine.store.RunningJournals()
	if err != nil {
		return err
	}
	for _, interrupted := range journals {
		if interrupted.OperationID == currentOperationID {
			continue
		}
		interrupted.Status = "failed"
		interrupted.ErrorCode = "CS-INTERRUPTED-RESTORED-001"
		interrupted.Stage = "official_restored"
		if err := engine.store.WriteJournal(interrupted); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) loadDesiredCompiled() (*CompiledTheme, *DesiredTheme, error) {
	return LoadDesiredCompiled(engine.store)
}

// LoadDesiredCompiled re-verifies and compiles the cached theme selected by
// the user. It is intentionally offline: a session controller may only use a
// package that was already committed by the verified apply transaction.
func LoadDesiredCompiled(store *Store) (*CompiledTheme, *DesiredTheme, error) {
	if store == nil {
		return nil, nil, ErrConfiguration
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found {
		return nil, nil, err
	}
	cache, err := store.ThemeCachePath(desired)
	if err != nil {
		return nil, nil, err
	}
	verified, err := theme.VerifyCached(cache)
	if err != nil {
		return nil, nil, err
	}
	if verified.Manifest.ThemePublicID != desired.ThemePublicID ||
		verified.Manifest.ThemeVersion != desired.ThemeVersion ||
		verified.PackageSHA256 != desired.PackageSHA256 {
		return nil, nil, fmt.Errorf("%w: cached desired theme mismatch", ErrStateUnsafe)
	}
	temporary, err := os.MkdirTemp(filepath.Join(store.Root(), "tmp"), ".prime-")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: create cache verification staging: %v", ErrStateUnsafe, err)
	}
	if err := os.Remove(temporary); err != nil {
		return nil, nil, fmt.Errorf("%w: prepare cache verification staging: %v", ErrStateUnsafe, err)
	}
	defer os.RemoveAll(temporary)
	if err := theme.Extract(verified, temporary); err != nil {
		return nil, nil, err
	}
	compiled, err := CompileTheme(verified, temporary)
	if err != nil {
		return nil, nil, err
	}
	return &compiled, &desired, nil
}

func (engine *Engine) primeSession(ctx context.Context, session Session, compiled CompiledTheme) error {
	primer, supported := engine.adapter.(SessionPrimer)
	if !supported {
		return nil
	}
	return primer.Prime(ctx, session, compiled)
}

func snapshotFromCompiled(compiled CompiledTheme) Snapshot {
	return Snapshot{
		StylePresent: true, StyleText: compiled.StyleText,
		BackgroundDataURL: compiled.BackgroundDataURL,
		ThemePublicID:     compiled.ThemePublicID, ThemeVersion: compiled.ThemeVersion,
		TemplateVersion: compiled.TemplateVersion, AppearanceMode: compiled.AppearanceMode,
	}
}

func (engine *Engine) failJournal(journal Journal, code string, cause error) error {
	journal.Status = "failed"
	journal.ErrorCode = code
	return errors.Join(cause, engine.store.WriteJournal(journal))
}

func (engine *Engine) failRecoverableJournal(journal Journal, code string, cause error) error {
	journal.Status = "running"
	journal.ErrorCode = code
	return errors.Join(cause, engine.store.WriteJournal(journal))
}

func (engine *Engine) failWithRollback(
	ctx context.Context,
	session Session,
	snapshot Snapshot,
	previousDesired *DesiredTheme,
	journal Journal,
	code string,
	cause error,
) error {
	// A canceled request must not cancel safety cleanup. The bounded detached
	// context carries values but never inherits cancellation or deadlines.
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()

	restoreErr := engine.adapter.Restore(rollbackCtx, session, snapshot)
	if restoreErr == nil {
		if snapshot.StylePresent {
			captured, captureErr := engine.adapter.Capture(rollbackCtx, session)
			if captureErr != nil || captured != snapshot {
				restoreErr = errors.Join(ErrRollbackFailed, captureErr)
			}
		} else {
			if verifyErr := engine.adapter.VerifyOfficial(rollbackCtx, session); verifyErr != nil {
				restoreErr = verifyErr
			} else if finalizer, supported := engine.adapter.(OfficialRollbackFinalizer); supported {
				restoreErr = finalizer.FinalizeOfficialRollback(rollbackCtx, session)
			}
		}
	}

	var desiredErr error
	if previousDesired == nil {
		desiredErr = engine.store.ClearDesired()
	} else {
		desiredErr = engine.store.WriteDesired(*previousDesired)
	}
	if restoreErr != nil || desiredErr != nil {
		// Keep the mutation-stage journal running so the next invocation cannot
		// skip durable recovery after an incomplete rollback.
		if journal.Stage != "apply" && journal.Stage != "verify" && journal.Stage != "commit" {
			journal.Stage = "commit"
		}
		journal.Status = "running"
		journal.ErrorCode = code
		persistErr := engine.store.WriteJournal(journal)
		return errors.Join(
			cause,
			fmt.Errorf("%w: %v", ErrRollbackFailed, errors.Join(restoreErr, desiredErr)),
			persistErr,
		)
	}
	return engine.failJournal(journal, code, cause)
}

func (engine *Engine) probeCapabilities(ctx context.Context, session Session) (RegionReport, error) {
	if waiter, supported := engine.adapter.(CapabilityWaiter); supported {
		return waiter.WaitForCapabilities(ctx, session)
	}
	return engine.adapter.Probe(ctx, session)
}

func CapabilitiesAllowApply(report RegionReport) bool {
	if report.StyleMarkerCount < 0 || report.StyleMarkerCount > 1 {
		return false
	}
	if report.StyleMarkerCount == 0 && (report.TemplateVersion != 0 || report.ThemePublicID != "") {
		return false
	}
	if report.StyleMarkerCount == 1 &&
		(!supportedTemplateVersion(report.TemplateVersion) ||
			!themePublicIDPattern.MatchString(report.ThemePublicID)) {
		return false
	}
	required := []string{
		"home",
		"mainBoundary",
		"sidebar",
		"composer",
		"topFade",
		"bottomFade",
		"templateScope",
		"themeContrast",
	}
	for _, name := range required {
		if report.Regions[name] != RegionPass {
			return false
		}
	}
	for _, name := range []string{
		"composerUtilityBar",
		"conversationActivity",
		"conversationDiffResource",
		"suggestionCards",
		"projectPicker",
	} {
		if report.Regions[name] != RegionPass && report.Regions[name] != RegionNotPresent {
			return false
		}
	}
	return true
}

func reportAllowsCommit(report RegionReport, compiled CompiledTheme) bool {
	return report.StyleMarkerCount == 1 &&
		report.TemplateVersion == compiled.TemplateVersion &&
		report.ThemePublicID == compiled.ThemePublicID &&
		report.BackgroundLoaded &&
		CapabilitiesAllowApply(report)
}

// ReportAllowsTheme reports whether a renderer verification proves that the
// exact compiled theme is present. Session controllers use the same commit
// predicate as the original apply transaction.
func ReportAllowsTheme(report RegionReport, compiled CompiledTheme) bool {
	return reportAllowsCommit(report, compiled)
}
