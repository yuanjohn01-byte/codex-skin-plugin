//go:build windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
$launchedProcessId = [CodexSkin.PackageLauncher]::Launch($args[0], $args[1])
if ($launchedProcessId -le 0) { throw 'activation did not return a process id' }
[pscustomobject]@{ processId = [int]$launchedProcessId } | ConvertTo-Json -Compress
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

func LaunchOrdinary(ctx context.Context, installation Installation) error {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return err
	}
	const script = `
$ErrorActionPreference = 'Stop'
if (-not ('CodexSkin.OrdinaryPackageLauncher' -as [type])) {
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace CodexSkin {
  [ComImport, Guid("2e941141-7f97-4756-ba1d-9decde894a3d"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IOrdinaryApplicationActivationManager {
    [PreserveSig] int ActivateApplication(
      [MarshalAs(UnmanagedType.LPWStr)] string appUserModelId,
      [MarshalAs(UnmanagedType.LPWStr)] string arguments,
      uint options,
      out uint processId);
  }
  [ComImport, Guid("45ba127d-10a8-46ea-8ab7-56ea9078943c")]
  class OrdinaryApplicationActivationManager {}
  public static class OrdinaryPackageLauncher {
    public static uint Launch(string appUserModelId) {
      var manager = (IOrdinaryApplicationActivationManager)new OrdinaryApplicationActivationManager();
      uint processId;
      int result = manager.ActivateApplication(appUserModelId, "", 0, out processId);
      Marshal.ThrowExceptionForHR(result);
      return processId;
    }
  }
}
'@
}
$launchedProcessId = [CodexSkin.OrdinaryPackageLauncher]::Launch($args[0])
if ($launchedProcessId -le 0) { throw 'activation did not return a process id' }
[pscustomobject]@{ processId = [int]$launchedProcessId } | ConvertTo-Json -Compress
`
	var result struct {
		ProcessID int `json:"processId"`
	}
	if err := runPowerShellJSON(
		ctx,
		script,
		[]string{installation.AppUserModelID},
		&result,
	); err != nil || result.ProcessID < 1 {
		return ErrLaunchFailed
	}
	return nil
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

func DefaultUserProfile(installation Installation) (string, error) {
	if installation.Platform != "windows" ||
		!packageIdentityPattern.MatchString(installation.PackageFamilyName) {
		return "", ErrCurrentUnsafe
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", ErrCurrentUnsafe
	}
	expected := filepath.Join(
		localAppData,
		"Packages",
		installation.PackageFamilyName,
		"LocalCache",
		"Roaming",
		"Codex",
	)
	return validateCurrentProfile(expected, expected)
}

