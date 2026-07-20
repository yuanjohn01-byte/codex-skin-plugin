//go:build windows

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestWindowsCredentialManagerRoundTrip(t *testing.T) {
	store, err := New()
	if err != nil {
		t.Fatal(err)
	}
	first := windowsCredentialPayload(t)
	second := windowsCredentialPayload(t)
	defer zeroBytes(first)
	defer zeroBytes(second)
	_ = store.Delete(context.Background(), testDeviceID)
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), testDeviceID)
	})

	if err := store.Put(context.Background(), testDeviceID, first); err != nil {
		t.Fatal(err)
	}
	assertWindowsStoredSecret(t, store, first)
	if bytes.Contains([]byte(strings.Join(os.Args, "\x00")), first) || bytes.Contains([]byte(strings.Join(os.Environ(), "\x00")), first) {
		t.Fatal("credential appeared in process arguments or environment")
	}
	if err := store.Put(context.Background(), testDeviceID, second); err != nil {
		t.Fatal(err)
	}
	assertWindowsStoredSecret(t, store, second)
	if err := store.Delete(context.Background(), testDeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), testDeviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
}

func windowsCredentialPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"refreshToken":  randomWindowsSecret(t),
		"deviceKey":     randomWindowsSecret(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func randomWindowsSecret(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func assertWindowsStoredSecret(t *testing.T, store *Store, expected []byte) {
	t.Helper()
	actual, err := store.Get(context.Background(), testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(actual)
	if !bytes.Equal(actual, expected) {
		t.Fatal("Credential Manager returned a different credential")
	}
}
