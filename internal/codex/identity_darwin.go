//go:build darwin

package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	officialBundleID = "com.openai.codex"
	officialTeamID   = "2DC432GLL2"
)

func DiscoverInstallation(ctx context.Context) (Installation, error) {
	candidates := map[string]bool{}
	output, err := exec.CommandContext(ctx, "/usr/bin/mdfind", "kMDItemCFBundleIdentifier == 'com.openai.codex'").Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			path := strings.TrimSpace(line)
			if path != "" {
				candidates[path] = true
			}
		}
	}
	candidates["/Applications/ChatGPT.app"] = true
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		if _, err := os.Lstat(path); err == nil {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	valid := []Installation{}
	for _, path := range paths {
		installation, err := verifyDarwinBundle(ctx, path)
		if err == nil {
			valid = append(valid, installation)
		}
	}
	if len(valid) == 0 {
		return Installation{}, ErrIdentityUntrusted
	}
	if len(valid) > 1 {
		return Installation{}, ErrInstallAmbiguous
	}
	return valid[0], nil
}

func LaunchControlled(ctx context.Context, installation Installation, profile string, port int) (int, error) {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return 0, err
	}
	profile, err := validateLaunchInputs(installation, profile, port)
	if err != nil {
		return 0, err
	}
	arguments := controlledDarwinExecutableArguments(profile, port)
	command := exec.CommandContext(
		ctx,
		"/usr/bin/open",
		controlledDarwinOpenArguments(installation.Root, profile, port)...,
	)
	launchServicesErr := command.Run()
	if launchServicesErr == nil {
		if pid, found, err := waitForControlledDarwinPID(
			ctx, installation.Executable, port, profile, 10*time.Second,
		); err != nil || found {
			return pid, err
		}
	}

	// LaunchServices can acknowledge the bundle launch while discarding the
	// Chromium arguments. If it produced one exact official ordinary process,
	// stop that verified process and retry with the freshly verified executable.
	// This is still the user-approved restart transaction; ambiguous or partially
	// controlled processes fail closed and are never stopped by name alone.
	installation, err = prepareDirectDarwinFallback(ctx, installation, darwinFallbackOperations{
		discoverCurrent: DiscoverCurrentInstance,
		stopCurrent:     StopCurrentInstance,
		discoverStable:  DiscoverStableInstallation,
	})
	if err != nil {
		return 0, err
	}
	profile, err = DefaultUserProfile(installation)
	if err != nil {
		return 0, err
	}
	arguments = controlledDarwinExecutableArguments(profile, port)
	direct := exec.Command(installation.Executable, arguments...)
	if err := direct.Start(); err != nil {
		return 0, fmt.Errorf("%w: verified executable start", ErrLaunchFailed)
	}
	if err := direct.Process.Release(); err != nil {
		return 0, fmt.Errorf("%w: verified executable release", ErrLaunchFailed)
	}
	if pid, found, err := waitForControlledDarwinPID(
		ctx, installation.Executable, port, profile, 10*time.Second,
	); err != nil || found {
		return pid, err
	}
	return 0, fmt.Errorf("%w: controlled process was not found", ErrLaunchFailed)
}

type darwinFallbackOperations struct {
	discoverCurrent func(context.Context, Installation) (CurrentInstance, error)
	stopCurrent     func(context.Context, Installation, CurrentInstance) error
	discoverStable  func(context.Context) (Installation, error)
}

func prepareDirectDarwinFallback(
	ctx context.Context,
	installation Installation,
	operations darwinFallbackOperations,
) (Installation, error) {
	if ctx == nil || operations.discoverCurrent == nil || operations.stopCurrent == nil ||
		operations.discoverStable == nil {
		return Installation{}, ErrLaunchFailed
	}
	current, err := operations.discoverCurrent(ctx, installation)
	switch {
	case errors.Is(err, ErrCurrentMissing):
		// LaunchServices did not leave a process behind. Revalidate the bundle
		// anyway because an in-place app update can complete during this window.
	case err != nil:
		return Installation{}, errors.Join(ErrLaunchFailed, err)
	case current.ControlledPort != 0:
		return Installation{}, fmt.Errorf("%w: unexpected controlled process after LaunchServices", ErrLaunchFailed)
	default:
		if err := operations.stopCurrent(ctx, installation, current); err != nil {
			return Installation{}, errors.Join(ErrLaunchFailed, err)
		}
	}
	fresh, err := operations.discoverStable(ctx)
	if err != nil {
		return Installation{}, errors.Join(ErrLaunchFailed, err)
	}
	return fresh, nil
}

