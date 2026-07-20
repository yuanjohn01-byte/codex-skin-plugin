package credentials

import (
	"context"
	"errors"
	"strings"
)

const (
	keychainService     = "com.codex-skin.plugin.refresh-token"
	windowsTargetPrefix = "CodexSkin/RefreshToken/"
	maximumSecretBytes  = 2048
)

var (
	ErrUnsupported     = errors.New("system credential store is unsupported")
	ErrInvalidDeviceID = errors.New("credential device ID is invalid")
	ErrInvalidSecret   = errors.New("credential secret is invalid")
	ErrNotFound        = errors.New("credential was not found")
	ErrUnavailable     = errors.New("system credential store is unavailable")
)

type backend interface {
	put(context.Context, string, []byte) error
	get(context.Context, string) ([]byte, error)
	delete(context.Context, string) error
}

type Store struct {
	backend backend
}

func New() (*Store, error) {
	platformBackend, err := newBackend()
	if err != nil {
		return nil, err
	}
	return newStore(platformBackend), nil
}

func newStore(platformBackend backend) *Store {
	return &Store{backend: platformBackend}
}

func (store *Store) Put(ctx context.Context, deviceID string, secret []byte) error {
	if store == nil || store.backend == nil {
		return ErrUnavailable
	}
	if !validDeviceID(deviceID) {
		return ErrInvalidDeviceID
	}
	if !validSecret(secret) {
		return ErrInvalidSecret
	}
	secretCopy := append([]byte(nil), secret...)
	defer zeroBytes(secretCopy)
	return store.backend.put(ctx, deviceID, secretCopy)
}

func (store *Store) Get(ctx context.Context, deviceID string) ([]byte, error) {
	if store == nil || store.backend == nil {
		return nil, ErrUnavailable
	}
	if !validDeviceID(deviceID) {
		return nil, ErrInvalidDeviceID
	}
	secret, err := store.backend.get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !validSecret(secret) {
		zeroBytes(secret)
		return nil, ErrInvalidSecret
	}
	return secret, nil
}

func (store *Store) Delete(ctx context.Context, deviceID string) error {
	if store == nil || store.backend == nil {
		return ErrUnavailable
	}
	if !validDeviceID(deviceID) {
		return ErrInvalidDeviceID
	}
	return store.backend.delete(ctx, deviceID)
}

func validDeviceID(value string) bool {
	if len(value) != 36 || !strings.HasPrefix(value, "dev_") {
		return false
	}
	for _, character := range value[4:] {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", character) {
			return false
		}
	}
	return true
}

func validSecret(value []byte) bool {
	if len(value) == 0 || len(value) > maximumSecretBytes {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
