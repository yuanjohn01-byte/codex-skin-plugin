// Package sessionflow records one renderer controller bound to one controlled
// Codex process. It deliberately has no scheduler or launch-at-login behavior.
package sessionflow

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

const (
	schemaVersion = 1
	maxStateBytes = 12 * 1024
)

var (
	ErrUnsafe  = errors.New("theme session state is unsafe")
	ErrBusy    = errors.New("another theme session is active")
	ErrMissing = errors.New("theme session is missing")
	ErrState   = errors.New("theme session state transition is invalid")

	sessionIDPattern = regexp.MustCompile(`^ses_[0-9a-f]{32}$`)
	publicIDPattern  = regexp.MustCompile(`^[0-9]{6}$`)
	semverPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reasonPattern    = regexp.MustCompile(`^[a-z0-9_-]{3,80}$`)
)

type Status string

const (
	StatusStarting Status = "starting"
	StatusActive   Status = "active"
	StatusStopping Status = "stop_requested"
	StatusEnded    Status = "ended"
	StatusFailed   Status = "failed"
)

type Record struct {
	SchemaVersion int             `json:"schemaVersion"`
	SessionID     string          `json:"sessionId"`
	Status        Status          `json:"status"`
	ThemePublicID string          `json:"themePublicId"`
	ThemeVersion  string          `json:"themeVersion"`
	PackageSHA256 string          `json:"packageSha256"`
	Codex         engine.Identity `json:"codex"`
	ControllerPID int             `json:"controllerPid,omitempty"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
	EndedReason   string          `json:"endedReason,omitempty"`
}

type Store struct {
	root    string
	path    string
	locking *engine.Store
	now     func() time.Time
}

func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrUnsafe
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafe
	}
	locking, err := engine.OpenStore(root, "")
	if err != nil {
		return nil, ErrUnsafe
	}
	directory := filepath.Join(root, "session")
	if err := ensureDirectory(directory); err != nil {
		return nil, err
	}
	return &Store{
		root: root, path: filepath.Join(directory, "current.json"), locking: locking, now: time.Now,
	}, nil
}

func (store *Store) Start(themePublicID, themeVersion, packageSHA256 string, codex engine.Identity) (Record, error) {
	if store == nil || !publicIDPattern.MatchString(themePublicID) ||
		!semverPattern.MatchString(themeVersion) || !digestPattern.MatchString(packageSHA256) ||
		!validIdentity(codex) {
		return Record{}, ErrUnsafe
	}
	return store.withLock(func(current Record, found bool) (Record, error) {
		if found && active(current.Status) {
			return Record{}, ErrBusy
		}
		value := make([]byte, 16)
		if _, err := rand.Read(value); err != nil {
			return Record{}, ErrUnsafe
		}
		now := store.now().UTC().Format(time.RFC3339Nano)
		record := Record{
			SchemaVersion: schemaVersion, SessionID: "ses_" + hex.EncodeToString(value),
			Status: StatusStarting, ThemePublicID: themePublicID, ThemeVersion: themeVersion,
			PackageSHA256: packageSHA256, Codex: codex, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.write(record); err != nil {
			return Record{}, err
		}
		return record, nil
	})
}

func (store *Store) Current() (Record, bool, error) {
	if store == nil {
		return Record{}, false, ErrUnsafe
	}
	// Writers publish records with an atomic rename. A reader can otherwise
	// observe metadata for the old inode and bytes from the new inode during the
	// narrow Lstat/ReadFile window. Retry only a bounded number of times; durable
	// corruption still fails closed.
	var record Record
	var found bool
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		record, found, err = store.read()
		if err == nil {
			return record, found, nil
		}
		time.Sleep(time.Millisecond)
	}
	return Record{}, false, err
}

func (store *Store) Claim(sessionID string, pid int) (Record, error) {
	return store.transition(sessionID, []Status{StatusStarting}, func(record *Record) error {
		if pid < 1 {
			return ErrState
		}
		record.ControllerPID = pid
		return nil
	})
}

func (store *Store) Activate(sessionID string, pid int) (Record, error) {
	return store.transition(sessionID, []Status{StatusStarting, StatusActive}, func(record *Record) error {
		if pid < 1 || (record.ControllerPID != 0 && record.ControllerPID != pid) {
			return ErrState
		}
		record.ControllerPID = pid
		record.Status = StatusActive
		return nil
	})
}

func (store *Store) Heartbeat(sessionID string, pid int) (Record, error) {
	return store.transition(sessionID, []Status{StatusActive}, func(record *Record) error {
		if pid < 1 || record.ControllerPID != pid {
			return ErrState
		}
		return nil
	})
}

// Switch moves the single active Runtime Supervisor to an already verified
// desired theme without changing its PID or Codex process identity.
func (store *Store) Switch(sessionID, themePublicID, themeVersion, packageSHA256 string) (Record, error) {
	if !publicIDPattern.MatchString(themePublicID) || !semverPattern.MatchString(themeVersion) ||
		!digestPattern.MatchString(packageSHA256) {
		return Record{}, ErrUnsafe
	}
	return store.transition(sessionID, []Status{StatusActive}, func(record *Record) error {
		record.ThemePublicID = themePublicID
		record.ThemeVersion = themeVersion
		record.PackageSHA256 = packageSHA256
		// A switch is not active until the same supervisor has applied and
		// verified the new renderer payload.
		record.Status = StatusStarting
		return nil
	})
}

func (store *Store) RequestStop() (Record, bool, error) {
	if store == nil {
		return Record{}, false, ErrUnsafe
	}
	unlock, err := store.locking.Lock()
	if err != nil {
		return Record{}, false, err
	}
	defer unlock()
	current, found, err := store.read()
	if err != nil {
		return Record{}, false, err
	}
	if !found || !active(current.Status) {
		return current, false, nil
	}
	// The controller process may not have claimed the freshly staged session yet.
	// Ending it under the same lock prevents a late child from claiming it after
	// Restore or a theme switch has already moved on.
	if current.Status == StatusStarting && current.ControllerPID == 0 {
		current.Status = StatusEnded
		current.EndedReason = "stop_before_claim"
		current.UpdatedAt = store.now().UTC().Format(time.RFC3339Nano)
		if err := store.write(current); err != nil {
			return Record{}, false, err
		}
		return current, true, nil
	}
	current.Status = StatusStopping
	current.UpdatedAt = store.now().UTC().Format(time.RFC3339Nano)
	if err := store.write(current); err != nil {
		return Record{}, false, err
	}
	return current, true, nil
}

func (store *Store) StopRequested(sessionID string) (bool, error) {
	record, found, err := store.Current()
	if err != nil || !found || record.SessionID != sessionID {
		if err == nil {
			err = ErrMissing
		}
		return false, err
	}
	return record.Status == StatusStopping, nil
}

// ExpireStale atomically fails an in-progress session whose last claim or
// heartbeat is older than maxAge. This prevents a controller that disappeared
// during process exit or computer shutdown from blocking the next apply.
func (store *Store) ExpireStale(maxAge time.Duration, reason string) (Record, bool, error) {
	if store == nil || maxAge <= 0 || !reasonPattern.MatchString(reason) {
		return Record{}, false, ErrUnsafe
	}
	var expired bool
	record, err := store.withLock(func(current Record, found bool) (Record, error) {
		if !found || !current.InProgress() || current.Fresh(store.now(), maxAge) {
			return current, nil
		}
		current.Status = StatusFailed
		current.EndedReason = reason
		current.UpdatedAt = store.now().UTC().Format(time.RFC3339Nano)
		if err := store.write(current); err != nil {
			return Record{}, err
		}
		expired = true
		return current, nil
	})
	return record, expired, err
}

func (store *Store) Finish(sessionID string, status Status, reason string) (Record, error) {
	if status != StatusEnded && status != StatusFailed || !reasonPattern.MatchString(reason) {
		return Record{}, ErrState
	}
	return store.transition(sessionID, []Status{StatusStarting, StatusActive, StatusStopping}, func(record *Record) error {
		record.Status = status
		record.EndedReason = reason
		return nil
	})
}

func (store *Store) transition(sessionID string, allowed []Status, change func(*Record) error) (Record, error) {
	if store == nil || !sessionIDPattern.MatchString(sessionID) || change == nil {
		return Record{}, ErrUnsafe
	}
	return store.withLock(func(current Record, found bool) (Record, error) {
		if !found || current.SessionID != sessionID {
			return Record{}, ErrMissing
		}
		allowedStatus := false
		for _, status := range allowed {
			if current.Status == status {
				allowedStatus = true
				break
			}
		}
		if !allowedStatus {
			return Record{}, ErrState
		}
		if err := change(&current); err != nil {
			return Record{}, err
		}
		current.UpdatedAt = store.now().UTC().Format(time.RFC3339Nano)
		if err := store.write(current); err != nil {
			return Record{}, err
		}
		return current, nil
	})
}

func (store *Store) withLock(action func(Record, bool) (Record, error)) (Record, error) {
	unlock, err := store.locking.Lock()
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	current, found, err := store.read()
	if err != nil {
		return Record{}, err
	}
	return action(current, found)
}

func (store *Store) read() (Record, bool, error) {
	info, err := os.Lstat(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > maxStateBytes {
		return Record{}, false, ErrUnsafe
	}
	raw, err := os.ReadFile(store.path)
	if err != nil || int64(len(raw)) != info.Size() {
		return Record{}, false, ErrUnsafe
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, false, ErrUnsafe
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !valid(record) {
		return Record{}, false, ErrUnsafe
	}
	return record, true, nil
}

func (store *Store) write(record Record) error {
	if !valid(record) {
		return ErrUnsafe
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return ErrUnsafe
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".session-current-*.tmp")
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
	if _, err := temporary.Write(raw); err != nil || temporary.Sync() != nil || temporary.Close() != nil {
		return ErrUnsafe
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return ErrUnsafe
	}
	cleanup = false
	return nil
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

func active(status Status) bool {
	return status == StatusStarting || status == StatusActive || status == StatusStopping
}

// InProgress reports whether the record still claims to own a live controller
// lifecycle. Callers must also check Fresh before treating it as active.
func (record Record) InProgress() bool {
	return active(record.Status)
}

// Fresh rejects an in-progress record whose controller heartbeat can no longer
// be trusted. Terminal records are never fresh controller claims.
func (record Record) Fresh(now time.Time, maxAge time.Duration) bool {
	if !record.InProgress() || maxAge <= 0 {
		return false
	}
	updated, err := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return false
	}
	age := now.UTC().Sub(updated)
	return age >= 0 && age <= maxAge
}

func valid(record Record) bool {
	if record.SchemaVersion != schemaVersion || !sessionIDPattern.MatchString(record.SessionID) ||
		!publicIDPattern.MatchString(record.ThemePublicID) || !semverPattern.MatchString(record.ThemeVersion) ||
		!digestPattern.MatchString(record.PackageSHA256) || !validIdentity(record.Codex) {
		return false
	}
	created, createdErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
	updated, updatedErr := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if createdErr != nil || updatedErr != nil || updated.Before(created) {
		return false
	}
	switch record.Status {
	case StatusStarting:
		return record.ControllerPID >= 0 && record.EndedReason == ""
	case StatusActive:
		return record.ControllerPID > 0 && record.EndedReason == ""
	case StatusStopping:
		return record.ControllerPID > 0 && record.EndedReason == ""
	case StatusEnded, StatusFailed:
		// A controller launch can fail, or Restore can cancel the session,
		// before a detached child has claimed a PID.
		return record.ControllerPID >= 0 && reasonPattern.MatchString(record.EndedReason)
	default:
		return false
	}
}

func validIdentity(identity engine.Identity) bool {
	return identity.Platform != "" && identity.AppIdentifier != "" && identity.Publisher != "" &&
		identity.Version != "" && digestPattern.MatchString(identity.ExecutableHash) &&
		identity.ProcessID > 0 && identity.ProcessStartID != ""
}

func (store *Store) String() string {
	if store == nil {
		return ""
	}
	return fmt.Sprintf("sessionflow:%s", store.root)
}
