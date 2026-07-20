package credentials

import (
	"context"
	"errors"
	"testing"
)

const testDeviceID = "dev_0123456789abcdefghijklmnopqrstuv"

type memoryBackend struct {
	secret []byte
}

func (backend *memoryBackend) put(_ context.Context, _ string, secret []byte) error {
	backend.secret = append(backend.secret[:0], secret...)
	return nil
}

func (backend *memoryBackend) get(context.Context, string) ([]byte, error) {
	if backend.secret == nil {
		return nil, ErrNotFound
	}
	return append([]byte(nil), backend.secret...), nil
}

func (backend *memoryBackend) delete(context.Context, string) error {
	if backend.secret == nil {
		return ErrNotFound
	}
	zeroBytes(backend.secret)
	backend.secret = nil
	return nil
}

func TestStoreValidatesAndCopiesSecrets(t *testing.T) {
	platformBackend := &memoryBackend{}
	store := newStore(platformBackend)
	secret := []byte("synthetic-credential")
	if err := store.Put(context.Background(), testDeviceID, secret); err != nil {
		t.Fatal(err)
	}
	secret[0] = 'X'
	stored, err := store.Get(context.Background(), testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(stored)
	if string(stored) != "synthetic-credential" {
		t.Fatal("store did not isolate the caller's secret buffer")
	}
	if err := store.Delete(context.Background(), testDeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), testDeviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
}

func TestStoreRejectsUnsafeInputs(t *testing.T) {
	store := newStore(&memoryBackend{})
	for _, deviceID := range []string{"", "dev_short", "../dev_0123456789abcdefghijklmnopqrstu", "dev_0123456789abcdefghijklmnopqrstu!"} {
		if err := store.Put(context.Background(), deviceID, []byte("secret")); !errors.Is(err, ErrInvalidDeviceID) {
			t.Fatalf("Put device ID error = %v", err)
		}
	}
	for _, secret := range [][]byte{nil, {}, []byte("line\nbreak"), make([]byte, maximumSecretBytes+1)} {
		if err := store.Put(context.Background(), testDeviceID, secret); !errors.Is(err, ErrInvalidSecret) {
			t.Fatalf("Put secret error = %v", err)
		}
	}
}
