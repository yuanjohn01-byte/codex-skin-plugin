package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

type fakeAdapter struct {
	state               Snapshot
	probe               RegionReport
	events              []string
	cancelOnApply       context.CancelFunc
	failOpen            bool
	failCapture         bool
	failApply           bool
	mutateBeforeFailure bool
	failVerify          bool
	failRestore         bool
	failOfficial        bool
	failVerifyOfficial  bool
	requireLiveRestore  bool
}

type primingAdapter struct {
	*fakeAdapter
	failPrime bool
}

type finalizingAdapter struct {
	*fakeAdapter
}

type restartConsentThemeAdapter struct {
	*fakeAdapter
}

type recoveryModeAdapter struct {
	*fakeAdapter
	themeModes    []string
	officialOpens int
}

type transitionThemeAdapter struct {
	*fakeAdapter
	target   CompiledTheme
	previous *CompiledTheme
}

func (adapter *transitionThemeAdapter) OpenVerifiedThemeTransitionSession(
	ctx context.Context,
	target CompiledTheme,
	previous *CompiledTheme,
) (Session, error) {
	adapter.target = target
	if previous != nil {
		copy := *previous
		adapter.previous = &copy
	}
	return adapter.fakeAdapter.OpenVerifiedSession(ctx)
}

type boundedVerificationAdapter struct {
	*fakeAdapter
	verification ThemeVerificationResult
	waitErr      error
}

func (adapter *boundedVerificationAdapter) WaitForThemeVerification(
	context.Context,
	Session,
	CompiledTheme,
) (ThemeVerificationResult, error) {
	adapter.events = append(adapter.events, "wait_verify")
	return adapter.verification, adapter.waitErr
}

func (adapter *restartConsentThemeAdapter) OpenVerifiedThemeSession(
	context.Context,
	CompiledTheme,
) (Session, error) {
	adapter.events = append(adapter.events, "open_theme_consent")
	return Session{}, ErrRestartConsent
}

func (adapter *recoveryModeAdapter) OpenVerifiedThemeSession(
	ctx context.Context,
	compiled CompiledTheme,
) (Session, error) {
	adapter.themeModes = append(adapter.themeModes, compiled.AppearanceMode)
	return adapter.fakeAdapter.OpenVerifiedSession(ctx)
}

func (adapter *recoveryModeAdapter) OpenVerifiedOfficialSession(ctx context.Context) (Session, error) {
	adapter.officialOpens++
	return adapter.fakeAdapter.OpenVerifiedSession(ctx)
}

func (adapter *finalizingAdapter) FinalizeOfficialRollback(context.Context, Session) error {
	adapter.events = append(adapter.events, "finalize_official")
	return nil
}

func (adapter *primingAdapter) Prime(_ context.Context, _ Session, compiled CompiledTheme) error {
	adapter.events = append(adapter.events, "prime")
	if adapter.failPrime {
		return errors.New("prime rejected")
	}
	if !adapter.state.StylePresent {
		return nil
	}
	if adapter.state.ThemePublicID != compiled.ThemePublicID ||
		adapter.state.ThemeVersion != compiled.ThemeVersion ||
		adapter.state.BackgroundDataURL != compiled.BackgroundDataURL {
		return ErrCapabilityBlocked
	}
	if adapter.state.TemplateVersion == compiled.TemplateVersion &&
		adapter.state.StyleText == compiled.StyleText {
		return nil
	}
	if adapter.state.TemplateVersion == TemplateVersion-1 &&
		adapter.state.StyleText == compiled.PreviousStyleText {
		return nil
	}
	if adapter.state.TemplateVersion == TemplateVersion-2 &&
		adapter.state.StyleText == compiled.MigrationStyleText {
		return nil
	}
	if adapter.state.TemplateVersion == MinimumTemplateVersion &&
		adapter.state.StyleText == compiled.LegacyStyleText {
		return nil
	}
	return ErrCapabilityBlocked
}

func (adapter *fakeAdapter) OpenVerifiedSession(context.Context) (Session, error) {
	adapter.events = append(adapter.events, "open")
	if adapter.failOpen {
		return Session{}, errors.New("identity rejected")
	}
	return Session{
		Identity: Identity{
			Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
			Version: "26.721.41059", ExecutableHash: strings.Repeat("a", 64), ProcessID: 123, ProcessStartID: "start-123",
		},
		OpaqueID: "fake-session",
	}, nil
}

func (adapter *fakeAdapter) Probe(context.Context, Session) (RegionReport, error) {
	adapter.events = append(adapter.events, "probe")
	report := adapter.probe
	if adapter.state.StylePresent {
		report.StyleMarkerCount = 1
		report.ThemePublicID = adapter.state.ThemePublicID
		report.TemplateVersion = adapter.state.TemplateVersion
	} else {
		report.StyleMarkerCount = 0
		report.ThemePublicID = ""
		report.TemplateVersion = 0
	}
	return report, nil
}

func (adapter *fakeAdapter) Capture(context.Context, Session) (Snapshot, error) {
	adapter.events = append(adapter.events, "capture")
	if adapter.failCapture {
		return Snapshot{}, errors.New("capture rejected")
	}
	return adapter.state, nil
}

func (adapter *fakeAdapter) Apply(_ context.Context, _ Session, compiled CompiledTheme) error {
	adapter.events = append(adapter.events, "apply")
	next := Snapshot{
		StylePresent: true, StyleText: compiled.StyleText, ThemePublicID: compiled.ThemePublicID,
		ThemeVersion: compiled.ThemeVersion, TemplateVersion: compiled.TemplateVersion,
		BackgroundDataURL: compiled.BackgroundDataURL, AppearanceMode: compiled.AppearanceMode,
	}
	if adapter.cancelOnApply != nil {
		adapter.state = next
		adapter.cancelOnApply()
		return context.Canceled
	}
	if adapter.failApply {
		if adapter.mutateBeforeFailure {
			adapter.state = next
		}
		return errors.New("apply rejected")
	}
	adapter.state = next
	return nil
}

