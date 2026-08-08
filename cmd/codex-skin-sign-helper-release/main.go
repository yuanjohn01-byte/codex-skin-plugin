package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/releasesigning"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Helper release signing failed:", err)
		os.Exit(1)
	}
}

func run() error {
	descriptorPath := flag.String("descriptor", "dist/helper/release-descriptor.json", "canonical Helper release descriptor")
	signaturePath := flag.String("signature", "dist/helper/helper-release-descriptor.sig", "raw detached signature output")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	descriptor, err := os.ReadFile(*descriptorPath)
	if err != nil || len(descriptor) == 0 || len(descriptor) > releasecontract.MaxDescriptor {
		return errors.New("descriptor is unavailable or oversized")
	}
	keyset, err := releasecontract.TrustedVerificationKeyset()
	if err != nil {
		return errors.New("trusted Helper release verification keyset is invalid")
	}
	secret := os.Getenv("CODEX_SKIN_HELPER_RELEASE_PRIVATE_KEY_PEM_B64")
	if secret == "" {
		return errors.New("protected signing key is unavailable")
	}
	signature, err := releasesigning.SignDescriptor(descriptor, secret, keyset)
	if err != nil {
		return errors.New("protected signing key did not match the trusted release key")
	}
	if err := writeAtomic(*signaturePath, signature); err != nil {
		return err
	}
	return nil
}

func writeAtomic(destination string, content []byte) error {
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errors.New("could not create signature output directory")
	}
	temporary, err := os.CreateTemp(directory, ".helper-release-signature-")
	if err != nil {
		return errors.New("could not create signature output")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("could not secure signature output")
	}
	if _, err := temporary.Write(content); err != nil {
		return errors.New("could not write signature output")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("could not sync signature output")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("could not close signature output")
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return errors.New("could not activate signature output")
	}
	committed = true
	return nil
}
