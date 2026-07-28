//go:build windows

package browseropen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func openPlatform(ctx context.Context, value string) error {
	windowsDirectory := os.Getenv("WINDIR")
	if windowsDirectory == "" {
		return fmt.Errorf("WINDIR is unavailable")
	}
	executable := filepath.Join(windowsDirectory, "System32", "rundll32.exe")
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("trusted browser launcher is unavailable")
	}
	return exec.CommandContext(ctx, executable, "url.dll,FileProtocolHandler", value).Run()
}
