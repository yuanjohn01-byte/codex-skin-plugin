//go:build windows

package restartflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

func StartWorker(executable, requestID string) error {
	if !requestIDPattern.MatchString(requestID) {
		return ErrUnsafe
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafe
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return ErrUnsafe
	}
	defer devNull.Close()
	systemRoot := os.Getenv("SystemRoot")
	command := exec.Command(executable, "__restart-worker", requestID)
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = devNull
	command.Env = []string{
		"SystemRoot=" + systemRoot,
		"WINDIR=" + systemRoot,
		"PATH=" + filepath.Join(systemRoot, "System32"),
		"LOCALAPPDATA=" + os.Getenv("LOCALAPPDATA"),
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		return ErrUnsafe
	}
	return command.Process.Release()
}
