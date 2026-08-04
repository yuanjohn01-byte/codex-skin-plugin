package releasesigning

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"testing"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

func TestSignDescriptorMatchesTrustedKeyset(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyset := keysetFor(t, publicKey)
	digest := make([]byte, 32)
	descriptor, err := releasecontract.CanonicalBytes(releasecontract.Descriptor{
		SchemaVersion: 1, HelperVersion: "0.1.0-test", ReleaseTag: "helper-v0.1.0-test",
		PublishedAt: "2026-08-03T00:00:00Z", SigningKeyID: "helper-test-2026-08",
		Artifacts: []releasecontract.Artifact{
			{Platform: "macos-arm64", Filename: "codex-skin-helper_0.1.0-test_macos_arm64", SHA256: hex.EncodeToString(digest), Size: 1},
			{Platform: "macos-x64", Filename: "codex-skin-helper_0.1.0-test_macos_x64", SHA256: hex.EncodeToString(digest), Size: 1},
			{Platform: "windows-x64", Filename: "codex-skin-helper_0.1.0-test_windows_x64.exe", SHA256: hex.EncodeToString(digest), Size: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	secret := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	signature, err := SignDescriptor(descriptor, secret, keyset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releasecontract.VerifyWithKeyset(descriptor, signature, keyset); err != nil {
		t.Fatal(err)
	}
	otherPublic, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil || len(otherPublic) == 0 {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalPKCS8PrivateKey(otherPrivate)
	if err != nil {
		t.Fatal(err)
	}
	wrongSecret := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherDER}))
	if _, err := SignDescriptor(descriptor, wrongSecret, keyset); err == nil {
		t.Fatal("untrusted Helper release signing key was accepted")
	}
}

func keysetFor(t *testing.T, publicKey ed25519.PublicKey) releasecontract.VerificationKeyset {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"keys": []map[string]any{{
			"keyId": "helper-test-2026-08", "algorithm": "Ed25519", "usage": "helper-release",
			"publicKeyBase64": base64.StdEncoding.EncodeToString(publicKey),
			"notBefore":       "2026-08-01T00:00:00Z", "notAfter": "2027-08-01T00:00:00Z", "status": "active",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyset, err := releasecontract.ParseVerificationKeyset(append(raw, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	return keyset
}