func controlledDarwinOpenArguments(bundle, profile string, port int) []string {
	// The prior instance is gone before this call. `-n -a <verified bundle>`
	// avoids the stale-primary LaunchServices race while preserving app launch
	// semantics. The exact process, flags, signature, executable hash, listener
	// and profile are still verified before CDP is accepted.
	return append([]string{"-n", "-a", bundle, "--args"}, controlledDarwinExecutableArguments(profile, port)...)
}

func controlledDarwinExecutableArguments(profile string, port int) []string {
	return []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profile,
		"--no-first-run",
	}
}

func LaunchOrdinary(ctx context.Context, installation Installation) error {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return err
	}
	command := exec.CommandContext(
		ctx,
		"/usr/bin/open",
		"-b",
		installation.AppIdentifier,
	)
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: reopen ordinary Codex", ErrLaunchFailed)
	}
	return nil
}

func controlledDarwinPIDs(
	output []byte,
	executable string,
	port int,
	profile string,
) []int {
	pids := []int{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		separator := strings.IndexAny(line, " \t")
		if separator < 1 {
			continue
		}
		pid, err := strconv.Atoi(line[:separator])
		if err != nil || pid < 1 {
			continue
		}
		commandLine := strings.TrimSpace(line[separator:])
		if commandLine != executable &&
			!strings.HasPrefix(commandLine, executable+" ") {
			continue
		}
		if requireControlledFlags(commandLine, port, profile) == nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

func waitForControlledDarwinPID(
	ctx context.Context,
	executable string,
	port int,
	profile string,
	timeout time.Duration,
) (int, bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-axo", "pid=,command=").Output()
		if err == nil {
			pids := controlledDarwinPIDs(output, executable, port, profile)
			switch len(pids) {
			case 1:
				return pids[0], true, nil
			case 0:
			default:
				return 0, false, fmt.Errorf("%w: controlled process is ambiguous", ErrLaunchFailed)
			}
		}
		select {
		case <-ctx.Done():
			return 0, false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return 0, false, nil
}

func darwinExecutablePIDs(ctx context.Context, executable string) ([]int, error) {
	output, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: process inventory", ErrLaunchFailed)
	}
	pids := []int{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		if parseErr == nil && pid > 0 && samePath(strings.Join(fields[1:], " "), executable) {
			pids = append(pids, pid)
		}
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("%w: process inventory", ErrLaunchFailed)
	}
	sort.Ints(pids)
	return pids, nil
}

func VerifyListener(ctx context.Context, installation Installation, launchedPID, port int, profile string) (ProcessIdentity, error) {
	if launchedPID < 1 || port < 1 || port > 65535 {
		return ProcessIdentity{}, ErrListenerUntrusted
	}
	command := exec.CommandContext(
		ctx,
		"/usr/sbin/lsof",
		"-nP",
		"-a",
		"-iTCP:"+strconv.Itoa(port),
		"-sTCP:LISTEN",
		"-Fpn",
	)
	output, err := command.Output()
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("%w: listener query", ErrListenerUntrusted)
	}
	pids := map[int]bool{}
	loopback := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "p") {
			pid, _ := strconv.Atoi(strings.TrimPrefix(line, "p"))
			if pid > 0 {
				pids[pid] = true
			}
		}
		if strings.HasPrefix(line, "n127.0.0.1:"+strconv.Itoa(port)) {
			loopback = true
		}
	}
	if scanner.Err() != nil || len(pids) < 1 || !loopback || !pids[launchedPID] {
		return ProcessIdentity{}, fmt.Errorf(
			"%w: listener tuple count=%d loopback=%t launched=%t",
			ErrListenerUntrusted,
			len(pids),
			loopback,
			pids[launchedPID],
		)
	}
	for pid := range pids {
		if pid == launchedPID {
			continue
		}
		if err := verifyDarwinSessionMember(ctx, installation, pid, launchedPID, profile); err != nil {
			return ProcessIdentity{}, fmt.Errorf("%w: listener process tree: %v", ErrListenerUntrusted, err)
		}
	}
	return VerifyProcess(ctx, installation, launchedPID, port, profile)
}

