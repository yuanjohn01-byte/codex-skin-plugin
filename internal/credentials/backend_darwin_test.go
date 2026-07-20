//go:build darwin

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinKeychainRoundTrip(t *testing.T) {
	keychain := filepath.Join(t.TempDir(), "codex-skin-test.keychain-db")
	keychainPassword := randomSecret(t)
	runSecurityTestCommand(t, "create-keychain", "-p", keychainPassword, keychain)
	t.Cleanup(func() {
		_ = exec.Command(securityBinary, "delete-keychain", keychain).Run()
	})
	runSecurityTestCommand(t, "unlock-keychain", "-p", keychainPassword, keychain)
	store := newStore(newDarwinBackend(keychain))
	first := credentialPayload(t)
	second := credentialPayload(t)
	defer zeroBytes(first)
	defer zeroBytes(second)

	if err := store.Put(context.Background(), testDeviceID, first); err != nil {
		t.Fatal(err)
	}
	assertSecretNotInProcessOrOrdinaryFile(t, keychain, first)
	assertStoredSecret(t, store, first)
	if err := store.Put(context.Background(), testDeviceID, second); err != nil {
		t.Fatal(err)
	}
	assertStoredSecret(t, store, second)
	if err := store.Delete(context.Background(), testDeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), testDeviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete error = %v", err)
	}
}

func credentialPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"refreshToken":  randomSecret(t),
		"deviceKey":     randomSecret(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func randomSecret(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func runSecurityTestCommand(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command(securityBinary, arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("security test setup failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func assertStoredSecret(t *testing.T, store *Store, expected []byte) {
	t.Helper()
	actual, err := store.Get(context.Background(), testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(actual)
	if !bytes.Equal(actual, expected) {
		t.Fatal("Keychain returned a different credential")
	}
}

func assertSecretNotInProcessOrOrdinaryFile(t *testing.T, keychain string, secret []byte) {
	t.Helper()
	if bytes.Contains([]byte(strings.Join(os.Args, "\x00")), secret) || bytes.Contains([]byte(strings.Join(os.Environ(), "\x00")), secret) {
		t.Fatal("credential appeared in process arguments or environment")
	}
	contents, err := os.ReadFile(keychain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, secret) {
		t.Fatal("credential appeared as plaintext in the Keychain database")
	}
}
