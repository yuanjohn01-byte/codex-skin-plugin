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
	return startDetached(executable, "__restart-worker", requestID)
}

// StartSession starts the controller for one already-approved, controlled
// Codex session. It never creates a login item or a persistent OS service.
func StartSession(executable, sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return ErrUnsafe
	}
	return startDetached(executable, "__theme-session", sessionID)
}

func startDetached(executable string, arguments ...string) error {
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
	localAppData := os.Getenv("LOCALAPPDATA")
	userProfile := os.Getenv("USERPROFILE")
	if !filepath.IsAbs(systemRoot) || !filepath.IsAbs(localAppData) || !filepath.IsAbs(userProfile) {
		return ErrUnsafe
	}
	command := exec.Command(executable, arguments...)
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = devNull
	command.Env = []string{
		"SystemRoot=" + systemRoot,
		"WINDIR=" + systemRoot,
		"PATH=" + filepath.Join(systemRoot, "System32"),
		"LOCALAPPDATA=" + localAppData,
		"USERPROFILE=" + userProfile,
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
