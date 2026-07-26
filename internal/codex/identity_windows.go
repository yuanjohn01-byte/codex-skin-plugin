//go:build windows

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var packageIdentityPattern = regexp.MustCompile(`^OpenAI\.Codex_[A-Za-z0-9._-]{1,110}$`)

type windowsInstallation struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	Publisher         string `json:"publisher"`
	PublisherID       string `json:"publisherId"`
	PackageFullName   string `json:"packageFullName"`
	PackageFamilyName string `json:"packageFamilyName"`
	InstallLocation   string `json:"installLocation"`
	Executable        string `json:"executable"`
	AppUserModelID    string `json:"appUserModelId"`
	SignatureKind     string `json:"signatureKind"`
}

type windowsProcess struct {
	ProcessID    int    `json:"processId"`
	Path         string `json:"path"`
	CommandLine  string `json:"commandLine"`
	CreationDate string `json:"creationDate"`
	SignerStatus string `json:"signerStatus"`
}

func DiscoverInstallation(ctx context.Context) (Installation, error) {
	const script = `
$ErrorActionPreference = 'Stop'
$valid = @()
foreach ($package in @(Get-AppxPackage -Name 'OpenAI.Codex' | Sort-Object Version -Descending)) {
  if ("$($package.Name)" -cne 'OpenAI.Codex' -or "$($package.SignatureKind)" -cne 'Store' -or [bool]$package.IsDevelopmentMode) { continue }
  $manifest = Get-AppxPackageManifest -Package $package
  $apps = @($manifest.Package.Applications.Application | Where-Object { "$($_.Executable)".Replace('/', '\') -ieq 'app\ChatGPT.exe' })
  if ($apps.Count -ne 1) { continue }
  $executable = Join-Path "$($package.InstallLocation)" 'app\ChatGPT.exe'
  if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) { continue }
  $valid += [pscustomobject]@{
    name = "$($package.Name)"
    version = "$($package.Version)"
    publisher = "$($package.Publisher)"
    publisherId = "$($package.PublisherId)"
    packageFullName = "$($package.PackageFullName)"
    packageFamilyName = "$($package.PackageFamilyName)"
    installLocation = "$($package.InstallLocation)"
    executable = $executable
    appUserModelId = "$($package.PackageFamilyName)!$($apps[0].Id)"
    signatureKind = "$($package.SignatureKind)"
  }
}
if ($valid.Count -ne 1) { throw 'official Codex package identity is missing or ambiguous' }
$valid[0] | ConvertTo-Json -Compress
`
	var raw windowsInstallation
	if err := runPowerShellJSON(ctx, script, nil, &raw); err != nil {
		return Installation{}, ErrIdentityUntrusted
	}
	if raw.Name != "OpenAI.Codex" ||
		raw.SignatureKind != "Store" ||
		raw.Version == "" ||
		raw.Publisher == "" ||
		raw.PublisherID == "" ||
		!packageIdentityPattern.MatchString(raw.PackageFamilyName) ||
		!strings.HasPrefix(raw.PackageFullName, "OpenAI.Codex_") ||
		!strings.HasPrefix(raw.AppUserModelID, raw.PackageFamilyName+"!") ||
		!samePath(raw.Executable, filepath.Join(raw.InstallLocation, "app", "ChatGPT.exe")) ||
		!strings.Contains(strings.ToLower(raw.InstallLocation), `\windowsapps\`) {
		return Installation{}, ErrIdentityUntrusted
	}
	rootInfo, err := os.Lstat(raw.InstallLocation)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Installation{}, ErrIdentityUntrusted
	}
	digest, err := hashOrdinaryFile(raw.Executable)
	if err != nil {
		return Installation{}, err
	}
	return Installation{
		Platform: "windows", AppIdentifier: raw.Name, Publisher: raw.Publisher,
		Version: raw.Version, Root: raw.InstallLocation, Executable: raw.Executable,
		ExecutableSHA256: digest, PackageFullName: raw.PackageFullName,
		PackageFamilyName: raw.PackageFamilyName, AppUserModelID: raw.AppUserModelID,
	}, nil
}

func LaunchControlled(ctx context.Context, installation Installation, profile string, port int) (int, error) {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return 0, err
	}
	profile, err := validateLaunchInputs(installation, profile, port)
	if err != nil || strings.ContainsAny(profile, "\"\r\n") {
		return 0, ErrLaunchFailed
	}
	const script = `
$ErrorActionPreference = 'Stop'
if (-not ('CodexSkin.PackageLauncher' -as [type])) {
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace CodexSkin {
  [ComImport, Guid("2e941141-7f97-4756-ba1d-9decde894a3d"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IApplicationActivationManager {
    [PreserveSig] int ActivateApplication(
      [MarshalAs(UnmanagedType.LPWStr)] string appUserModelId,
      [MarshalAs(UnmanagedType.LPWStr)] string arguments,
      uint options,
      out uint processId);
  }
  [ComImport, Guid("45ba127d-10a8-46ea-8ab7-56ea9078943c")]
  class ApplicationActivationManager {}
  public static class PackageLauncher {
    public static uint Launch(string appUserModelId, string arguments) {
      var manager = (IApplicationActivationManager)new ApplicationActivationManager();
      uint processId;
      int result = manager.ActivateApplication(appUserModelId, arguments, 0, out processId);
      Marshal.ThrowExceptionForHR(result);
      return processId;
    }
  }
}
'@
}
$pid = [CodexSkin.PackageLauncher]::Launch($args[0], $args[1])
if ($pid -le 0) { throw 'activation did not return a process id' }
[pscustomobject]@{ processId = [int]$pid } | ConvertTo-Json -Compress
`
	argumentLine := strings.Join([]string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + quoteWindowsArgument(profile),
		"--no-first-run",
	}, " ")
	var result struct {
		ProcessID int `json:"processId"`
	}
	if err := runPowerShellJSON(ctx, script, []string{installation.AppUserModelID, argumentLine}, &result); err != nil || result.ProcessID < 1 {
		return 0, ErrLaunchFailed
	}
	return result.ProcessID, nil
}

func VerifyListener(ctx context.Context, installation Installation, launchedPID, port int, profile string) (ProcessIdentity, error) {
	const script = `
$ErrorActionPreference = 'Stop'
$connections = @(Get-NetTCPConnection -State Listen -LocalPort ([int]$args[0]))
if ($connections.Count -ne 1) { throw 'listener is missing or ambiguous' }
[pscustomobject]@{
  processId = [int]$connections[0].OwningProcess
  localAddress = "$($connections[0].LocalAddress)"
} | ConvertTo-Json -Compress
`
	var listener struct {
		ProcessID    int    `json:"processId"`
		LocalAddress string `json:"localAddress"`
	}
	if err := runPowerShellJSON(ctx, script, []string{strconv.Itoa(port)}, &listener); err != nil ||
		listener.ProcessID != launchedPID ||
		listener.LocalAddress != "127.0.0.1" {
		return ProcessIdentity{}, ErrListenerUntrusted
	}
	return VerifyProcess(ctx, installation, listener.ProcessID, port, profile)
}

func VerifyProcess(ctx context.Context, installation Installation, pid, port int, profile string) (ProcessIdentity, error) {
	const script = `
$ErrorActionPreference = 'Stop'
$process = Get-CimInstance Win32_Process -Filter ("ProcessId = " + [int]$args[0])
$native = Get-Process -Id ([int]$args[0])
$signature = Get-AuthenticodeSignature -LiteralPath $native.Path
[pscustomobject]@{
  processId = [int]$process.ProcessId
  path = "$($native.Path)"
  commandLine = "$($process.CommandLine)"
  creationDate = "$($process.CreationDate)"
  signerStatus = "$($signature.Status)"
} | ConvertTo-Json -Compress
`
	var process windowsProcess
	if err := runPowerShellJSON(ctx, script, []string{strconv.Itoa(pid)}, &process); err != nil ||
		process.ProcessID != pid ||
		process.SignerStatus != "Valid" ||
		!samePath(process.Path, installation.Executable) ||
		process.CreationDate == "" {
		return ProcessIdentity{}, ErrListenerUntrusted
	}
	if err := requireControlledFlags(process.CommandLine, port, profile); err != nil {
		return ProcessIdentity{}, err
	}
	digest, err := hashOrdinaryFile(process.Path)
	if err != nil || digest != installation.ExecutableSHA256 {
		return ProcessIdentity{}, ErrListenerUntrusted
	}
	return ProcessIdentity{
		ProcessID: pid, ProcessStartID: process.CreationDate, Executable: process.Path,
		ExecutableSHA256: digest, CommandLine: process.CommandLine,
	}, nil
}

func StopOwnedProcess(
	ctx context.Context,
	installation Installation,
	expected ProcessIdentity,
	port int,
	profile string,
) error {
	current, err := VerifyListener(ctx, installation, expected.ProcessID, port, profile)
	if err != nil ||
		current.ProcessStartID != expected.ProcessStartID ||
		current.ExecutableSHA256 != expected.ExecutableSHA256 {
		return ErrListenerUntrusted
	}
	const script = `
$ErrorActionPreference = 'Stop'
$process = Get-Process -Id ([int]$args[0])
$process.CloseMainWindow() | Out-Null
if (-not $process.WaitForExit(10000)) { throw 'controlled process did not exit' }
[pscustomobject]@{ stopped = $true } | ConvertTo-Json -Compress
`
	var result struct {
		Stopped bool `json:"stopped"`
	}
	if err := runPowerShellJSON(ctx, script, []string{strconv.Itoa(expected.ProcessID)}, &result); err != nil || !result.Stopped {
		return ErrLaunchFailed
	}
	return nil
}

func verifyInstallationFresh(ctx context.Context, expected Installation) error {
	current, err := DiscoverInstallation(ctx)
	if err != nil ||
		current.AppIdentifier != expected.AppIdentifier ||
		current.Publisher != expected.Publisher ||
		current.Version != expected.Version ||
		current.PackageFullName != expected.PackageFullName ||
		current.PackageFamilyName != expected.PackageFamilyName ||
		current.AppUserModelID != expected.AppUserModelID ||
		current.ExecutableSHA256 != expected.ExecutableSHA256 ||
		!samePath(current.Executable, expected.Executable) {
		return ErrIdentityUntrusted
	}
	return nil
}

func runPowerShellJSON(ctx context.Context, script string, args []string, target any) error {
	commandArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-Command", script}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, "powershell.exe", commandArgs...)
	command.Env = []string{
		"SystemRoot=" + os.Getenv("SystemRoot"),
		"WINDIR=" + os.Getenv("WINDIR"),
		"PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0") + ";" + filepath.Join(os.Getenv("SystemRoot"), "System32"),
	}
	output, err := command.Output()
	if err != nil || len(output) < 2 || len(output) > 128*1024 {
		return fmt.Errorf("%w: system identity probe", ErrIdentityUntrusted)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: system identity JSON", ErrIdentityUntrusted)
	}
	return nil
}

func quoteWindowsArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
