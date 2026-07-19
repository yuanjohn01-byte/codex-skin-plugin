param(
  [string]$HelperPath = "dist/helper/codex-skin-helper_0.1.0-s3_windows_x64.exe",
  [string]$OutputPath = "dist/signing/windows-signing-spike-summary.json"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
if (Test-Path Variable:PSNativeCommandUseErrorActionPreference) {
  $PSNativeCommandUseErrorActionPreference = $false
}

function Resolve-SignTool {
  $command = Get-Command "signtool.exe" -ErrorAction SilentlyContinue
  if ($command) {
    return $command.Source
  }

  $kitsRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
  $signToolPattern = Join-Path $kitsRoot "*\x64\signtool.exe"
  $candidates = Get-ChildItem -Path $signToolPattern -File -ErrorAction SilentlyContinue | Sort-Object FullName -Descending
  if (-not $candidates) {
    throw "SignTool was not found in PATH or the Windows 10 SDK"
  }
  return @($candidates)[0].FullName
}

function Invoke-BoundedCommand {
  param(
    [string]$Executable,
    [string[]]$Arguments,
    [bool]$ExpectSuccess = $true
  )

  $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
  $startInfo.FileName = $Executable
  $startInfo.UseShellExecute = $false
  $startInfo.CreateNoWindow = $true
  $startInfo.RedirectStandardOutput = $true
  $startInfo.RedirectStandardError = $true
  foreach ($argument in $Arguments) {
    $null = $startInfo.ArgumentList.Add($argument)
  }
  $process = [System.Diagnostics.Process]::new()
  $process.StartInfo = $startInfo
  if (-not $process.Start()) {
    throw "bounded process did not start"
  }
  if (-not $process.WaitForExit(30000)) {
    $process.Kill($true)
    throw "bounded command timed out"
  }
  $succeeded = $process.ExitCode -eq 0
  if ($ExpectSuccess -and -not $succeeded) {
    throw "bounded command failed"
  }
  return $succeeded
}

$resolvedHelper = (Resolve-Path $HelperPath).Path
$signTool = Resolve-SignTool
$certutil = Join-Path $env:SystemRoot "System32\certutil.exe"
$scratch = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-skin-windows-signing-" + [guid]::NewGuid().ToString("N"))
$null = New-Item -ItemType Directory -Path $scratch
$target = Join-Path $scratch "codex-skin-helper_0.1.0-s3_windows_x64.exe"
$tampered = Join-Path $scratch "codex-skin-helper_0.1.0-s3_windows_x64.tampered.exe"
$publicCertificate = Join-Path $scratch "internal-spike-public.cer"
$certificate = $null
$thumbprint = $null
$summary = $null

try {
  Write-Host "phase=copy-and-unsigned-run"
  Copy-Item -LiteralPath $resolvedHelper -Destination $target

  $beforeVersion = (& $target version --json) | ConvertFrom-Json
  $beforeDoctor = (& $target doctor --json) | ConvertFrom-Json
  if (-not $beforeVersion.ok -or $beforeVersion.data.helperVersion -ne "0.1.0-s3") {
    throw "unsigned Helper version contract failed"
  }
  if (-not $beforeDoctor.ok -or $beforeDoctor.data.platform -ne "windows" -or $beforeDoctor.data.architecture -ne "x64" -or $beforeDoctor.data.nodeRequired) {
    throw "unsigned Helper doctor contract failed"
  }

  Write-Host "phase=create-ephemeral-certificate"
  $certificate = New-SelfSignedCertificate `
    -Type CodeSigningCert `
    -Subject "CN=Codex Skin Internal Signing Spike" `
    -CertStoreLocation "Cert:\CurrentUser\My" `
    -HashAlgorithm "SHA256" `
    -KeyAlgorithm "RSA" `
    -KeyLength 3072 `
    -KeyExportPolicy "NonExportable" `
    -NotAfter (Get-Date).AddDays(1)
  $thumbprint = $certificate.Thumbprint
  Write-Host "phase=trust-public-certificate-current-user"
  $null = Export-Certificate -Cert $certificate -FilePath $publicCertificate
  if (-not (Invoke-BoundedCommand -Executable $certutil -Arguments @("-user", "-addstore", "-f", "Root", $publicCertificate))) {
    throw "current-user test root import failed"
  }

  Write-Host "phase=authenticode-sign"
  if (-not (Invoke-BoundedCommand -Executable $signTool -Arguments @("sign", "/fd", "SHA256", "/sha1", $thumbprint, "/s", "My", $target))) {
    throw "self-signed Authenticode signing failed"
  }
  Write-Host "phase=authenticode-verify"
  if (-not (Invoke-BoundedCommand -Executable $signTool -Arguments @("verify", "/pa", "/all", $target))) {
    throw "locally trusted Authenticode verification failed"
  }
  $authenticode = Get-AuthenticodeSignature -FilePath $target
  if ($authenticode.Status -ne "Valid") {
    throw "Get-AuthenticodeSignature did not report Valid"
  }

  Write-Host "phase=signed-helper-run"
  $originalPath = $env:PATH
  try {
    $env:PATH = "$env:SystemRoot\System32;$env:SystemRoot"
    if (Get-Command node -ErrorAction SilentlyContinue) { throw "Node remained on the signed-run PATH" }
    if (Get-Command python -ErrorAction SilentlyContinue) { throw "Python remained on the signed-run PATH" }
    if (Get-Command go -ErrorAction SilentlyContinue) { throw "Go remained on the signed-run PATH" }
    $signedVersion = (& $target version --json) | ConvertFrom-Json
    $signedDoctor = (& $target doctor --json) | ConvertFrom-Json
  }
  finally {
    $env:PATH = $originalPath
  }
  if (-not $signedVersion.ok -or $signedVersion.data.helperVersion -ne "0.1.0-s3") {
    throw "signed Helper version contract failed"
  }
  if (-not $signedDoctor.ok -or $signedDoctor.data.platform -ne "windows" -or $signedDoctor.data.architecture -ne "x64" -or $signedDoctor.data.nodeRequired) {
    throw "signed Helper doctor contract failed"
  }

  Write-Host "phase=tamper-rejection"
  Copy-Item -LiteralPath $target -Destination $tampered
  [byte[]]$tamperedBytes = [System.IO.File]::ReadAllBytes($tampered)
  if ($tamperedBytes.Length -le 1024) {
    throw "signed Helper is unexpectedly small"
  }
  $tamperedBytes[1024] = $tamperedBytes[1024] -bxor 0x01
  [System.IO.File]::WriteAllBytes($tampered, $tamperedBytes)
  $tamperedAccepted = Invoke-BoundedCommand -Executable $signTool -Arguments @("verify", "/pa", "/all", $tampered) -ExpectSuccess $false
  if ($tamperedAccepted) {
    throw "SignTool accepted a modified PE image"
  }

  $signToolVersion = (Get-Item $signTool).VersionInfo.FileVersion
  $summary = [ordered]@{
    schemaVersion = 1
    scope = "internal-self-signed-feasibility-only"
    formalDistributionReady = $false
    smartScreenTested = $false
    smartScreenReputationEstablished = $false
    timestampApplied = $false
    certificate = [ordered]@{
      kind = "ephemeral-self-signed"
      store = "CurrentUser"
      privateKeyExported = $false
      publiclyTrusted = $false
    }
    tools = [ordered]@{
      signToolFileVersion = $signToolVersion
      fileDigestAlgorithm = "SHA256"
      verificationPolicy = "Authenticode /pa"
    }
    artifact = [ordered]@{
      filename = "codex-skin-helper_0.1.0-s3_windows_x64.exe"
      authenticodeStatusWhileLocallyTrusted = "Valid"
      signedHelperExecutedWithoutNodePythonGo = $true
      tamperRejected = $true
      signedSha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $target).Hash.ToLowerInvariant()
    }
    limitations = @(
      "self-signed certificates do not provide public trust or publisher reputation",
      "no RFC 3161 timestamp was applied",
      "SmartScreen UI or cloud reputation was not tested",
      "certificate type alone does not establish SmartScreen reputation"
    )
  }
}
finally {
  Write-Host "phase=certificate-cleanup"
  if ($thumbprint) {
    $null = Invoke-BoundedCommand -Executable $certutil -Arguments @("-user", "-delstore", "Root", $thumbprint) -ExpectSuccess $false
    Remove-Item -LiteralPath "Cert:\CurrentUser\My\$thumbprint" -Force -ErrorAction SilentlyContinue
  }
  Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}

if (-not $summary) {
  throw "Windows signing summary was not produced"
}
if ((Test-Path "Cert:\CurrentUser\Root\$thumbprint") -or (Test-Path "Cert:\CurrentUser\My\$thumbprint")) {
  throw "ephemeral code-signing certificate was not removed"
}
$summary.certificate["storeCleanupVerified"] = $true

$resolvedOutput = [System.IO.Path]::GetFullPath($OutputPath)
$outputDirectory = Split-Path -Parent $resolvedOutput
$null = New-Item -ItemType Directory -Path $outputDirectory -Force
$temporaryOutput = Join-Path $outputDirectory ("." + [System.IO.Path]::GetFileName($resolvedOutput) + ".tmp")
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $temporaryOutput -Encoding utf8
Move-Item -LiteralPath $temporaryOutput -Destination $resolvedOutput -Force
$summary | ConvertTo-Json -Depth 6 -Compress
