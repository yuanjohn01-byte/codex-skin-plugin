package userflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/deviceauth"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restartflow"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/themeapi"
)

const flowTestToken = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmbG93LXRlc3QtdXNlciJ9.c2lnbmF0dXJlLXdpdGgtZW5vdWdoLWJhc2U2NHVybC1ieXRlcw"

type memoryState struct {
	value  flowstate.State
	writes []flowstate.State
}

func (state *memoryState) Read() (flowstate.State, error) {
	return state.value, nil
}

func (state *memoryState) Write(value flowstate.State) error {
	state.value = value
	state.writes = append(state.writes, value)
	return nil
}

type fakeAuth struct {
	token        *deviceauth.AccessToken
	startCalls   int
	refreshCalls int
}

func (auth *fakeAuth) Start(context.Context, deviceauth.StartInput) (deviceauth.StartResult, error) {
	auth.startCalls++
	return deviceauth.StartResult{
		Outcome:         deviceauth.OutcomeStarted,
		VerificationURL: "https://app.example/device/approve?token=" + string(make([]byte, 43)),
		Interval:        4 * time.Second,
	}, nil
}

func (auth *fakeAuth) Refresh(context.Context, string) (deviceauth.Result, error) {
	auth.refreshCalls++
	return deviceauth.Result{
		Outcome:     deviceauth.OutcomeAuthorized,
		AccessToken: auth.token,
		Device:      deviceauth.Device{ID: "dev_" + "a2345678901234567890123456789012"},
	}, nil
}

func (auth *fakeAuth) AuthorizeAndContinue(context.Context, deviceauth.Continuation) (deviceauth.Result, error) {
	return deviceauth.Result{
		Outcome:     deviceauth.OutcomeAuthorized,
		AccessToken: auth.token,
		Device:      deviceauth.Device{ID: "dev_" + "a2345678901234567890123456789012"},
	}, nil
}

type fakeThemes struct {
	results       []themeapi.Result
	metadataCalls int
	downloaded    bool
	recorded      bool
}

func (themes *fakeThemes) Metadata(context.Context, string, string) (themeapi.Result, error) {
	index := themes.metadataCalls
	themes.metadataCalls++
	if index >= len(themes.results) {
		return themeapi.Result{}, errors.New("unexpected metadata call")
	}
	return themes.results[index], nil
}

func (themes *fakeThemes) Download(_ context.Context, _ themeapi.Release, _ string, destination string) error {
	themes.downloaded = true
	return os.WriteFile(destination, []byte("verified-by-test-applier"), 0o600)
}

func (themes *fakeThemes) RecordApply(_ context.Context, _ themeapi.Release, _ string, _ time.Time, _ time.Duration) error {
	themes.recorded = true
	return nil
}

type fakeApplier struct{}

func (fakeApplier) Apply(_ context.Context, release themeapi.Release, packagePath string) (engine.ApplyResult, error) {
	raw, err := os.ReadFile(packagePath)
	if err != nil || string(raw) != "verified-by-test-applier" {
		return engine.ApplyResult{}, ErrTheme
	}
	return engine.ApplyResult{
		OperationID:   "op_test",
		ThemePublicID: release.ThemePublicID,
		ThemeVersion:  release.ThemeVersion,
	}, nil
}

func testRelease() themeapi.Release {
	return themeapi.Release{
		ThemePublicID:    "100001",
		ThemeVersion:     "1.0.0",
		MinEngineVersion: "0.1.0",
		DownloadPath:     "/api/v1/plugin/themes/100001/download",
		Descriptor: theme.Descriptor{
			ThemePublicID:   "100001",
			ThemeVersion:    "1.0.0",
			PackageByteSize: 1,
		},
	}
}

