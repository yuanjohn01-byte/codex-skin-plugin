//go:build !windows

package codex

import "os/exec"

func configurePowerShellProcess(command *exec.Cmd) {}