func (adapter *fakeAdapter) Verify(_ context.Context, _ Session, compiled CompiledTheme) (RegionReport, error) {
	adapter.events = append(adapter.events, "verify")
	if adapter.failVerify {
		return RegionReport{}, errors.New("marker mismatch")
	}
	report := passingReport()
	report.StyleMarkerCount = 1
	report.ThemePublicID = compiled.ThemePublicID
	report.TemplateVersion = compiled.TemplateVersion
	report.BackgroundLoaded = true
	return report, nil
}

func (adapter *fakeAdapter) Restore(ctx context.Context, _ Session, snapshot Snapshot) error {
	adapter.events = append(adapter.events, "rollback")
	if adapter.requireLiveRestore && ctx.Err() != nil {
		return ctx.Err()
	}
	if adapter.failRestore {
		return errors.New("rollback rejected")
	}
	adapter.state = snapshot
	return nil
}

func (adapter *fakeAdapter) RestoreOfficial(context.Context, Session) error {
	adapter.events = append(adapter.events, "restore_official")
	if adapter.failOfficial {
		return errors.New("official restore rejected")
	}
	adapter.state = Snapshot{}
	return nil
}

func (adapter *fakeAdapter) VerifyOfficial(context.Context, Session) error {
	adapter.events = append(adapter.events, "verify_official")
	if adapter.failVerifyOfficial || adapter.state.StylePresent {
		return errors.New("theme marker remains")
	}
	return nil
}

func (adapter *fakeAdapter) Close(context.Context, Session) error {
	adapter.events = append(adapter.events, "close")
	return nil
}

func TestApplyVerifiedCommitsOnlyAfterVerification(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	adapter := &fakeAdapter{probe: passingReport()}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := instance.ApplyVerified(context.Background(), verified)
	if err != nil {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if result.ThemePublicID != "100001" || result.ThemeVersion != "1.0.0" {
		t.Fatalf("result = %#v", result)
	}
	wantEvents := []string{"open", "capture", "probe", "apply", "verify", "close"}
	if strings.Join(adapter.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", adapter.events, wantEvents)
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found {
		t.Fatalf("ReadDesired() = %#v, %v, %v", desired, found, err)
	}
	if desired.PackageSHA256 != verified.PackageSHA256 || desired.TemplateVersion != TemplateVersion {
		t.Fatalf("desired = %#v", desired)
	}
	cache := filepath.Join(store.Root(), "themes", "100001", "1.0.0", verified.PackageSHA256)
	if _, err := os.Stat(filepath.Join(cache, "manifest.json")); err != nil {
		t.Fatalf("committed cache: %v", err)
	}
	for _, name := range []string{
		"package.cskin", "release-descriptor.json", "release-descriptor.sig", "verification-keyset.json",
	} {
		if _, err := os.Stat(filepath.Join(cache, name)); err != nil {
			t.Fatalf("committed verification bundle %s: %v", name, err)
		}
	}
	if _, err := theme.VerifyCached(cache); err != nil {
		t.Fatalf("VerifyCached() error = %v", err)
	}
	if strings.Contains(adapter.state.StyleText, "%!") {
		t.Fatalf("compiled template contains fmt artifact")
	}
	if !strings.Contains(adapter.state.StyleText, `:root[data-codex-skin="active"] {`) ||
		!strings.Contains(adapter.state.StyleText, "background: rgb(14 18 24 /") ||
		strings.Contains(adapter.state.StyleText, "rgb(data-codex-skin") {
		t.Fatalf("compiled template token substitution is invalid")
	}
	if !strings.Contains(adapter.state.StyleText, `main[data-codex-skin-main="true"]`) ||
		!strings.Contains(adapter.state.StyleText, "aside.app-shell-left-panel") ||
		!strings.Contains(adapter.state.StyleText, "data-codex-skin-composer") ||
		!strings.Contains(adapter.state.StyleText, ".group\\/home-suggestions") ||
		!strings.Contains(adapter.state.StyleText, ".app-shell-main-content-top-fade") ||
		!strings.Contains(adapter.state.StyleText, ":has(") ||
		!strings.Contains(adapter.state.StyleText, ".thread-scroll-container") ||
		!strings.Contains(adapter.state.StyleText, ".bg-gradient-to-t.from-token-main-surface-primary") ||
		!strings.Contains(adapter.state.StyleText, `[class~="from-surface"][class~="via-surface"]`) ||
		!strings.Contains(adapter.state.StyleText, "aside.app-shell-left-panel::after") ||
		!strings.Contains(adapter.state.StyleText, "content: none !important") ||
		!strings.Contains(adapter.state.StyleText, "background-color: var(--color-surface) !important") ||
		!strings.Contains(adapter.state.StyleText, "background-image: none !important") ||
		!strings.Contains(adapter.state.StyleText, "transition: none !important") ||
		strings.Contains(adapter.state.StyleText, `main[data-codex-skin-main="true"] [class~="text-token-text-primary"],
:root[data-codex-skin="active"] main[data-codex-skin-main="true"] [class~="text-token-foreground"]`) {
		t.Fatalf("compiled template is missing fixed region selectors")
	}
	activeV12 := strings.SplitN(adapter.state.StyleText, "/* V11 source is retained", 2)[0]
	for _, forbidden := range []string{
		`:root[data-codex-skin="active"] body {`,
		`:root[data-codex-skin="active"]:has(`,
		`:has(
      main`,
		`) main[data-codex-skin-main="true"] {`,
	} {
		if strings.Contains(activeV12, forbidden) {
			t.Fatalf("compiled template v12 leaks into a native surface: %q", forbidden)
		}
	}
	if !strings.HasPrefix(adapter.state.BackgroundDataURL, "data:image/png;base64,") ||
		strings.Contains(adapter.state.StyleText, "data:image/") {
		t.Fatalf("compiled background transport is invalid")
	}
}

func TestApplyVerifiedPassesCommittedThemeToTransitionOpener(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	adapter := &transitionThemeAdapter{
		fakeAdapter: &fakeAdapter{probe: passingReport()},
	}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("first ApplyVerified() error = %v", err)
	}
	if adapter.previous != nil {
		t.Fatalf("first transition previous = %#v, want nil", adapter.previous)
	}
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("second ApplyVerified() error = %v", err)
	}
	if adapter.previous == nil {
		t.Fatal("transition opener did not receive the committed prior theme")
	}
	if adapter.previous.ThemePublicID != "100001" ||
		adapter.previous.AppearanceMode != "dark" ||
		adapter.target.ThemePublicID != "100001" {
		t.Fatalf("transition target=%#v previous=%#v", adapter.target, adapter.previous)
	}
}

