// Package bootstrap installs verified Helper releases outside the Plugin cache.
package bootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

const (
	descriptorFilename = "helper-release-descriptor.json"
	signatureFilename  = "helper-release-descriptor.sig"
	currentSchema      = 1
	defaultSelfTest    = 10 * time.Second
)

var (
	ErrUnsafePath     = errors.New("bootstrap path is unsafe")
	ErrConfiguration  = errors.New("bootstrap configuration is invalid")
	ErrDownload       = errors.New("release download failed")
	ErrCurrentInvalid = errors.New("current Helper pointer is invalid")
	ErrSelfTest       = errors.New("installed Helper self-test failed")
	ErrDowngrade      = errors.New("Helper downgrade is not allowed")
)

type Source interface {
	Fetch(ctx context.Context, releaseTag, filename string, maxBytes int64) ([]byte, error)
}

type SelfTester interface {
	Test(ctx context.Context, executable, expectedVersion, expectedPlatform string) error
}

type Config struct {
	Root            string
	PluginCache     string
	ReleaseTag      string
	RuntimeGOOS     string
	RuntimeGOARCH   string
	Source          Source
	TrustedKeys     map[string]ed25519.PublicKey
	SelfTester      SelfTester
	SelfTestTimeout time.Duration
}

type Result struct {
	Root            string
	Executable      string
	HelperVersion   string
	PreviousVersion string
	Reused          bool
}

type currentPointer struct {
	SchemaVersion int    `json:"schemaVersion"`
	HelperVersion string `json:"helperVersion"`
	Platform      string `json:"platform"`
	Filename      string `json:"filename"`
	SHA256        string `json:"sha256"`
}

