// Package flowstate stores only non-secret Plugin continuation state outside
// the replaceable Plugin cache. Refresh tokens and device proof remain in the
// native credential store.
package flowstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const schemaVersion = 1

var (
	ErrUnsafe = errors.New("Plugin continuation state is unsafe")
	deviceID  = regexp.MustCompile(`^dev_[A-Za-z0-9_-]{32}$`)
	publicID  = regexp.MustCompile(`^[0-9]{6}$`)
)

type State struct {
	SchemaVersion        int    `json:"schemaVersion"`
	DeviceID             string `json:"deviceId,omitempty"`
	PendingThemePublicID string `json:"pendingThemePublicId,omitempty"`
}

type Store struct {
	path string
}

func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrUnsafe
	}
	stateDirectory := filepath.Join(root, "state")
	info, err := os.Lstat(stateDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafe
	}
	return &Store{path: filepath.Join(stateDirectory, "plugin-flow.json")}, nil
}

func (store *Store) Read() (State, error) {
	if store == nil {
		return State{}, ErrUnsafe
	}
	info, err := os.Lstat(store.path)
	if os.IsNotExist(err) {
		return State{SchemaVersion: schemaVersion}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > 4096 {
		return State{}, ErrUnsafe
	}
	file, err := os.Open(store.path)
	if err != nil {
		return State{}, ErrUnsafe
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return State{}, ErrUnsafe
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, ErrUnsafe
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !valid(state) {
		return State{}, ErrUnsafe
	}
	return state, nil
}

func (store *Store) Write(state State) error {
	if store == nil || !valid(state) {
		return ErrUnsafe
	}
	state.SchemaVersion = schemaVersion
	raw, err := json.Marshal(state)
	if err != nil {
		return ErrUnsafe
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".plugin-flow-*.tmp")
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
	if err := replaceFile(temporaryPath, store.path); err != nil {
		return fmt.Errorf("%w: replace", ErrUnsafe)
	}
	cleanup = false
	return nil
}

func valid(state State) bool {
	return state.SchemaVersion == schemaVersion &&
		(state.DeviceID == "" || deviceID.MatchString(state.DeviceID)) &&
		(state.PendingThemePublicID == "" || publicID.MatchString(state.PendingThemePublicID))
}