func newTestRunner(t *testing.T, state *memoryState, auth *fakeAuth, themes *fakeThemes, opened *[]string, now *time.Time) *Runner {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner, err := New(Config{
		Root:              root,
		BaseURL:           "https://app.example",
		Auth:              auth,
		Themes:            themes,
		State:             state,
		Applier:           fakeApplier{},
		OpenURL:           func(_ context.Context, value string) error { *opened = append(*opened, value); return nil },
		Wait:              func(context.Context, time.Duration) error { *now = now.Add(4 * time.Second); return nil },
		Now:               func() time.Time { return *now },
		DeviceDisplayName: "Codex Skin on macOS",
		Platform:          "macos",
		PluginVersion:     "0.1.0-paid-alpha",
		EngineVersion:     "0.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestApplyContinuesAcrossAuthorizationAndPurchase(t *testing.T) {
	token, err := deviceauth.NewAccessToken(flowTestToken, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state := &memoryState{value: flowstate.State{SchemaVersion: 1}}
	auth := &fakeAuth{token: token}
	release := testRelease()
	themes := &fakeThemes{results: []themeapi.Result{
		{Outcome: themeapi.OutcomeAccessRequired, PricingPath: "/pricing?theme=100001"},
		{Outcome: themeapi.OutcomeReady, Release: &release},
	}}
	opened := []string{}
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	runner := newTestRunner(t, state, auth, themes, &opened, &now)

	result, err := runner.Apply(context.Background(), "100001")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Authorized || !result.PurchaseShown || result.OperationID != "op_test" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(opened) != 2 ||
		opened[1] != "https://app.example/pricing?theme=100001" ||
		state.value.PendingThemePublicID != "" ||
		state.value.DeviceID == "" ||
		!themes.downloaded ||
		!themes.recorded {
		t.Fatalf("flow did not continue: opened=%v state=%+v downloaded=%v", opened, state.value, themes.downloaded)
	}
}

func TestApplyRefreshesExistingDeviceWithoutOpeningBrowser(t *testing.T) {
	token, err := deviceauth.NewAccessToken(flowTestToken, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := "dev_" + "a2345678901234567890123456789012"
	state := &memoryState{value: flowstate.State{SchemaVersion: 1, DeviceID: deviceID}}
	auth := &fakeAuth{token: token}
	release := testRelease()
	themes := &fakeThemes{results: []themeapi.Result{{Outcome: themeapi.OutcomeReady, Release: &release}}}
	opened := []string{}
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	runner := newTestRunner(t, state, auth, themes, &opened, &now)
	result, err := runner.Apply(context.Background(), "100001")
	if err != nil {
		t.Fatal(err)
	}
	if result.Authorized || auth.refreshCalls != 1 || auth.startCalls != 0 || len(opened) != 0 {
		t.Fatalf("unexpected refresh flow: result=%+v auth=%+v opened=%v", result, auth, opened)
	}
}

func TestApplyPreservesPendingThemeOnUnavailable(t *testing.T) {
	token, err := deviceauth.NewAccessToken(flowTestToken, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := "dev_" + "a2345678901234567890123456789012"
	state := &memoryState{value: flowstate.State{SchemaVersion: 1, DeviceID: deviceID}}
	auth := &fakeAuth{token: token}
	themes := &fakeThemes{results: []themeapi.Result{{Outcome: themeapi.OutcomeUnavailable}}}
	opened := []string{}
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	runner := newTestRunner(t, state, auth, themes, &opened, &now)
	if _, err := runner.Apply(context.Background(), "100001"); !errors.Is(err, ErrTheme) {
		t.Fatalf("Apply error = %v, want ErrTheme", err)
	}
	if state.value.PendingThemePublicID != "100001" || themes.downloaded {
		t.Fatalf("pending continuation lost: state=%+v downloaded=%v", state.value, themes.downloaded)
	}
}

type restartConsentAdapter struct{}

func (restartConsentAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	return engine.Session{}, engine.ErrRestartConsent
}

func (restartConsentAdapter) Probe(context.Context, engine.Session) (engine.RegionReport, error) {
	return engine.RegionReport{}, nil
}

func (restartConsentAdapter) Capture(context.Context, engine.Session) (engine.Snapshot, error) {
	return engine.Snapshot{}, nil
}

func (restartConsentAdapter) Apply(context.Context, engine.Session, engine.CompiledTheme) error {
	return nil
}

func (restartConsentAdapter) Verify(context.Context, engine.Session, engine.CompiledTheme) (engine.RegionReport, error) {
	return engine.RegionReport{}, nil
}

func (restartConsentAdapter) Restore(context.Context, engine.Session, engine.Snapshot) error {
	return nil
}

func (restartConsentAdapter) RestoreOfficial(context.Context, engine.Session) error {
	return nil
}

func (restartConsentAdapter) VerifyOfficial(context.Context, engine.Session) error {
	return nil
}

func (restartConsentAdapter) Close(context.Context, engine.Session) error {
	return nil
}

func TestEngineApplierStagesSignedPackageBeforeRequestingRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	store, err := engine.OpenStore(root, "")
	if err != nil {
		t.Fatal(err)
	}
	restartStore, err := restartflow.New(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := engine.New(store, restartConsentAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(
		"..",
		"..",
		"fixtures",
		"free-test-theme-v1",
		"signed-release-v1",
	)
	descriptorBytes, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.json"))
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(
		filepath.Join(fixture, "package.cskin"),
		descriptorBytes,
		signatureBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	release := themeapi.Release{
		ThemePublicID:    verified.Manifest.ThemePublicID,
		ThemeVersion:     verified.Manifest.ThemeVersion,
		Descriptor:       verified.Descriptor,
		DescriptorBytes:  descriptorBytes,
		SignatureBytes:   signatureBytes,
		MinEngineVersion: verified.Manifest.Compatibility.MinEngineVersion,
	}
	applier := EngineApplier{Engine: instance, Restart: restartStore}
	if _, err := applier.Apply(
		context.Background(),
		release,
		filepath.Join(fixture, "package.cskin"),
	); !errors.Is(err, ErrRestart) {
		t.Fatalf("Apply error = %v, want ErrRestart", err)
	}
	request, found, err := restartStore.Current()
	if err != nil || !found ||
		request.Status != restartflow.StatusPending ||
		request.ThemePublicID != verified.Manifest.ThemePublicID {
		t.Fatalf("restart request = %#v, found=%t, error=%v", request, found, err)
	}
}
