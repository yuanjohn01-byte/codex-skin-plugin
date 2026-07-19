package bootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

type memorySource struct {
	files map[string][]byte
	calls int
}

func (source *memorySource) Fetch(_ context.Context, tag, filename string, maxBytes int64) ([]byte, error) {
	source.calls++
	content, ok := source.files[tag+"/"+filename]
	if !ok {
		return nil, fmt.Errorf("fixture asset not found")
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("fixture asset exceeds limit")
	}
	return bytes.Clone(content), nil
}

type fakeSelfTester struct {
	failVersion string
	calls       int
}

func (tester *fakeSelfTester) Test(_ context.Context, executable, version, _ string) error {
	tester.calls++
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("executable is not a regular file")
	}
	if version == tester.failVersion {
		return fmt.Errorf("injected self-test failure")
	}
	return nil
}

type signingFixture struct {
	keyID      string
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func newSigningFixture(t *testing.T) signingFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signingFixture{keyID: "runtime-bootstrap-test", publicKey: publicKey, privateKey: privateKey}
}

func (fixture signingFixture) addRelease(t *testing.T, source *memorySource, version string, payload []byte) string {
	t.Helper()
	digest := sha256.Sum256(payload)
	digestText := hex.EncodeToString(digest[:])
	tag := "helper-v" + version
	descriptor := releasecontract.Descriptor{
		SchemaVersion: 1,
		HelperVersion: version,
		ReleaseTag:    tag,
		PublishedAt:   "2026-07-20T00:00:00Z",
		SigningKeyID:  fixture.keyID,
		Artifacts: []releasecontract.Artifact{
			{Platform: "macos-arm64", Filename: expectedFilename(version, "macos-arm64"), SHA256: digestText, Size: int64(len(payload))},
			{Platform: "macos-x64", Filename: expectedFilename(version, "macos-x64"), SHA256: digestText, Size: int64(len(payload))},
			{Platform: "windows-x64", Filename: expectedFilename(version, "windows-x64"), SHA256: digestText, Size: int64(len(payload))},
		},
	}
	canonical, err := releasecontract.CanonicalBytes(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(fixture.privateKey, releasecontract.SigningMessage(canonical))
	if source.files == nil {
		source.files = map[string][]byte{}
	}
	source.files[tag+"/"+descriptorFilename] = canonical
	source.files[tag+"/"+signatureFilename] = signature
	for _, artifact := range descriptor.Artifacts {
		source.files[tag+"/"+artifact.Filename] = bytes.Clone(payload)
	}
	return tag
}

func configFor(root, cache, tag string, source Source, key signingFixture, tester SelfTester) Config {
	return Config{
		Root:          root,
		PluginCache:   cache,
		ReleaseTag:    tag,
		RuntimeGOOS:   "darwin",
		RuntimeGOARCH: "arm64",
		Source:        source,
		TrustedKeys:   map[string]ed25519.PublicKey{key.keyID: key.publicKey},
		SelfTester:    tester,
	}
}

func TestInstallOutsidePluginCachePreservesState(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "recovery"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	stateSentinel := filepath.Join(root, "state", "desired-theme.json")
	recoverySentinel := filepath.Join(root, "recovery", "restore.sentinel")
	if err := os.WriteFile(stateSentinel, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoverySentinel, []byte("recovery"), 0o600); err != nil {
		t.Fatal(err)
	}

	key := newSigningFixture(t)
	source := &memorySource{}
	tag := key.addRelease(t, source, "0.1.0-s3", []byte("self-contained helper"))
	tester := &fakeSelfTester{}
	first, err := Install(context.Background(), configFor(root, cache, tag, source, key, tester))
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.HelperVersion != "0.1.0-s3" || !pathContains(first.Root, first.Executable) || pathContains(cache, first.Executable) {
		t.Fatalf("unexpected first install: %#v", first)
	}

	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := Install(context.Background(), configFor(root, cache, tag, source, key, tester))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.Executable != first.Executable {
		t.Fatalf("same release was not reused: %#v", second)
	}
	for _, sentinel := range []string{stateSentinel, recoverySentinel, first.Executable, filepath.Join(root, "bin", "current.json")} {
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("out-of-cache file did not survive Plugin cache replacement: %s: %v", sentinel, err)
		}
	}
}

