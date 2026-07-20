// Package release verifies and selects immutable Codex Skin Helper releases.
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	SchemaVersion   = 1
	MaxDescriptor   = 64 * 1024
	MaxArtifactSize = 50 * 1024 * 1024
	signatureDomain = "codex-skin/helper-release-descriptor/v1\x00"
)

var (
	ErrDescriptorInvalid   = errors.New("release descriptor is invalid")
	ErrDescriptorCanonical = errors.New("release descriptor is not canonical")
	ErrSignatureInvalid    = errors.New("release descriptor signature is invalid")
	ErrUnknownSigningKey   = errors.New("release descriptor signing key is not trusted")
	ErrPlatformUnsupported = errors.New("runtime platform is not supported")
	ErrArtifactMismatch    = errors.New("release artifact does not match descriptor")
)

var (
	strictSemver  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?$`)
	keyIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var supportedPlatforms = []string{"macos-arm64", "macos-x64", "windows-x64"}

type Artifact struct {
	Platform string `json:"platform"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type Descriptor struct {
	SchemaVersion int        `json:"schemaVersion"`
	HelperVersion string     `json:"helperVersion"`
	ReleaseTag    string     `json:"releaseTag"`
	PublishedAt   string     `json:"publishedAt"`
	SigningKeyID  string     `json:"signingKeyId"`
	Artifacts     []Artifact `json:"artifacts"`
}

type VersionRelation string

const (
	VersionInstall   VersionRelation = "install"
	VersionUpgrade   VersionRelation = "upgrade"
	VersionCurrent   VersionRelation = "current"
	VersionDowngrade VersionRelation = "downgrade"
)

type Selection struct {
	Descriptor Descriptor
	Artifact   Artifact
	Relation   VersionRelation
}

// CanonicalBytes returns the only descriptor byte representation accepted by Verify.
func CanonicalBytes(descriptor Descriptor) ([]byte, error) {
	if err := validateDescriptor(descriptor); err != nil {
		return nil, err
	}
	content, err := json.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrDescriptorInvalid, err)
	}
	return append(content, '\n'), nil
}

// SigningMessage applies domain separation to canonical descriptor bytes.
func SigningMessage(canonicalDescriptor []byte) []byte {
	message := make([]byte, 0, len(signatureDomain)+len(canonicalDescriptor))
	message = append(message, signatureDomain...)
	return append(message, canonicalDescriptor...)
}

// Verify parses canonical bytes and authenticates the detached Ed25519 signature.
func Verify(rawDescriptor, signature []byte, trustedKeys map[string]ed25519.PublicKey) (Descriptor, error) {
	if len(rawDescriptor) == 0 || len(rawDescriptor) > MaxDescriptor {
		return Descriptor{}, fmt.Errorf("%w: byte length", ErrDescriptorInvalid)
	}

	var descriptor Descriptor
	decoder := json.NewDecoder(bytes.NewReader(rawDescriptor))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return Descriptor{}, fmt.Errorf("%w: decode: %v", ErrDescriptorInvalid, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Descriptor{}, err
	}
	canonical, err := CanonicalBytes(descriptor)
	if err != nil {
		return Descriptor{}, err
	}
	if !bytes.Equal(rawDescriptor, canonical) {
		return Descriptor{}, ErrDescriptorCanonical
	}

	publicKey, ok := trustedKeys[descriptor.SigningKeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Descriptor{}, ErrUnknownSigningKey
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, SigningMessage(rawDescriptor), signature) {
		return Descriptor{}, ErrSignatureInvalid
	}
	return descriptor, nil
}

// SelectVerified authenticates a release and chooses the one exact runtime artifact.
func SelectVerified(
	rawDescriptor, signature []byte,
	trustedKeys map[string]ed25519.PublicKey,
	currentVersion, goos, goarch string,
) (Selection, error) {
	descriptor, err := Verify(rawDescriptor, signature, trustedKeys)
	if err != nil {
		return Selection{}, err
	}
	platform, err := PlatformForRuntime(goos, goarch)
	if err != nil {
		return Selection{}, err
	}
	var selected Artifact
	for _, artifact := range descriptor.Artifacts {
		if artifact.Platform == platform {
			selected = artifact
			break
		}
	}
	if selected.Platform == "" {
		return Selection{}, fmt.Errorf("%w: missing %s", ErrDescriptorInvalid, platform)
	}
	relation, err := Relation(currentVersion, descriptor.HelperVersion)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Descriptor: descriptor, Artifact: selected, Relation: relation}, nil
}

func PlatformForRuntime(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return "macos-arm64", nil
	case "darwin/amd64":
		return "macos-x64", nil
	case "windows/amd64":
		return "windows-x64", nil
	default:
		return "", fmt.Errorf("%w: %s/%s", ErrPlatformUnsupported, goos, goarch)
	}
}

