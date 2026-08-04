package release

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

//go:embed trusted-verification-keys-v1.json
var trustedVerificationKeys []byte

type VerificationKey struct {
	KeyID           string `json:"keyId"`
	Algorithm       string `json:"algorithm"`
	Usage           string `json:"usage"`
	PublicKeyBase64 string `json:"publicKeyBase64"`
	NotBefore       string `json:"notBefore"`
	NotAfter        string `json:"notAfter"`
	Status          string `json:"status"`
	publicKey       []byte
	parsedNotBefore time.Time
	parsedNotAfter  time.Time
}

type VerificationKeyset struct {
	SchemaVersion int               `json:"schemaVersion"`
	Keys          []VerificationKey `json:"keys"`
}

func TrustedVerificationKeyset() (VerificationKeyset, error) {
	return ParseVerificationKeyset(trustedVerificationKeys)
}

func ParseVerificationKeyset(raw []byte) (VerificationKeyset, error) {
	if len(raw) == 0 || len(raw) > MaxDescriptor {
		return VerificationKeyset{}, fmt.Errorf("%w: verification keyset byte length", ErrDescriptorInvalid)
	}
	var keyset VerificationKeyset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&keyset); err != nil {
		return VerificationKeyset{}, fmt.Errorf("%w: verification keyset decode: %v", ErrDescriptorInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errorsIsEOF(err) {
		return VerificationKeyset{}, fmt.Errorf("%w: verification keyset trailing data", ErrDescriptorInvalid)
	}
	if keyset.SchemaVersion != 1 || len(keyset.Keys) < 1 || len(keyset.Keys) > 8 {
		return VerificationKeyset{}, fmt.Errorf("%w: verification keyset shape", ErrDescriptorInvalid)
	}
	seen := make(map[string]bool, len(keyset.Keys))
	active := 0
	for index := range keyset.Keys {
		key := &keyset.Keys[index]
		if !keyIDPattern.MatchString(key.KeyID) || seen[key.KeyID] ||
			key.Algorithm != "Ed25519" || key.Usage != "helper-release" {
			return VerificationKeyset{}, fmt.Errorf("%w: verification key identity", ErrDescriptorInvalid)
		}
		seen[key.KeyID] = true
		decoded, err := base64.StdEncoding.DecodeString(key.PublicKeyBase64)
		if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != key.PublicKeyBase64 {
			return VerificationKeyset{}, fmt.Errorf("%w: verification public key", ErrDescriptorInvalid)
		}
		notBefore, err := time.Parse(time.RFC3339Nano, key.NotBefore)
		if err != nil || notBefore.UTC().Format(time.RFC3339Nano) != key.NotBefore {
			return VerificationKeyset{}, fmt.Errorf("%w: verification key notBefore", ErrDescriptorInvalid)
		}
		notAfter, err := time.Parse(time.RFC3339Nano, key.NotAfter)
		if err != nil || notAfter.UTC().Format(time.RFC3339Nano) != key.NotAfter || !notBefore.Before(notAfter) {
			return VerificationKeyset{}, fmt.Errorf("%w: verification key notAfter", ErrDescriptorInvalid)
		}
		if key.Status != "active" && key.Status != "verify-only" && key.Status != "revoked" {
			return VerificationKeyset{}, fmt.Errorf("%w: verification key status", ErrDescriptorInvalid)
		}
		if key.Status == "active" {
			active++
		}
		key.publicKey = append([]byte(nil), decoded...)
		key.parsedNotBefore = notBefore
		key.parsedNotAfter = notAfter
	}
	if active == 0 {
		return VerificationKeyset{}, fmt.Errorf("%w: no active verification key", ErrDescriptorInvalid)
	}
	return keyset, nil
}

func (keyset VerificationKeyset) publicKeyFor(descriptor Descriptor) ([]byte, bool) {
	publishedAt, err := time.Parse(time.RFC3339, descriptor.PublishedAt)
	if err != nil {
		return nil, false
	}
	for _, key := range keyset.Keys {
		if key.KeyID == descriptor.SigningKeyID && key.Status != "revoked" &&
			!publishedAt.Before(key.parsedNotBefore) && publishedAt.Before(key.parsedNotAfter) {
			return append([]byte(nil), key.publicKey...), true
		}
	}
	return nil, false
}

func errorsIsEOF(err error) bool {
	return err == io.EOF
}
