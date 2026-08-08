package codex

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

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
