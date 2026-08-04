// Package restartflow stores a single, bounded, non-secret continuation that
// can survive restarting the current official Codex Desktop process.
package restartflow

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

const (
	schemaVersion    = 1
	requestTTL       = 30 * time.Minute
	maxRequestBytes  = 8 * 1024
	maxDescriptor    = 64 * 1024
	packageFilename  = "theme.cskin"
	descriptorName   = "release-descriptor.json"
	signatureName    = "release-descriptor.sig"
	currentFilename  = "current.json"
	requestsDirname  = "requests"
	operationApply   = "apply"
	operationRestore = "restore"
)

var (
	ErrUnsafe  = errors.New("restart continuation state is unsafe")
	ErrBusy    = errors.New("another restart continuation is active")
	ErrMissing = errors.New("restart continuation is missing")
	ErrExpired = errors.New("restart confirmation expired")
	ErrState   = errors.New("restart continuation state transition is invalid")

	requestIDPattern = regexp.MustCompile(`^rst_[0-9a-f]{32}$`)
	sessionIDPattern = regexp.MustCompile(`^ses_[0-9a-f]{32}$`)
	publicIDPattern  = regexp.MustCompile(`^[0-9]{6}$`)
	semverPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	operationPattern = regexp.MustCompile(`^op_[A-Za-z0-9_-]{16,128}$`)
)

type Status string

