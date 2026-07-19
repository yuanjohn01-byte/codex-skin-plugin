package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func testRelease(t *testing.T) (Descriptor, []byte, []byte, map[string]ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("verified helper artifact")
	digest := sha256.Sum256(payload)
	descriptor := Descriptor{
		SchemaVersion: 1,
		HelperVersion: "0.1.0-s3",
		ReleaseTag:    "helper-v0.1.0-s3",
		PublishedAt:   "2026-07-20T00:00:00Z",
		SigningKeyID:  "internal-runtime-test",
		Artifacts: []Artifact{
			{Platform: "macos-arm64", Filename: "codex-skin-helper_0.1.0-s3_macos_arm64", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))},
			{Platform: "macos-x64", Filename: "codex-skin-helper_0.1.0-s3_macos_x64", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))},
			{Platform: "windows-x64", Filename: "codex-skin-helper_0.1.0-s3_windows_x64.exe", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))},
		},
	}
	canonical, err := CanonicalBytes(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, SigningMessage(canonical))
	return descriptor, canonical, signature, map[string]ed25519.PublicKey{descriptor.SigningKeyID: publicKey}
}

func TestSelectVerifiedByRuntimeAndVersion(t *testing.T) {
	_, canonical, signature, keys := testRelease(t)
	tests := []struct {
		name       string
		current    string
		goos       string
		goarch     string
		platform   string
		relation   VersionRelation
		fileSuffix string
	}{
		{name: "install arm64", goos: "darwin", goarch: "arm64", platform: "macos-arm64", relation: VersionInstall, fileSuffix: "macos_arm64"},
		{name: "upgrade x64", current: "0.1.0-s2", goos: "darwin", goarch: "amd64", platform: "macos-x64", relation: VersionUpgrade, fileSuffix: "macos_x64"},
		{name: "current windows", current: "0.1.0-s3", goos: "windows", goarch: "amd64", platform: "windows-x64", relation: VersionCurrent, fileSuffix: "windows_x64.exe"},
		{name: "classify downgrade", current: "0.1.0", goos: "darwin", goarch: "arm64", platform: "macos-arm64", relation: VersionDowngrade, fileSuffix: "macos_arm64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection, err := SelectVerified(canonical, signature, keys, test.current, test.goos, test.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if selection.Artifact.Platform != test.platform || selection.Relation != test.relation {
				t.Fatalf("unexpected selection: %#v", selection)
			}
			if !bytes.HasSuffix([]byte(selection.Artifact.Filename), []byte(test.fileSuffix)) {
				t.Fatalf("wrong filename: %s", selection.Artifact.Filename)
			}
		})
	}
}

func TestVerifyRejectsUntrustedAndModifiedDescriptors(t *testing.T) {
	descriptor, canonical, signature, keys := testRelease(t)
	if _, err := Verify(canonical, signature, keys); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		descriptor []byte
		signature  []byte
		keys       map[string]ed25519.PublicKey
		want       error
	}{
		{name: "unknown key", descriptor: canonical, signature: signature, keys: map[string]ed25519.PublicKey{}, want: ErrUnknownSigningKey},
		{name: "bad signature", descriptor: canonical, signature: append([]byte(nil), signature[:63]...), keys: keys, want: ErrSignatureInvalid},
		{name: "tampered canonical bytes", descriptor: bytes.Replace(canonical, []byte("0.1.0-s3"), []byte("0.1.0-s4"), 1), signature: signature, keys: keys, want: ErrDescriptorInvalid},
		{name: "not canonical", descriptor: append(bytes.Clone(bytes.TrimSpace(canonical)), ' ', '\n'), signature: signature, keys: keys, want: ErrDescriptorCanonical},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Verify(test.descriptor, test.signature, test.keys)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}

	unknownField := map[string]any{}
	if err := json.Unmarshal(canonical, &unknownField); err != nil {
		t.Fatal(err)
	}
	unknownField["downloadUrl"] = "https://example.invalid/unsafe"
	withUnknown, err := json.Marshal(unknownField)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(append(withUnknown, '\n'), signature, keys); !errors.Is(err, ErrDescriptorInvalid) {
		t.Fatalf("unknown field was not rejected: %v", err)
	}

	descriptor.Artifacts[1].Platform = descriptor.Artifacts[0].Platform
	if _, err := CanonicalBytes(descriptor); !errors.Is(err, ErrDescriptorInvalid) {
		t.Fatalf("duplicate or out-of-order platform was not rejected: %v", err)
	}
}

func TestVerifyArtifact(t *testing.T) {
	payload := []byte("verified helper artifact")
	digest := sha256.Sum256(payload)
	artifact := Artifact{Size: int64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
	if err := VerifyArtifact(bytes.NewReader(payload), artifact); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifact(bytes.NewReader(append(payload, '!')), artifact); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("oversize artifact was not rejected: %v", err)
	}
	if err := VerifyArtifact(bytes.NewReader(payload[:len(payload)-1]), artifact); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("truncated artifact was not rejected: %v", err)
	}
	artifact.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := VerifyArtifact(bytes.NewReader(payload), artifact); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("bad digest was not rejected: %v", err)
	}
}

func TestSemverPrecedenceAndUnsupportedRuntime(t *testing.T) {
	tests := []struct {
		current string
		target  string
		want    VersionRelation
	}{
		{current: "1.0.0-alpha", target: "1.0.0-alpha.1", want: VersionUpgrade},
		{current: "1.0.0-beta.2", target: "1.0.0-beta.11", want: VersionUpgrade},
		{current: "1.0.0-rc.1", target: "1.0.0", want: VersionUpgrade},
		{current: "1.0.1", target: "1.0.0", want: VersionDowngrade},
	}
	for _, test := range tests {
		got, err := Relation(test.current, test.target)
		if err != nil || got != test.want {
			t.Fatalf("Relation(%q, %q) = %q, %v; want %q", test.current, test.target, got, err, test.want)
		}
	}
	if _, err := Relation("01.0.0", "1.0.0"); !errors.Is(err, ErrDescriptorInvalid) {
		t.Fatalf("invalid current version was accepted: %v", err)
	}
	if _, err := PlatformForRuntime("linux", "amd64"); !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("unsupported platform was accepted: %v", err)
	}
}