func TestFailedUpgradeKeepsCurrentThenSuccessfulUpgradeSwitches(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := newSigningFixture(t)
	source := &memorySource{}
	firstTag := key.addRelease(t, source, "0.1.0-s3", []byte("first helper"))
	secondTag := key.addRelease(t, source, "0.1.0-s4", []byte("second helper"))
	tester := &fakeSelfTester{}
	first, err := Install(context.Background(), configFor(root, cache, firstTag, source, key, tester))
	if err != nil {
		t.Fatal(err)
	}
	firstPointer, err := os.ReadFile(filepath.Join(root, "bin", "current.json"))
	if err != nil {
		t.Fatal(err)
	}

	tester.failVersion = "0.1.0-s4"
	if _, err := Install(context.Background(), configFor(root, cache, secondTag, source, key, tester)); !errors.Is(err, ErrSelfTest) {
		t.Fatalf("self-test failure was not returned: %v", err)
	}
	afterFailure, err := os.ReadFile(filepath.Join(root, "bin", "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstPointer, afterFailure) {
		t.Fatal("failed upgrade changed the current pointer")
	}
	if _, err := os.Stat(first.Executable); err != nil {
		t.Fatalf("failed upgrade removed current Helper: %v", err)
	}
	staging, err := filepath.Glob(filepath.Join(root, "bin", ".staging-*"))
	if err != nil || len(staging) != 0 {
		t.Fatalf("failed upgrade left staging directories: %v, %v", staging, err)
	}

	tester.failVersion = ""
	second, err := Install(context.Background(), configFor(root, cache, secondTag, source, key, tester))
	if err != nil {
		t.Fatal(err)
	}
	if second.HelperVersion != "0.1.0-s4" || second.PreviousVersion != "0.1.0-s3" || second.Reused {
		t.Fatalf("unexpected upgrade result: %#v", second)
	}
	if _, err := os.Stat(first.Executable); err != nil {
		t.Fatalf("upgrade removed last-known-good Helper: %v", err)
	}
	pointer, ok, err := readCurrent(filepath.Join(root, "bin"), "macos-arm64")
	if err != nil || !ok || pointer.HelperVersion != "0.1.0-s4" {
		t.Fatalf("current pointer did not switch after success: %#v, %v", pointer, err)
	}
}

func TestBadArtifactAndOverlappingPathsFailBeforeActivation(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := newSigningFixture(t)
	source := &memorySource{}
	tag := key.addRelease(t, source, "0.1.0-s3", []byte("trusted helper"))
	filename := expectedFilename("0.1.0-s3", "macos-arm64")
	source.files[tag+"/"+filename] = []byte("altered helper")
	tester := &fakeSelfTester{}
	if _, err := Install(context.Background(), configFor(root, cache, tag, source, key, tester)); !errors.Is(err, releasecontract.ErrArtifactMismatch) {
		t.Fatalf("bad artifact was not rejected: %v", err)
	}
	if tester.calls != 0 {
		t.Fatal("bad artifact reached self-test")
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad artifact created a current pointer: %v", err)
	}

	overlapSource := &memorySource{}
	overlapTag := key.addRelease(t, overlapSource, "0.1.0-s3", []byte("trusted helper"))
	overlapConfig := configFor(filepath.Join(cache, "application-data"), cache, overlapTag, overlapSource, key, tester)
	if _, err := Install(context.Background(), overlapConfig); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("overlapping Plugin cache was accepted: %v", err)
	}
	if overlapSource.calls != 0 {
		t.Fatal("unsafe paths reached the release source")
	}
}

func TestDowngradeAndSymlinkRootAreRejected(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := newSigningFixture(t)
	source := &memorySource{}
	newTag := key.addRelease(t, source, "0.2.0", []byte("new helper"))
	oldTag := key.addRelease(t, source, "0.1.0", []byte("old helper"))
	tester := &fakeSelfTester{}
	if _, err := Install(context.Background(), configFor(root, cache, newTag, source, key, tester)); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), configFor(root, cache, oldTag, source, key, tester)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade was not rejected: %v", err)
	}
	pointer, ok, err := readCurrent(filepath.Join(root, "bin"), "macos-arm64")
	if err != nil || !ok || pointer.HelperVersion != "0.2.0" {
		t.Fatalf("downgrade changed current: %#v, %v", pointer, err)
	}

	symlinkRoot := filepath.Join(temporary, "symlink-root")
	if err := os.Symlink(root, symlinkRoot); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation is unavailable for this Windows test user")
		}
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), configFor(symlinkRoot, cache, newTag, source, key, tester)); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink application root was accepted: %v", err)
	}
}

