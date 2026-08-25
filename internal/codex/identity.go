// Package codex discovers and verifies the official Codex Desktop installation,
// controlled process, and loopback CDP listener.
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	stableInstallationPollInterval  = 500 * time.Millisecond
	stableInstallationConfirmations = 5
	currentInstancePollInterval     = 250 * time.Millisecond
	currentInstanceConfirmations    = 4
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

// DiscoverStableInstallation rediscovers the official Codex installation at
// the launch boundary and requires a fully verified identity before and after
// a series of cheap immutable-file probes. confirmations counts both fully
// verified endpoints, so the normal five-observation sequence is:
// full -> probe -> probe -> probe -> full. Codex may replace its bundle/package
// while an older process is still running; a stop-and-relaunch flow must never
// reuse the pre-update executable hash after that replacement.
func DiscoverStableInstallation(ctx context.Context) (Installation, error) {
	return discoverStableInstallation(
		ctx,
		DiscoverInstallation,
		probeStableInstallation,
		stableInstallationPollInterval,
		stableInstallationConfirmations,
	)
}

func discoverStableInstallation(
	ctx context.Context,
	discover func(context.Context) (Installation, error),
	probe func(context.Context, Installation) (Installation, error),
	interval time.Duration,
	confirmations int,
) (Installation, error) {
	if ctx == nil || discover == nil || probe == nil || interval <= 0 || confirmations < 3 {
		return Installation{}, ErrIdentityUntrusted
	}
	for {
		candidate, err := discover(ctx)
		if err == nil {
			stable := true
			// Reserve the final observation for a full platform verification.
			// This preserves the original five-observation policy on platforms
			// whose probes are full checks, while avoiding repeated expensive
			// codesign/spctl work on macOS during the restart race.
			for confirmation := 1; confirmation < confirmations-1; confirmation++ {
				select {
				case <-ctx.Done():
					return Installation{}, errors.Join(ErrIdentityUntrusted, ctx.Err())
				case <-time.After(interval):
				}
				current, probeErr := probe(ctx, candidate)
				if probeErr != nil || !sameInstallation(candidate, current) {
					stable = false
					break
				}
			}
			if stable {
				// A stable hash is not a substitute for the official signature and
				// Gatekeeper/package verification. Repeat the complete check at the
				// exact launch boundary before returning this installation.
				fresh, finalErr := discover(ctx)
				if finalErr == nil && sameInstallation(candidate, fresh) {
					return fresh, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return Installation{}, errors.Join(ErrIdentityUntrusted, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// WaitForCurrentInstance positively confirms that an official Codex process
// remains the same process for a bounded series of probes. LaunchServices and
// packaged-app activation can accept a request even when the app exits before
// showing a usable window, so an accepted launch request alone is not success.
func WaitForCurrentInstance(ctx context.Context, installation Installation) (CurrentInstance, error) {
	return waitForCurrentInstance(
		ctx,
		installation,
		DiscoverCurrentInstance,
		currentInstancePollInterval,
		currentInstanceConfirmations,
	)
}

func waitForCurrentInstance(
	ctx context.Context,
	installation Installation,
	discover func(context.Context, Installation) (CurrentInstance, error),
	interval time.Duration,
	confirmations int,
) (CurrentInstance, error) {
	if ctx == nil || discover == nil || interval <= 0 || confirmations < 2 {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	var candidate CurrentInstance
	consecutive := 0
	for {
		current, err := discover(ctx, installation)
		if err == nil {
			if consecutive > 0 && sameCurrentInstance(candidate, current) {
				consecutive++
			} else {
				candidate = current
				consecutive = 1
			}
			if consecutive >= confirmations {
				return candidate, nil
			}
		} else {
			candidate = CurrentInstance{}
			consecutive = 0
		}
		select {
		case <-ctx.Done():
			return CurrentInstance{}, errors.Join(ErrCurrentMissing, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func sameInstallation(left, right Installation) bool {
	return left.Platform == right.Platform &&
		left.AppIdentifier == right.AppIdentifier &&
		left.Publisher == right.Publisher &&
		left.Version == right.Version &&
		samePath(left.Root, right.Root) &&
		samePath(left.Executable, right.Executable) &&
		left.ExecutableSHA256 == right.ExecutableSHA256 &&
		left.PackageFullName == right.PackageFullName &&
		left.PackageFamilyName == right.PackageFamilyName &&
		left.AppUserModelID == right.AppUserModelID
}

func sameCurrentInstance(left, right CurrentInstance) bool {
	return left.Process.ProcessID == right.Process.ProcessID &&
		left.Process.ProcessStartID == right.Process.ProcessStartID &&
		left.Process.ExecutableSHA256 == right.Process.ExecutableSHA256 &&
		samePath(left.Process.Executable, right.Process.Executable) &&
		samePath(left.Profile, right.Profile) &&
		left.ControlledPort == right.ControlledPort
}

func hashOrdinaryFile(path string) (string, error) {
	file, identity, err := openOrdinaryFile(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("%w: executable hash", ErrIdentityUntrusted)
	}
	if err := verifyOpenOrdinaryFile(path, file, identity); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func readBoundedOrdinaryFile(path string, limit int64) ([]byte, error) {
	if limit < 1 {
		return nil, ErrIdentityUntrusted
	}
	file, identity, err := openOrdinaryFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if identity.Size() < 1 || identity.Size() > limit {
		return nil, fmt.Errorf("%w: ordinary file size", ErrIdentityUntrusted)
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) != identity.Size() {
		return nil, fmt.Errorf("%w: ordinary file read", ErrIdentityUntrusted)
	}
	if err := verifyOpenOrdinaryFile(path, file, identity); err != nil {
		return nil, err
	}
	return content, nil
}

func openOrdinaryFile(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%w: ordinary file", ErrIdentityUntrusted)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: ordinary file open", ErrIdentityUntrusted)
	}
	opened, openedErr := file.Stat()
	after, afterErr := os.Lstat(path)
	if openedErr != nil || afterErr != nil ||
		!opened.Mode().IsRegular() || !after.Mode().IsRegular() ||
		after.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%w: ordinary file changed before open", ErrIdentityUntrusted)
	}
	return file, opened, nil
}

func verifyOpenOrdinaryFile(path string, file *os.File, identity os.FileInfo) error {
	opened, openedErr := file.Stat()
	current, currentErr := os.Lstat(path)
	if openedErr != nil || currentErr != nil ||
		!opened.Mode().IsRegular() || !current.Mode().IsRegular() ||
		current.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(identity, opened) || !os.SameFile(opened, current) ||
		opened.Size() != identity.Size() ||
		!opened.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("%w: ordinary file changed during read", ErrIdentityUntrusted)
	}
	return nil
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
