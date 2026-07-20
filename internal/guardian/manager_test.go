package guardian

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

type digestVerifier struct {
	allowed map[string]bool
	calls   int
}

func (verifier *digestVerifier) Verify(_ context.Context, executable, _ string) error {
	verifier.calls++
	content, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(content)
	if !verifier.allowed[hex.EncodeToString(digest[:])] {
		return fmt.Errorf("digest is not in the signed test set")
	}
	return nil
}

type fakeSelfTester struct {
	failVersion string
	calls       int
}

func (tester *fakeSelfTester) Test(_ context.Context, executable, version, _ string) error {
	tester.calls++
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("Guardian executable is missing")
	}
	if version == tester.failVersion {
		return fmt.Errorf("injected self-test failure")
	}
	return nil
}

type trackingRegistrar struct {
	descriptor DescriptorRegistrar
	current    Registration
	installs   int
	removes    int
	failNext   bool
}

func (registrar *trackingRegistrar) Install(ctx context.Context, registration Registration) error {
	registrar.installs++
	if registrar.failNext {
		registrar.failNext = false
		return fmt.Errorf("injected registration failure")
	}
	if err := registrar.descriptor.Install(ctx, registration); err != nil {
		return err
	}
	registrar.current = registration
	return nil
}

func (registrar *trackingRegistrar) Remove(ctx context.Context, registration Registration) error {
	registrar.removes++
	if err := registrar.descriptor.Remove(ctx, registration); err != nil {
		return err
	}
	registrar.current = Registration{}
	return nil
}

func candidate(version string, content []byte) Candidate {
	digest := sha256.Sum256(content)
	return Candidate{Version: version, Content: bytes.Clone(content), SHA256: hex.EncodeToString(digest[:])}
}

func guardianConfig(root, cache string, verifier SignatureVerifier, tester SelfTester, registrar Registrar) Config {
	return Config{
		Root:          root,
		PluginCache:   cache,
		RuntimeGOOS:   "darwin",
		RuntimeGOARCH: "arm64",
		Verifier:      verifier,
		SelfTester:    tester,
		Registrar:     registrar,
	}
}

