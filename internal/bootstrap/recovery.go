package bootstrap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

func installRecovery(root, helper, platform string) (string, error) {
	recoveryRoot := filepath.Join(root, "recovery")
	engineRoot := filepath.Join(recoveryRoot, "engine")
	if err := ensureSecureDirectory(recoveryRoot, 0o700); err != nil {
		return "", err
	}
	if err := ensureSecureDirectory(engineRoot, 0o700); err != nil {
		return "", err
	}
	binaryName := "codex-skin"
	entryName := "restore.command"
	entryContent := []byte("#!/bin/sh\nset -eu\nSCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$SCRIPT_DIR/engine/codex-skin\" theme restore\n")
	if platform == "windows-x64" {
		binaryName = "codex-skin.exe"
		entryName = "restore.cmd"
		entryContent = []byte("@echo off\r\nsetlocal\r\n\"%~dp0engine\\codex-skin.exe\" theme restore\r\n")
	}
	recoveryBinary := filepath.Join(engineRoot, binaryName)
	if err := copyExecutableAtomic(helper, recoveryBinary); err != nil {
		return "", err
	}
	entry := filepath.Join(recoveryRoot, entryName)
	if err := writeRecoveryEntry(entry, entryContent); err != nil {
		return "", err
	}
	return entry, nil
}

func copyExecutableAtomic(source, destination string) error {
	if err := rejectSymlinkIfPresent(source); err != nil {
		return err
	}
	if err := rejectSymlinkIfPresent(destination); err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() < 1 || sourceInfo.Size() > releasecontract.MaxArtifactSize {
		return fmt.Errorf("%w: recovery source", ErrUnsafePath)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("%w: open recovery source", ErrUnsafePath)
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".recovery-bin-")
	if err != nil {
		return fmt.Errorf("%w: create recovery binary", ErrUnsafePath)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o700); err != nil {
		return fmt.Errorf("%w: recovery binary permissions", ErrUnsafePath)
	}
	written, err := io.Copy(temporary, io.LimitReader(input, releasecontract.MaxArtifactSize+1))
	if err != nil || written != sourceInfo.Size() {
		return fmt.Errorf("%w: copy recovery binary", ErrUnsafePath)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync recovery binary", ErrUnsafePath)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close recovery binary", ErrUnsafePath)
	}
	if err := atomicReplace(temporaryPath, destination); err != nil {
		return fmt.Errorf("%w: replace recovery binary", ErrUnsafePath)
	}
	cleanup = false
	return syncDirectory(filepath.Dir(destination))
}

func writeRecoveryEntry(destination string, content []byte) error {
	if err := rejectSymlinkIfPresent(destination); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".recovery-entry-")
	if err != nil {
		return fmt.Errorf("%w: create recovery entry", ErrUnsafePath)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o700); err != nil {
		return fmt.Errorf("%w: recovery entry permissions", ErrUnsafePath)
	}
	if _, err := temporary.Write(content); err != nil {
		return fmt.Errorf("%w: write recovery entry", ErrUnsafePath)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync recovery entry", ErrUnsafePath)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close recovery entry", ErrUnsafePath)
	}
	if err := atomicReplace(temporaryPath, destination); err != nil {
		return fmt.Errorf("%w: replace recovery entry", ErrUnsafePath)
	}
	cleanup = false
	return syncDirectory(filepath.Dir(destination))
}