func TestApplyFailureRollsBackAndDoesNotCommitDesired(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	before := Snapshot{
		StylePresent: true, StyleText: "trusted prior style", ThemePublicID: "199999",
		ThemeVersion: "2.0.0", TemplateVersion: 1,
		BackgroundDataURL: "data:image/png;base64,AA==", AppearanceMode: "dark",
	}
	adapter := &fakeAdapter{state: before, probe: passingReport(), failVerify: true}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(context.Background(), verified)
	if err == nil || !errors.Is(err, ErrVerifyFailed) && !strings.Contains(err.Error(), "marker mismatch") {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if adapter.state != before {
		t.Fatalf("state after rollback = %#v, want %#v", adapter.state, before)
	}
	if _, found, err := store.ReadDesired(); err != nil || found {
		t.Fatalf("desired after failure found=%v err=%v", found, err)
	}
	if !strings.Contains(strings.Join(adapter.events, ","), "verify,rollback") {
		t.Fatalf("events = %v", adapter.events)
	}
}

func TestApplyVerifiedPersistsBoundedVerificationSummaryBeforeRollback(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	report := passingReport()
	report.StyleMarkerCount = 1
	report.TemplateVersion = TemplateVersion
	report.ThemePublicID = verified.Manifest.ThemePublicID
	report.BackgroundLoaded = false
	report.Regions["sidebar"] = RegionFail
	adapter := &boundedVerificationAdapter{
		fakeAdapter: &fakeAdapter{probe: passingReport()},
		verification: ThemeVerificationResult{
			Report: report, Attempts: 4, ReapplyAttempted: true, ProbeCompleted: true,
		},
		waitErr: ErrVerifyFailed,
	}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.ApplyVerified(context.Background(), verified); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "state", "operations"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("operation journals = %#v, error = %v", entries, err)
	}
	var journal Journal
	found, err := readJSON(filepath.Join(store.Root(), "state", "operations", entries[0].Name()), &journal)
	if err != nil || !found || journal.Verification == nil {
		t.Fatalf("journal = %#v, found=%t, error=%v", journal, found, err)
	}
	if journal.ErrorCode != "CS-VERIFY-001" || journal.Verification.Attempts != 4 ||
		!journal.Verification.ReapplyAttempted || !journal.Verification.ProbeCompleted ||
		journal.Verification.BackgroundLoaded ||
		journal.Verification.Regions["sidebar"] != RegionFail {
		t.Fatalf("verification journal = %#v", journal)
	}
	if got := strings.Join(adapter.events, ","); got != "open,capture,probe,apply,wait_verify,rollback,verify_official,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestJournalRejectsUnallowlistedVerificationFields(t *testing.T) {
	store := testStore(t)
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	err = store.WriteJournal(Journal{
		OperationID: operationID, Kind: "apply", Stage: "verify", Status: "running",
		Verification: &VerificationSummary{
			Attempts: 1,
			Regions:  map[string]RegionStatus{"untrusted-dom-text": RegionPass},
		},
	})
	if !errors.Is(err, ErrStateUnsafe) {
		t.Fatalf("WriteJournal() error = %v, want state safety rejection", err)
	}
}

func TestRestorePreservesInterruptedJournalStageInRedactedTrace(t *testing.T) {
	store := testStore(t)
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		OperationID: operationID, Kind: "apply", Stage: "verify", Status: "running",
		ThemePublicID: "100001", ThemeVersion: "1.0.0",
	}
	if err := store.WriteJournal(journal); err != nil {
		t.Fatal(err)
	}
	instance, err := New(store, &fakeAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.resolveInterruptedJournals("op_ffffffffffffffff"); err != nil {
		t.Fatal(err)
	}
	var restored Journal
	found, err := readJSON(filepath.Join(store.Root(), "state", "operations", operationID+".json"), &restored)
	if err != nil || !found {
		t.Fatalf("restored journal found=%t err=%v", found, err)
	}
	if restored.Stage != "official_restored" || restored.InterruptedStage != "verify" ||
		restored.ErrorCode != "CS-INTERRUPTED-RESTORED-001" {
		t.Fatalf("restored journal = %#v", restored)
	}
	if len(restored.Trace) < 2 || restored.Trace[0].Stage != "verify" ||
		restored.Trace[len(restored.Trace)-1].Stage != "official_restored" {
		t.Fatalf("redacted trace = %#v", restored.Trace)
	}
}

