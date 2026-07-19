// Package guardian manages the versioned, per-user Skin Guardian lifecycle.
package guardian

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	currentSchema       = 1
	versionSchema       = 1
	registrationID      = "com.codexskin.guardian"
	defaultSelfTestTime = 10 * time.Second
	maxGuardianSize     = 64 * 1024 * 1024
)

var (
	ErrConfiguration   = errors.New("Guardian configuration is invalid")
	ErrUnsafePath      = errors.New("Guardian path is unsafe")
	ErrArtifact        = errors.New("Guardian artifact is invalid")
	ErrSignature       = errors.New("Guardian signature verification failed")
	ErrSelfTest        = errors.New("Guardian self-test failed")
	ErrRegistration    = errors.New("Guardian registration failed")
	ErrCurrentInvalid  = errors.New("current Guardian pointer is invalid")
	ErrDowngrade       = errors.New("Guardian downgrade requires explicit rollback")
	ErrRollbackInvalid = errors.New("Guardian rollback target is invalid")
)

type Candidate struct {
	Version string
	Content []byte
	SHA256  string
}

type SignatureVerifier interface {
	Verify(ctx context.Context, executable, platform string) error
}

type SelfTester interface {
	Test(ctx context.Context, executable, expectedVersion, expectedPlatform string) error
}

type Registrar interface {
	Install(ctx context.Context, registration Registration) error
	Remove(ctx context.Context, registration Registration) error
}

type Config struct {
	Root            string
	PluginCache     string
	RuntimeGOOS     string
	RuntimeGOARCH   string
	Verifier        SignatureVerifier
	SelfTester      SelfTester
	Registrar       Registrar
	SelfTestTimeout time.Duration
}

type Result struct {
	Executable      string
	GuardianVersion string
	PreviousVersion string
	Reused          bool
}

type currentRecord struct {
	SchemaVersion   int    `json:"schemaVersion"`
	GuardianVersion string `json:"guardianVersion"`
	Platform        string `json:"platform"`
	Filename        string `json:"filename"`
	SHA256          string `json:"sha256"`
}

type versionRecord struct {
	SchemaVersion   int    `json:"schemaVersion"`
	GuardianVersion string `json:"guardianVersion"`
	Platform        string `json:"platform"`
	Filename        string `json:"filename"`
	SHA256          string `json:"sha256"`
}

type preparedConfig struct {
	Config
	root         string
	guardianRoot string
	versionsRoot string
	platform     string
	timeout      time.Duration
}

