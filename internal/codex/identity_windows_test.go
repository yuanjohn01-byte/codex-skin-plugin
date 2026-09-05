//go:build windows

package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	}{
		{name: "plain_inherited"},
		{name: "plain_minimal", minimal: true},
		{name: "transport_inherited", encoded: true},
		{name: "transport_production", encoded: true, minimal: true},
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
			const script = `[Console]::Out.Write('{"ok":true}')`
			var err error
			if test.encoded && test.minimal {
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

// Temporary fixed-data probes for the module search seam. No paths are logged.
func TestWindowsPowerShellEnvironmentReduction(t *testing.T) {
	root := os.Getenv("SystemRoot")
	directory := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0")
	executable := filepath.Join(directory, "powershell.exe")
	minimal := []string{"SystemRoot=" + root, "WINDIR=" + root,
		"PATH=" + directory + ";" + filepath.Join(root, "System32"),
		"TEMP=" + os.TempDir(), "TMP=" + os.TempDir()}
	systemModules := filepath.Join(directory, "Modules")
	// Each trial changes a single feature relative to an observed failing seam.
	for _, test := range []struct {
		name, prelude string
		additions     []string
	}{
		{name: "module_path_and_programfiles", additions: []string{"PSModulePath=" + systemModules, "ProgramFiles=" + os.Getenv("ProgramFiles")}},
		{name: "module_path_and_profile", additions: []string{"PSModulePath=" + systemModules, "USERPROFILE=" + os.Getenv("USERPROFILE"), "LOCALAPPDATA=" + os.Getenv("LOCALAPPDATA"), "APPDATA=" + os.Getenv("APPDATA")}},
		{name: "reset_module_search_inside_process", prelude: "$env:PSModulePath = $PSHOME + '\\Modules'; "},
		{name: "explicit_utility_import", prelude: "Import-Module Microsoft.PowerShell.Utility -ErrorAction Stop; "},
		{name: "qualified_utility", prelude: "$value = Microsoft.PowerShell.Utility\\ConvertFrom-Json -InputObject '[]'; "},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var result struct {
				OK bool `json:"ok"`
			}
			var err error
			if len(test.additions) > 0 {
				err = runPowerShellCommandJSON(ctx, executable, append(append([]string{}, minimal...), test.additions...), `[Console]::Out.Write('{"ok":true}')`, nil, &result)
			} else {
				script := "$ErrorActionPreference = 'Stop'; " + test.prelude + `[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); $value = ConvertFrom-Json -InputObject '[]'; [Console]::Out.Write('{"ok":true}')`
				command := exec.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
				command.Env = minimal
				command.WaitDelay = time.Second
				var output []byte
				output, err = command.Output()
				if err == nil {
					err = json.Unmarshal(output, &result)
				}
			}
			t.Logf("[DEBUG-win-env] passed=%t deadline=%t", err == nil && result.OK && ctx.Err() == nil, ctx.Err() != nil)
		})
	}
	remaining := filepath.SplitList(os.Getenv("PSModulePath"))
	if len(remaining) == 0 {
		t.Fatal("inherited module control unavailable")
	}
	budget, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	probe := func(paths []string) bool {
		ctx, cancel := context.WithTimeout(budget, 5*time.Second)
		defer cancel()
		var result struct {
			OK bool `json:"ok"`
		}
		env := append(append([]string{}, minimal...), "PSModulePath="+strings.Join(paths, ";"))
		err := runPowerShellCommandJSON(ctx, executable, env, `[Console]::Out.Write('{"ok":true}')`, nil, &result)
		passed := err == nil && ctx.Err() == nil && result.OK
		t.Logf("[DEBUG-win-env] module_entries=%d passed=%t", len(paths), passed)
		return passed
	}
	if !probe(remaining) {
		t.Fatal("inherited module control failed")
	}
	groups := 2
	for len(remaining) > 0 && budget.Err() == nil {
		width := (len(remaining) + groups - 1) / groups
		reduced := false
		for start := 0; start < len(remaining) && budget.Err() == nil; start += width {
			end := min(start+width, len(remaining))
			candidate := append(append([]string{}, remaining[:start]...), remaining[end:]...)
			if probe(candidate) {
				remaining = candidate
				groups = max(2, groups-1)
				reduced = true
				break
			}
		}
		if !reduced {
			if groups >= len(remaining) {
				break
			}
			groups = min(len(remaining), groups*2)
		}
	}
	for _, entry := range remaining {
		kind := "unclassified"
		for name, path := range map[string]string{
			"system-windows-powershell":       systemModules,
			"programfiles-windows-powershell": filepath.Join(os.Getenv("ProgramFiles"), "WindowsPowerShell", "Modules"),
			"programfiles-powershell-7":       filepath.Join(os.Getenv("ProgramFiles"), "PowerShell", "7", "Modules"),
			"programfiles-powershell":         filepath.Join(os.Getenv("ProgramFiles"), "PowerShell", "Modules"),
		} {
			if strings.EqualFold(filepath.Clean(entry), filepath.Clean(path)) {
				kind = name
			}
		}
		t.Logf("[DEBUG-win-env] remaining module class=%s", kind)
	}
	t.Logf("[DEBUG-win-env] remaining=%d budget_expired=%t", len(remaining), budget.Err() != nil)
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
