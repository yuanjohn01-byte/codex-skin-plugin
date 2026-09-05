//go:build windows

package codex

import (
	"context"
	"testing"
	"time"
)

func init() {
	nativePowerShellJSONForTest = runPowerShellJSON
}

func TestWindowsPowerShellSystemRunner(t *testing.T) {
	t.Setenv("CODEX_SKIN_UNRELATED_SECRET", "must-not-reach-probe")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result struct {
		Major    int    `json:"major"`
		Value    string `json:"value"`
		Compiled int    `json:"compiled"`
		Isolated bool   `json:"isolated"`
	}
	const script = `
Add-Type -TypeDefinition 'public static class CodexSkinSystemFixture { public static int Value() { return 42; } }'
[pscustomobject]@{
  major = $PSVersionTable.PSVersion.Major
  value = $args[0]
  compiled = [CodexSkinSystemFixture]::Value()
  isolated = ($null -eq $env:CODEX_SKIN_UNRELATED_SECRET)
} | ConvertTo-Json -Compress
`
	const value = `C:\Users\测试 User\Codex`
	err := runPowerShellJSON(ctx, script, []string{value}, &result)
	if err != nil || result.Major != 5 || result.Value != value || result.Compiled != 42 || !result.Isolated {
		t.Fatalf("production Windows PowerShell runner = %#v, %v", result, err)
	}
}
