//go:build windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func init() {
	nativePowerShellJSONForTest = runPowerShellJSON
}

// Exercise both shipped PowerShell transport entry points with fixed JSON only.
// Raw -Command / uninitialized module-search comparisons were diagnosis probes,
// not supported launch paths; keeping them here gates releases on unused behavior.
func TestWindowsPowerShellStartupMatrix(t *testing.T) {
	root := os.Getenv("SystemRoot")
	directory := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0")
	executable := filepath.Join(directory, "powershell.exe")
	for _, test := range []struct {
		name       string
		production bool
	}{
		{name: "transport_inherited"},
		{name: "transport_production", production: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			started := time.Now()
			var result struct {
				OK bool `json:"ok"`
			}
			const script = `[Console]::Out.Write('{"ok":true}')`
			var err error
			if test.production {
				err = runPowerShellJSON(ctx, script, nil, &result)
			} else {
				err = runPowerShellCommandJSON(ctx, executable, os.Environ(), script, nil, &result)
			}
			t.Logf("startup elapsed=%s deadline=%t command_error=%t", time.Since(started).Round(time.Millisecond), ctx.Err() != nil, err != nil)
			if ctx.Err() != nil || err != nil || !result.OK {
				t.Fatal("fixed-response PowerShell startup probe failed")
			}
		})
	}
}

func TestWindowsPowerShellSystemRunner(t *testing.T) {
	t.Setenv("CODEX_SKIN_UNRELATED_SECRET", "must-not-reach-probe")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result struct {
		Major         int    `json:"major"`
		Value         string `json:"value"`
		Compiled      int    `json:"compiled"`
		Isolated      bool   `json:"isolated"`
		SystemModules bool   `json:"systemModules"`
	}
	const script = `
Add-Type -TypeDefinition 'public static class CodexSkinSystemFixture { public static int Value() { return 42; } }'
[pscustomobject]@{
  major = $PSVersionTable.PSVersion.Major
  value = $args[0]
  compiled = [CodexSkinSystemFixture]::Value()
  isolated = ($null -eq $env:CODEX_SKIN_UNRELATED_SECRET)
  systemModules = ($env:PSModulePath -ceq ($PSHOME + '\Modules'))
} | ConvertTo-Json -Compress
`
	const value = `C:\Users\测试 User\Codex`
	err := runPowerShellJSON(ctx, script, []string{value}, &result)
	if err != nil || result.Major != 5 || result.Value != value || result.Compiled != 42 || !result.Isolated || !result.SystemModules {
		t.Fatalf("production Windows PowerShell runner = %#v, %v", result, err)
	}
}

func TestWindowsPowerShellIgnoresInheritedModuleSearch(t *testing.T) {
	t.Setenv("PSModulePath", t.TempDir())
	root := os.Getenv("SystemRoot")
	executable := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result struct {
		OK bool `json:"ok"`
	}
	err := runPowerShellCommandJSON(ctx, executable, os.Environ(), `[pscustomobject]@{ ok = ($env:PSModulePath -ceq ($PSHOME + '\Modules')) } | ConvertTo-Json -Compress`, nil, &result)
	if err != nil || ctx.Err() != nil || !result.OK {
		t.Fatal("Windows probe did not restrict module discovery to the system PowerShell directory")
	}
}
