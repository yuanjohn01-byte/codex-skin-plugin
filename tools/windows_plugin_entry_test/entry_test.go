package windowspluginentrytest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const bootstrapFilename = "codex-skin-bootstrap_0.1.0-paid-alpha.10_windows_x64.exe"

func TestMain(main *testing.M) {
	if os.Getenv("CODEX_SKIN_WINDOWS_ENTRY_FIXTURE") == "1" {
		fixtureProcess()
		os.Exit(0)
	}
	os.Exit(main.Run())
}

func fixtureProcess() {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.HasPrefix(name, "codex-skin-bootstrap_") {
		if os.Getenv("CODEX_SKIN_FIXTURE_BOOTSTRAP_FAIL") == "1" {
			fmt.Println(`{"type":"result","protocolVersion":1,"ok":false,"status":"failed","data":null,"error":{"code":"CS-BOOTSTRAP-INSTALL-001","action":"retry_helper_install","retryable":true}}`)
			os.Exit(50)
		}
		logLine(os.Getenv("CODEX_SKIN_FIXTURE_BOOTSTRAP_LOG"), strings.Join(os.Args[1:], " "))
		destination := filepath.Join(os.Getenv("LOCALAPPDATA"), "CodexSkin", "recovery", "engine")
		if err := os.MkdirAll(destination, 0o700); err != nil {
			os.Exit(80)
		}
		if err := copyFile(os.Args[0], filepath.Join(destination, "codex-skin.exe")); err != nil {
			os.Exit(80)
		}
		fmt.Println(`{"type":"result","protocolVersion":1,"ok":true,"status":"completed","data":{"helperVersion":"0.1.0-paid-alpha.11"},"error":null}`)
		return
	}
	logLine(os.Getenv("CODEX_SKIN_FIXTURE_HELPER_LOG"), strings.Join(os.Args[1:], " "))
}

func TestPowerShellEntryBootstrapsOnlyApplyAndFailsClosed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Plugin entry fixture runs on the native Windows CI job")
	}
	powershell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		powershell, err = exec.LookPath("powershell.exe")
		if err != nil {
			t.Fatal("PowerShell is unavailable")
		}
	}
	root := repositoryRoot(t)
	scripts := filepath.Join(t.TempDir(), "plugin", "scripts")
	if err := os.MkdirAll(filepath.Join(scripts, ".bootstrap"), 0o700); err != nil {
		t.Fatal(err)
	}
	copyRequired(t, filepath.Join(root, "plugins", "codex-skin", "scripts", "codex-skin.ps1"), filepath.Join(scripts, "codex-skin.ps1"))
	launcher := filepath.Join(scripts, ".bootstrap", bootstrapFilename)
	copyRequired(t, os.Args[0], launcher)
	content, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	pins := fmt.Sprintf(`# Generated fixture pins.
$bootstrapReleaseTag = "helper-v0.1.0-paid-alpha.11"
$bootstrapVersion = "0.1.0-paid-alpha.10"
$bootstrapBuildCommit = "%s"
$bootstrapBuiltAt = "2026-08-03T00:00:00Z"
$bootstrapFilename = "%s"
$bootstrapSHA256 = "%s"
`, strings.Repeat("a", 40), bootstrapFilename, hex.EncodeToString(digest[:]))
	if err := os.WriteFile(filepath.Join(scripts, "bootstrap-pins.ps1"), []byte(pins), 0o600); err != nil {
		t.Fatal(err)
	}
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	bootstrapLog := filepath.Join(t.TempDir(), "bootstrap.log")
	helperLog := filepath.Join(t.TempDir(), "helper.log")
	baseEnvironment := append(os.Environ(),
		"CODEX_SKIN_WINDOWS_ENTRY_FIXTURE=1",
		"CODEX_SKIN_FIXTURE_BOOTSTRAP_LOG="+bootstrapLog,
		"CODEX_SKIN_FIXTURE_HELPER_LOG="+helperLog,
		"LOCALAPPDATA="+localAppData,
		"PROCESSOR_ARCHITECTURE=AMD64",
	)

	apply := exec.Command(powershell, "-NoProfile", "-File", filepath.Join(scripts, "codex-skin.ps1"), "theme", "apply", "100005", "--json")
	apply.Env = baseEnvironment
	if output, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("apply failed: %v: %s", err, output)
	}
	assertFileContains(t, bootstrapLog, "install --plugin-cache")
	assertFileContains(t, helperLog, "theme apply 100005 --json")
	if err := os.Remove(bootstrapLog); err != nil {
		t.Fatal(err)
	}
	status := exec.Command(powershell, "-NoProfile", "-File", filepath.Join(scripts, "codex-skin.ps1"), "status", "--json")
	status.Env = baseEnvironment
	if output, err := status.CombinedOutput(); err != nil {
		t.Fatalf("status failed: %v: %s", err, output)
	}
	if _, err := os.Stat(bootstrapLog); !os.IsNotExist(err) {
		t.Fatal("status unexpectedly invoked Bootstrap")
	}
	assertFileContains(t, helperLog, "status --json")

	failedLocalAppData := filepath.Join(t.TempDir(), "LocalAppData")
	failed := exec.Command(powershell, "-NoProfile", "-File", filepath.Join(scripts, "codex-skin.ps1"), "theme", "apply", "100005", "--json")
	failed.Env = append(baseEnvironment, "LOCALAPPDATA="+failedLocalAppData, "CODEX_SKIN_FIXTURE_BOOTSTRAP_FAIL=1")
	output, err := failed.CombinedOutput()
	var exitError *exec.ExitError
	if !strings.Contains(string(output), "CS-BOOTSTRAP-INSTALL-001") || err == nil || !asExitError(err, &exitError) || exitError.ExitCode() != 50 {
		t.Fatalf("Bootstrap failure did not fail closed: %v: %s", err, output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func copyRequired(t *testing.T, source, destination string) {
	t.Helper()
	if err := copyFile(source, destination); err != nil {
		t.Fatal(err)
	}
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func logLine(path, value string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(80)
	}
	_, _ = fmt.Fprintln(file, value)
	_ = file.Close()
}

func assertFileContains(t *testing.T, path, wanted string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), wanted) {
		t.Fatalf("%s does not contain %q: %v: %s", path, wanted, err, content)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	value, ok := err.(*exec.ExitError)
	if ok {
		*target = value
	}
	return ok
}