func Install(ctx context.Context, config Config) (Result, error) {
	if config.Source == nil || config.SelfTester == nil || len(config.TrustedKeys) == 0 {
		return Result{}, fmt.Errorf("%w: incomplete bootstrap configuration", ErrConfiguration)
	}
	goos := config.RuntimeGOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := config.RuntimeGOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	platform, err := releasecontract.PlatformForRuntime(goos, goarch)
	if err != nil {
		return Result{}, err
	}
	if !validReleaseTag(config.ReleaseTag) {
		return Result{}, fmt.Errorf("%w: invalid release tag", ErrDownload)
	}

	root, err := secureAbsolute(config.Root)
	if err != nil {
		return Result{}, err
	}
	cache, err := secureAbsolute(config.PluginCache)
	if err != nil {
		return Result{}, err
	}
	if pathsOverlap(root, cache) {
		return Result{}, fmt.Errorf("%w: application root overlaps Plugin cache", ErrUnsafePath)
	}
	if err := ensureSecureDirectory(root, 0o700); err != nil {
		return Result{}, err
	}
	binRoot := filepath.Join(root, "bin")
	if err := ensureSecureDirectory(binRoot, 0o700); err != nil {
		return Result{}, err
	}

	current, hasCurrent, err := readCurrent(binRoot, platform)
	if err != nil {
		return Result{}, err
	}
	currentVersion := ""
	if hasCurrent {
		currentVersion = current.HelperVersion
	}

	descriptorBytes, err := config.Source.Fetch(ctx, config.ReleaseTag, descriptorFilename, releasecontract.MaxDescriptor)
	if err != nil {
		return Result{}, fmt.Errorf("%w: descriptor: %v", ErrDownload, err)
	}
	signature, err := config.Source.Fetch(ctx, config.ReleaseTag, signatureFilename, ed25519.SignatureSize)
	if err != nil {
		return Result{}, fmt.Errorf("%w: signature: %v", ErrDownload, err)
	}
	if len(signature) != ed25519.SignatureSize {
		return Result{}, fmt.Errorf("%w: signature length", ErrDownload)
	}
	selection, err := releasecontract.SelectVerified(
		descriptorBytes,
		signature,
		config.TrustedKeys,
		currentVersion,
		goos,
		goarch,
	)
	if err != nil {
		return Result{}, err
	}
	if selection.Descriptor.ReleaseTag != config.ReleaseTag {
		return Result{}, fmt.Errorf("%w: requested tag differs from signed descriptor", ErrDownload)
	}
	if selection.Relation == releasecontract.VersionDowngrade {
		return Result{}, ErrDowngrade
	}

	timeout := config.SelfTestTimeout
	if timeout <= 0 {
		timeout = defaultSelfTest
	}
	if selection.Relation == releasecontract.VersionCurrent {
		if !hasCurrent || !pointerMatches(current, selection.Artifact) {
			return Result{}, fmt.Errorf("%w: current release metadata differs", ErrCurrentInvalid)
		}
		executable := filepath.Join(binRoot, current.HelperVersion, current.Filename)
		if err := verifyExisting(ctx, executable, selection.Artifact, config.SelfTester, timeout, platform); err != nil {
			return Result{}, err
		}
		return Result{Root: root, Executable: executable, HelperVersion: current.HelperVersion, PreviousVersion: currentVersion, Reused: true}, nil
	}

	artifactBytes, err := config.Source.Fetch(ctx, config.ReleaseTag, selection.Artifact.Filename, selection.Artifact.Size+1)
	if err != nil {
		return Result{}, fmt.Errorf("%w: artifact: %v", ErrDownload, err)
	}
	if err := releasecontract.VerifyArtifact(bytes.NewReader(artifactBytes), selection.Artifact); err != nil {
		return Result{}, err
	}

	staging, err := os.MkdirTemp(binRoot, ".staging-")
	if err != nil {
		return Result{}, fmt.Errorf("%w: create staging: %v", ErrUnsafePath, err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := rejectSymlink(staging); err != nil {
		return Result{}, err
	}
	stagedExecutable := filepath.Join(staging, selection.Artifact.Filename)
	if err := writeExecutable(stagedExecutable, artifactBytes); err != nil {
		return Result{}, err
	}
	if err := verifyExisting(ctx, stagedExecutable, selection.Artifact, config.SelfTester, timeout, platform); err != nil {
		return Result{}, err
	}

	versionDirectory := filepath.Join(binRoot, selection.Descriptor.HelperVersion)
	moved, err := installVersionDirectory(ctx, staging, versionDirectory, selection.Artifact, config.SelfTester, timeout, platform)
	if err != nil {
		return Result{}, err
	}
	cleanupStaging = !moved
	executable := filepath.Join(versionDirectory, selection.Artifact.Filename)
	pointer := currentPointer{
		SchemaVersion: currentSchema,
		HelperVersion: selection.Descriptor.HelperVersion,
		Platform:      platform,
		Filename:      selection.Artifact.Filename,
		SHA256:        selection.Artifact.SHA256,
	}
	if err := writeCurrent(binRoot, pointer); err != nil {
		return Result{}, err
	}
	return Result{Root: root, Executable: executable, HelperVersion: pointer.HelperVersion, PreviousVersion: currentVersion}, nil
}

func DefaultRoot(goos, home, localAppData string) (string, error) {
	switch goos {
	case "darwin":
		if home == "" {
			return "", fmt.Errorf("%w: HOME is empty", ErrUnsafePath)
		}
		return filepath.Join(home, "Library", "Application Support", "CodexSkin"), nil
	case "windows":
		if localAppData == "" {
			return "", fmt.Errorf("%w: LOCALAPPDATA is empty", ErrUnsafePath)
		}
		separator := `\`
		if strings.Contains(localAppData, "/") && !strings.Contains(localAppData, `\`) {
			separator = "/"
		}
		return strings.TrimRight(localAppData, `\/`) + separator + "CodexSkin", nil
	default:
		return "", releasecontract.ErrPlatformUnsupported
	}
}

func readCurrent(binRoot, expectedPlatform string) (currentPointer, bool, error) {
	path := filepath.Join(binRoot, "current.json")
	if err := rejectSymlinkIfPresent(path); err != nil {
		return currentPointer{}, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return currentPointer{}, false, nil
	}
	if err != nil {
		return currentPointer{}, false, fmt.Errorf("%w: read: %v", ErrCurrentInvalid, err)
	}
	if len(content) == 0 || len(content) > 16*1024 {
		return currentPointer{}, false, fmt.Errorf("%w: byte length", ErrCurrentInvalid)
	}
	var pointer currentPointer
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pointer); err != nil {
		return currentPointer{}, false, fmt.Errorf("%w: decode: %v", ErrCurrentInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return currentPointer{}, false, fmt.Errorf("%w: trailing data", ErrCurrentInvalid)
	}
	if pointer.SchemaVersion != currentSchema || pointer.Platform != expectedPlatform {
		return currentPointer{}, false, fmt.Errorf("%w: schema or platform", ErrCurrentInvalid)
	}
	if _, err := releasecontract.Relation("", pointer.HelperVersion); err != nil {
		return currentPointer{}, false, fmt.Errorf("%w: helper version", ErrCurrentInvalid)
	}
	expected := expectedFilename(pointer.HelperVersion, pointer.Platform)
	if pointer.Filename != expected || !validDigest(pointer.SHA256) {
		return currentPointer{}, false, fmt.Errorf("%w: filename or digest", ErrCurrentInvalid)
	}
	return pointer, true, nil
}

func writeCurrent(binRoot string, pointer currentPointer) error {
	content, err := json.Marshal(pointer)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrCurrentInvalid, err)
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(binRoot, ".current-")
	if err != nil {
		return fmt.Errorf("%w: create pointer: %v", ErrUnsafePath, err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("%w: pointer permissions: %v", ErrUnsafePath, err)
	}
	if _, err := temporary.Write(content); err != nil {
		return fmt.Errorf("%w: write pointer: %v", ErrUnsafePath, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync pointer: %v", ErrUnsafePath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close pointer: %v", ErrUnsafePath, err)
	}
	closed = true
	if err := atomicReplace(temporaryPath, filepath.Join(binRoot, "current.json")); err != nil {
		return fmt.Errorf("%w: activate pointer: %v", ErrUnsafePath, err)
	}
	return syncDirectory(binRoot)
}

func writeExecutable(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("%w: create executable: %v", ErrUnsafePath, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("%w: write executable: %v", ErrUnsafePath, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("%w: sync executable: %v", ErrUnsafePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: close executable: %v", ErrUnsafePath, err)
	}
	closed = true
	return nil
}

func installVersionDirectory(
	ctx context.Context,
	staging, destination string,
	artifact releasecontract.Artifact,
	tester SelfTester,
	timeout time.Duration,
	platform string,
) (bool, error) {
	if err := rejectSymlinkIfPresent(destination); err != nil {
		return false, err
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("%w: version path is not a directory", ErrUnsafePath)
		}
		if err := os.Chmod(destination, 0o700); err != nil {
			return false, fmt.Errorf("%w: version directory permissions: %v", ErrUnsafePath, err)
		}
		existing := filepath.Join(destination, artifact.Filename)
		return false, verifyExisting(ctx, existing, artifact, tester, timeout, platform)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w: inspect version directory: %v", ErrUnsafePath, err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return false, fmt.Errorf("%w: install version directory: %v", ErrUnsafePath, err)
	}
	return true, syncDirectory(filepath.Dir(destination))
}

func verifyExisting(
	parent context.Context,
	executable string,
	artifact releasecontract.Artifact,
	tester SelfTester,
	timeout time.Duration,
	platform string,
) error {
	if err := rejectSymlink(executable); err != nil {
		return err
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: executable is not a regular file", ErrUnsafePath)
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		return fmt.Errorf("%w: executable permissions: %v", ErrUnsafePath, err)
	}
	file, err := os.Open(executable)
	if err != nil {
		return fmt.Errorf("%w: open executable: %v", ErrCurrentInvalid, err)
	}
	verifyErr := releasecontract.VerifyArtifact(file, artifact)
	closeErr := file.Close()
	if verifyErr != nil {
		return verifyErr
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close executable: %v", ErrCurrentInvalid, closeErr)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := tester.Test(ctx, executable, artifactVersion(artifact.Filename), platform); err != nil {
		return fmt.Errorf("%w: %v", ErrSelfTest, err)
	}
	return nil
}

func pointerMatches(pointer currentPointer, artifact releasecontract.Artifact) bool {
	return pointer.Platform == artifact.Platform && pointer.Filename == artifact.Filename && pointer.SHA256 == artifact.SHA256
}

func expectedFilename(version, platform string) string {
	suffixes := map[string]string{
		"macos-arm64": "macos_arm64",
		"macos-x64":   "macos_x64",
		"windows-x64": "windows_x64.exe",
	}
	return "codex-skin-helper_" + version + "_" + suffixes[platform]
}

func artifactVersion(filename string) string {
	const prefix = "codex-skin-helper_"
	trimmed := strings.TrimPrefix(filename, prefix)
	for _, suffix := range []string{"_macos_arm64", "_macos_x64", "_windows_x64.exe"} {
		if strings.HasSuffix(trimmed, suffix) {
			return strings.TrimSuffix(trimmed, suffix)
		}
	}
	return ""
}

func validReleaseTag(value string) bool {
	if !strings.HasPrefix(value, "helper-v") || len(value) > 72 {
		return false
	}
	version := strings.TrimPrefix(value, "helper-v")
	for _, character := range version {
		if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '.' || character == '-') {
			return false
		}
	}
	return version != ""
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func secureAbsolute(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: absolute path: %v", ErrUnsafePath, err)
	}
	absolute = filepath.Clean(absolute)
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: root is a symlink", ErrUnsafePath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: inspect root: %v", ErrUnsafePath, err)
	}

	cursor := absolute
	missing := []string{}
	for {
		if _, err := os.Lstat(cursor); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: resolve path: %v", ErrUnsafePath, err)
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("%w: no existing path prefix", ErrUnsafePath)
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", fmt.Errorf("%w: resolve existing path: %v", ErrUnsafePath, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(left, right string) bool {
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(parent, child string) bool {
	if runtime.GOOS == "windows" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func ensureSecureDirectory(path string, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w: directory is a symlink or non-directory", ErrUnsafePath)
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("%w: directory permissions: %v", ErrUnsafePath, err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect directory: %v", ErrUnsafePath, err)
	}
	if err := os.Mkdir(path, mode); err != nil {
		return fmt.Errorf("%w: create directory: %v", ErrUnsafePath, err)
	}
	return rejectSymlink(path)
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: inspect path: %v", ErrUnsafePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symbolic link", ErrUnsafePath)
	}
	return nil
}

func rejectSymlinkIfPresent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect path: %v", ErrUnsafePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symbolic link", ErrUnsafePath)
	}
	return nil
}

// CommandSelfTester executes only the fixed version/doctor JSON contracts.
type CommandSelfTester struct{}

func (CommandSelfTester) Test(ctx context.Context, executable, expectedVersion, expectedPlatform string) error {
	versionOutput, err := runSelfTestCommand(ctx, executable, "version")
	if err != nil {
		return err
	}
	if err := validateSelfTest(versionOutput, "version", expectedVersion, expectedPlatform); err != nil {
		return err
	}
	doctorOutput, err := runSelfTestCommand(ctx, executable, "doctor")
	if err != nil {
		return err
	}
	return validateSelfTest(doctorOutput, "doctor", expectedVersion, expectedPlatform)
}

func runSelfTestCommand(ctx context.Context, executable, command string) ([]byte, error) {
	process := exec.CommandContext(ctx, executable, command, "--json")
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		process.Env = []string{"SystemRoot=" + systemRoot, "WINDIR=" + systemRoot, "PATH=" + filepath.Join(systemRoot, "System32")}
	} else {
		process.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	}
	output, err := process.Output()
	if err != nil {
		return nil, fmt.Errorf("command %s failed", command)
	}
	if len(output) == 0 || len(output) > 64*1024 {
		return nil, fmt.Errorf("command %s returned invalid output length", command)
	}
	return output, nil
}

type selfTestResult struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion"`
	OK              bool            `json:"ok"`
	Status          string          `json:"status"`
	Data            json.RawMessage `json:"data"`
	Error           json.RawMessage `json:"error"`
}

type selfTestData struct {
	Command       string `json:"command"`
	HelperVersion string `json:"helperVersion"`
	Platform      string `json:"platform"`
	Architecture  string `json:"architecture"`
	NodeRequired  bool   `json:"nodeRequired"`
}

func validateSelfTest(output []byte, command, expectedVersion, expectedPlatform string) error {
	var result selfTestResult
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("%s output is not JSON", command)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s output contains extra values", command)
	}
	if result.Type != "result" || result.ProtocolVersion != 1 || !result.OK || result.Status != "completed" || string(result.Error) != "null" {
		return fmt.Errorf("%s result contract failed", command)
	}
	var data selfTestData
	if err := json.Unmarshal(result.Data, &data); err != nil {
		return fmt.Errorf("%s data contract failed", command)
	}
	if data.Command != command || data.HelperVersion != expectedVersion {
		return fmt.Errorf("%s version contract failed", command)
	}
	if command == "doctor" {
		expectedOS, expectedArchitecture := platformDoctorValues(expectedPlatform)
		if data.Platform != expectedOS || data.Architecture != expectedArchitecture || data.NodeRequired {
			return fmt.Errorf("doctor runtime contract failed")
		}
	}
	return nil
}

func platformDoctorValues(platform string) (string, string) {
	switch platform {
	case "macos-arm64":
		return "macos", "arm64"
	case "macos-x64":
		return "macos", "x64"
	case "windows-x64":
		return "windows", "x64"
	default:
		return "", ""
	}
}