func VerifyProcess(ctx context.Context, installation Installation, pid, port int, profile string) (ProcessIdentity, error) {
	commandOutput, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("%w: process query", ErrListenerUntrusted)
	}
	commandLine := strings.TrimSpace(string(commandOutput))
	if err := requireControlledFlags(commandLine, port, profile); err != nil {
		return ProcessIdentity{}, fmt.Errorf("%w: controlled flags", ErrListenerUntrusted)
	}
	if !strings.HasPrefix(commandLine, installation.Executable+" ") && commandLine != installation.Executable {
		return ProcessIdentity{}, fmt.Errorf("%w: executable command", ErrListenerUntrusted)
	}
	startOutput, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil || strings.TrimSpace(string(startOutput)) == "" {
		return ProcessIdentity{}, fmt.Errorf("%w: process start", ErrListenerUntrusted)
	}
	digest, err := hashOrdinaryFile(installation.Executable)
	if err != nil || digest != installation.ExecutableSHA256 {
		return ProcessIdentity{}, fmt.Errorf("%w: executable digest", ErrListenerUntrusted)
	}
	return ProcessIdentity{
		ProcessID: pid, ProcessStartID: strings.Join(strings.Fields(string(startOutput)), " "),
		Executable: installation.Executable, ExecutableSHA256: digest,
		CommandLine: commandLine,
	}, nil
}

