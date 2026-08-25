//go:build darwin

package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestDirectDarwinFallbackStopsOnlyVerifiedOrdinaryProcessAndRediscoversApp(t *testing.T) {
	original := Installation{
		Platform: "macos", AppIdentifier: officialBundleID, Publisher: officialTeamID,
		Version: "26.727.1", Root: "/Applications/ChatGPT.app",
		Executable:       "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		ExecutableSHA256: "old",
	}
	fresh := original
	fresh.Version = "26.727.2"
	fresh.ExecutableSHA256 = "fresh"
	ordinary := CurrentInstance{Process: ProcessIdentity{ProcessID: 4312}, ControlledPort: 0}
	stopped := false
	got, err := prepareDirectDarwinFallback(context.Background(), original, darwinFallbackOperations{
		discoverCurrent: func(context.Context, Installation) (CurrentInstance, error) {
			return ordinary, nil
		},
		stopCurrent: func(_ context.Context, installation Installation, current CurrentInstance) error {
			if !sameInstallation(installation, original) || current.Process.ProcessID != ordinary.Process.ProcessID {
				t.Fatal("fallback did not bind the exact verified process")
			}
			stopped = true
			return nil
		},
		discoverStable: func(context.Context) (Installation, error) { return fresh, nil },
	})
	if err != nil || !stopped || !sameInstallation(got, fresh) {
		t.Fatalf("fallback = %#v, stopped=%t, err=%v", got, stopped, err)
	}
}

func TestDirectDarwinFallbackRejectsUnexpectedControlledOrAmbiguousProcess(t *testing.T) {
	installation := Installation{Platform: "macos", AppIdentifier: officialBundleID}
	for _, test := range []struct {
		name     string
		current  CurrentInstance
		discover error
	}{
		{name: "controlled", current: CurrentInstance{ControlledPort: 9341}},
		{name: "ambiguous", discover: ErrCurrentAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			stopCalled := false
			_, err := prepareDirectDarwinFallback(context.Background(), installation, darwinFallbackOperations{
				discoverCurrent: func(context.Context, Installation) (CurrentInstance, error) {
					return test.current, test.discover
				},
				stopCurrent: func(context.Context, Installation, CurrentInstance) error {
					stopCalled = true
					return nil
				},
				discoverStable: func(context.Context) (Installation, error) {
					return installation, nil
				},
			})
			if !errors.Is(err, ErrLaunchFailed) || stopCalled {
				t.Fatalf("err=%v stopCalled=%t", err, stopCalled)
			}
		})
	}
}

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

func TestPlistValueReadsProvidedSnapshot(t *testing.T) {
	plist := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.openai.codex</string></dict></plist>`)
	got, err := plistValue(context.Background(), plist, "CFBundleIdentifier")
	if err != nil || got != officialBundleID {
		t.Fatalf("plist value = %q, err=%v", got, err)
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
