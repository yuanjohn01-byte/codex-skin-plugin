package codex

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Embed the production source, not a copy of its scripts. The Windows test
// binary can also run without a checkout, Go, or an installed desktop app.
//
//go:embed identity_windows.go
var windowsIdentitySource string

// Bound to the product entry by identity_windows_test.go. Portable checks use
// PS 7 only as an additional fast loop, never as native Windows evidence.
var nativePowerShellJSONForTest func(context.Context, string, []string, any) error

func windowsIdentityScript(t *testing.T, function string) string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "identity_windows.go", windowsIdentitySource, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		for _, statement := range fn.Body.List {
			decl, ok := statement.(*ast.DeclStmt)
			if !ok {
				continue
			}
			general, ok := decl.Decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "script" || len(value.Values) != 1 {
					continue
				}
				literal, ok := value.Values[0].(*ast.BasicLit)
				if !ok {
					t.Fatal("production script is not a literal")
				}
				script, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				return script
			}
		}
	}
	t.Fatalf("no production script in %s", function)
	return ""
}

func testPowerShell(t *testing.T) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Never let pwsh mask incompatibility with the Windows PowerShell 5.1
		// executable used by the product, even when the test shell is PS 7.
		root := os.Getenv("SystemRoot")
		executable := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if !filepath.IsAbs(executable) {
			t.Fatal("Windows PowerShell system path is unavailable")
		}
		if _, err := os.Stat(executable); err != nil {
			t.Fatal(err)
		}
		return executable, os.Environ()
	}
	executable := os.Getenv("CODEX_SKIN_TEST_PWSH")
	if executable == "" {
		var err error
		executable, err = exec.LookPath("pwsh")
		if err != nil {
			t.Skip("optional portable PowerShell check; native Windows tests remain required")
		}
	}
	return executable, append(os.Environ(), "POWERSHELL_TELEMETRY_OPTOUT=1", "POWERSHELL_UPDATECHECK=Off")
}

func runTestPowerShellJSON(t *testing.T, script string, args []string, target any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		if nativePowerShellJSONForTest == nil {
			t.Fatal("native Windows test runner is not bound to production")
		}
		return nativePowerShellJSONForTest(ctx, script, args, target)
	}
	executable, environment := testPowerShell(t)
	return runPowerShellCommandJSON(ctx, executable, environment, script, args, target)
}

func TestWindowsPowerShellArgumentRoundTrip(t *testing.T) {
	for index, args := range [][]string{
		{}, {"one"}, {"one", "two"},
		{"", "", "last"},
		{`OpenAI.Codex_fixture!App`, `--remote-debugging-address=127.0.0.1 --remote-debugging-port=43127 --user-data-dir="C:\Users\测试 User\Codex" --no-first-run`},
		{`C:\User's folder\`, `double"quote`, "$null", "$(throw 'argument was executed')", "a;b|c&d", "`tick", "line\r\nbreak", "中文 🎨"},
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			var result struct {
				Arguments []string `json:"arguments"`
			}
			err := runTestPowerShellJSON(t, `[pscustomobject]@{ arguments = @($args) } | ConvertTo-Json -Compress`, args, &result)
			if err != nil || !reflect.DeepEqual(result.Arguments, args) {
				t.Fatalf("argument round trip = %#v, %v; want %#v", result.Arguments, err, args)
			}
		})
	}
}

func TestWindowsPowerShellLegacyJSONArrayBinding(t *testing.T) {
	// PS 5.1 emits a parsed array as one pipeline object. PS 7 enumerates
	// it by default. Its official -NoEnumerate option reproduces the legacy
	// behavior so portable tests cannot hide a Windows-only binding defect.
	const legacyConversion = `
function ConvertFrom-Json {
  param([string]$InputObject)
  if ($PSVersionTable.PSVersion.Major -ge 6) {
    Microsoft.PowerShell.Utility\ConvertFrom-Json -InputObject $InputObject -NoEnumerate
  } else {
    Microsoft.PowerShell.Utility\ConvertFrom-Json -InputObject $InputObject
  }
}
`
	for index, args := range [][]string{{}, {"one"}, {"", "two", "中文", `C:\User's folder\`}} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			payload, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			// The outer test invocation has already consumed stdin. Feed the
			// same base64 payload through a parameter at just that input seam;
			// run the exact production decoding/binding expression unchanged.
			binding := strings.ReplaceAll(powerShellArgumentBinding, "[Console]::In.ReadToEnd()", "$args[0]")
			script := legacyConversion + binding + "\n" + `& { [pscustomobject]@{ arguments = @($args) } | ConvertTo-Json -Compress } @codexSkinArguments`
			var result struct {
				Arguments []string `json:"arguments"`
			}
			err = runTestPowerShellJSON(t, script, []string{base64.StdEncoding.EncodeToString(payload)}, &result)
			if err != nil || !reflect.DeepEqual(result.Arguments, args) {
				t.Fatalf("legacy array binding = %#v, %v; want %#v", result.Arguments, err, args)
			}
		})
	}
}

