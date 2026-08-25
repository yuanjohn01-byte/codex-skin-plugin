package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverStableInstallationUsesCheapProbesBetweenFullChecks(t *testing.T) {
	candidate := testInstallation("26.818.31338", "first")
	discoverCalls := 0
	probeCalls := 0
	got, err := discoverStableInstallation(
		context.Background(),
		func(context.Context) (Installation, error) {
			discoverCalls++
			return candidate, nil
		},
		func(_ context.Context, expected Installation) (Installation, error) {
			probeCalls++
			return expected, nil
		},
		time.Millisecond,
		4,
	)
	if err != nil || !sameInstallation(got, candidate) {
		t.Fatalf("stable installation = %#v, err=%v", got, err)
	}
	if discoverCalls != 2 {
		t.Fatalf("complete discovery calls = %d, want 2", discoverCalls)
	}
	if probeCalls != 2 {
		t.Fatalf("cheap probe calls = %d, want 2", probeCalls)
	}
}

func TestDiscoverStableInstallationRestartsAfterProbeDrift(t *testing.T) {
	old := testInstallation("26.818.31338", "old")
	fresh := testInstallation("26.819.11345", "fresh")
	discoverCalls := 0
	probeCalls := 0
	got, err := discoverStableInstallation(
		context.Background(),
		func(context.Context) (Installation, error) {
			discoverCalls++
			switch discoverCalls {
			case 1:
				return old, nil
			default:
				return fresh, nil
			}
		},
		func(_ context.Context, expected Installation) (Installation, error) {
			probeCalls++
			if probeCalls == 1 && sameInstallation(expected, old) {
				return fresh, nil
			}
			return expected, nil
		},
		time.Millisecond,
		4,
	)
	if err != nil || !sameInstallation(got, fresh) {
		t.Fatalf("stable installation after drift = %#v, err=%v", got, err)
	}
	if discoverCalls != 3 {
		t.Fatalf("complete discovery calls = %d, want 3", discoverCalls)
	}
	if probeCalls != 3 {
		t.Fatalf("cheap probe calls = %d, want 3", probeCalls)
	}
}

func TestDiscoverStableInstallationRejectsMissingProbe(t *testing.T) {
	_, err := discoverStableInstallation(
		context.Background(),
		func(context.Context) (Installation, error) { return Installation{}, nil },
		nil,
		time.Millisecond,
		3,
	)
	if !errors.Is(err, ErrIdentityUntrusted) {
		t.Fatalf("missing stable probe error = %v", err)
	}
}

func TestDiscoverStableInstallationRestartsAfterFinalFullCheckDrift(t *testing.T) {
	old := testInstallation("26.818.31338", "old")
	fresh := testInstallation("26.819.11345", "fresh")
	discoverCalls := 0
	probeCalls := 0
	got, err := discoverStableInstallation(
		context.Background(),
		func(context.Context) (Installation, error) {
			discoverCalls++
			if discoverCalls == 1 {
				return old, nil
			}
			return fresh, nil
		},
		func(_ context.Context, expected Installation) (Installation, error) {
			probeCalls++
			return expected, nil
		},
		time.Millisecond,
		3,
	)
	if err != nil || !sameInstallation(got, fresh) {
		t.Fatalf("stable installation after final drift = %#v, err=%v", got, err)
	}
	if discoverCalls != 4 || probeCalls != 2 {
		t.Fatalf("full/probe calls = %d/%d, want 4/2", discoverCalls, probeCalls)
	}
}

func TestDiscoverStableInstallationRestartsAfterProbeError(t *testing.T) {
	candidate := testInstallation("26.819.11345", "fresh")
	discoverCalls := 0
	probeCalls := 0
	got, err := discoverStableInstallation(
		context.Background(),
		func(context.Context) (Installation, error) {
			discoverCalls++
			return candidate, nil
		},
		func(_ context.Context, expected Installation) (Installation, error) {
			probeCalls++
			if probeCalls == 1 {
				return Installation{}, ErrIdentityUntrusted
			}
			return expected, nil
		},
		time.Millisecond,
		3,
	)
	if err != nil || !sameInstallation(got, candidate) {
		t.Fatalf("stable installation after probe error = %#v, err=%v", got, err)
	}
	if discoverCalls != 3 || probeCalls != 2 {
		t.Fatalf("full/probe calls = %d/%d, want 3/2", discoverCalls, probeCalls)
	}
}

func TestDiscoverStableInstallationHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := discoverStableInstallation(
		ctx,
		func(context.Context) (Installation, error) {
			return testInstallation("26.819.11345", "fresh"), nil
		},
		func(context.Context, Installation) (Installation, error) {
			t.Fatal("probe must not run after cancellation")
			return Installation{}, nil
		},
		time.Millisecond,
		3,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stable discovery error = %v", err)
	}
}
func testInstallation(version, digest string) Installation {
	return Installation{
		Platform: "macos", AppIdentifier: "com.openai.codex", Publisher: "2DC432GLL2",
		Version: version, Root: "/Applications/ChatGPT.app",
		Executable: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT", ExecutableSHA256: digest,
	}
}

func TestIdentityFileReadersRejectSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("official"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := hashOrdinaryFile(link); !errors.Is(err, ErrIdentityUntrusted) {
		t.Fatalf("symlink hash error = %v", err)
	}
	if _, err := readBoundedOrdinaryFile(link, 1024); !errors.Is(err, ErrIdentityUntrusted) {
		t.Fatalf("symlink read error = %v", err)
	}
}
func TestControlledFlagsRequireExactLoopbackProfileAndSinglePort(t *testing.T) {
	profile := filepath.Join(string(filepath.Separator), "Users", "test", "Application Support", "Codex")
	valid := fmt.Sprintf(
		`/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --remote-debugging-address=127.0.0.1 --remote-debugging-port=32145 --user-data-dir="%s" --no-first-run`,
		profile,
	)
	port, controlled, err := controlledFlags(valid, profile)
	if err != nil || !controlled || port != 32145 {
		t.Fatalf("valid controlled flags = port %d controlled %t error %v", port, controlled, err)
	}
	ordinary := `/Applications/ChatGPT.app/Contents/MacOS/ChatGPT`
	port, controlled, err = controlledFlags(ordinary, profile)
	if err != nil || controlled || port != 0 {
		t.Fatalf("ordinary flags = port %d controlled %t error %v", port, controlled, err)
	}
	for _, commandLine := range []string{
		fmt.Sprintf(
			`ChatGPT --remote-debugging-address=127.0.0.1.evil --remote-debugging-port=32145 --user-data-dir="%s"`,
			profile,
		),
		fmt.Sprintf(
			`ChatGPT --remote-debugging-address=127.0.0.1 --remote-debugging-port=32145 --user-data-dir="%s-copy"`,
			profile,
		),
		fmt.Sprintf(
			`ChatGPT --remote-debugging-address=127.0.0.1 --remote-debugging-port=32145 --remote-debugging-port=32146 --user-data-dir="%s"`,
			profile,
		),
		fmt.Sprintf(
			`ChatGPT --remote-debugging-address=127.0.0.1 --user-data-dir="%s"`,
			profile,
		),
	} {
		if _, _, err := controlledFlags(commandLine, profile); !errors.Is(err, ErrListenerUntrusted) {
			t.Fatalf("spoofed flags did not fail closed: %q, %v", commandLine, err)
		}
	}
}

func TestControlledFlagsAcceptMacUnquotedProfileWithSpaces(t *testing.T) {
	profile := "/Users/test/Library/Application Support/Codex"
	commandLine := `/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --remote-debugging-address=127.0.0.1 --remote-debugging-port=45678 --user-data-dir=/Users/test/Library/Application Support/Codex --no-first-run`
	port, controlled, err := controlledFlags(commandLine, profile)
	if err != nil || !controlled || port != 45678 {
		t.Fatalf("unquoted profile = port %d controlled %t error %v", port, controlled, err)
	}
}