func Install(ctx context.Context, config Config, candidate Candidate) (Result, error) {
	prepared, err := prepare(config)
	if err != nil {
		return Result{}, err
	}
	if err := validateCandidate(candidate); err != nil {
		return Result{}, err
	}
	current, hasCurrent, err := readCurrent(prepared.guardianRoot, prepared.platform)
	if err != nil {
		return Result{}, err
	}
	previousVersion := ""
	if hasCurrent {
		previousVersion = current.GuardianVersion
		relation, err := releasecontract.Relation(current.GuardianVersion, candidate.Version)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrArtifact, err)
		}
		if relation == releasecontract.VersionDowngrade {
			return Result{}, ErrDowngrade
		}
		if relation == releasecontract.VersionCurrent {
			if candidate.SHA256 != current.SHA256 {
				return Result{}, fmt.Errorf("%w: current digest differs", ErrCurrentInvalid)
			}
			executable := executablePath(prepared, current.GuardianVersion)
			if err := verifyInstalled(ctx, prepared, executable, current.GuardianVersion, current.SHA256); err != nil {
				return Result{}, err
			}
			if err := prepared.Registrar.Install(ctx, fixedRegistration(prepared.platform, executable)); err != nil {
				return Result{}, fmt.Errorf("%w: repair current registration: %v", ErrRegistration, err)
			}
			return Result{Executable: executable, GuardianVersion: current.GuardianVersion, PreviousVersion: previousVersion, Reused: true}, nil
		}
	}

	staging, err := os.MkdirTemp(prepared.guardianRoot, ".staging-")
	if err != nil {
		return Result{}, fmt.Errorf("%w: create staging: %v", ErrUnsafePath, err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return Result{}, fmt.Errorf("%w: staging permissions: %v", ErrUnsafePath, err)
	}
	filename := expectedFilename(candidate.Version, prepared.platform)
	stagedExecutable := filepath.Join(staging, filename)
	if err := writeExclusive(stagedExecutable, candidate.Content, 0o700); err != nil {
		return Result{}, err
	}
	if err := verifyInstalled(ctx, prepared, stagedExecutable, candidate.Version, candidate.SHA256); err != nil {
		return Result{}, err
	}
	record := versionRecord{
		SchemaVersion:   versionSchema,
		GuardianVersion: candidate.Version,
		Platform:        prepared.platform,
		Filename:        filename,
		SHA256:          candidate.SHA256,
	}
	if err := writeJSONAtomic(staging, "metadata.json", record); err != nil {
		return Result{}, err
	}

	destination := filepath.Join(prepared.versionsRoot, candidate.Version)
	moved, err := installVersion(staging, destination, record)
	if err != nil {
		return Result{}, err
	}
	installedExecutable := filepath.Join(destination, filename)
	if !moved {
		if err := verifyInstalled(ctx, prepared, installedExecutable, candidate.Version, candidate.SHA256); err != nil {
			return Result{}, err
		}
	}
	registration := fixedRegistration(prepared.platform, installedExecutable)
	if err := prepared.Registrar.Install(ctx, registration); err != nil {
		if moved {
			_ = os.RemoveAll(destination)
		}
		return Result{}, fmt.Errorf("%w: install: %v", ErrRegistration, err)
	}
	newCurrent := currentRecord{
		SchemaVersion:   currentSchema,
		GuardianVersion: candidate.Version,
		Platform:        prepared.platform,
		Filename:        filename,
		SHA256:          candidate.SHA256,
	}
	if err := writeJSONAtomic(prepared.guardianRoot, "current.json", newCurrent); err != nil {
		_ = prepared.Registrar.Remove(ctx, registration)
		if hasCurrent {
			_ = prepared.Registrar.Install(ctx, fixedRegistration(prepared.platform, executablePath(prepared, current.GuardianVersion)))
		}
		if moved {
			_ = os.RemoveAll(destination)
		}
		return Result{}, err
	}
	return Result{Executable: installedExecutable, GuardianVersion: candidate.Version, PreviousVersion: previousVersion}, nil
}

func Rollback(ctx context.Context, config Config, targetVersion string) (Result, error) {
	prepared, err := prepare(config)
	if err != nil {
		return Result{}, err
	}
	current, hasCurrent, err := readCurrent(prepared.guardianRoot, prepared.platform)
	if err != nil {
		return Result{}, err
	}
	if !hasCurrent {
		return Result{}, fmt.Errorf("%w: no active Guardian", ErrRollbackInvalid)
	}
	relation, err := releasecontract.Relation(current.GuardianVersion, targetVersion)
	if err != nil || relation != releasecontract.VersionDowngrade {
		return Result{}, fmt.Errorf("%w: target must be an installed older version", ErrRollbackInvalid)
	}
	record, err := readVersion(prepared, targetVersion)
	if err != nil {
		return Result{}, err
	}
	executable := filepath.Join(prepared.versionsRoot, targetVersion, record.Filename)
	if err := verifyInstalled(ctx, prepared, executable, targetVersion, record.SHA256); err != nil {
		return Result{}, err
	}
	registration := fixedRegistration(prepared.platform, executable)
	if err := prepared.Registrar.Install(ctx, registration); err != nil {
		return Result{}, fmt.Errorf("%w: rollback: %v", ErrRegistration, err)
	}
	rolledBack := currentRecord(record)
	if err := writeJSONAtomic(prepared.guardianRoot, "current.json", rolledBack); err != nil {
		_ = prepared.Registrar.Install(ctx, fixedRegistration(prepared.platform, executablePath(prepared, current.GuardianVersion)))
		return Result{}, err
	}
	return Result{Executable: executable, GuardianVersion: targetVersion, PreviousVersion: current.GuardianVersion}, nil
}

func Uninstall(ctx context.Context, config Config) error {
	prepared, err := prepare(config)
	if err != nil {
		return err
	}
	current, hasCurrent, readErr := readCurrent(prepared.guardianRoot, prepared.platform)
	registration := Registration{ID: registrationID, Platform: prepared.platform}
	if readErr == nil && hasCurrent {
		registration = fixedRegistration(prepared.platform, executablePath(prepared, current.GuardianVersion))
	}
	if err := prepared.Registrar.Remove(ctx, registration); err != nil {
		return fmt.Errorf("%w: remove: %v", ErrRegistration, err)
	}
	if err := os.RemoveAll(prepared.guardianRoot); err != nil {
		return fmt.Errorf("%w: remove Guardian files: %v", ErrUnsafePath, err)
	}
	return nil
}