func StopOwnedProcess(
	ctx context.Context,
	installation Installation,
	expected ProcessIdentity,
	port int,
	profile string,
) error {
	current, err := VerifyProcess(ctx, installation, expected.ProcessID, port, profile)
	if err != nil && errors.Is(syscall.Kill(expected.ProcessID, 0), syscall.ESRCH) {
		return nil
	}
	if err != nil ||
		current.ProcessStartID != expected.ProcessStartID ||
		current.ExecutableSHA256 != expected.ExecutableSHA256 {
		return ErrListenerUntrusted
	}
	process, err := os.FindProcess(expected.ProcessID)
	if err != nil {
		return ErrListenerUntrusted
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("%w: terminate controlled process", ErrLaunchFailed)
	}
	for attempts := 0; attempts < 40; attempts++ {
		if err := syscall.Kill(expected.ProcessID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%w: controlled process did not exit", ErrLaunchFailed)
}

func DefaultUserProfile(installation Installation) (string, error) {
	if installation.Platform != "macos" {
		return "", ErrCurrentUnsafe
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ErrCurrentUnsafe
	}
	expected := filepath.Join(home, "Library", "Application Support", "Codex")
	return validateCurrentProfile(expected, expected)
}

func DiscoverCurrentInstance(ctx context.Context, installation Installation) (CurrentInstance, error) {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return CurrentInstance{}, err
	}
	expectedProfile, err := DefaultUserProfile(installation)
	if err != nil {
		return CurrentInstance{}, err
	}
	output, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-axo", "pid=,comm=").Output()
	if err != nil {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	pids := []int{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		executable := strings.Join(fields[1:], " ")
		if parseErr == nil && pid > 0 && samePath(executable, installation.Executable) {
			pids = append(pids, pid)
		}
	}
	if scanner.Err() != nil {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	if len(pids) == 0 {
		return CurrentInstance{}, ErrCurrentMissing
	}
	if len(pids) != 1 {
		return CurrentInstance{}, ErrCurrentAmbiguous
	}
	process, commandLine, err := inspectDarwinCurrentProcess(ctx, installation, pids[0])
	if err != nil {
		return CurrentInstance{}, err
	}
	profile := expectedProfile
	if candidate, found := darwinFlagValue(commandLine, "--user-data-dir="); found {
		profile, err = validateCurrentProfile(candidate, expectedProfile)
		if err != nil {
			return CurrentInstance{}, err
		}
	}
	port, controlled, err := controlledFlags(commandLine, profile)
	if err != nil {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	if controlled {
		verified, verifyErr := VerifyListener(ctx, installation, process.ProcessID, port, profile)
		if verifyErr != nil ||
			verified.ProcessStartID != process.ProcessStartID ||
			verified.ExecutableSHA256 != process.ExecutableSHA256 {
			return CurrentInstance{}, ErrCurrentUnsafe
		}
		process = verified
	}
	return CurrentInstance{
		Process: process, Profile: profile, ControlledPort: port,
	}, nil
}

func StopCurrentInstance(
	ctx context.Context,
	installation Installation,
	expected CurrentInstance,
) error {
	current, err := DiscoverCurrentInstance(ctx, installation)
	if err != nil ||
		current.Process.ProcessID != expected.Process.ProcessID ||
		current.Process.ProcessStartID != expected.Process.ProcessStartID ||
		current.Process.ExecutableSHA256 != expected.Process.ExecutableSHA256 ||
		!samePath(current.Profile, expected.Profile) ||
		current.ControlledPort != expected.ControlledPort {
		return ErrCurrentUnsafe
	}
	process, err := os.FindProcess(expected.Process.ProcessID)
	if err != nil {
		return ErrCurrentUnsafe
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("%w: request current Codex exit", ErrLaunchFailed)
	}
	for attempts := 0; attempts < 60; attempts++ {
		if err := syscall.Kill(expected.Process.ProcessID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%w: current Codex did not exit normally", ErrLaunchFailed)
}

func inspectDarwinCurrentProcess(
	ctx context.Context,
	installation Installation,
	pid int,
) (ProcessIdentity, string, error) {
	commandOutput, err := exec.CommandContext(
		ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=",
	).Output()
	if err != nil {
		return ProcessIdentity{}, "", ErrCurrentUnsafe
	}
	commandLine := strings.TrimSpace(string(commandOutput))
	if commandLine != installation.Executable &&
		!strings.HasPrefix(commandLine, installation.Executable+" ") {
		return ProcessIdentity{}, "", ErrCurrentUnsafe
	}
	startOutput, err := exec.CommandContext(
		ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=",
	).Output()
	if err != nil || strings.TrimSpace(string(startOutput)) == "" {
		return ProcessIdentity{}, "", ErrCurrentUnsafe
	}
	digest, err := hashOrdinaryFile(installation.Executable)
	if err != nil || digest != installation.ExecutableSHA256 {
		return ProcessIdentity{}, "", ErrCurrentUnsafe
	}
	return ProcessIdentity{
		ProcessID: pid, ProcessStartID: strings.Join(strings.Fields(string(startOutput)), " "),
		Executable: installation.Executable, ExecutableSHA256: digest,
		CommandLine: commandLine,
	}, commandLine, nil
}

func darwinFlagValue(commandLine, marker string) (string, bool) {
	index := strings.Index(commandLine, marker)
	if index < 0 {
		return "", false
	}
	value := commandLine[index+len(marker):]
	if strings.HasPrefix(value, `"`) {
		value = strings.TrimPrefix(value, `"`)
		end := strings.Index(value, `"`)
		if end < 0 {
			return "", true
		}
		return value[:end], true
	}
	if end := strings.Index(value, " --"); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value), true
}

func verifyDarwinBundle(ctx context.Context, bundle string) (Installation, error) {
	info, err := os.Lstat(bundle)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || filepath.Ext(bundle) != ".app" {
		return Installation{}, ErrIdentityUntrusted
	}
	infoPlist := filepath.Join(bundle, "Contents", "Info.plist")
	bundleID, err := plistValue(ctx, infoPlist, "CFBundleIdentifier")
	if err != nil || bundleID != officialBundleID {
		return Installation{}, ErrIdentityUntrusted
	}
	executableName, err := plistValue(ctx, infoPlist, "CFBundleExecutable")
	if err != nil || executableName == "" || filepath.Base(executableName) != executableName {
		return Installation{}, ErrIdentityUntrusted
	}
	version, err := plistValue(ctx, infoPlist, "CFBundleShortVersionString")
	if err != nil || version == "" {
		return Installation{}, ErrIdentityUntrusted
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--deep", bundle).Run(); err != nil {
		return Installation{}, ErrIdentityUntrusted
	}
	details, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", bundle).CombinedOutput()
	if err != nil ||
		!lineEquals(details, "Identifier", officialBundleID) ||
		!lineEquals(details, "TeamIdentifier", officialTeamID) ||
		!bytes.Contains(details, []byte("Authority=Developer ID Application: OpenAI OpCo, LLC ("+officialTeamID+")")) {
		return Installation{}, ErrIdentityUntrusted
	}
	assessment, err := exec.CommandContext(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=4", bundle).CombinedOutput()
	if err != nil || !bytes.Contains(bytes.ToLower(assessment), []byte("accepted")) {
		return Installation{}, ErrIdentityUntrusted
	}
	executable := filepath.Join(bundle, "Contents", "MacOS", executableName)
	digest, err := hashOrdinaryFile(executable)
	if err != nil {
		return Installation{}, err
	}
	return Installation{
		Platform: "macos", AppIdentifier: officialBundleID, Publisher: officialTeamID,
		Version: version, Root: bundle, Executable: executable, ExecutableSHA256: digest,
	}, nil
}

func verifyDarwinSessionMember(
	ctx context.Context,
	installation Installation,
	pid int,
	launchedPID int,
	profile string,
) error {
	commandOutput, err := exec.CommandContext(
		ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=",
	).Output()
	if err != nil {
		return fmt.Errorf("%w: member command", ErrListenerUntrusted)
	}
	executableOutput, err := exec.CommandContext(
		ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "comm=",
	).Output()
	if err != nil {
		return fmt.Errorf("%w: member executable", ErrListenerUntrusted)
	}
	executable := strings.TrimSpace(string(executableOutput))
	hasControlledProfile := strings.Contains(string(commandOutput), "--user-data-dir="+profile)
	isSignedComputerUseChild := filepath.Base(executable) == "SkyComputerUseService"
	if !hasControlledProfile && !isSignedComputerUseChild {
		return fmt.Errorf(
			"%w: member profile executable=%s type=%s",
			ErrListenerUntrusted,
			filepath.Base(executable),
			darwinProcessType(string(commandOutput)),
		)
	}
	relative, err := filepath.Rel(installation.Root, executable)
	outsideOfficialBundle := err != nil ||
		relative == "." ||
		relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator))
	if outsideOfficialBundle && !isSignedComputerUseChild {
		return fmt.Errorf(
			"%w: member bundle path executable=%s controlledProfile=%t",
			ErrListenerUntrusted,
			filepath.Base(executable),
			hasControlledProfile,
		)
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", executable).Run(); err != nil {
		return fmt.Errorf("%w: member signature", ErrListenerUntrusted)
	}
	details, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", executable).CombinedOutput()
	if err != nil || !lineEquals(details, "TeamIdentifier", officialTeamID) {
		return fmt.Errorf("%w: member team", ErrListenerUntrusted)
	}
	current := pid
	for depth := 0; depth < 8; depth++ {
		parentOutput, err := exec.CommandContext(
			ctx, "/bin/ps", "-p", strconv.Itoa(current), "-o", "ppid=",
		).Output()
		if err != nil {
			return fmt.Errorf("%w: member parent query", ErrListenerUntrusted)
		}
		parent, err := strconv.Atoi(strings.TrimSpace(string(parentOutput)))
		if err != nil || parent < 1 {
			return fmt.Errorf("%w: member parent shape", ErrListenerUntrusted)
		}
		if parent == launchedPID {
			return nil
		}
		current = parent
	}
	return fmt.Errorf("%w: member ancestry", ErrListenerUntrusted)
}

func darwinProcessType(commandLine string) string {
	const marker = "--type="
	index := strings.Index(commandLine, marker)
	if index < 0 {
		return "browser"
	}
	value := commandLine[index+len(marker):]
	if end := strings.IndexAny(value, " \t\r\n"); end >= 0 {
		value = value[:end]
	}
	if value == "" || len(value) > 64 {
		return "invalid"
	}
	return value
}

func verifyInstallationFresh(ctx context.Context, expected Installation) error {
	current, err := verifyDarwinBundle(ctx, expected.Root)
	if err != nil ||
		current.AppIdentifier != expected.AppIdentifier ||
		current.Publisher != expected.Publisher ||
		current.Version != expected.Version ||
		current.ExecutableSHA256 != expected.ExecutableSHA256 ||
		!samePath(current.Executable, expected.Executable) {
		return ErrIdentityUntrusted
	}
	return nil
}

func plistValue(ctx context.Context, plistPath, key string) (string, error) {
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", ErrIdentityUntrusted
	}
	output, err := exec.CommandContext(
		ctx,
		"/usr/bin/plutil",
		"-extract",
		key,
		"raw",
		"-o",
		"-",
		plistPath,
	).Output()
	if err != nil {
		return "", ErrIdentityUntrusted
	}
	return strings.TrimSpace(string(output)), nil
}

func lineEquals(content []byte, key, value string) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == key+"="+value {
			return true
		}
	}
	return false
}