func TestInstallUpgradeRollbackAndUninstallPreserveOtherState(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	registrationDirectory := filepath.Join(temporary, "user-registration")
	for _, directory := range []string{cache, filepath.Join(root, "bin"), filepath.Join(root, "state"), filepath.Join(root, "recovery")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sentinels := []string{
		filepath.Join(root, "bin", "helper.sentinel"),
		filepath.Join(root, "state", "desired-theme.json"),
		filepath.Join(root, "recovery", "restore.sentinel"),
	}
	for _, path := range sentinels {
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	first := candidate("0.1.0-s3", []byte("signed guardian one"))
	second := candidate("0.1.0-s4", []byte("signed guardian two"))
	verifier := &digestVerifier{allowed: map[string]bool{first.SHA256: true, second.SHA256: true}}
	tester := &fakeSelfTester{}
	registrar := &trackingRegistrar{descriptor: DescriptorRegistrar{Directory: registrationDirectory, WindowsUserID: "fixture-user"}}
	config := guardianConfig(root, cache, verifier, tester, registrar)

	installed, err := Install(context.Background(), config, first)
	if err != nil || installed.Reused || installed.GuardianVersion != first.Version {
		t.Fatalf("first install failed: %#v, %v", installed, err)
	}
	if registrar.current.Executable != installed.Executable || !strings.HasSuffix(registrar.current.Executable, expectedFilename(first.Version, "macos-arm64")) {
		t.Fatalf("registration did not pin the installed version: %#v", registrar.current)
	}
	if _, err := os.Stat(filepath.Join(registrationDirectory, "com.codexskin.guardian.plist")); err != nil {
		t.Fatalf("LaunchAgent descriptor was not installed: %v", err)
	}
	reused, err := Install(context.Background(), config, first)
	if err != nil || !reused.Reused || reused.Executable != installed.Executable {
		t.Fatalf("idempotent install failed: %#v, %v", reused, err)
	}

	upgraded, err := Install(context.Background(), config, second)
	if err != nil || upgraded.GuardianVersion != second.Version || upgraded.PreviousVersion != first.Version {
		t.Fatalf("upgrade failed: %#v, %v", upgraded, err)
	}
	if registrar.current.Executable != upgraded.Executable {
		t.Fatal("upgrade did not atomically replace the fixed registration")
	}
	if _, err := os.Stat(installed.Executable); err != nil {
		t.Fatalf("upgrade removed rollback version: %v", err)
	}

	unsigned := candidate("0.1.0-s5", []byte("unsigned guardian"))
	callsBefore := tester.calls
	if _, err := Install(context.Background(), config, unsigned); !errors.Is(err, ErrSignature) {
		t.Fatalf("unsigned upgrade was not rejected: %v", err)
	}
	if tester.calls != callsBefore || registrar.current.Executable != upgraded.Executable {
		t.Fatal("unsigned candidate reached self-test or changed registration")
	}
	if _, err := os.Stat(filepath.Join(root, "guardian", "versions", unsigned.Version)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsigned candidate left a version directory: %v", err)
	}

	rolledBack, err := Rollback(context.Background(), config, first.Version)
	if err != nil || rolledBack.GuardianVersion != first.Version || rolledBack.PreviousVersion != second.Version {
		t.Fatalf("rollback failed: %#v, %v", rolledBack, err)
	}
	if registrar.current.Executable != installed.Executable {
		t.Fatal("rollback did not repin the previous verified executable")
	}

	if err := Uninstall(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if registrar.current.ID != "" {
		t.Fatal("uninstall left a current registration")
	}
	if _, err := os.Stat(filepath.Join(registrationDirectory, "com.codexskin.guardian.plist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left the LaunchAgent descriptor: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "guardian")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left Guardian files: %v", err)
	}
	for _, path := range sentinels {
		if content, err := os.ReadFile(path); err != nil || string(content) != "preserve" {
			t.Fatalf("uninstall changed preserved state %s: %q, %v", path, content, err)
		}
	}
}

func TestFailedUpgradeAndTamperedRollbackKeepCurrentRegistration(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	first := candidate("0.1.0-s3", []byte("signed guardian one"))
	second := candidate("0.1.0-s4", []byte("signed guardian two"))
	verifier := &digestVerifier{allowed: map[string]bool{first.SHA256: true, second.SHA256: true}}
	tester := &fakeSelfTester{}
	registrar := &trackingRegistrar{descriptor: DescriptorRegistrar{Directory: filepath.Join(temporary, "registration"), WindowsUserID: "fixture-user"}}
	config := guardianConfig(root, cache, verifier, tester, registrar)
	installed, err := Install(context.Background(), config, first)
	if err != nil {
		t.Fatal(err)
	}
	currentBefore, err := os.ReadFile(filepath.Join(root, "guardian", "current.json"))
	if err != nil {
		t.Fatal(err)
	}

	tester.failVersion = second.Version
	if _, err := Install(context.Background(), config, second); !errors.Is(err, ErrSelfTest) {
		t.Fatalf("self-test failure was not returned: %v", err)
	}
	currentAfterSelfTest, err := os.ReadFile(filepath.Join(root, "guardian", "current.json"))
	if err != nil || !bytes.Equal(currentBefore, currentAfterSelfTest) || registrar.current.Executable != installed.Executable {
		t.Fatalf("failed self-test changed active Guardian: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "guardian", "versions", second.Version)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed self-test left candidate version: %v", err)
	}
	tester.failVersion = ""

	registrar.failNext = true
	if _, err := Install(context.Background(), config, second); !errors.Is(err, ErrRegistration) {
		t.Fatalf("registration failure was not returned: %v", err)
	}
	currentAfter, err := os.ReadFile(filepath.Join(root, "guardian", "current.json"))
	if err != nil || !bytes.Equal(currentBefore, currentAfter) || registrar.current.Executable != installed.Executable {
		t.Fatalf("failed registration changed active Guardian: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "guardian", "versions", second.Version)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed registration left candidate version: %v", err)
	}

	if _, err := Install(context.Background(), config, second); err != nil {
		t.Fatal(err)
	}
	firstExecutable := filepath.Join(root, "guardian", "versions", first.Version, expectedFilename(first.Version, "macos-arm64"))
	if err := os.WriteFile(firstExecutable, []byte("tampered rollback target"), 0o700); err != nil {
		t.Fatal(err)
	}
	activeBefore := registrar.current.Executable
	if _, err := Rollback(context.Background(), config, first.Version); !errors.Is(err, ErrArtifact) {
		t.Fatalf("tampered rollback target was not rejected: %v", err)
	}
	if registrar.current.Executable != activeBefore {
		t.Fatal("tampered rollback changed active registration")
	}
}

func TestRegistrationDescriptorsArePerUserAndFixed(t *testing.T) {
	mac := fixedRegistration("macos-arm64", "/Users/fixture/Library/Application Support/CodexSkin/guardian/versions/0.1.0-s3/codex-skin-guardian_0.1.0-s3_macos_arm64")
	plist, err := renderLaunchAgent(mac)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeXML(plist); err != nil {
		t.Fatalf("LaunchAgent plist is not well-formed XML: %v", err)
	}
	plistText := string(plist)
	for _, required := range []string{"RunAtLoad", "<false/>", "ProcessType", "Aqua", "--internal-spike"} {
		if !strings.Contains(plistText, required) {
			t.Fatalf("LaunchAgent is missing %q", required)
		}
	}
	for _, forbidden := range []string{"KeepAlive</key>\n  <true", "root", "sudo", "http://127.0.0.1"} {
		if strings.Contains(plistText, forbidden) {
			t.Fatalf("LaunchAgent contains forbidden privilege/endpoint %q", forbidden)
		}
	}

	windows := fixedRegistration("windows-x64", `C:\Users\fixture\AppData\Local\CodexSkin\guardian\versions\0.1.0-s3\codex-skin-guardian_0.1.0-s3_windows_x64.exe`)
	task, err := renderScheduledTask(windows, `DESKTOP\fixture`)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeXML(task); err != nil {
		t.Fatalf("Scheduled Task XML is not well-formed: %v", err)
	}
	taskText := string(task)
	for _, required := range []string{"InteractiveToken", "LeastPrivilege", "IgnoreNew", "RunOnlyIfNetworkAvailable>false", "--internal-spike"} {
		if !strings.Contains(taskText, required) {
			t.Fatalf("Scheduled Task is missing %q", required)
		}
	}
	for _, forbidden := range []string{"SYSTEM", "HighestAvailable", "PowerShell", "cmd.exe", "http://127.0.0.1", "https://"} {
		if strings.Contains(taskText, forbidden) {
			t.Fatalf("Scheduled Task contains forbidden privilege/command %q", forbidden)
		}
	}
	if _, err := renderScheduledTask(windows, "SYSTEM"); !errors.Is(err, ErrRegistration) {
		t.Fatalf("SYSTEM registration was accepted: %v", err)
	}
	unsafe := windows
	unsafe.Arguments = []string{"run", "--command", "whoami"}
	if err := validateRegistration(unsafe); !errors.Is(err, ErrRegistration) {
		t.Fatalf("arbitrary registration arguments were accepted: %v", err)
	}
}

func decodeXML(content []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func TestNativeGuardianLifecycle(t *testing.T) {
	firstPath := os.Getenv("CODEX_SKIN_TEST_GUARDIAN_V1")
	secondPath := os.Getenv("CODEX_SKIN_TEST_GUARDIAN_V2")
	if firstPath == "" || secondPath == "" {
		t.Skip("set native Guardian v1/v2 paths to run the lifecycle integration test")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native Guardian integration is limited to product platforms")
	}
	firstContent, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	first := candidate("0.1.0-s3", firstContent)
	second := candidate("0.1.0-s4", secondContent)
	verifier := &digestVerifier{allowed: map[string]bool{first.SHA256: true, second.SHA256: true}}
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	platform, err := releasecontract.PlatformForRuntime(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip("native runtime is outside the supported product matrix")
	}
	registrar := &trackingRegistrar{descriptor: DescriptorRegistrar{Directory: filepath.Join(temporary, "registration"), WindowsUserID: "fixture-user"}}
	config := Config{Root: root, PluginCache: cache, Verifier: verifier, SelfTester: CommandSelfTester{}, Registrar: registrar}
	installed, err := Install(context.Background(), config, first)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := Install(context.Background(), config, second)
	if err != nil || upgraded.PreviousVersion != first.Version {
		t.Fatalf("native upgrade failed: %#v, %v", upgraded, err)
	}
	tampered := candidate("0.1.0-s5", append(bytes.Clone(secondContent), 0))
	if _, err := Install(context.Background(), config, tampered); !errors.Is(err, ErrSignature) {
		t.Fatalf("native unsigned upgrade was not rejected: %v", err)
	}
	rolledBack, err := Rollback(context.Background(), config, first.Version)
	if err != nil || rolledBack.Executable != installed.Executable {
		t.Fatalf("native rollback failed: %#v, %v", rolledBack, err)
	}
	if err := Uninstall(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "guardian")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native uninstall left Guardian files: %v; platform=%s", err, platform)
	}
}