func TestRollbackFailureIsBlocking(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	adapter := &fakeAdapter{probe: passingReport(), failApply: true, failRestore: true}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(context.Background(), verified)
	if !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	running, readErr := store.RunningJournals()
	if readErr != nil || len(running) != 1 || running[0].Stage != "apply" {
		t.Fatalf("running rollback journal = %#v, error = %v", running, readErr)
	}
	adapter.failRestore = false
	if err := instance.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}
	running, readErr = store.RunningJournals()
	if readErr != nil || len(running) != 0 {
		t.Fatalf("running journals after recovery = %#v, error = %v", running, readErr)
	}
}

func TestIncompleteRollbackAfterCompleteStageRemainsRecoverable(t *testing.T) {
	store := testStore(t)
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	recoveryID, err := store.NewRecoveryID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{}
	if err := store.WriteRecoveryPoint(RecoveryPoint{
		RecoveryID: recoveryID, OperationID: operationID, CapturedAt: canonicalNow(),
	}, snapshot); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		OperationID: operationID, RecoveryID: recoveryID,
		Kind: "apply", Stage: "complete", Status: "completed",
	}
	adapter := &fakeAdapter{failRestore: true}
	instance, _ := New(store, adapter)
	sentinel := errors.New("complete journal write failed")
	if err := instance.failWithRollback(
		context.Background(),
		Session{OpaqueID: "fake-session"},
		snapshot,
		nil,
		journal,
		"CS-STATE-001",
		sentinel,
	); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("failWithRollback() error = %v", err)
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 1 || running[0].Stage != "commit" {
		t.Fatalf("recoverable journal = %#v, error = %v", running, err)
	}
	adapter.failRestore = false
	if err := instance.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}
	running, err = store.RunningJournals()
	if err != nil || len(running) != 0 {
		t.Fatalf("running journals after recovery = %#v, error = %v", running, err)
	}
}