func TestDefaultRootAndSelfTestContracts(t *testing.T) {
	macRoot, err := DefaultRoot("darwin", "/Users/fixture", "")
	if err != nil || filepath.ToSlash(macRoot) != "/Users/fixture/Library/Application Support/CodexSkin" {
		t.Fatalf("unexpected macOS root: %q, %v", macRoot, err)
	}
	windowsRoot, err := DefaultRoot("windows", "", `C:\Users\fixture\AppData\Local`)
	if err != nil || windowsRoot != `C:\Users\fixture\AppData\Local\CodexSkin` {
		t.Fatalf("unexpected Windows root: %q, %v", windowsRoot, err)
	}
	if _, err := DefaultRoot("linux", "/home/fixture", ""); !errors.Is(err, releasecontract.ErrPlatformUnsupported) {
		t.Fatalf("unsupported default root was accepted: %v", err)
	}

	version := []byte(`{"type":"result","protocolVersion":1,"ok":true,"status":"completed","data":{"command":"version","helperVersion":"0.1.0-s3"},"error":null}` + "\n")
	doctor := []byte(`{"type":"result","protocolVersion":1,"ok":true,"status":"completed","data":{"command":"doctor","helperVersion":"0.1.0-s3","platform":"macos","architecture":"arm64","nodeRequired":false},"error":null}` + "\n")
	if err := validateSelfTest(version, "version", "0.1.0-s3", "macos-arm64"); err != nil {
		t.Fatal(err)
	}
	if err := validateSelfTest(doctor, "doctor", "0.1.0-s3", "macos-arm64"); err != nil {
		t.Fatal(err)
	}
	if err := validateSelfTest(bytes.Replace(doctor, []byte(`"nodeRequired":false`), []byte(`"nodeRequired":true`), 1), "doctor", "0.1.0-s3", "macos-arm64"); err == nil {
		t.Fatal("doctor self-test accepted a Node dependency")
	}
}

func TestReleaseURLAndAssetRestrictions(t *testing.T) {
	for _, filename := range []string{descriptorFilename, signatureFilename, "codex-skin-helper_0.1.0-s3_windows_x64.exe"} {
		if !validAssetName(filename) {
			t.Fatalf("valid release asset was rejected: %s", filename)
		}
	}
	for _, filename := range []string{"../helper.exe", "helper.zip", "codex-skin-helper_0.1.0-s3_windows_x64"} {
		if validAssetName(filename) {
			t.Fatalf("unsafe release asset was accepted: %s", filename)
		}
	}
	trusted, _ := url.Parse("https://release-assets.githubusercontent.com/path")
	if err := validateReleaseURL(trusted); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"http://github.com/file", "https://user@example.com/file", "https://127.0.0.1/file", "https://github.com:444/file"} {
		candidate, _ := url.Parse(value)
		if err := validateReleaseURL(candidate); err == nil {
			t.Fatalf("unsafe release URL was accepted: %s", value)
		}
	}

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("fixture")),
			ContentLength: 7,
			Request:       request,
			Header:        make(http.Header),
		}, nil
	})
	source := newHTTPReleaseSource(&http.Client{Transport: transport})
	content, err := source.Fetch(context.Background(), "helper-v0.1.0-s3", descriptorFilename, 7)
	if err != nil || string(content) != "fixture" {
		t.Fatalf("fixed GitHub source failed: %q, %v", content, err)
	}
	if _, err := source.Fetch(context.Background(), "helper-v0.1.0-s3", "../helper", 7); err == nil {
		t.Fatal("HTTP source accepted path traversal")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRealHelperInstallation(t *testing.T) {
	helperPath := os.Getenv("CODEX_SKIN_TEST_HELPER")
	if helperPath == "" {
		t.Skip("set CODEX_SKIN_TEST_HELPER to run the native Helper installation test")
	}
	platform, err := releasecontract.PlatformForRuntime(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip("native runtime is outside the supported product matrix")
	}
	payload, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	key := newSigningFixture(t)
	source := &memorySource{}
	tag := key.addRelease(t, source, "0.1.0-s3", payload)
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Root:          root,
		PluginCache:   cache,
		ReleaseTag:    tag,
		RuntimeGOOS:   runtime.GOOS,
		RuntimeGOARCH: runtime.GOARCH,
		Source:        source,
		TrustedKeys:   map[string]ed25519.PublicKey{key.keyID: key.publicKey},
		SelfTester:    CommandSelfTester{},
	}
	result, err := Install(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.HelperVersion != "0.1.0-s3" || !strings.Contains(filepath.ToSlash(result.Executable), "/bin/0.1.0-s3/") {
		t.Fatalf("unexpected native install: %#v", result)
	}
	pointer, ok, err := readCurrent(filepath.Join(root, "bin"), platform)
	if err != nil || !ok || pointer.HelperVersion != result.HelperVersion {
		t.Fatalf("native current pointer failed: %#v, %v", pointer, err)
	}
}
