// Package codex discovers and verifies the official Codex Desktop installation,
// controlled process, and loopback CDP listener.
package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrIdentityUntrusted = errors.New("official Codex identity could not be verified")
	ErrInstallAmbiguous  = errors.New("multiple official Codex installations were found")
	ErrListenerUntrusted = errors.New("CDP listener ownership could not be verified")
	ErrLaunchFailed      = errors.New("controlled Codex launch failed")
)

type Installation struct {
	Platform          string
	AppIdentifier     string
	Publisher         string
	Version           string
	Root              string
	Executable        string
	ExecutableSHA256  string
	PackageFullName   string
	PackageFamilyName string
	AppUserModelID    string
}

type ProcessIdentity struct {
	ProcessID        int
	ProcessStartID   string
	Executable       string
	ExecutableSHA256 string
	CommandLine      string
}

func hashOrdinaryFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: executable file", ErrIdentityUntrusted)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: executable open", ErrIdentityUntrusted)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("%w: executable hash", ErrIdentityUntrusted)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func validateLaunchInputs(installation Installation, profile string, port int) (string, error) {
	if port < 1 || port > 65535 ||
		installation.Executable == "" ||
		installation.ExecutableSHA256 == "" {
		return "", ErrLaunchFailed
	}
	absolute, err := filepath.Abs(profile)
	if err != nil || absolute != filepath.Clean(profile) {
		return "", ErrLaunchFailed
	}
	if info, err := os.Lstat(absolute); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrLaunchFailed
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", ErrLaunchFailed
	} else if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", ErrLaunchFailed
	}
	if err := os.Chmod(absolute, 0o700); err != nil && installation.Platform != "windows" {
		return "", ErrLaunchFailed
	}
	return absolute, nil
}

func requireControlledFlags(commandLine string, port int, profile string) error {
	required := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profile,
	}
	for _, item := range required {
		if !strings.Contains(commandLine, item) {
			return ErrListenerUntrusted
		}
	}
	return nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}