func prepare(config Config) (preparedConfig, error) {
	if config.Verifier == nil || config.SelfTester == nil || config.Registrar == nil {
		return preparedConfig{}, fmt.Errorf("%w: verifier, self-test, and registrar are required", ErrConfiguration)
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
		return preparedConfig{}, err
	}
	root, err := secureAbsolute(config.Root)
	if err != nil {
		return preparedConfig{}, err
	}
	cache, err := secureAbsolute(config.PluginCache)
	if err != nil {
		return preparedConfig{}, err
	}
	if pathsOverlap(root, cache) {
		return preparedConfig{}, fmt.Errorf("%w: application root overlaps Plugin cache", ErrUnsafePath)
	}
	if err := ensureDirectory(root); err != nil {
		return preparedConfig{}, err
	}
	guardianRoot := filepath.Join(root, "guardian")
	if err := ensureDirectory(guardianRoot); err != nil {
		return preparedConfig{}, err
	}
	versionsRoot := filepath.Join(guardianRoot, "versions")
	if err := ensureDirectory(versionsRoot); err != nil {
		return preparedConfig{}, err
	}
	timeout := config.SelfTestTimeout
	if timeout <= 0 {
		timeout = defaultSelfTestTime
	}
	return preparedConfig{Config: config, root: root, guardianRoot: guardianRoot, versionsRoot: versionsRoot, platform: platform, timeout: timeout}, nil
}

func validateCandidate(candidate Candidate) error {
	if _, err := releasecontract.Relation("", candidate.Version); err != nil {
		return fmt.Errorf("%w: version: %v", ErrArtifact, err)
	}
	if len(candidate.Content) < 1 || len(candidate.Content) > maxGuardianSize || !validDigest(candidate.SHA256) {
		return fmt.Errorf("%w: size or digest", ErrArtifact)
	}
	digest := sha256.Sum256(candidate.Content)
	if hex.EncodeToString(digest[:]) != candidate.SHA256 {
		return fmt.Errorf("%w: SHA-256 mismatch", ErrArtifact)
	}
	return nil
}

func verifyInstalled(ctx context.Context, config preparedConfig, executable, version, digest string) error {
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: executable is not a regular file", ErrUnsafePath)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		return fmt.Errorf("%w: read executable: %v", ErrArtifact, err)
	}
	actual := sha256.Sum256(content)
	if hex.EncodeToString(actual[:]) != digest {
		return fmt.Errorf("%w: installed SHA-256 mismatch", ErrArtifact)
	}
	if err := config.Verifier.Verify(ctx, executable, config.platform); err != nil {
		return fmt.Errorf("%w: %v", ErrSignature, err)
	}
	testContext, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()
	if err := config.SelfTester.Test(testContext, executable, version, config.platform); err != nil {
		return fmt.Errorf("%w: %v", ErrSelfTest, err)
	}
	return nil
}

func installVersion(staging, destination string, expected versionRecord) (bool, error) {
	info, err := os.Lstat(destination)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("%w: version path is unsafe", ErrUnsafePath)
		}
		record, err := readStrictJSON[versionRecord](filepath.Join(destination, "metadata.json"), 16*1024)
		if err != nil || record != expected {
			return false, fmt.Errorf("%w: existing version metadata differs", ErrArtifact)
		}
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w: inspect version: %v", ErrUnsafePath, err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return false, fmt.Errorf("%w: install version: %v", ErrUnsafePath, err)
	}
	return true, syncDirectory(filepath.Dir(destination))
}

func readCurrent(guardianRoot, platform string) (currentRecord, bool, error) {
	path := filepath.Join(guardianRoot, "current.json")
	if info, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return currentRecord{}, false, nil
	} else if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return currentRecord{}, false, fmt.Errorf("%w: pointer path", ErrCurrentInvalid)
	}
	record, err := readStrictJSON[currentRecord](path, 16*1024)
	if err != nil {
		return currentRecord{}, false, fmt.Errorf("%w: %v", ErrCurrentInvalid, err)
	}
	if record.SchemaVersion != currentSchema || record.Platform != platform || record.Filename != expectedFilename(record.GuardianVersion, platform) || !validDigest(record.SHA256) {
		return currentRecord{}, false, fmt.Errorf("%w: fields", ErrCurrentInvalid)
	}
	if _, err := releasecontract.Relation("", record.GuardianVersion); err != nil {
		return currentRecord{}, false, fmt.Errorf("%w: version", ErrCurrentInvalid)
	}
	return record, true, nil
}