func TestCanceledApplyUsesDetachedVerifiedRollback(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &fakeAdapter{
		probe:              passingReport(),
		cancelOnApply:      cancel,
		requireLiveRestore: true,
	}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(ctx, verified)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if adapter.state.StylePresent {
		t.Fatalf("canceled apply left renderer mutated: %#v", adapter.state)
	}
	if _, found, err := store.ReadDesired(); err != nil || found {
		t.Fatalf("desired after canceled apply found=%v error=%v", found, err)
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 0 {
		t.Fatalf("running journals = %#v, error=%v", running, err)
	}
}

func TestRollbackRestoresPreviousDesiredState(t *testing.T) {
	store := testStore(t)
	previous := DesiredTheme{
		SchemaVersion: StateSchemaVersion,
		ThemePublicID: "199999", ThemeVersion: "2.0.0", PackageSHA256: strings.Repeat("a", 64),
		TemplateVersion: TemplateVersion, AppliedAt: "2026-07-26T05:00:00Z",
	}
	current := DesiredTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: strings.Repeat("b", 64),
		TemplateVersion: TemplateVersion, AppliedAt: "2026-07-26T06:00:00Z",
	}
	if err := store.WriteDesired(current); err != nil {
		t.Fatal(err)
	}
	before := Snapshot{
		StylePresent: true, StyleText: "trusted previous style",
		BackgroundDataURL: "data:image/png;base64,AA==",
		ThemePublicID:     "199999", ThemeVersion: "2.0.0", TemplateVersion: TemplateVersion,
		AppearanceMode: "dark",
	}
	adapter := &fakeAdapter{state: Snapshot{
		StylePresent: true, StyleText: "partially committed style",
		BackgroundDataURL: "data:image/png;base64,AQ==",
		ThemePublicID:     "100001", ThemeVersion: "1.0.0", TemplateVersion: TemplateVersion,
		AppearanceMode: "dark",
	}}
	instance, _ := New(store, adapter)
	operationID, _ := store.NewOperationID()
	journal := Journal{
		OperationID: operationID, Kind: "apply", Stage: "commit", Status: "running",
		ThemePublicID: "100001", ThemeVersion: "1.0.0",
	}
	if err := store.WriteJournal(journal); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("post-commit state failure")
	if err := instance.failWithRollback(
		context.Background(),
		Session{OpaqueID: "fake-session"},
		before,
		&previous,
		journal,
		"CS-STATE-001",
		sentinel,
	); !errors.Is(err, sentinel) {
		t.Fatalf("failWithRollback() error = %v", err)
	}
	if adapter.state != before {
		t.Fatalf("restored snapshot = %#v, want %#v", adapter.state, before)
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found || desired != previous {
		t.Fatalf("restored desired = %#v, found=%v, error=%v", desired, found, err)
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 0 {
		t.Fatalf("running journals = %#v, error=%v", running, err)
	}
}

func TestCompileRejectsUnsupportedMinimumEngineBeforeRenderer(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	verified.Manifest.Compatibility.MinEngineVersion = "999.0.0"
	store := testStore(t)
	adapter := &fakeAdapter{probe: passingReport()}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(context.Background(), verified)
	if !errors.Is(err, theme.ErrEngineIncompatible) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if len(adapter.events) != 0 {
		t.Fatalf("renderer opened for incompatible theme: %v", adapter.events)
	}
}

func TestCapabilityFailureRollsBackBeforeApply(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	report := passingReport()
	report.Regions["sidebar"] = RegionFail
	adapter := &finalizingAdapter{fakeAdapter: &fakeAdapter{probe: report}}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(context.Background(), verified)
	if !errors.Is(err, ErrCapabilityBlocked) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if got := strings.Join(adapter.events, ","); got != "open,capture,probe,rollback,verify_official,finalize_official,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestPrimeFailureRestoresTrustedPreviousTheme(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	base := &fakeAdapter{probe: passingReport()}
	adapter := &primingAdapter{fakeAdapter: base}
	instance, _ := New(store, adapter)
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("first ApplyVerified() error = %v", err)
	}
	previous := adapter.state
	adapter.events = nil
	adapter.failPrime = true
	if _, err := instance.ApplyVerified(context.Background(), verified); err == nil {
		t.Fatal("prime failure was accepted")
	}
	if adapter.state != previous {
		t.Fatalf("state after prime rollback = %#v, want %#v", adapter.state, previous)
	}
	if got := strings.Join(adapter.events, ","); got != "open,prime,rollback,capture,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestCaptureFailureRestoresOfficialPreflightState(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	adapter := &finalizingAdapter{fakeAdapter: &fakeAdapter{probe: passingReport(), failCapture: true}}
	instance, _ := New(store, adapter)
	if _, err := instance.ApplyVerified(context.Background(), verified); err == nil {
		t.Fatal("capture failure was accepted")
	}
	if adapter.state.StylePresent {
		t.Fatalf("state after capture rollback = %#v", adapter.state)
	}
	if got := strings.Join(adapter.events, ","); got != "open,capture,rollback,verify_official,finalize_official,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestOpenFailureKeepsPrepareJournalRecoverable(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	base := &fakeAdapter{probe: passingReport(), failOpen: true}
	adapter := &finalizingAdapter{fakeAdapter: base}
	instance, _ := New(store, adapter)
	if _, err := instance.ApplyVerified(context.Background(), verified); err == nil {
		t.Fatal("open failure was accepted")
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 1 || running[0].Stage != "prepare" || running[0].RecoveryID == "" {
		t.Fatalf("recoverable prepare journal = %#v, error = %v", running, err)
	}
	base.failOpen = false
	if err := instance.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}
	running, err = store.RunningJournals()
	if err != nil || len(running) != 0 {
		t.Fatalf("running journals after recovery = %#v, error = %v", running, err)
	}
	if got := strings.Join(adapter.events, ","); got != "open,open,restore_official,verify_official,finalize_official,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestCapabilitiesRecordHomeUtilityBarDiagnosticsWithoutRollingBackThemedShell(t *testing.T) {
	report := passingReport()
	report.Regions["composerUtilityBar"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without the home-only utility bar was rejected")
	}

	report.Regions["composerUtilityBar"] = RegionFail
	if !CapabilitiesAllowApply(report) {
		t.Fatal("a diagnostic-only utility bar failure rolled back the verified shell")
	}
}

func TestCapabilitiesRecordConversationActivityDiagnosticsWithoutRollingBackThemedShell(t *testing.T) {
	report := passingReport()
	report.Regions["conversationActivity"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without a rendered activity disclosure was rejected")
	}

	report.Regions["conversationActivity"] = RegionFail
	if !CapabilitiesAllowApply(report) {
		t.Fatal("a diagnostic-only activity failure rolled back the verified shell")
	}
}

func TestCapabilitiesRecordDiffResourceDiagnosticsWithoutRollingBackThemedShell(t *testing.T) {
	report := passingReport()
	report.Regions["conversationDiffResource"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without a rendered diff resource card was rejected")
	}

	report.Regions["conversationDiffResource"] = RegionFail
	if !CapabilitiesAllowApply(report) {
		t.Fatal("a diagnostic-only resource card failure rolled back the verified shell")
	}
}

func TestCapabilitiesAllowCurrentHomeWithoutLegacyComposerOrTopFade(t *testing.T) {
	report := passingReport()
	report.StyleMarkerCount = 1
	report.TemplateVersion = TemplateVersion
	report.ThemePublicID = "100012"
	report.BackgroundLoaded = true
	report.Regions = map[string]RegionStatus{
		"shellMain":                RegionPass,
		"sidebar":                  RegionPass,
		"headerTint":               RegionPass,
		"templateScope":            RegionPass,
		"themeContrast":            RegionPass,
		"home":                     RegionPass,
		"mainBoundary":             RegionFail,
		"composer":                 RegionNotPresent,
		"topFade":                  RegionFail,
		"bottomFade":               RegionNotPresent,
		"composerUtilityBar":       RegionNotPresent,
		"conversationActivity":     RegionNotPresent,
		"conversationDiffResource": RegionNotPresent,
		"suggestionCards":          RegionPass,
		"projectPicker":            RegionNotPresent,
	}
	if !CapabilitiesAllowApply(report) {
		t.Fatalf("current Home fixture was rejected: %#v", report)
	}
}

func TestRestoreOfficialIsOfflineIdempotentAndClearsDesired(t *testing.T) {
	store := testStore(t)
	if err := store.WriteDesired(DesiredTheme{
		ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: strings.Repeat("a", 64),
		TemplateVersion: 1, AppliedAt: "2026-07-26T06:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{
		state: Snapshot{
			StylePresent: true, StyleText: "style", ThemePublicID: "100001",
			ThemeVersion: "1.0.0", TemplateVersion: 1,
			BackgroundDataURL: "data:image/png;base64,AA==", AppearanceMode: "dark",
		},
		probe: passingReport(),
	}
	instance, _ := New(store, adapter)
	first, err := instance.RestoreOfficial(context.Background())
	if err != nil {
		t.Fatalf("first RestoreOfficial() error = %v", err)
	}
	if !first.WasThemed || adapter.state.StylePresent {
		t.Fatalf("first result=%#v state=%#v", first, adapter.state)
	}
	if _, found, err := store.ReadDesired(); err != nil || found {
		t.Fatalf("desired after restore found=%v err=%v", found, err)
	}
	second, err := instance.RestoreOfficial(context.Background())
	if err != nil || second.WasThemed {
		t.Fatalf("second RestoreOfficial() = %#v, %v", second, err)
	}
}

func TestCachedPackageIsRevalidatedBeforeASecondApply(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	base := &fakeAdapter{probe: passingReport()}
	adapter := &primingAdapter{fakeAdapter: base}
	instance, _ := New(store, adapter)
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("first ApplyVerified() error = %v", err)
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found {
		t.Fatalf("ReadDesired() = %#v, %v, %v", desired, found, err)
	}
	cache, err := store.ThemeCachePath(desired)
	if err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(cache, "package.cskin")
	content, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-1] ^= 0xff
	if err := os.WriteFile(packagePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	eventsBefore := len(adapter.events)
	if _, err := instance.ApplyVerified(context.Background(), verified); err == nil {
		t.Fatal("tampered cached package was accepted")
	}
	if strings.Contains(strings.Join(adapter.events[eventsBefore:], ","), "capture") ||
		strings.Contains(strings.Join(adapter.events[eventsBefore:], ","), "apply") {
		t.Fatalf("renderer mutated after cache verification failure: %v", adapter.events[eventsBefore:])
	}
}

func TestApplyMigratesVerifiedPreviousTemplateStateToCurrentTemplate(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	base := &fakeAdapter{probe: passingReport()}
	adapter := &primingAdapter{fakeAdapter: base}
	instance, _ := New(store, adapter)
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("first ApplyVerified() error = %v", err)
	}
	desired, found, err := store.ReadDesired()
	if err != nil || !found {
		t.Fatalf("ReadDesired() = %#v, %v, %v", desired, found, err)
	}
	cache, err := store.ThemeCachePath(desired)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileTheme(verified, cache)
	if err != nil {
		t.Fatal(err)
	}
	desired.TemplateVersion = TemplateVersion - 1
	if err := store.WriteDesired(desired); err != nil {
		t.Fatal(err)
	}
	adapter.state = Snapshot{
		StylePresent: true, StyleText: compiled.PreviousStyleText,
		BackgroundDataURL: compiled.BackgroundDataURL,
		ThemePublicID:     compiled.ThemePublicID,
		ThemeVersion:      compiled.ThemeVersion,
		TemplateVersion:   TemplateVersion - 1,
		AppearanceMode:    compiled.AppearanceMode,
	}
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("migration ApplyVerified() error = %v", err)
	}
	migrated, found, err := store.ReadDesired()
	if err != nil || !found || migrated.TemplateVersion != TemplateVersion {
		t.Fatalf("migrated desired = %#v, found=%v, err=%v", migrated, found, err)
	}
	if adapter.state.TemplateVersion != TemplateVersion ||
		adapter.state.StyleText != compiled.StyleText {
		t.Fatalf("migrated renderer state = %#v", adapter.state)
	}
}

