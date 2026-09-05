//go:build windows

package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// Temporary bounded diagnostic: keep a known-working environment while removing
// groups. Negative trials are not acceptance; StartupMatrix still asserts the
// unchanged production path. Never print arbitrary environment keys or values.
func TestWindowsPowerShellEnvironmentReduction(t *testing.T) {
	root := os.Getenv("SystemRoot")
	directory := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0")
	executable := filepath.Join(directory, "powershell.exe")
	minimal := []string{"SystemRoot=" + root, "WINDIR=" + root,
		"PATH=" + directory + ";" + filepath.Join(root, "System32"),
		"TEMP=" + os.TempDir(), "TMP=" + os.TempDir()}
	base := map[string]string{}
	for _, entry := range minimal {
		key, value, _ := strings.Cut(entry, "=")
		base[strings.ToUpper(key)] = value
	}
	var remaining []string
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if existing, ok := base[strings.ToUpper(key)]; !ok || existing != value {
			remaining = append(remaining, entry)
		}
	}
	sort.Strings(remaining)
	budget, cancel := context.WithTimeout(context.Background(), 85*time.Second)
	defer cancel()
	trials := 0
	probe := func(additions []string) bool {
		ctx, cancel := context.WithTimeout(budget, 5*time.Second)
		defer cancel()
		var result struct {
			OK bool `json:"ok"`
		}
		err := runPowerShellCommandJSON(ctx, executable, append(append([]string{}, minimal...), additions...), `[Console]::Out.Write('{"ok":true}')`, nil, &result)
		passed := err == nil && ctx.Err() == nil && result.OK
		trials++
		t.Logf("[DEBUG-win-env] trial=%d variables=%d passed=%t", trials, len(additions), passed)
		return passed
	}
	if !probe(remaining) {
		t.Fatal("inherited control failed; environment cannot be reduced")
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
	// Only fixed, non-secret Windows/PowerShell variable names may be reported.
	known := map[string]bool{}
	for _, key := range strings.Fields("PATH USERPROFILE LOCALAPPDATA APPDATA PROGRAMFILES PROGRAMW6432 COMMONPROGRAMFILES COMMONPROGRAMW6432 PROGRAMDATA ALLUSERSPROFILE COMSPEC HOMEDRIVE HOMEPATH PSMODULEPATH PSMODULEANALYSISCACHEPATH PSEXECUTIONPOLICYPREFERENCE PSHOME PROCESSOR_ARCHITECTURE PROCESSOR_IDENTIFIER PROCESSOR_LEVEL PROCESSOR_REVISION NUMBER_OF_PROCESSORS OS COMPUTERNAME USERNAME USERDOMAIN USERDNSDOMAIN SYSTEMROOT WINDIR TEMP TMP") {
		known[key] = true
	}
	unknown := 0
	for _, entry := range remaining {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if known[key] {
			t.Logf("[DEBUG-win-env] remaining known variable=%s", key)
		} else {
			unknown++
		}
	}
	t.Logf("[DEBUG-win-env] remaining=%d unclassified=%d budget_expired=%t", len(remaining), unknown, budget.Err() != nil)
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
