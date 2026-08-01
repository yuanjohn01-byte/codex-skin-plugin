package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

type fakeAdapter struct {
	state               Snapshot
	probe               RegionReport
	events              []string
	cancelOnApply       context.CancelFunc
	failOpen            bool
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
}

func (adapter *primingAdapter) Prime(_ context.Context, _ Session, compiled CompiledTheme) error {
	adapter.events = append(adapter.events, "prime")
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
	wantEvents := []string{"open", "probe", "capture", "apply", "verify", "close"}
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
		!strings.Contains(adapter.state.StyleText, ".composer-surface-chrome") ||
		!strings.Contains(adapter.state.StyleText, ".group\\/home-suggestions") ||
		!strings.Contains(adapter.state.StyleText, ".app-shell-main-content-top-fade") ||
		!strings.Contains(adapter.state.StyleText, ":has(") ||
		!strings.Contains(adapter.state.StyleText, ".thread-scroll-container") ||
		!strings.Contains(adapter.state.StyleText, ".bg-gradient-to-t.from-token-main-surface-primary") ||
		!strings.Contains(adapter.state.StyleText, "aside.app-shell-left-panel::after") ||
		!strings.Contains(adapter.state.StyleText, "content: none !important") ||
		!strings.Contains(adapter.state.StyleText, "background-image: none !important") ||
		!strings.Contains(adapter.state.StyleText, "transition: none !important") ||
		strings.Contains(adapter.state.StyleText, `main[data-codex-skin-main="true"] [class~="text-token-text-primary"],
:root[data-codex-skin="active"] main[data-codex-skin-main="true"] [class~="text-token-foreground"]`) {
		t.Fatalf("compiled template is missing fixed region selectors")
	}
	if !strings.HasPrefix(adapter.state.BackgroundDataURL, "data:image/png;base64,") ||
		strings.Contains(adapter.state.StyleText, "data:image/") {
		t.Fatalf("compiled background transport is invalid")
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

func TestCapabilityFailureStopsBeforeBackupOrApply(t *testing.T) {
	verified := verifiedThemeForEngine(t)
	store := testStore(t)
	report := passingReport()
	report.Regions["sidebar"] = RegionFail
	adapter := &fakeAdapter{probe: report}
	instance, _ := New(store, adapter)
	_, err := instance.ApplyVerified(context.Background(), verified)
	if !errors.Is(err, ErrCapabilityBlocked) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	if got := strings.Join(adapter.events, ","); got != "open,probe,close" {
		t.Fatalf("events = %s", got)
	}
}

func TestCapabilitiesAllowTaskPageWithoutHomeUtilityBar(t *testing.T) {
	report := passingReport()
	report.Regions["composerUtilityBar"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without the home-only utility bar was rejected")
	}

	report.Regions["composerUtilityBar"] = RegionFail
	if CapabilitiesAllowApply(report) {
		t.Fatal("present but invalid utility bar was accepted")
	}
}

func TestCapabilitiesAllowTaskPageWithoutConversationActivity(t *testing.T) {
	report := passingReport()
	report.Regions["conversationActivity"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without a rendered activity disclosure was rejected")
	}

	report.Regions["conversationActivity"] = RegionFail
	if CapabilitiesAllowApply(report) {
		t.Fatal("present but unreadable activity disclosure was accepted")
	}
}

func TestCapabilitiesAllowTaskPageWithoutConversationDiffResource(t *testing.T) {
	report := passingReport()
	report.Regions["conversationDiffResource"] = RegionNotPresent
	if !CapabilitiesAllowApply(report) {
		t.Fatal("task page without a rendered diff resource card was rejected")
	}

	report.Regions["conversationDiffResource"] = RegionFail
	if CapabilitiesAllowApply(report) {
		t.Fatal("present but unreadable diff resource card was accepted")
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
	for _, stage := range []string{"apply", "verify", "commit"} {
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
