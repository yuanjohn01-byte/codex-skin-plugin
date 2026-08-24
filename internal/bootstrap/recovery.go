package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

type recoverySnapshot struct {
	path    string
	existed bool
	content []byte
}

type recoveryTransaction struct {
	binary        string
	entry         string
	binaryBytes   []byte
	entryBytes    []byte
	oldBinary     recoverySnapshot
	oldEntry      recoverySnapshot
	activatedBin  bool
	activatedAll  bool
	directorySync func(string) error
}

func installRecovery(root, helper, platform string) (string, error) {
	transaction, err := newRecoveryTransaction(root, helper, platform, "", syncDirectory)
	if err != nil {
		return "", err
	}
	if err := transaction.activate(); err != nil {
		return "", err
	}
	transaction.commit()
	return transaction.entry, nil
}

func installRecoveryVerified(root, helper, platform, expectedSHA256 string) (string, error) {
	transaction, err := newRecoveryTransaction(root, helper, platform, expectedSHA256, syncDirectory)
	if err != nil {
		return "", err
	}
	if err := transaction.activate(); err != nil {
		return "", err
	}
	transaction.commit()
	return transaction.entry, nil
}

func newRecoveryTransaction(root, helper, platform, expectedSHA256 string, directorySync func(string) error) (*recoveryTransaction, error) {
	recoveryRoot := filepath.Join(root, "recovery")
	engineRoot := filepath.Join(recoveryRoot, "engine")
	if err := ensureSecureDirectory(recoveryRoot, 0o700); err != nil {
		return nil, err
	}
	if err := ensureSecureDirectory(engineRoot, 0o700); err != nil {
		return nil, err
	}
	binaryName := "codex-skin"
	entryName := "restore.command"
	entryContent := []byte("#!/bin/sh\nset -eu\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/engine/codex-skin\" theme restore\n")
	if platform == "windows-x64" {
		binaryName = "codex-skin.exe"
		entryName = "restore.cmd"
		entryContent = []byte("@echo off\r\nsetlocal\r\n\"%~dp0engine\\codex-skin.exe\" theme restore\r\n")
	}

	helperBytes, err := readRegularFile(helper, releasecontract.MaxArtifactSize)
	if err != nil {
		return nil, fmt.Errorf("%w: recovery source", ErrUnsafePath)
	}
	digest := sha256.Sum256(helperBytes)
	actualSHA256 := hex.EncodeToString(digest[:])
	if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
		return nil, fmt.Errorf("%w: recovery source digest changed", releasecontract.ErrArtifactMismatch)
	}
	binary := filepath.Join(engineRoot, binaryName)
	entry := filepath.Join(recoveryRoot, entryName)
	oldBinary, err := captureRecoveryFile(binary, releasecontract.MaxArtifactSize)
	if err != nil {
		return nil, err
	}
	oldEntry, err := captureRecoveryFile(entry, 16*1024)
	if err != nil {
		return nil, err
	}
	return &recoveryTransaction{
		binary: binary, entry: entry, binaryBytes: helperBytes, entryBytes: entryContent,
		oldBinary: oldBinary, oldEntry: oldEntry, directorySync: directorySync,
	}, nil
}

func (transaction *recoveryTransaction) activate() error {
	replaced, err := writeRecoveryFile(transaction.binary, transaction.binaryBytes, transaction.directorySync)
	transaction.activatedBin = replaced
	if err != nil {
		if replaced {
			if rollbackErr := transaction.rollback(); rollbackErr != nil {
				return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
		}
		return err
	}
	replaced, err = writeRecoveryFile(transaction.entry, transaction.entryBytes, transaction.directorySync)
	transaction.activatedAll = replaced
	if err != nil {
		if rollbackErr := transaction.rollback(); rollbackErr != nil {
			return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return err
	}
	return nil
}

func (transaction *recoveryTransaction) rollback() error {
	var failures []error
	if transaction.activatedAll {
		if err := restoreRecoveryFile(transaction.oldEntry, transaction.directorySync); err != nil {
			failures = append(failures, err)
		}
	}
	if transaction.activatedBin {
		if err := restoreRecoveryFile(transaction.oldBinary, transaction.directorySync); err != nil {
			failures = append(failures, err)
		}
	}
	transaction.activatedAll = false
	transaction.activatedBin = false
	return errors.Join(failures...)
}

func (transaction *recoveryTransaction) commit() {
	transaction.oldBinary.content = nil
	transaction.oldEntry.content = nil
	transaction.activatedAll = false
	transaction.activatedBin = false
}

func captureRecoveryFile(path string, maxBytes int64) (recoverySnapshot, error) {
	if err := rejectSymlinkIfPresent(path); err != nil {
		return recoverySnapshot{}, err
	}
	content, err := readRegularFile(path, maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return recoverySnapshot{path: path}, nil
	}
	if err != nil {
		return recoverySnapshot{}, fmt.Errorf("%w: inspect existing recovery file", ErrUnsafePath)
	}
	return recoverySnapshot{path: path, existed: true, content: content}, nil
}

func restoreRecoveryFile(snapshot recoverySnapshot, directorySync func(string) error) error {
	if snapshot.existed {
		_, err := writeRecoveryFile(snapshot.path, snapshot.content, directorySync)
		return err
	}
	if err := rejectSymlinkIfPresent(snapshot.path); err != nil {
		return err
	}
	if err := os.Remove(snapshot.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: remove recovery file during rollback", ErrUnsafePath)
	}
	return directorySync(filepath.Dir(snapshot.path))
}

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	if err := rejectSymlinkIfPresent(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: file is not a bounded regular file", ErrUnsafePath)
	}
	content, err := os.ReadFile(path)
	if err != nil || int64(len(content)) != info.Size() {
		return nil, fmt.Errorf("%w: read bounded regular file", ErrUnsafePath)
	}
	return content, nil
}

func writeRecoveryFile(destination string, content []byte, directorySync func(string) error) (bool, error) {
	if err := rejectSymlinkIfPresent(destination); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".recovery-")
	if err != nil {
		return false, fmt.Errorf("%w: create recovery file", ErrUnsafePath)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o700); err != nil {
		return false, fmt.Errorf("%w: recovery file permissions", ErrUnsafePath)
	}
	if _, err := temporary.Write(content); err != nil {
		return false, fmt.Errorf("%w: write recovery file", ErrUnsafePath)
	}
	if err := temporary.Sync(); err != nil {
		return false, fmt.Errorf("%w: sync recovery file", ErrUnsafePath)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("%w: close recovery file", ErrUnsafePath)
	}
	closed = true
	if err := atomicReplace(temporaryPath, destination); err != nil {
		return false, fmt.Errorf("%w: replace recovery file", ErrUnsafePath)
	}
	if err := directorySync(filepath.Dir(destination)); err != nil {
		return true, fmt.Errorf("%w: persist recovery file: %v", ErrUnsafePath, err)
	}
	return true, nil
}
