package bootstrapcli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/bootstrap"
	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

type memorySource map[string][]byte

func (source memorySource) Fetch(_ context.Context, tag, name string, maxBytes int64) ([]byte, error) {
	content, ok := source[tag+"/"+name]
	if !ok || int64(len(content)) > maxBytes {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), content...), nil
}

type acceptingTester struct{}

func (acceptingTester) Test(context.Context, string, string, string) error { return nil }

func bootstrapFixture(t *testing.T) (string, memorySource, releasecontract.VerificationKeyset) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keysetRaw, err := json.Marshal(map[string]any{
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
	keyset, err := releasecontract.ParseVerificationKeyset(append(keysetRaw, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	version := "0.1.0-test"
	tag := "helper-v" + version
	source := memorySource{}
	platforms := []struct {
		platform string
		filename string
	}{
		{"macos-arm64", "codex-skin-helper_0.1.0-test_macos_arm64"},
		{"macos-x64", "codex-skin-helper_0.1.0-test_macos_x64"},
		{"windows-x64", "codex-skin-helper_0.1.0-test_windows_x64.exe"},
	}
	artifacts := make([]releasecontract.Artifact, 0, len(platforms))
	for _, platform := range platforms {
		content := []byte("fixture-" + platform.platform)
		digest := sha256.Sum256(content)
		artifacts = append(artifacts, releasecontract.Artifact{
			Platform: platform.platform, Filename: platform.filename,
			SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content)),
		})
		source[tag+"/"+platform.filename] = content
	}
	descriptor := releasecontract.Descriptor{
		SchemaVersion: 1, HelperVersion: version, ReleaseTag: tag,
		PublishedAt: "2026-08-03T00:00:00Z", SigningKeyID: "helper-test-2026-08", Artifacts: artifacts,
	}
	canonical, err := releasecontract.CanonicalBytes(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	source[tag+"/helper-release-descriptor.json"] = canonical
	source[tag+"/helper-release-descriptor.sig"] = ed25519.Sign(privateKey, releasecontract.SigningMessage(canonical))
	return tag, source, keyset
}

func TestInstallCreatesAndReusesExternalHelper(t *testing.T) {
	tag, source, keyset := bootstrapFixture(t)
	temporary := t.TempDir()
	root := filepath.Join(temporary, "app")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := Runtime{
		GOOS: "darwin", GOARCH: "arm64", Root: root, ReleaseTag: tag,
		Source: source, SelfTester: acceptingTester{}, Keyset: &keyset,
	}
	for attempt := 0; attempt < 2; attempt++ {
		var stdout, stderr bytes.Buffer
		if exit := Run([]string{"install", "--plugin-cache", cache, "--json"}, &stdout, &stderr, environment); exit != 0 {
			t.Fatalf("attempt %d failed with %d: %s", attempt, exit, stderr.String())
		}
		var result struct {
			OK   bool `json:"ok"`
			Data struct {
				HelperVersion  string `json:"helperVersion"`
				HelperSHA256   string `json:"helperSha256"`
				RecoverySHA256 string `json:"recoverySha256"`
				Reused         bool   `json:"reused"`
				RecoveryReady  bool   `json:"recoveryReady"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.Data.HelperVersion != "0.1.0-test" ||
			len(result.Data.HelperSHA256) != 64 || result.Data.RecoverySHA256 != result.Data.HelperSHA256 ||
			!result.Data.RecoveryReady || result.Data.Reused != (attempt == 1) {
			t.Fatalf("unexpected install result: %s", stdout.String())
		}
	}
	for _, path := range []string{
		filepath.Join(root, "bin", "current.json"),
		filepath.Join(root, "recovery", "engine", "codex-skin"),
		filepath.Join(root, "recovery", "restore.command"),
	} {
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("expected regular installed file %s: %v", path, err)
		}
	}
}

func TestInstallRejectsTamperedSignatureBeforeActivation(t *testing.T) {
	tag, source, keyset := bootstrapFixture(t)
	source[tag+"/helper-release-descriptor.sig"][0] ^= 0xff
	temporary := t.TempDir()
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := Run(
		[]string{"install", "--plugin-cache", cache, "--json"}, &stdout, &stderr,
		Runtime{
			GOOS: "darwin", GOARCH: "arm64", Root: filepath.Join(temporary, "app"), ReleaseTag: tag,
			Source: source, SelfTester: acceptingTester{}, Keyset: &keyset,
		},
	)
	if exit != exitRejected || !bytes.Contains(stdout.Bytes(), []byte(`"code":"CS-BOOTSTRAP-INSTALL-001"`)) {
		t.Fatalf("tampered signature returned %d: %s %s", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(temporary, "app", "bin", "current.json")); !os.IsNotExist(err) {
		t.Fatal("tampered release activated a Helper pointer")
	}
}

var _ bootstrap.Source = memorySource{}
var _ bootstrap.SelfTester = acceptingTester{}
