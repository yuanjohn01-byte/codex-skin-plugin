//go:build windows

package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func init() {
	nativePowerShellJSONForTest = runPowerShellJSON
}

// These probes execute only a fixed synthetic JSON response, never app
// discovery/activation. Log stage/timing only, not process paths or environment.
func TestWindowsPowerShellStartupMatrix(t *testing.T) {
	root := os.Getenv("SystemRoot")
	directory := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0")
	executable := filepath.Join(directory, "powershell.exe")
	minimal := []string{
		"SystemRoot=" + root, "WINDIR=" + root,
		"PATH=" + directory + ";" + filepath.Join(root, "System32"),
		"TEMP=" + os.TempDir(), "TMP=" + os.TempDir(),
	}
	for _, test := range []struct {
		name    string
		encoded bool
		minimal bool
		keys    []string
	}{
		{name: "plain_inherited"},
		{name: "plain_minimal", minimal: true},
		{name: "transport_inherited", encoded: true},
		{name: "transport_production", encoded: true, minimal: true},
		{name: "transport_userprofile", encoded: true, minimal: true, keys: []string{"USERPROFILE"}},
		{name: "transport_localappdata", encoded: true, minimal: true, keys: []string{"LOCALAPPDATA"}},
		{name: "transport_appdata", encoded: true, minimal: true, keys: []string{"APPDATA"}},
		{name: "transport_programfiles", encoded: true, minimal: true, keys: []string{"ProgramFiles"}},
		{name: "transport_programdata", encoded: true, minimal: true, keys: []string{"ProgramData"}},
		{name: "transport_comspec", encoded: true, minimal: true, keys: []string{"ComSpec"}},
		{name: "transport_windows_directories", encoded: true, minimal: true, keys: []string{"USERPROFILE", "LOCALAPPDATA", "APPDATA", "ProgramFiles", "ProgramW6432", "CommonProgramFiles", "CommonProgramW6432", "ProgramData", "ALLUSERSPROFILE", "ComSpec", "HOMEDRIVE", "HOMEPATH"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			started := time.Now()
			var result struct {
				OK bool `json:"ok"`
			}
			environment := os.Environ()
			if test.minimal {
				environment = minimal
			}
			if len(test.keys) > 0 {
				environment = append([]string{}, minimal...)
				for _, key := range test.keys {
					value, exists := os.LookupEnv(key)
					if !exists || value == "" {
						t.Fatal("required diagnostic directory variable is unavailable")
					}
					environment = append(environment, key+"="+value)
				}
			}
			const script = `[Console]::Out.Write('{"ok":true}')`
			var err error
			if test.encoded && test.minimal && len(test.keys) == 0 {
				err = runPowerShellJSON(ctx, script, nil, &result)
			} else if test.encoded {
				err = runPowerShellCommandJSON(ctx, executable, environment, script, nil, &result)
			} else {
				command := exec.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
				command.Env = environment
				command.WaitDelay = time.Second
				var output []byte
				output, err = command.Output()
				if err == nil {
					err = json.Unmarshal(output, &result)
				}
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
