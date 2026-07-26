package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	imagecolor "image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

type fakeAdapter struct {
	state              Snapshot
	probe              RegionReport
	events             []string
	failOpen           bool
	failApply          bool
	failVerify         bool
	failRestore        bool
	failOfficial       bool
	failVerifyOfficial bool
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
		adapter.state.TemplateVersion != compiled.TemplateVersion ||
		adapter.state.StyleText != compiled.StyleText ||
		adapter.state.BackgroundDataURL != compiled.BackgroundDataURL {
		return ErrCapabilityBlocked
	}
	return nil
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
	if adapter.failApply {
		return errors.New("apply rejected")
	}
	adapter.state = Snapshot{
		StylePresent: true, StyleText: compiled.StyleText, ThemePublicID: compiled.ThemePublicID,
		ThemeVersion: compiled.ThemeVersion, TemplateVersion: compiled.TemplateVersion,
		BackgroundDataURL: compiled.BackgroundDataURL,
	}
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

func (adapter *fakeAdapter) Restore(_ context.Context, _ Session, snapshot Snapshot) error {
	adapter.events = append(adapter.events, "rollback")
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
	if !strings.Contains(adapter.state.StyleText, "main.main-surface") ||
		!strings.Contains(adapter.state.StyleText, "aside.app-shell-left-panel") ||
		!strings.Contains(adapter.state.StyleText, ".composer-surface-chrome") ||
		!strings.Contains(adapter.state.StyleText, ".group\\/home-suggestions") ||
		!strings.Contains(adapter.state.StyleText, ".app-shell-main-content-top-fade") ||
		!strings.Contains(adapter.state.StyleText, "main.main-surface {\n  box-shadow: none !important") ||
		!strings.Contains(adapter.state.StyleText, "aside.app-shell-left-panel::after") ||
		!strings.Contains(adapter.state.StyleText, "content: none !important") ||
		!strings.Contains(adapter.state.StyleText, "background-image: none !important") ||
		!strings.Contains(adapter.state.StyleText, "transition: none !important") {
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
		BackgroundDataURL: "data:image/png;base64,AA==",
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
			BackgroundDataURL: "data:image/png;base64,AA==",
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
			"composerUtilityBar": RegionPass,
			"suggestionCards":    RegionNotPresent,
			"projectPicker":      RegionNotPresent, "composer": RegionPass, "topFade": RegionPass,
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
	imageBytes := tinyEnginePNG(t)
	imageSum := sha256.Sum256(imageBytes)
	imageDigest := hex.EncodeToString(imageSum[:])
	assetPath := "assets/" + imageDigest + ".png"
	manifestValue := theme.Manifest{
		SchemaVersion: 1, ThemePublicID: "100001", ThemeVersion: "1.0.0", Name: "Engine Fixture",
		Design: theme.Design{
			Mode: "dark",
			Tokens: theme.Tokens{
				BackgroundImage: assetPath, BackgroundOverlay: 0.42, SurfaceOpacity: 0.82,
				SurfaceBlurPx: 18, TextPrimary: "#F7F7FA", TextSecondary: "#C8CAD3",
				Accent: "#A78BFA", Border: "#FFFFFF24", RadiusScale: 1,
			},
			Regions: theme.Regions{Home: true, Sidebar: true, SuggestionCards: true, ProjectPicker: true, Composer: true},
		},
		Customization: theme.Customization{Allowed: []string{"backgroundOverlay", "surfaceOpacity", "surfaceBlurPx", "accent", "radiusScale"}},
		Assets: []theme.Asset{{
			Path: assetPath, Role: "background", ContentType: "image/png", ByteSize: int64(len(imageBytes)), SHA256: imageDigest,
		}},
		Compatibility: theme.Compatibility{Platforms: []string{"macos", "windows"}, MinEngineVersion: "0.1.0"},
	}
	manifest := canonicalEngineJSON(t, manifestValue)
	var packageBuffer bytes.Buffer
	zipWriter := zip.NewWriter(&packageBuffer)
	for _, item := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {assetPath, imageBytes}} {
		header := &zip.FileHeader{Name: item.name, Method: zip.Store}
		header.SetMode(0o600)
		entry, err := zipWriter.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	packageBytes := packageBuffer.Bytes()
	packageSum := sha256.Sum256(packageBytes)
	manifestSum := sha256.Sum256(manifest)
	descriptorValue := theme.Descriptor{
		DescriptorVersion: 1, ThemePublicID: "100001", ThemeVersion: "1.0.0", SchemaVersion: 1,
		ManifestSHA256: hex.EncodeToString(manifestSum[:]), PackageSHA256: hex.EncodeToString(packageSum[:]),
		PackageByteSize: int64(len(packageBytes)), PublishedAt: "2026-07-26T06:00:00Z", SigningKeyID: "engine-test-key",
	}
	descriptor := canonicalEngineJSON(t, descriptorValue)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, append([]byte("codex-skin/theme-release-descriptor/v1\x00"), descriptor...))
	keyset := prettyEngineJSON(t, theme.VerificationKeyset{
		SchemaVersion: 1,
		Keys: []theme.VerificationKey{{
			KeyID: "engine-test-key", Algorithm: "Ed25519", Usage: "theme-release",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey), NotBefore: "2026-01-01T00:00:00Z",
			NotAfter: "2028-01-01T00:00:00Z", Status: "active",
		}},
	})
	packagePath := filepath.Join(t.TempDir(), "theme.cskin")
	if err := os.WriteFile(packagePath, packageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(
		packagePath,
		descriptor,
		[]byte(base64.StdEncoding.EncodeToString(signature)+"\n"),
		keyset,
	)
	if err != nil {
		t.Fatalf("theme.Verify() error = %v", err)
	}
	return verified
}

func tinyEnginePNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, imagecolor.RGBA{R: 0x22, G: 0x44, B: 0x66, A: 0xff})
	var content bytes.Buffer
	if err := png.Encode(&content, picture); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func canonicalEngineJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(content, '\n')
}

func prettyEngineJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(content, '\n')
}