// Relation classifies the target without authorizing downgrade installation.
func Relation(currentVersion, targetVersion string) (VersionRelation, error) {
	target, err := parseSemver(targetVersion)
	if err != nil {
		return "", fmt.Errorf("%w: target version: %v", ErrDescriptorInvalid, err)
	}
	if currentVersion == "" {
		return VersionInstall, nil
	}
	current, err := parseSemver(currentVersion)
	if err != nil {
		return "", fmt.Errorf("%w: current version: %v", ErrDescriptorInvalid, err)
	}
	switch compareSemver(target, current) {
	case 1:
		return VersionUpgrade, nil
	case 0:
		return VersionCurrent, nil
	default:
		return VersionDowngrade, nil
	}
}

// VerifyArtifact streams at most one byte beyond the trusted size before checking SHA-256.
func VerifyArtifact(reader io.Reader, artifact Artifact) error {
	if artifact.Size < 1 || artifact.Size > MaxArtifactSize || !digestPattern.MatchString(artifact.SHA256) {
		return fmt.Errorf("%w: invalid expectation", ErrArtifactMismatch)
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(reader, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrArtifactMismatch, err)
	}
	if written != artifact.Size {
		return fmt.Errorf("%w: size", ErrArtifactMismatch)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != artifact.SHA256 {
		return fmt.Errorf("%w: sha256", ErrArtifactMismatch)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrDescriptorInvalid)
		}
		return fmt.Errorf("%w: trailing bytes: %v", ErrDescriptorInvalid, err)
	}
	return nil
}

func validateDescriptor(descriptor Descriptor) error {
	if descriptor.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema version", ErrDescriptorInvalid)
	}
	if _, err := parseSemver(descriptor.HelperVersion); err != nil {
		return fmt.Errorf("%w: helper version: %v", ErrDescriptorInvalid, err)
	}
	if descriptor.ReleaseTag != "helper-v"+descriptor.HelperVersion {
		return fmt.Errorf("%w: release tag", ErrDescriptorInvalid)
	}
	publishedAt, err := time.Parse(time.RFC3339, descriptor.PublishedAt)
	if err != nil || publishedAt.UTC().Format(time.RFC3339) != descriptor.PublishedAt {
		return fmt.Errorf("%w: published timestamp", ErrDescriptorInvalid)
	}
	if !keyIDPattern.MatchString(descriptor.SigningKeyID) {
		return fmt.Errorf("%w: signing key id", ErrDescriptorInvalid)
	}
	if len(descriptor.Artifacts) != len(supportedPlatforms) {
		return fmt.Errorf("%w: artifact count", ErrDescriptorInvalid)
	}
	for index, artifact := range descriptor.Artifacts {
		expectedPlatform := supportedPlatforms[index]
		if artifact.Platform != expectedPlatform {
			return fmt.Errorf("%w: artifact platform order", ErrDescriptorInvalid)
		}
		if artifact.Filename != expectedFilename(descriptor.HelperVersion, expectedPlatform) {
			return fmt.Errorf("%w: artifact filename", ErrDescriptorInvalid)
		}
		if !digestPattern.MatchString(artifact.SHA256) {
			return fmt.Errorf("%w: artifact sha256", ErrDescriptorInvalid)
		}
		if artifact.Size < 1 || artifact.Size > MaxArtifactSize {
			return fmt.Errorf("%w: artifact size", ErrDescriptorInvalid)
		}
	}
	return nil
}

func expectedFilename(version, platform string) string {
	suffixes := map[string]string{
		"macos-arm64": "macos_arm64",
		"macos-x64":   "macos_x64",
		"windows-x64": "windows_x64.exe",
	}
	return "codex-skin-helper_" + version + "_" + suffixes[platform]
}

type semver struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
}

func parseSemver(value string) (semver, error) {
	matches := strictSemver.FindStringSubmatch(value)
	if matches == nil || len(value) > 64 {
		return semver{}, fmt.Errorf("not strict SemVer")
	}
	parts := make([]uint64, 3)
	for index := range parts {
		parsed, err := strconv.ParseUint(matches[index+1], 10, 64)
		if err != nil {
			return semver{}, fmt.Errorf("numeric component overflow")
		}
		parts[index] = parsed
	}
	var prerelease []string
	if matches[4] != "" {
		prerelease = strings.Split(matches[4], ".")
	}
	return semver{major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease}, nil
}

func compareSemver(left, right semver) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftPart, rightPart := left.prerelease[index], right.prerelease[index]
		if leftPart == rightPart {
			continue
		}
		leftNumber, leftNumeric := numericIdentifier(leftPart)
		rightNumber, rightNumeric := numericIdentifier(rightPart)
		if leftNumeric && rightNumeric {
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		}
		if leftNumeric {
			return -1
		}
		if rightNumeric {
			return 1
		}
		if leftPart < rightPart {
			return -1
		}
		return 1
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func numericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil
}
