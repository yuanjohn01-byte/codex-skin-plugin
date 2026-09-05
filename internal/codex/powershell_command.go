package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// Only the Windows identity adapter uses this runner in production. Keeping
// the process boundary separate lets its actual scripts run in regression
// tests without launching, stopping, or attaching to a desktop application.
func runPowerShellCommandJSON(ctx context.Context, executable string, environment []string, script string, args []string, target any) error {
	// -Command string arguments are command text, not script parameters.
	// Send data separately over stdin and splat it into the fixed script block.
	// Base64 keeps stdin ASCII even under Windows PowerShell 5.1's code page;
	// it is transport encoding, not encryption or permission to run input code.
	if args == nil {
		args = []string{}
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("%w: system identity arguments", ErrIdentityUntrusted)
	}
	invocation := `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$codexSkinArguments = @(ConvertFrom-Json -InputObject ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd()))))
& {
` + script + "\n} @codexSkinArguments\n"
	encoded := utf16.Encode([]rune(invocation))
	commandBytes := make([]byte, 2*len(encoded))
	for index, value := range encoded {
		binary.LittleEndian.PutUint16(commandBytes[2*index:], value)
	}
	commandArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-EncodedCommand", base64.StdEncoding.EncodeToString(commandBytes)}
	command := exec.CommandContext(ctx, executable, commandArgs...)
	command.Env = environment
	command.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(payload))
	var output powerShellOutput
	command.Stdout = &output
	// A probe can contain local paths. Never surface or retain raw stderr.
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || output.overflow || output.buffer.Len() < 2 {
		return fmt.Errorf("%w: system identity probe", ErrIdentityUntrusted)
	}
	if bytes.Equal(bytes.TrimSpace(output.buffer.Bytes()), []byte("null")) {
		return fmt.Errorf("%w: system identity JSON", ErrIdentityUntrusted)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.buffer.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: system identity JSON", ErrIdentityUntrusted)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: system identity JSON", ErrIdentityUntrusted)
	}
	return nil
}

// Drain stdout without retaining unbounded output from a failed system probe.
type powerShellOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (output *powerShellOutput) Write(value []byte) (int, error) {
	const limit = 128 * 1024
	length := len(value)
	if remaining := limit - output.buffer.Len(); length > remaining {
		value = value[:remaining]
		output.overflow = true
	}
	_, _ = output.buffer.Write(value)
	return length, nil
}
