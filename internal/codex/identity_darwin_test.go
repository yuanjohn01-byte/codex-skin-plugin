//go:build darwin

package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestControlledDarwinOpenArgumentsUsesVerifiedBundleAndNewInstance(t *testing.T) {
	profile := "/Users/example/Library/Application Support/Codex"
	port := 32145
	got := controlledDarwinOpenArguments("/Applications/ChatGPT.app", profile, port)
	want := []string{
		"-n",
		"-a",
		"/Applications/ChatGPT.app",
		"--args",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profile,
		"--no-first-run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("controlledDarwinOpenArguments() = %q, want %q", got, want)
	}
	if got[0] != "-n" || got[1] != "-a" {
		t.Fatal("controlled current-profile launch must bypass a stale LaunchServices primary")
	}
	if got[2] != "/Applications/ChatGPT.app" {
		t.Fatal("controlled launch must use the already-verified bundle path")
	}
}

func TestControlledDarwinExecutableArgumentsStayLoopbackOnly(t *testing.T) {
	profile := "/Users/example/Library/Application Support/Codex"
	got := controlledDarwinExecutableArguments(profile, 32145)
	want := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=32145",
		"--user-data-dir=" + profile,
		"--no-first-run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("controlledDarwinExecutableArguments() = %q, want %q", got, want)
	}
}

func TestControlledDarwinPIDsSelectsOnlyExactVerifiedLaunch(t *testing.T) {
	executable := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	profile := "/Users/example/Library/Application Support/Codex"
	output := []byte(
		"  101 " + executable + "\n" +
			"  102 " + executable + " --remote-debugging-address=0.0.0.0 --remote-debugging-port=9222 --user-data-dir=" + profile + "\n" +
			"  103 " + executable + " --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=" + profile + " --no-first-run\n" +
			"  104 /tmp/ChatGPT --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=" + profile + "\n",
	)
	got := controlledDarwinPIDs(output, executable, 9222, profile)
	if want := []int{103}; !reflect.DeepEqual(got, want) {
		t.Fatalf("controlledDarwinPIDs() = %v, want %v", got, want)
	}
}

func TestControlledDarwinPIDsPreservesAmbiguity(t *testing.T) {
	executable := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	profile := "/Users/example/Library/Application Support/Codex"
	command := executable + " --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=" + profile
	output := []byte("  201 " + command + "\n  202 " + command + "\n")
	got := controlledDarwinPIDs(output, executable, 9222, profile)
	if want := []int{201, 202}; !reflect.DeepEqual(got, want) {
		t.Fatalf("controlledDarwinPIDs() = %v, want %v", got, want)
	}
}

func TestCurrentOfficialCodexIdentity(t *testing.T) {
	if os.Getenv("CODEX_SKIN_REAL_CODEX") != "1" {
		t.Skip("set CODEX_SKIN_REAL_CODEX=1 for the local Gate B identity probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := verifyDarwinBundle(ctx, "/Applications/ChatGPT.app"); err != nil {
		t.Fatalf("verifyDarwinBundle() error = %v", err)
	}
	installation, err := DiscoverInstallation(ctx)
	if err != nil {
		t.Fatalf("DiscoverInstallation() error = %v", err)
	}
	if installation.Platform != "macos" ||
		installation.AppIdentifier != officialBundleID ||
		installation.Publisher != officialTeamID ||
		installation.Version == "" ||
		len(installation.ExecutableSHA256) != 64 {
		t.Fatalf("installation = %#v", installation)
	}
	current, err := DiscoverCurrentInstance(ctx, installation)
	if err != nil {
		t.Fatalf("DiscoverCurrentInstance() error = %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if current.Process.ProcessID < 1 ||
		current.Profile != filepath.Join(home, "Library", "Application Support", "Codex") {
		t.Fatalf("current instance = %#v", current)
	}
	t.Logf(
		"verified appIdentifier=%s publisher=%s version=%s executableSHA256=%s currentPID=%d profile=%s controlledPort=%d",
		installation.AppIdentifier,
		installation.Publisher,
		installation.Version,
		installation.ExecutableSHA256,
		current.Process.ProcessID,
		current.Profile,
		current.ControlledPort,
	)
}