const (
	StatusPending   Status = "pending_confirmation"
	StatusApproved  Status = "restart_approved"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Request struct {
	SchemaVersion  int    `json:"schemaVersion"`
	RequestID      string `json:"requestId"`
	Kind           string `json:"kind"`
	Status         Status `json:"status"`
	ThemePublicID  string `json:"themePublicId,omitempty"`
	ThemeVersion   string `json:"themeVersion,omitempty"`
	PackageSHA256  string `json:"packageSha256,omitempty"`
	CreatedAt      string `json:"createdAt"`
	ExpiresAt      string `json:"expiresAt"`
	UpdatedAt      string `json:"updatedAt"`
	OperationID    string `json:"operationId,omitempty"`
	ResultThemeID  string `json:"resultThemeId,omitempty"`
	ResultVersion  string `json:"resultVersion,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	RestartStarted bool   `json:"restartStarted,omitempty"`
}

type Store struct {
	root        string
	directory   string
	requestsDir string
	currentPath string
	now         func() time.Time
}

func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrUnsafe
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafe
	}
	directory := filepath.Join(root, "restart")
	requestsDir := filepath.Join(directory, requestsDirname)
	for _, path := range []string{directory, requestsDir} {
		if err := ensureDirectory(path); err != nil {
			return nil, err
		}
	}
	return &Store{
		root: root, directory: directory, requestsDir: requestsDir,
		currentPath: filepath.Join(directory, currentFilename), now: time.Now,
	}, nil
}

func (store *Store) StageApply(verified theme.Verified) (Request, error) {
	if store == nil ||
		verified.Manifest.ThemePublicID == "" ||
		verified.Manifest.ThemeVersion == "" ||
		verified.PackageSHA256 == "" ||
		verified.PackagePath == "" ||
		len(verified.DescriptorBytes) < 1 ||
		len(verified.DescriptorBytes) > maxDescriptor ||
		len(verified.Signature) != 64 {
		return Request{}, ErrUnsafe
	}
	unlock, err := store.lock()
	if err != nil {
		return Request{}, err
	}
	defer unlock()
	old, oldFound, err := store.readCurrent()
	if err != nil {
		return Request{}, err
	}
	if oldFound && active(old, store.now()) {
		return Request{}, ErrBusy
	}
	request, err := store.newRequest(operationApply)
	if err != nil {
		return Request{}, err
	}
	request.ThemePublicID = verified.Manifest.ThemePublicID
	request.ThemeVersion = verified.Manifest.ThemeVersion
	request.PackageSHA256 = verified.PackageSHA256
	if !valid(request) {
		return Request{}, ErrUnsafe
	}
	payloadDirectory := store.payloadDirectory(request.RequestID)
	if err := os.Mkdir(payloadDirectory, 0o700); err != nil {
		return Request{}, ErrUnsafe
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(payloadDirectory)
		}
	}()
	if err := copyBoundedRegular(
		verified.PackagePath,
		filepath.Join(payloadDirectory, packageFilename),
		theme.MaxPackageBytes,
	); err != nil {
		return Request{}, err
	}
	if err := writeExclusive(
		filepath.Join(payloadDirectory, descriptorName),
		verified.DescriptorBytes,
	); err != nil {
		return Request{}, err
	}
	if err := writeExclusive(
		filepath.Join(payloadDirectory, signatureName),
		[]byte(base64.StdEncoding.EncodeToString(verified.Signature)+"\n"),
	); err != nil {
		return Request{}, err
	}
	if err := syncDirectory(payloadDirectory); err != nil {
		return Request{}, err
	}
	if err := store.writeCurrent(request); err != nil {
		return Request{}, err
	}
	cleanup = false
	if oldFound {
		store.removePayload(old)
	}
	return request, nil
}

func (store *Store) StageRestore() (Request, error) {
	if store == nil {
		return Request{}, ErrUnsafe
	}
	unlock, err := store.lock()
	if err != nil {
		return Request{}, err
	}
	defer unlock()
	old, oldFound, err := store.readCurrent()
	if err != nil {
		return Request{}, err
	}
	if oldFound && active(old, store.now()) {
		return Request{}, ErrBusy
	}
	request, err := store.newRequest(operationRestore)
	if err != nil {
		return Request{}, err
	}
	if err := store.writeCurrent(request); err != nil {
		return Request{}, err
	}
	if oldFound {
		store.removePayload(old)
	}
	return request, nil
}

func (store *Store) Current() (Request, bool, error) {
	if store == nil {
		return Request{}, false, ErrUnsafe
	}
	return store.readCurrent()
}

func (store *Store) Approve(requestID string) (Request, error) {
	return store.transition(requestID, []Status{StatusPending}, func(request *Request) error {
		expiry, err := time.Parse(time.RFC3339, request.ExpiresAt)
		if err != nil || !store.now().Before(expiry) {
			return ErrExpired
		}
		request.Status = StatusApproved
		return nil
	})
}

func (store *Store) Begin(requestID string) (Request, error) {
	return store.transition(requestID, []Status{StatusApproved}, func(request *Request) error {
		expiry, err := time.Parse(time.RFC3339, request.ExpiresAt)
		if err != nil || !store.now().Before(expiry) {
			return ErrExpired
		}
		request.Status = StatusRunning
		request.RestartStarted = true
		return nil
	})
}

func (store *Store) Complete(
	requestID, operationID, themePublicID, themeVersion string,
) (Request, error) {
	return store.transition(requestID, []Status{StatusRunning}, func(request *Request) error {
		request.Status = StatusCompleted
		request.OperationID = operationID
		request.ResultThemeID = themePublicID
		request.ResultVersion = themeVersion
		request.ErrorCode = ""
		if request.Kind == operationApply &&
			(themePublicID != request.ThemePublicID || themeVersion != request.ThemeVersion) {
			return ErrState
		}
		if request.Kind == operationRestore && (themePublicID != "" || themeVersion != "") {
			return ErrState
		}
		return nil
	})
}

func (store *Store) Fail(requestID, errorCode string) (Request, error) {
	return store.transition(
		requestID,
		[]Status{StatusApproved, StatusRunning},
		func(request *Request) error {
			if len(errorCode) < 3 || len(errorCode) > 80 {
				return ErrState
			}
			request.Status = StatusFailed
			request.ErrorCode = errorCode
			return nil
		},
	)
}

func (store *Store) LoadVerified(request Request) (theme.Verified, error) {
	if store == nil || !valid(request) || request.Kind != operationApply ||
		request.Status != StatusRunning {
		return theme.Verified{}, ErrState
	}
	current, found, err := store.readCurrent()
	if err != nil || !found || current != request {
		return theme.Verified{}, ErrState
	}
	directory := store.payloadDirectory(request.RequestID)
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 3 {
		return theme.Verified{}, ErrUnsafe
	}
	expected := map[string]bool{
		packageFilename: true,
		descriptorName:  true,
		signatureName:   true,
	}
	for _, entry := range entries {
		if !expected[entry.Name()] || !entry.Type().IsRegular() ||
			entry.Type()&os.ModeSymlink != 0 {
			return theme.Verified{}, ErrUnsafe
		}
	}
	descriptor, err := readBoundedRegular(
		filepath.Join(directory, descriptorName),
		maxDescriptor,
	)
	if err != nil {
		return theme.Verified{}, err
	}
	signature, err := readBoundedRegular(
		filepath.Join(directory, signatureName),
		256,
	)
	if err != nil {
		return theme.Verified{}, ErrUnsafe
	}
	verified, err := theme.Verify(
		filepath.Join(directory, packageFilename),
		descriptor,
		signature,
	)
	if err != nil ||
		verified.Manifest.ThemePublicID != request.ThemePublicID ||
		verified.Manifest.ThemeVersion != request.ThemeVersion ||
		verified.PackageSHA256 != request.PackageSHA256 {
		return theme.Verified{}, ErrUnsafe
	}
	return verified, nil
}

func (store *Store) newRequest(kind string) (Request, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return Request{}, ErrUnsafe
	}
	now := store.now().UTC()
	return Request{
		SchemaVersion: schemaVersion,
		RequestID:     "rst_" + hex.EncodeToString(value),
		Kind:          kind,
		Status:        StatusPending,
		CreatedAt:     now.Format(time.RFC3339),
		ExpiresAt:     now.Add(requestTTL).Format(time.RFC3339),
		UpdatedAt:     now.Format(time.RFC3339),
	}, nil
}

func (store *Store) transition(
	requestID string,
	allowed []Status,
	change func(*Request) error,
) (Request, error) {
	if store == nil || !requestIDPattern.MatchString(requestID) || change == nil {
		return Request{}, ErrUnsafe
	}
	unlock, err := store.lock()
	if err != nil {
		return Request{}, err
	}
	defer unlock()
	request, found, err := store.readCurrent()
	if err != nil {
		return Request{}, err
	}
	if !found || request.RequestID != requestID {
		return Request{}, ErrMissing
	}
	allowedStatus := false
	for _, status := range allowed {
		if request.Status == status {
			allowedStatus = true
			break
		}
	}
	if !allowedStatus {
		return Request{}, ErrState
	}
	if err := change(&request); err != nil {
		return Request{}, err
	}
	request.UpdatedAt = store.now().UTC().Format(time.RFC3339)
	if !valid(request) {
		return Request{}, ErrState
	}
	if err := store.writeCurrent(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (store *Store) readCurrent() (Request, bool, error) {
	info, err := os.Lstat(store.currentPath)
	if errors.Is(err, os.ErrNotExist) {
		return Request{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > maxRequestBytes {
		return Request{}, false, ErrUnsafe
	}
	raw, err := readBoundedRegular(store.currentPath, maxRequestBytes)
	if err != nil {
		return Request{}, false, err
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, false, ErrUnsafe
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !valid(request) {
		return Request{}, false, ErrUnsafe
	}
	return request, true, nil
}

func (store *Store) writeCurrent(request Request) error {
	if !valid(request) {
		return ErrUnsafe
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return ErrUnsafe
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(store.directory, ".restart-current-*.tmp")
	if err != nil {
		return ErrUnsafe
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return ErrUnsafe
	}
	if _, err := temporary.Write(raw); err != nil {
		return ErrUnsafe
	}
	if err := temporary.Sync(); err != nil {
		return ErrUnsafe
	}
	if err := temporary.Close(); err != nil {
		return ErrUnsafe
	}
	if err := replaceFile(temporaryPath, store.currentPath); err != nil {
		return ErrUnsafe
	}
	cleanup = false
	return syncDirectory(store.directory)
}

func (store *Store) payloadDirectory(requestID string) string {
	return filepath.Join(store.requestsDir, requestID)
}

func (store *Store) removePayload(request Request) {
	if !requestIDPattern.MatchString(request.RequestID) {
		return
	}
	directory := store.payloadDirectory(request.RequestID)
	info, err := os.Lstat(directory)
	if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		_ = os.RemoveAll(directory)
	}
}

func (store *Store) lock() (func(), error) {
	return acquireFileLock(filepath.Join(store.directory, ".lock"))
}

func valid(request Request) bool {
	if request.SchemaVersion != schemaVersion ||
		!requestIDPattern.MatchString(request.RequestID) ||
		(request.Kind != operationApply && request.Kind != operationRestore) ||
		!validStatus(request.Status) {
		return false
	}
	created, createdErr := time.Parse(time.RFC3339, request.CreatedAt)
	expires, expiresErr := time.Parse(time.RFC3339, request.ExpiresAt)
	updated, updatedErr := time.Parse(time.RFC3339, request.UpdatedAt)
	if createdErr != nil || expiresErr != nil || updatedErr != nil ||
		!expires.After(created) || updated.Before(created) {
		return false
	}
	if request.Kind == operationApply {
		if !publicIDPattern.MatchString(request.ThemePublicID) ||
			!semverPattern.MatchString(request.ThemeVersion) ||
			!digestPattern.MatchString(request.PackageSHA256) {
			return false
		}
	} else if request.ThemePublicID != "" || request.ThemeVersion != "" ||
		request.PackageSHA256 != "" {
		return false
	}
	if request.OperationID != "" && !operationPattern.MatchString(request.OperationID) {
		return false
	}
	if request.Status == StatusCompleted {
		if request.OperationID == "" || request.ErrorCode != "" {
			return false
		}
		if request.Kind == operationApply &&
			(request.ResultThemeID != request.ThemePublicID ||
				request.ResultVersion != request.ThemeVersion) {
			return false
		}
		if request.Kind == operationRestore &&
			(request.ResultThemeID != "" || request.ResultVersion != "") {
			return false
		}
	} else if request.OperationID != "" || request.ResultThemeID != "" ||
		request.ResultVersion != "" {
		return false
	}
	if request.Status == StatusFailed {
		return request.ErrorCode != ""
	}
	if (request.Status == StatusPending || request.Status == StatusApproved) &&
		request.RestartStarted {
		return false
	}
	if (request.Status == StatusRunning || request.Status == StatusCompleted) &&
		!request.RestartStarted {
		return false
	}
	return request.ErrorCode == ""
}

func validStatus(status Status) bool {
	switch status {
	case StatusPending, StatusApproved, StatusRunning, StatusCompleted, StatusFailed:
		return true
	default:
		return false
	}
}

func active(request Request, now time.Time) bool {
	if request.Status == StatusCompleted || request.Status == StatusFailed {
		return false
	}
	expiry, err := time.Parse(time.RFC3339, request.ExpiresAt)
	if err != nil {
		return true
	}
	if request.Status == StatusRunning {
		return now.Before(expiry.Add(2 * time.Minute))
	}
	return now.Before(expiry)
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafe
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) || os.Mkdir(path, 0o700) != nil {
		return ErrUnsafe
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafe
	}
	return nil
}

func copyBoundedRegular(source, destination string, limit int64) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > limit {
		return ErrUnsafe
	}
	input, err := os.Open(source)
	if err != nil {
		return ErrUnsafe
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrUnsafe
	}
	cleanup := true
	defer func() {
		_ = output.Close()
		if cleanup {
			_ = os.Remove(destination)
		}
	}()
	written, err := io.Copy(output, io.LimitReader(input, limit+1))
	if err != nil || written != info.Size() {
		return ErrUnsafe
	}
	if err := output.Sync(); err != nil {
		return ErrUnsafe
	}
	if err := output.Close(); err != nil {
		return ErrUnsafe
	}
	cleanup = false
	return nil
}

func writeExclusive(path string, content []byte) error {
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrUnsafe
	}
	cleanup := true
	defer func() {
		_ = output.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if _, err := output.Write(content); err != nil {
		return ErrUnsafe
	}
	if err := output.Sync(); err != nil {
		return ErrUnsafe
	}
	if err := output.Close(); err != nil {
		return ErrUnsafe
	}
	cleanup = false
	return nil
}

func readBoundedRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > limit {
		return nil, ErrUnsafe
	}
	input, err := os.Open(path)
	if err != nil {
		return nil, ErrUnsafe
	}
	defer input.Close()
	content, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || int64(len(content)) != info.Size() {
		return nil, ErrUnsafe
	}
	return content, nil
}
