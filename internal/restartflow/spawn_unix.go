//go:build darwin

package restartflow

import (
	"os"
	"os/exec"
	"syscall"
)

func StartWorker(executable, requestID string) error {
	if !requestIDPattern.MatchString(requestID) {
		return ErrUnsafe
	}
	return startDetached(executable, "__restart-worker", requestID)
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
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ErrUnsafe
	}
	command := exec.Command(executable, arguments...)
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = devNull
	command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return ErrUnsafe
	}
	return command.Process.Release()
}
