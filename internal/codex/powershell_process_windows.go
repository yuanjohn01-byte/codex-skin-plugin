//go:build windows

package codex

import (
	"os/exec"
	"syscall"
)

func configurePowerShellProcess(command *exec.Cmd) {
	// The probe is a console program, but needs only our redirected pipes.
	// Do not combine CREATE_NO_WINDOW with DETACHED_PROCESS/CREATE_NEW_CONSOLE.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