func DiscoverCurrentInstance(ctx context.Context, installation Installation) (CurrentInstance, error) {
	if err := verifyInstallationFresh(ctx, installation); err != nil {
		return CurrentInstance{}, err
	}
	profile, err := DefaultUserProfile(installation)
	if err != nil {
		return CurrentInstance{}, err
	}
	const script = `
$ErrorActionPreference = 'Stop'
$expected = "$($args[0])"
$matches = @()
foreach ($process in @(Get-CimInstance Win32_Process | Where-Object {
  "$($_.ExecutablePath)" -ieq $expected -and
  "$($_.CommandLine)" -notmatch '(?i)(?:^|\s)--type(?:=|\s)'
})) {
  $native = Get-Process -Id ([int]$process.ProcessId)
  $signature = Get-AuthenticodeSignature -LiteralPath $native.Path
  $matches += [pscustomobject]@{
    processId = [int]$process.ProcessId
    path = "$($native.Path)"
    commandLine = "$($process.CommandLine)"
    creationDate = "$($process.CreationDate)"
    signerStatus = "$($signature.Status)"
  }
}
ConvertTo-Json -InputObject @($matches) -Compress
`
	var processes []windowsProcess
	if err := runPowerShellJSON(ctx, script, []string{installation.Executable}, &processes); err != nil {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	if len(processes) == 0 {
		return CurrentInstance{}, ErrCurrentMissing
	}
	if len(processes) != 1 {
		return CurrentInstance{}, ErrCurrentAmbiguous
	}
	process := processes[0]
	if process.ProcessID < 1 ||
		process.SignerStatus != "Valid" ||
		!samePath(process.Path, installation.Executable) ||
		process.CreationDate == "" {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	digest, err := hashOrdinaryFile(process.Path)
	if err != nil || digest != installation.ExecutableSHA256 {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	port, controlled, err := controlledFlags(process.CommandLine, profile)
	if err != nil {
		return CurrentInstance{}, ErrCurrentUnsafe
	}
	identity := ProcessIdentity{
		ProcessID: process.ProcessID, ProcessStartID: process.CreationDate,
		Executable: process.Path, ExecutableSHA256: digest,
		CommandLine: process.CommandLine,
	}
	if controlled {
		verified, verifyErr := VerifyListener(
			ctx,
			installation,
			process.ProcessID,
			port,
			profile,
		)
		if verifyErr != nil ||
			verified.ProcessStartID != identity.ProcessStartID ||
			verified.ExecutableSHA256 != identity.ExecutableSHA256 {
			return CurrentInstance{}, ErrCurrentUnsafe
		}
		identity = verified
	}
	return CurrentInstance{
		Process: identity, Profile: profile, ControlledPort: port,
	}, nil
}

func StopCurrentInstance(
	ctx context.Context,
	installation Installation,
	expected CurrentInstance,
) error {
	current, err := DiscoverCurrentInstance(ctx, installation)
	if err != nil ||
		current.Process.ProcessID != expected.Process.ProcessID ||
		current.Process.ProcessStartID != expected.Process.ProcessStartID ||
		current.Process.ExecutableSHA256 != expected.Process.ExecutableSHA256 ||
		!samePath(current.Profile, expected.Profile) ||
		current.ControlledPort != expected.ControlledPort {
		return ErrCurrentUnsafe
	}
	const script = `
$ErrorActionPreference = 'Stop'
$process = Get-Process -Id ([int]$args[0])
if (-not $process.CloseMainWindow()) { throw 'current Codex has no closeable main window' }
if (-not $process.WaitForExit(15000)) { throw 'current Codex did not exit normally' }
[pscustomobject]@{ stopped = $true } | ConvertTo-Json -Compress
`
	var result struct {
		Stopped bool `json:"stopped"`
	}
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := runPowerShellJSON(
		stopCtx,
		script,
		[]string{strconv.Itoa(expected.Process.ProcessID)},
		&result,
	); err != nil || !result.Stopped {
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

// Windows package discovery is already the platform's complete identity check.
// Keep the existing behavior for its stable probes rather than weakening the
// Store package validation while the shared launch flow is optimized on macOS.
func probeStableInstallation(ctx context.Context, expected Installation) (Installation, error) {
	if err := verifyInstallationFresh(ctx, expected); err != nil {
		return Installation{}, err
	}
	return expected, nil
}

func runPowerShellJSON(ctx context.Context, script string, args []string, target any) error {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return ErrIdentityUntrusted
	}
	system32 := filepath.Join(root, "System32")
	powerShellDirectory := filepath.Join(system32, "WindowsPowerShell", "v1.0")
	environment := []string{
		"SystemRoot=" + root,
		"WINDIR=" + root,
		"PATH=" + powerShellDirectory + ";" + system32,
		// Windows PowerShell 5.1's Add-Type compiler needs a writable temp
		// directory. Do not make it fall back to the protected Windows folder.
		"TEMP=" + os.TempDir(),
		"TMP=" + os.TempDir(),
	}
	return runPowerShellCommandJSON(ctx, filepath.Join(powerShellDirectory, "powershell.exe"), environment, script, args, target)
}

func quoteWindowsArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