func readVersion(config preparedConfig, version string) (versionRecord, error) {
	if _, err := releasecontract.Relation("", version); err != nil {
		return versionRecord{}, fmt.Errorf("%w: version", ErrRollbackInvalid)
	}
	path := filepath.Join(config.versionsRoot, version, "metadata.json")
	record, err := readStrictJSON[versionRecord](path, 16*1024)
	if err != nil {
		return versionRecord{}, fmt.Errorf("%w: metadata: %v", ErrRollbackInvalid, err)
	}
	if record.SchemaVersion != versionSchema || record.GuardianVersion != version || record.Platform != config.platform || record.Filename != expectedFilename(version, config.platform) || !validDigest(record.SHA256) {
		return versionRecord{}, fmt.Errorf("%w: metadata fields", ErrRollbackInvalid)
	}
	return record, nil
}

func readStrictJSON[T any](path string, limit int64) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return value, fmt.Errorf("invalid JSON byte length")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("JSON has trailing data")
	}
	return value, nil
}

func writeJSONAtomic(directory, filename string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode JSON: %v", ErrUnsafePath, err)
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(directory, ".guardian-json-")
	if err != nil {
		return fmt.Errorf("%w: create JSON: %v", ErrUnsafePath, err)
	}
	path := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(path)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("%w: JSON permissions: %v", ErrUnsafePath, err)
	}
	if _, err := temporary.Write(content); err != nil {
		return fmt.Errorf("%w: write JSON: %v", ErrUnsafePath, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync JSON: %v", ErrUnsafePath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close JSON: %v", ErrUnsafePath, err)
	}
	closed = true
	if err := atomicReplace(path, filepath.Join(directory, filename)); err != nil {
		return fmt.Errorf("%w: activate JSON: %v", ErrUnsafePath, err)
	}
	return syncDirectory(directory)
}

func writeExclusive(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("%w: create file: %v", ErrUnsafePath, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("%w: write file: %v", ErrUnsafePath, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("%w: sync file: %v", ErrUnsafePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: close file: %v", ErrUnsafePath, err)
	}
	closed = true
	return nil
}

func expectedFilename(version, platform string) string {
	suffixes := map[string]string{
		"macos-arm64": "macos_arm64",
		"macos-x64":   "macos_x64",
		"windows-x64": "windows_x64.exe",
	}
	return "codex-skin-guardian_" + version + "_" + suffixes[platform]
}

func executablePath(config preparedConfig, version string) string {
	return filepath.Join(config.versionsRoot, version, expectedFilename(version, config.platform))
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

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: directory is unsafe", ErrUnsafePath)
		}
		return os.Chmod(path, 0o700)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect directory: %v", ErrUnsafePath, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return fmt.Errorf("%w: create directory: %v", ErrUnsafePath, err)
	}
	return nil
}

// CommandSelfTester executes only the Guardian version contract.
type CommandSelfTester struct{}

func (CommandSelfTester) Test(ctx context.Context, executable, expectedVersion, _ string) error {
	command := exec.CommandContext(ctx, executable, "version", "--json")
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		command.Env = []string{"SystemRoot=" + systemRoot, "WINDIR=" + systemRoot, "PATH=" + filepath.Join(systemRoot, "System32")}
	} else {
		command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	}
	output, err := command.Output()
	if err != nil || len(output) == 0 || len(output) > 16*1024 {
		return fmt.Errorf("version command failed")
	}
	var result struct {
		SchemaVersion   int    `json:"schemaVersion"`
		Component       string `json:"component"`
		GuardianVersion string `json:"guardianVersion"`
		Status          string `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("version output is invalid JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("version output has trailing values")
	}
	if result.SchemaVersion != 1 || result.Component != "codex-skin-guardian" || result.GuardianVersion != expectedVersion || result.Status != "ready" {
		return fmt.Errorf("version contract differs")
	}
	return nil
}
