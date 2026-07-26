package theme

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	imagecolor "image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testPublishedAt = "2026-07-26T06:00:00Z"

type testRelease struct {
	packagePath   string
	packageBytes  []byte
	manifest      Manifest
	manifestBytes []byte
	descriptor    []byte
	signature     []byte
	keyset        []byte
	privateKey    ed25519.PrivateKey
}

type archiveEntry struct {
	name    string
	content []byte
	method  uint16
	mode    os.FileMode
}

func TestVerifyAndExtractUsesVerifiedBytes(t *testing.T) {
	release := makeTestRelease(t, nil)
	verified, err := verifyWithKeyset(release.packagePath, release.descriptor, release.signature, release.keyset)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	if err := os.WriteFile(release.packagePath, []byte("replaced after verification"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "staged")
	if err := Extract(verified, destination); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	extracted, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(release.manifest.Assets[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256Hex(extracted); got != release.manifest.Assets[0].SHA256 {
		t.Fatalf("extracted asset hash = %s", got)
	}
	info, err := os.Stat(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if info.Mode().Perm()&0o111 != 0 {
			t.Fatalf("manifest mode = %o, must not be executable", info.Mode().Perm())
		}
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o, want 600", info.Mode().Perm())
	}
}

func TestVerifyRejectsSignaturePackageAndKeyFailures(t *testing.T) {
	release := makeTestRelease(t, nil)

	tamperedSignature := append([]byte(nil), release.signature...)
	tamperedSignature[0] = 'A'
	if _, err := verifyWithKeyset(release.packagePath, release.descriptor, tamperedSignature, release.keyset); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("tampered signature error = %v", err)
	}

	descriptor := parseDescriptorForTest(t, release.descriptor)
	descriptor.PackageSHA256 = strings.Repeat("0", 64)
	badDescriptor := canonicalForTest(t, descriptor)
	badSignature := signForTest(release.privateKey, badDescriptor)
	if _, err := verifyWithKeyset(release.packagePath, badDescriptor, badSignature, release.keyset); !errors.Is(err, ErrPackageMismatch) {
		t.Fatalf("package mismatch error = %v", err)
	}

	var keyset VerificationKeyset
	if err := json.Unmarshal(release.keyset, &keyset); err != nil {
		t.Fatal(err)
	}
	keyset.Keys[0].Status = "revoked"
	revokedKeyset := prettyForTest(t, keyset)
	if _, err := verifyWithKeyset(release.packagePath, release.descriptor, release.signature, revokedKeyset); !errors.Is(err, ErrSigningKeyInvalid) {
		t.Fatalf("revoked key error = %v", err)
	}
}

func TestVerifyPinsCanonicalEmbeddedTrustRoot(t *testing.T) {
	fixture := filepath.Join("..", "..", "fixtures", "free-test-theme-v1", "signed-release-v1")
	descriptor := readFileForTest(t, filepath.Join(fixture, "release-descriptor.json"))
	signature := readFileForTest(t, filepath.Join(fixture, "release-descriptor.sig"))
	verified, err := Verify(filepath.Join(fixture, "package.cskin"), descriptor, signature)
	if err != nil {
		t.Fatalf("Verify() trusted fixture error = %v", err)
	}
	if verified.Descriptor.SigningKeyID != "theme-alpha-2026-01" ||
		!bytes.Equal(verified.KeysetBytes, trustedKeysetRaw) {
		t.Fatalf("trusted verification result = %#v", verified.Descriptor)
	}

	selfSigned := makeTestRelease(t, nil)
	if _, err := Verify(
		selfSigned.packagePath,
		selfSigned.descriptor,
		selfSigned.signature,
	); !errors.Is(err, ErrSigningKeyInvalid) {
		t.Fatalf("self-signed keyset bypass error = %v", err)
	}

	var keyset VerificationKeyset
	if err := json.Unmarshal(trustedKeysetRaw, &keyset); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKeyset(canonicalForTest(t, keyset)); !errors.Is(err, ErrKeysetCanonical) {
		t.Fatalf("non-canonical keyset error = %v", err)
	}
}

func TestEngineCompatibilityUsesSemverPrecedence(t *testing.T) {
	for _, test := range []struct {
		current    string
		minimum    string
		compatible bool
	}{
		{current: "0.2.0", minimum: "0.2.0", compatible: true},
		{current: "0.2.1", minimum: "0.2.0", compatible: true},
		{current: "0.2.0", minimum: "0.2.1", compatible: false},
		{current: "0.2.0", minimum: "0.2.0-rc.1", compatible: true},
		{current: "0.2.0-rc.2", minimum: "0.2.0-rc.10", compatible: false},
		{current: "0.2.0-rc.10", minimum: "0.2.0-rc.2", compatible: true},
		{current: "invalid", minimum: "0.2.0", compatible: false},
	} {
		if got := EngineCompatible(test.current, test.minimum); got != test.compatible {
			t.Fatalf(
				"EngineCompatible(%q, %q) = %v, want %v",
				test.current,
				test.minimum,
				got,
				test.compatible,
			)
		}
	}
}

func TestVerifyRejectsSymlinkPackage(t *testing.T) {
	release := makeTestRelease(t, nil)
	link := filepath.Join(t.TempDir(), "package.cskin")
	if err := os.Symlink(release.packagePath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyWithKeyset(link, release.descriptor, release.signature, release.keyset); !errors.Is(err, ErrPackageInvalid) {
		t.Fatalf("symlink package error = %v", err)
	}
}

func TestManifestRejectsUnknownDuplicateAndNonCanonicalJSON(t *testing.T) {
	release := makeTestRelease(t, nil)
	manifest := string(release.manifestBytes)

	unknown := strings.Replace(manifest, `"schemaVersion":1`, `"schemaVersion":1,"css":"*"`, 1)
	if _, err := ParseManifest([]byte(unknown)); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("unknown field error = %v", err)
	}
	duplicate := strings.Replace(manifest, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1)
	if _, err := ParseManifest([]byte(duplicate)); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("duplicate key error = %v", err)
	}
	pretty := prettyForTest(t, release.manifest)
	if _, err := ParseManifest(pretty); !errors.Is(err, ErrManifestCanonical) {
		t.Fatalf("non-canonical error = %v", err)
	}
}

func TestVerifyRejectsMaliciousArchiveShapes(t *testing.T) {
	base := makeTestRelease(t, nil)
	asset := base.manifest.Assets[0]
	imageBytes := readFileForTest(t, filepath.Join(filepath.Dir(base.packagePath), "asset.png"))

	tests := []struct {
		name    string
		entries []archiveEntry
	}{
		{
			name: "compressed asset",
			entries: []archiveEntry{
				{name: "manifest.json", content: base.manifestBytes, method: zip.Store, mode: 0o600},
				{name: asset.Path, content: imageBytes, method: zip.Deflate, mode: 0o600},
			},
		},
		{
			name: "path traversal",
			entries: []archiveEntry{
				{name: "manifest.json", content: base.manifestBytes, method: zip.Store, mode: 0o600},
				{name: "../escape.png", content: imageBytes, method: zip.Store, mode: 0o600},
			},
		},
		{
			name: "undeclared file",
			entries: []archiveEntry{
				{name: "manifest.json", content: base.manifestBytes, method: zip.Store, mode: 0o600},
				{name: asset.Path, content: imageBytes, method: zip.Store, mode: 0o600},
				{name: "assets/" + strings.Repeat("a", 64) + ".png", content: imageBytes, method: zip.Store, mode: 0o600},
			},
		},
		{
			name: "symlink entry",
			entries: []archiveEntry{
				{name: "manifest.json", content: base.manifestBytes, method: zip.Store, mode: 0o600},
				{name: asset.Path, content: imageBytes, method: zip.Store, mode: os.ModeSymlink | 0o777},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := makeTestRelease(t, test.entries)
			if _, err := verifyWithKeyset(release.packagePath, release.descriptor, release.signature, release.keyset); !errors.Is(err, ErrPackageInvalid) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestVerifyRejectsInvalidPNGChecksum(t *testing.T) {
	release := makeTestRelease(t, nil)
	imageBytes := readFileForTest(t, filepath.Join(filepath.Dir(release.packagePath), "asset.png"))
	imageBytes[len(imageBytes)-5] ^= 0xff
	imageDigest := sha256Hex(imageBytes)
	release.manifest.Design.Tokens.BackgroundImage = "assets/" + imageDigest + ".png"
	release.manifest.Assets[0].Path = release.manifest.Design.Tokens.BackgroundImage
	release.manifest.Assets[0].SHA256 = imageDigest
	release.manifest.Assets[0].ByteSize = int64(len(imageBytes))
	manifestBytes := canonicalForTest(t, release.manifest)
	entries := []archiveEntry{
		{name: "manifest.json", content: manifestBytes, method: zip.Store, mode: 0o600},
		{name: release.manifest.Assets[0].Path, content: imageBytes, method: zip.Store, mode: 0o600},
	}
	badRelease := makeTestReleaseWithManifest(t, release.manifest, manifestBytes, entries, release.privateKey, release.keyset)
	if _, err := verifyWithKeyset(badRelease.packagePath, badRelease.descriptor, badRelease.signature, badRelease.keyset); !errors.Is(err, ErrPackageInvalid) {
		t.Fatalf("invalid PNG error = %v", err)
	}
}

func TestExtractRejectsExistingDestination(t *testing.T) {
	release := makeTestRelease(t, nil)
	verified, err := verifyWithKeyset(release.packagePath, release.descriptor, release.signature, release.keyset)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := Extract(verified, destination); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("existing destination error = %v", err)
	}
}

func makeTestRelease(t *testing.T, overrideEntries []archiveEntry) testRelease {
	t.Helper()
	imageBytes := tinyPNG(t)
	imageDigest := sha256Hex(imageBytes)
	assetPath := "assets/" + imageDigest + ".png"
	manifest := Manifest{
		SchemaVersion: 1,
		ThemePublicID: "100001",
		ThemeVersion:  "1.0.0",
		Name:          "Synthetic Contract Theme",
		Design: Design{
			Mode: "dark",
			Tokens: Tokens{
				BackgroundImage:   assetPath,
				BackgroundOverlay: 0.42,
				SurfaceOpacity:    0.82,
				SurfaceBlurPx:     18,
				TextPrimary:       "#F7F7FA",
				TextSecondary:     "#C8CAD3",
				Accent:            "#A78BFA",
				Border:            "#FFFFFF24",
				RadiusScale:       1,
			},
			Regions: Regions{Home: true, Sidebar: true, SuggestionCards: true, ProjectPicker: true, Composer: true},
		},
		Customization: Customization{Allowed: []string{"backgroundOverlay", "surfaceOpacity", "surfaceBlurPx", "accent", "radiusScale"}},
		Assets: []Asset{{
			Path: assetPath, Role: "background", ContentType: "image/png", ByteSize: int64(len(imageBytes)), SHA256: imageDigest,
		}},
		Compatibility: Compatibility{Platforms: []string{"macos", "windows"}, MinEngineVersion: "0.1.0"},
	}
	manifestBytes := canonicalForTest(t, manifest)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyset := makeKeyset(t, privateKey.Public().(ed25519.PublicKey))
	entries := overrideEntries
	if entries == nil {
		entries = []archiveEntry{
			{name: "manifest.json", content: manifestBytes, method: zip.Store, mode: 0o600},
			{name: assetPath, content: imageBytes, method: zip.Store, mode: 0o600},
		}
	}
	release := makeTestReleaseWithManifest(t, manifest, manifestBytes, entries, privateKey, keyset)
	if err := os.WriteFile(filepath.Join(filepath.Dir(release.packagePath), "asset.png"), imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return release
}

func makeTestReleaseWithManifest(
	t *testing.T,
	manifest Manifest,
	manifestBytes []byte,
	entries []archiveEntry,
	privateKey ed25519.PrivateKey,
	keyset []byte,
) testRelease {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	packageBytes := archive.Bytes()
	descriptorValue := Descriptor{
		DescriptorVersion: 1,
		ThemePublicID:     manifest.ThemePublicID,
		ThemeVersion:      manifest.ThemeVersion,
		SchemaVersion:     manifest.SchemaVersion,
		ManifestSHA256:    sha256Hex(manifestBytes),
		PackageSHA256:     sha256Hex(packageBytes),
		PackageByteSize:   int64(len(packageBytes)),
		PublishedAt:       testPublishedAt,
		SigningKeyID:      "test-theme-key",
	}
	descriptor := canonicalForTest(t, descriptorValue)
	signature := signForTest(privateKey, descriptor)
	root := t.TempDir()
	packagePath := filepath.Join(root, "theme.cskin")
	if err := os.WriteFile(packagePath, packageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return testRelease{
		packagePath: packagePath, packageBytes: packageBytes, manifest: manifest, manifestBytes: manifestBytes,
		descriptor: descriptor, signature: signature, keyset: keyset, privateKey: privateKey,
	}
}

func makeKeyset(t *testing.T, publicKey ed25519.PublicKey) []byte {
	t.Helper()
	return prettyForTest(t, VerificationKeyset{
		SchemaVersion: 1,
		Keys: []VerificationKey{{
			KeyID:           "test-theme-key",
			Algorithm:       "Ed25519",
			Usage:           "theme-release",
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
			NotBefore:       "2026-01-01T00:00:00Z",
			NotAfter:        "2028-01-01T00:00:00Z",
			Status:          "active",
		}},
	})
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, imagecolor.RGBA{R: 0x32, G: 0x77, B: 0x8a, A: 0xff})
	var content bytes.Buffer
	if err := png.Encode(&content, picture); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func canonicalForTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func prettyForTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func signForTest(privateKey ed25519.PrivateKey, descriptor []byte) []byte {
	signature := ed25519.Sign(privateKey, append([]byte(signatureDomainText), descriptor...))
	return []byte(base64.StdEncoding.EncodeToString(signature) + "\n")
}

func parseDescriptorForTest(t *testing.T, raw []byte) Descriptor {
	t.Helper()
	var descriptor Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func readFileForTest(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestSHA256HelperMatchesStandardLibrary(t *testing.T) {
	content := []byte("codex-skin")
	sum := sha256.Sum256(content)
	if got, want := sha256Hex(content), base64.StdEncoding.EncodeToString(sum[:]); got == want {
		t.Fatal("sha256Hex unexpectedly returned base64")
	}
}
