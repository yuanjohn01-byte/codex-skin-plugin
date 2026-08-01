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
	ErrCurrentMissing    = errors.New("current official Codex instance was not found")
	ErrCurrentAmbiguous  = errors.New("current official Codex instance is ambiguous")
	ErrCurrentUnsafe     = errors.New("current official Codex instance is unsafe")
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

type CurrentInstance struct {
	Process        ProcessIdentity
	Profile        string
	ControlledPort int
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
	created := false
	if info, err := os.Lstat(absolute); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrLaunchFailed
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", ErrLaunchFailed
	} else if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", ErrLaunchFailed
	} else {
		created = true
	}
	if created {
		if err := os.Chmod(absolute, 0o700); err != nil && installation.Platform != "windows" {
			return "", ErrLaunchFailed
		}
	}
	if info, err := os.Lstat(absolute); err != nil ||
		!info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return "", ErrLaunchFailed
	}
	return absolute, nil
}

func requireControlledFlags(commandLine string, port int, profile string) error {
	actualPort, controlled, err := controlledFlags(commandLine, profile)
	if err != nil || !controlled || actualPort != port {
		return ErrListenerUntrusted
	}
	return nil
}

func controlledFlags(commandLine, profile string) (int, bool, error) {
	address, addressCount, addressErr := commandFlagValue(
		commandLine,
		"--remote-debugging-address=",
	)
	portText, portCount, portErr := commandFlagValue(
		commandLine,
		"--remote-debugging-port=",
	)
	actualProfile, profileCount, profileErr := commandFlagValue(
		commandLine,
		"--user-data-dir=",
	)
	hasAddress := addressCount > 0
	hasPort := portCount > 0
	hasProfile := profileCount > 0
	if !hasAddress && !hasPort && !hasProfile {
		return 0, false, nil
	}
	if addressErr != nil || portErr != nil || profileErr != nil ||
		addressCount != 1 || portCount != 1 || profileCount != 1 ||
		address != "127.0.0.1" || !samePath(actualProfile, profile) {
		return 0, false, ErrListenerUntrusted
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, false, ErrListenerUntrusted
	}
	return port, true, nil
}

func commandFlagValue(commandLine, marker string) (string, int, error) {
	count := strings.Count(commandLine, marker)
	if count == 0 {
		return "", 0, nil
	}
	if count != 1 {
		return "", count, ErrListenerUntrusted
	}
	index := strings.Index(commandLine, marker)
	if index > 0 {
		previous := commandLine[index-1]
		if previous != ' ' && previous != '\t' {
			return "", count, ErrListenerUntrusted
		}
	}
	value := commandLine[index+len(marker):]
	if value == "" {
		return "", count, ErrListenerUntrusted
	}
	if value[0] == '"' {
		value = value[1:]
		end := strings.IndexByte(value, '"')
		if end < 0 ||
			end+1 < len(value) &&
				value[end+1] != ' ' &&
				value[end+1] != '\t' {
			return "", count, ErrListenerUntrusted
		}
		return value[:end], count, nil
	}
	if end := strings.Index(value, " --"); end >= 0 {
		value = value[:end]
	} else if end := strings.IndexByte(value, '\t'); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", count, ErrListenerUntrusted
	}
	return value, count, nil
}

func validateCurrentProfile(path, expected string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || absolute != filepath.Clean(path) || !samePath(absolute, expected) {
		return "", ErrCurrentUnsafe
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrCurrentUnsafe
	}
	return absolute, nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}
