//go:build windows

package codex

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestPowerShellProcessCreationFlags(t *testing.T) {
	command := exec.Command("unused-fixture.exe")
	configurePowerShellProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags != 0x08000000 {
		t.Fatal("PowerShell must use CREATE_NO_WINDOW without detached/new-console flags")
	}
}

// Check the actual production process boundary, not only the attribute helper.
// This launches only a fixed read-only fixture in system Windows PowerShell 5.1.
func TestWindowsPowerShellRunsWithoutConsole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const script = `
Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class CodexSkinConsoleFixture { [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow(); }'
[pscustomobject]@{ noConsole = ([CodexSkinConsoleFixture]::GetConsoleWindow() -eq [IntPtr]::Zero); value = $args[0] } | ConvertTo-Json -Compress
`
	var result struct {
		NoConsole bool   `json:"noConsole"`
		Value     string `json:"value"`
	}
	err := runPowerShellJSON(ctx, script, []string{"fixture 中文 space"}, &result)
	if err != nil || !result.NoConsole || result.Value != "fixture 中文 space" {
		t.Fatalf("headless system PowerShell: %+v, %v", result, err)
	}
}
