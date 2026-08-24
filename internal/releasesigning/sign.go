package releasesigning

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

var ErrPrivateKey = errors.New("Helper release signing key is invalid")

func SignDescriptor(
	descriptor []byte,
	privateKeyPEMBase64 string,
	keyset releasecontract.VerificationKeyset,
) ([]byte, error) {
	pemBytes, err := base64.StdEncoding.Strict().DecodeString(privateKeyPEMBase64)
	if err != nil || len(pemBytes) == 0 || len(pemBytes) > 16*1024 {
		return nil, ErrPrivateKey
	}
	block, remainder := pem.Decode(pemBytes)
	if block == nil || block.Type != "PRIVATE KEY" || len(remainder) != 0 {
		return nil, ErrPrivateKey
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrPrivateKey
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrPrivateKey
	}
	signature := ed25519.Sign(privateKey, releasecontract.SigningMessage(descriptor))
	if _, err := releasecontract.VerifyWithKeyset(descriptor, signature, keyset); err != nil {
		return nil, fmt.Errorf("%w: signing key does not match the trusted descriptor key: %v", ErrPrivateKey, err)
	}
	return signature, nil
}
