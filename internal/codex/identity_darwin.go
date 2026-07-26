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
	command := exec.Command(
		installation.Executable,
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port="+strconv.Itoa(port),
		"--user-data-dir="+profile,
		"--no-first-run",
	)
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("%w: start", ErrLaunchFailed)
	}
	pid := command.Process.Pid
	go func() {
		_ = command.Wait()
	}()
	return pid, nil
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
