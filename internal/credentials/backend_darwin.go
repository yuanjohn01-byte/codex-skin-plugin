//go:build darwin

package credentials

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

const securityBinary = "/usr/bin/security"

type darwinBackend struct {
	keychain string
}

func newBackend() (backend, error) {
	return &darwinBackend{}, nil
}

func newDarwinBackend(keychain string) backend {
	return &darwinBackend{keychain: keychain}
}

func (backend *darwinBackend) put(ctx context.Context, deviceID string, secret []byte) error {
	prefix := []byte("add-generic-password -a " + deviceID + " -s " + keychainService + " -U -w ")
	payload := make([]byte, 0, len(prefix)+len(secret)+len(backend.keychain)+8)
	payload = append(payload, prefix...)
	payload = appendSecurityQuoted(payload, secret)
	if backend.keychain != "" {
		payload = append(payload, ' ')
		payload = appendSecurityQuoted(payload, []byte(backend.keychain))
	}
	payload = append(payload, '\n')
	defer zeroBytes(payload)
	command := exec.CommandContext(ctx, securityBinary, "-i")
	command.Stdin = bytes.NewReader(payload)
	if err := command.Run(); err != nil {
		return commandError(ctx, err)
	}
	return nil
}

func appendSecurityQuoted(destination, value []byte) []byte {
	destination = append(destination, '"')
	for _, character := range value {
		if character == '\\' || character == '"' {
			destination = append(destination, '\\')
		}
		destination = append(destination, character)
	}
	return append(destination, '"')
}

func (backend *darwinBackend) get(ctx context.Context, deviceID string) ([]byte, error) {
	arguments := []string{
		"find-generic-password",
		"-a", deviceID,
		"-s", keychainService,
		"-w",
	}
	if backend.keychain != "" {
		arguments = append(arguments, backend.keychain)
	}
	command := exec.CommandContext(ctx, securityBinary, arguments...)
	output, err := command.Output()
	if err != nil {
		zeroBytes(output)
		return nil, commandError(ctx, err)
	}
	output = bytes.TrimSuffix(output, []byte{'\n'})
	output = bytes.TrimSuffix(output, []byte{'\r'})
	return output, nil
}

func (backend *darwinBackend) delete(ctx context.Context, deviceID string) error {
	arguments := []string{
		"delete-generic-password",
		"-a", deviceID,
		"-s", keychainService,
	}
	if backend.keychain != "" {
		arguments = append(arguments, backend.keychain)
	}
	command := exec.CommandContext(ctx, securityBinary, arguments...)
	if err := command.Run(); err != nil {
		return commandError(ctx, err)
	}
	return nil
}

func commandError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 44 {
		return ErrNotFound
	}
	return ErrUnavailable
}
