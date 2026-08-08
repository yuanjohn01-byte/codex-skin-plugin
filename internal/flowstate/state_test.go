package flowstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTripAndRejectsSecrets(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		SchemaVersion:        1,
		DeviceID:             "dev_" + "a2345678901234567890123456789012",
		PendingThemePublicID: "100001",
	}
	if err := store.Write(state); err != nil {
		t.Fatal(err)
	}
	actual, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if actual != state {
		t.Fatalf("state = %+v, want %+v", actual, state)
	}
	raw, err := os.ReadFile(filepath.Join(root, "state", "plugin-flow.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"refreshToken", "accessToken", "deviceKey", "codeVerifier"} {
		if contains := string(raw); len(contains) > 0 && stringContains(contains, forbidden) {
			t.Fatalf("state contains secret field %q", forbidden)
		}
	}
}

func TestStateRejectsSymlinkAndUnknownFields(t *testing.T) {
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "state")
	if err := os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "plugin-flow.json"), []byte(`{"schemaVersion":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("unknown field error = %v", err)
	}
	if err := os.Remove(filepath.Join(stateDirectory, "plugin-flow.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(stateDirectory, "plugin-flow.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink error = %v", err)
	}
}

func stringContains(value, expected string) bool {
	for index := 0; index+len(expected) <= len(value); index++ {
		if value[index:index+len(expected)] == expected {
			return true
		}
	}
	return false
}