func TestWindowsPowerShellLaunchBindsArgumentsAndFailsClosed(t *testing.T) {
	const appID = "OpenAI.Codex_fixture!App"
	const arguments = `--remote-debugging-address=127.0.0.1 --remote-debugging-port=43127 --user-data-dir="C:\Users\测试 User\Codex" --no-first-run`
	for _, fixture := range []struct {
		name string
		body string
		ok   bool
	}{
		{"success", "return 4242;", true},
		{"zero_process_id", "return 0;", false},
		{"activation_failure", `throw new System.Exception("fixture activation failure");`, false},
	} {
		for _, function := range []string{"LaunchControlled", "LaunchOrdinary"} {
			t.Run(fixture.name+"/"+function, func(t *testing.T) {
				prefix := fmt.Sprintf(`
Add-Type -TypeDefinition @'
namespace CodexSkin {
  public static class PackageLauncher {
    public static uint Launch(string appId, string arguments) {
      if (appId != %s || arguments != %s) throw new System.Exception("arguments mismatch");
      %s
    }
  }
  public static class OrdinaryPackageLauncher {
    public static uint Launch(string appId) {
      if (appId != %s) throw new System.Exception("appId mismatch");
      %s
    }
  }
}
'@
`, strconv.Quote(appID), strconv.Quote(arguments), fixture.body, strconv.Quote(appID), fixture.body)
				args := []string{appID}
				if function == "LaunchControlled" {
					args = append(args, arguments)
				}
				var result struct {
					ProcessID int `json:"processId"`
				}
				err := runTestPowerShellJSON(t, prefix+windowsIdentityScript(t, function), args, &result)
				if fixture.ok {
					if err != nil || result.ProcessID != 4242 {
						t.Fatalf("launch = %d, %v; want 4242", result.ProcessID, err)
					}
				} else if !errors.Is(err, ErrIdentityUntrusted) || result.ProcessID != 0 {
					t.Fatalf("failed launch did not fail closed: %d, %v", result.ProcessID, err)
				}
			})
		}
	}
}

func TestWindowsPowerShellActivationDeclarationsCompile(t *testing.T) {
	for _, function := range []string{"LaunchControlled", "LaunchOrdinary"} {
		t.Run(function, func(t *testing.T) {
			// Compile the real Add-Type declarations; stop before the only
			// line that activates an application. Never call the real COM API.
			declarations, _, found := strings.Cut(windowsIdentityScript(t, function), "\n$launchedProcessId =")
			if !found {
				t.Fatal("production activation boundary changed; update this test")
			}
			var result struct {
				Compiled bool `json:"compiled"`
			}
			err := runTestPowerShellJSON(t, declarations+"\n"+`[pscustomobject]@{ compiled = $true } | ConvertTo-Json -Compress`, nil, &result)
			if err != nil || !result.Compiled {
				t.Fatalf("activation declarations did not compile: %v", err)
			}
		})
	}
}

func TestWindowsPowerShellJSONFailsClosed(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		script string
	}{
		{"exit_failure", `'{"ok":true}'; exit 9`},
		{"script_failure", `throw 'sensitive fixture path'; '{"ok":true}'`},
		{"nonterminating_error", `Write-Error 'sensitive fixture path'; '{"ok":true}'`},
		{"no_output", ""},
		{"null", "'null'"},
		{"malformed", "'not json'"},
		{"unknown_field", `'{"ok":true,"unexpected":1}'`},
		{"second_object", `'{"ok":true}'; '{"ok":false}'`},
		{"trailing_text", `'{"ok":true} trailing'`},
		{"oversized", `' ' * (128 * 1024); '{"ok":true}'`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var result struct {
				OK bool `json:"ok"`
			}
			err := runTestPowerShellJSON(t, fixture.script, nil, &result)
			if !errors.Is(err, ErrIdentityUntrusted) || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid probe output did not fail closed with a redacted error: %v", err)
			}
		})
	}
}

func TestWindowsPowerShellCancellation(t *testing.T) {
	executable, environment := testPowerShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result struct {
		OK bool `json:"ok"`
	}
	started := time.Now()
	err := runPowerShellCommandJSON(ctx, executable, environment, `Start-Sleep -Seconds 60; '{"ok":true}'`, nil, &result)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) || !errors.Is(err, ErrIdentityUntrusted) || result.OK {
		t.Fatalf("cancelled probe = %#v, %v, %v", result, err, ctx.Err())
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("cancelled probe did not exit promptly")
	}
}

func TestWindowsPowerShellLaunchReturnsActivationPID(t *testing.T) {
	// Only COM activation is replaced: production PowerShell statements, the
	// real child process, argument binding, and JSON decoding all execute.
	const activationFixture = `
Add-Type -TypeDefinition @'
namespace CodexSkin {
  public static class PackageLauncher {
    public static uint Launch(string appId, string arguments) { return 4242; }
  }
  public static class OrdinaryPackageLauncher {
    public static uint Launch(string appId) { return 4242; }
  }
}
'@
`
	for _, function := range []string{"LaunchControlled", "LaunchOrdinary"} {
		t.Run(function, func(t *testing.T) {
			var result struct {
				ProcessID int `json:"processId"`
			}
			// No arguments isolates the reserved-variable failure from the
			// separate broken -Command argument transport.
			err := runTestPowerShellJSON(t, activationFixture+windowsIdentityScript(t, function), nil, &result)
			if err != nil || result.ProcessID != 4242 {
				t.Fatalf("activation PID = %d, %v; want 4242", result.ProcessID, err)
			}
		})
	}
}