func TestInterruptedApplyStagesRestoreDurableLastKnownGood(t *testing.T) {
	for _, stage := range []string{"prepare", "backup", "apply", "verify", "commit"} {
		t.Run(stage, func(t *testing.T) {
			store := testStore(t)
			previous := DesiredTheme{
				SchemaVersion: 1, ThemePublicID: "199999", ThemeVersion: "2.0.0", PackageSHA256: strings.Repeat("a", 64),
				TemplateVersion: 1, AppliedAt: "2026-07-26T05:00:00Z",
			}
			if err := store.WriteDesired(DesiredTheme{
				ThemePublicID: "100001", ThemeVersion: "1.0.0", PackageSHA256: strings.Repeat("b", 64),
				TemplateVersion: 1, AppliedAt: "2026-07-26T06:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			operationID, _ := store.NewOperationID()
			recoveryID, _ := store.NewRecoveryID()
			before := Snapshot{
				StylePresent: true, StyleText: "trusted previous fixed template",
				BackgroundDataURL: "data:image/png;base64,AA==",
				ThemePublicID:     "199999", ThemeVersion: "2.0.0", TemplateVersion: 1,
				AppearanceMode: "dark",
			}
			if err := store.WriteRecoveryPoint(RecoveryPoint{
				RecoveryID: recoveryID, OperationID: operationID, CapturedAt: "2026-07-26T05:59:00Z",
				WasThemed: true, PreviousDesired: &previous,
			}, before); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteJournal(Journal{
				OperationID: operationID, Kind: "apply", Stage: stage, Status: "running",
				ThemePublicID: "100001", ThemeVersion: "1.0.0", RecoveryID: recoveryID,
				StartedAt: "2026-07-26T06:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			adapter := &fakeAdapter{
				state: Snapshot{
					StylePresent: true, StyleText: "partially applied new template",
					BackgroundDataURL: "data:image/png;base64,AQ==",
					ThemePublicID:     "100001", ThemeVersion: "1.0.0", TemplateVersion: 1,
					AppearanceMode: "dark",
				},
				probe: passingReport(),
			}
			instance, _ := New(store, adapter)
			if err := instance.RecoverInterrupted(context.Background()); err != nil {
				t.Fatalf("RecoverInterrupted() error = %v", err)
			}
			if adapter.state != before {
				t.Fatalf("restored state = %#v, want %#v", adapter.state, before)
			}
			desired, found, err := store.ReadDesired()
			if err != nil || !found || desired != previous {
				t.Fatalf("desired = %#v, found=%v, err=%v", desired, found, err)
			}
			running, err := store.RunningJournals()
			if err != nil || len(running) != 0 {
				t.Fatalf("running journals = %#v, err=%v", running, err)
			}
		})
	}
}

func TestInterruptedRecoveryOpensTheNativeModeRequiredByTheSnapshot(t *testing.T) {
	tests := []struct {
		name             string
		snapshot         Snapshot
		wantThemeMode    string
		wantOfficialOpen int
	}{
		{
			name: "themed snapshot reopens with its dark native palette",
			snapshot: Snapshot{
				StylePresent: true, StyleText: "trusted dark style",
				BackgroundDataURL: "data:image/png;base64,AA==",
				ThemePublicID:     "199999", ThemeVersion: "2.0.0", TemplateVersion: 1,
				AppearanceMode: "dark",
			},
			wantThemeMode: "dark",
		},
		{
			name:             "official snapshot consumes the original native preference",
			snapshot:         Snapshot{},
			wantOfficialOpen: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := testStore(t)
			operationID, err := store.NewOperationID()
			if err != nil {
				t.Fatal(err)
			}
			recoveryID, err := store.NewRecoveryID()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteRecoveryPoint(RecoveryPoint{
				RecoveryID: recoveryID, OperationID: operationID, CapturedAt: canonicalNow(),
				WasThemed: testCase.snapshot.StylePresent,
			}, testCase.snapshot); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteJournal(Journal{
				OperationID: operationID, Kind: "apply", Stage: "apply", Status: "running",
				ThemePublicID: "100001", ThemeVersion: "1.0.0", RecoveryID: recoveryID,
			}); err != nil {
				t.Fatal(err)
			}
			adapter := &recoveryModeAdapter{fakeAdapter: &fakeAdapter{
				state: Snapshot{
					StylePresent: true, StyleText: "partially applied style",
					BackgroundDataURL: "data:image/png;base64,AQ==",
					ThemePublicID:     "100001", ThemeVersion: "1.0.0", TemplateVersion: 1,
					AppearanceMode: "light",
				},
				probe: passingReport(),
			}}
			instance, err := New(store, adapter)
			if err != nil {
				t.Fatal(err)
			}
			if err := instance.RecoverInterrupted(context.Background()); err != nil {
				t.Fatalf("RecoverInterrupted() error = %v", err)
			}
			if got := strings.Join(adapter.themeModes, ","); got != testCase.wantThemeMode {
				t.Fatalf("theme session modes = %q, want %q", got, testCase.wantThemeMode)
			}
			if adapter.officialOpens != testCase.wantOfficialOpen {
				t.Fatalf("official session opens = %d, want %d", adapter.officialOpens, testCase.wantOfficialOpen)
			}
			if adapter.state != testCase.snapshot {
				t.Fatalf("recovered state = %#v, want %#v", adapter.state, testCase.snapshot)
			}
		})
	}
}

func TestLegacyV8VerificationJournalIsRetiredBeforeFreshApply(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	recoveryID, err := store.NewRecoveryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRecoveryPoint(RecoveryPoint{
		RecoveryID: recoveryID, OperationID: operationID, CapturedAt: canonicalNow(),
	}, Snapshot{}); err != nil {
		t.Fatal(err)
	}
	legacy := Journal{
		OperationID: operationID, Kind: "apply", Stage: "verify", Status: "running",
		ThemePublicID: "100001", ThemeVersion: "1.0.0", RecoveryID: recoveryID,
		ErrorCode: "CS-VERIFY-001", Verification: legacyV8VerificationSummary(),
	}
	if err := store.WriteJournal(legacy); err != nil {
		t.Fatal(err)
	}

	adapter := &fakeAdapter{probe: passingReport()}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.ApplyVerified(context.Background(), verified); err != nil {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if strings.Contains(strings.Join(adapter.events, ","), "rollback") ||
		strings.Contains(strings.Join(adapter.events, ","), "restore_official") {
		t.Fatalf("legacy verification unexpectedly reopened old recovery: %v", adapter.events)
	}

	var migrated Journal
	found, err := readJSON(filepath.Join(store.Root(), "state", "operations", operationID+".json"), &migrated)
	if err != nil || !found {
		t.Fatalf("migrated journal found=%t err=%v", found, err)
	}
	if migrated.Status != "failed" || migrated.Stage != "legacy_migrated" ||
		migrated.InterruptedStage != "verify" || migrated.ErrorCode != legacyV8VerificationMigrationCode {
		t.Fatalf("migrated journal = %#v", migrated)
	}
	point, _, err := store.ReadRecoveryPoint(recoveryID)
	if err != nil || point.RecoveryID != recoveryID {
		t.Fatalf("legacy recovery point was not retained: %#v err=%v", point, err)
	}
}

func TestCurrentVerificationJournalStillUsesFailClosedRecovery(t *testing.T) {
	store := testStore(t)
	operationID, err := store.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	recoveryID, err := store.NewRecoveryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteRecoveryPoint(RecoveryPoint{
		RecoveryID: recoveryID, OperationID: operationID, CapturedAt: canonicalNow(),
	}, Snapshot{}); err != nil {
		t.Fatal(err)
	}
	current := Journal{
		OperationID: operationID, Kind: "apply", Stage: "verify", Status: "running",
		ThemePublicID: "100001", ThemeVersion: "1.0.0", RecoveryID: recoveryID,
		ErrorCode: "CS-VERIFY-001", Verification: legacyV8VerificationSummary(),
	}
	current.Verification.TemplateVersion = TemplateVersion
	if err := store.WriteJournal(current); err != nil {
		t.Fatal(err)
	}

	adapter := &fakeAdapter{probe: passingReport(), failOfficial: true}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.RecoverInterrupted(context.Background()); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("RecoverInterrupted() error = %v, want rollback failure", err)
	}
	running, err := store.RunningJournals()
	if err != nil || len(running) != 1 || running[0].OperationID != operationID {
		t.Fatalf("current verification journal was not kept recoverable: %#v err=%v", running, err)
	}
}

func TestInterruptedPreMutationStageDoesNotTouchRenderer(t *testing.T) {
	store := testStore(t)
	operationID, _ := store.NewOperationID()
	if err := store.WriteJournal(Journal{
		OperationID: operationID, Kind: "apply", Stage: "stage", Status: "running",
		ThemePublicID: "100001", ThemeVersion: "1.0.0", StartedAt: "2026-07-26T06:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{probe: passingReport()}
	instance, _ := New(store, adapter)
	if err := instance.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.events) != 0 {
		t.Fatalf("pre-mutation recovery opened renderer: %v", adapter.events)
	}
}

func TestRestartConsentLeavesNoRecoverableApplyJournal(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	adapter := &restartConsentThemeAdapter{fakeAdapter: &fakeAdapter{probe: passingReport()}}
	instance, err := New(store, adapter)
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.ApplyVerified(context.Background(), verified)
	if !errors.Is(err, ErrRestartConsent) {
		t.Fatalf("ApplyVerified() error = %v, want ErrRestartConsent", err)
	}
	running, readErr := store.RunningJournals()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(running) != 0 {
		t.Fatalf("restart consent left a recoverable journal: %#v", running)
	}
	entries, readErr := os.ReadDir(filepath.Join(store.Root(), "state", "operations"))
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("restart consent journal entries = %#v, error = %v", entries, readErr)
	}
	var journal Journal
	if _, readErr := readJSON(filepath.Join(store.Root(), "state", "operations", entries[0].Name()), &journal); readErr != nil {
		t.Fatal(readErr)
	}
	if journal.Status != "pending_confirmation" || journal.Stage != "restart_confirmation" || journal.ErrorCode != "" {
		t.Fatalf("restart consent journal was reported as a failure: %#v", journal)
	}
	if strings.Join(adapter.events, ",") != "open_theme_consent" {
		t.Fatalf("restart consent touched the renderer: %v", adapter.events)
	}
}

func TestStoreRejectsPluginOverlapSymlinkAndConcurrentWriter(t *testing.T) {
	parent := t.TempDir()
	cache := filepath.Join(parent, "plugin")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(filepath.Join(cache, "state"), cache); !errors.Is(err, ErrStateUnsafe) {
		t.Fatalf("plugin overlap error = %v", err)
	}

	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(link, cache); !errors.Is(err, ErrStateUnsafe) {
		t.Fatalf("symlink root error = %v", err)
	}

	store := testStore(t)
	unlock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := store.Lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestForegroundOperationWaitsForShortRuntimeHealthLock(t *testing.T) {
	store := testStore(t)
	releaseHealth, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = releaseHealth()
		close(released)
	}()
	unlock, err := acquireOperationLock(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	<-released
}

func TestForegroundOperationLockWaitHonorsCancellation(t *testing.T) {
	store := testStore(t)
	releaseHealth, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHealth()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acquireOperationLock(ctx, store); !errors.Is(err, context.Canceled) ||
		!errors.Is(err, ErrBusy) {
		t.Fatalf("lock wait error = %v", err)
	}
}

func passingReport() RegionReport {
	return RegionReport{
		StyleMarkerCount: 0,
		Regions: map[string]RegionStatus{
			"home": RegionPass, "mainBoundary": RegionPass, "sidebar": RegionPass,
			"composerUtilityBar":       RegionPass,
			"conversationActivity":     RegionPass,
			"conversationDiffResource": RegionPass,
			"suggestionCards":          RegionNotPresent,
			"projectPicker":            RegionNotPresent,
			"composer":                 RegionPass,
			"topFade":                  RegionPass,
			"bottomFade":               RegionPass,
			"templateScope":            RegionPass,
			"themeContrast":            RegionPass,
		},
	}
}

func legacyV8VerificationSummary() *VerificationSummary {
	report := passingReport()
	report.Scope = "shell"
	report.RuntimeVersion = 2
	report.StyleMarkerCount = 1
	report.TemplateVersion = 8
	report.ThemePublicID = "100001"
	report.BackgroundLoaded = true
	report.Regions["mainBoundary"] = RegionFail
	report.Regions["templateScope"] = RegionFail
	report.Regions["topFade"] = RegionFail
	return &VerificationSummary{
		Attempts: 66, ReapplyAttempted: true, ProbeCompleted: true,
		Scope: report.Scope, RuntimeVersion: report.RuntimeVersion,
		StyleMarkerCount: report.StyleMarkerCount, TemplateVersion: report.TemplateVersion,
		ThemePublicID: report.ThemePublicID, BackgroundLoaded: report.BackgroundLoaded,
		Regions: report.Regions,
	}
}

func TestTaskRouteFallbackAcceptsVerifiedShellWithoutLegacyThreadSurface(t *testing.T) {
	compiled := CompiledTheme{
		ThemePublicID:   "100005",
		ThemeVersion:    "1.0.1",
		TemplateVersion: TemplateVersion,
	}
	report := passingReport()
	report.Scope = "thread"
	report.StyleMarkerCount = 1
	report.ThemePublicID = compiled.ThemePublicID
	report.TemplateVersion = compiled.TemplateVersion
	report.BackgroundLoaded = true
	report.Regions["shellMain"] = RegionPass
	report.Regions["headerTint"] = RegionPass
	// The legacy thread surface is intentionally L2/diagnostic-only. Newer
	// Codex task pages can retain the verified shell while replacing it.
	report.Regions["conversationActivity"] = RegionNotPresent
	report.Regions["conversationDiffResource"] = RegionNotPresent
	report.Regions["topFade"] = RegionNotPresent

	if !ReportAllowsTheme(report, compiled) {
		t.Fatalf("verified task-route fallback was rejected: %#v", report)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "CodexSkin")
	cache := filepath.Join(t.TempDir(), "plugin-cache")
	store, err := OpenStore(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func verifiedThemeForEngine(t *testing.T) theme.Verified {
	t.Helper()
	fixture := filepath.Join("..", "..", "fixtures", "free-test-theme-v1", "signed-release-v1")
	descriptor, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.json"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(
		filepath.Join(fixture, "package.cskin"),
		descriptor,
		signature,
	)
	if err != nil {
		t.Fatalf("theme.Verify() error = %v", err)
	}
	return verified
}
