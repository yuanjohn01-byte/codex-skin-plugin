param(
  [Parameter(Mandatory = $true)][string]$V1Path,
  [Parameter(Mandatory = $true)][string]$V2Path,
  [string]$OutputPath = "dist/guardian/windows-lifecycle-summary.json"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$scratch = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-skin-guardian-windows-" + [guid]::NewGuid().ToString("N"))
$null = New-Item -ItemType Directory -Path $scratch
$v1 = Join-Path $scratch "guardian-v1.exe"
$v2 = Join-Path $scratch "guardian-v2.exe"
$tampered = Join-Path $scratch "guardian-v2-tampered.exe"
$taskName = "CodexSkinGuardianInternalSpike-" + [guid]::NewGuid().ToString("N")
$certificate = $null
$thumbprint = $null
$taskRegistered = $false
$summary = $null

try {
  Copy-Item -LiteralPath (Resolve-Path $V1Path).Path -Destination $v1
  Copy-Item -LiteralPath (Resolve-Path $V2Path).Path -Destination $v2
  $certificate = New-SelfSignedCertificate `
    -Type CodeSigningCert `
    -Subject "CN=Codex Skin Guardian Internal Spike" `
    -CertStoreLocation "Cert:\CurrentUser\My" `
    -HashAlgorithm "SHA256" `
    -KeyAlgorithm "RSA" `
    -KeyLength 3072 `
    -KeyExportPolicy "NonExportable" `
    -NotAfter (Get-Date).AddDays(1)
  $thumbprint = $certificate.Thumbprint

  foreach ($path in @($v1, $v2)) {
    $signature = Set-AuthenticodeSignature -FilePath $path -Certificate $certificate -HashAlgorithm SHA256
    if (-not $signature.SignerCertificate -or $signature.Status -eq "HashMismatch" -or $signature.Status -eq "NotSigned") {
      throw "Guardian Authenticode signature was not attached"
    }
  }

  Copy-Item -LiteralPath $v2 -Destination $tampered
  [byte[]]$tamperedBytes = [System.IO.File]::ReadAllBytes($tampered)
  if ($tamperedBytes.Length -le 1024) { throw "Guardian PE image is unexpectedly small" }
  $tamperedBytes[1024] = $tamperedBytes[1024] -bxor 0x01
  [System.IO.File]::WriteAllBytes($tampered, $tamperedBytes)
  if ((Get-AuthenticodeSignature -FilePath $tampered).Status -ne "HashMismatch") {
    throw "tampered Guardian did not report Authenticode HashMismatch"
  }

  $env:CODEX_SKIN_TEST_GUARDIAN_V1 = $v1
  $env:CODEX_SKIN_TEST_GUARDIAN_V2 = $v2
  & go test ./internal/guardian -run TestNativeGuardianLifecycle -count=1 -v
  if ($LASTEXITCODE -ne 0) { throw "native Windows Guardian lifecycle test failed" }

  $originalPath = $env:PATH
  try {
    $env:PATH = "$env:SystemRoot\System32;$env:SystemRoot"
    if (Get-Command node -ErrorAction SilentlyContinue) { throw "Node remained on the Guardian PATH" }
    if (Get-Command python -ErrorAction SilentlyContinue) { throw "Python remained on the Guardian PATH" }
    if (Get-Command go -ErrorAction SilentlyContinue) { throw "Go remained on the Guardian PATH" }
    $version = (& $v1 version --json) | ConvertFrom-Json
  }
  finally {
    $env:PATH = $originalPath
  }
  if ($version.guardianVersion -ne "0.1.0-s3" -or $version.status -ne "ready") {
    throw "signed Guardian version contract failed"
  }

  $currentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
  $action = New-ScheduledTaskAction -Execute $v1 -Argument "run --reason process --json --internal-spike"
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
  $principal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
  $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 5) -StartWhenAvailable
  $null = Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force
  $taskRegistered = $true
  $task = Get-ScheduledTask -TaskName $taskName
  if ($task.Principal.RunLevel.ToString() -ne "Limited" -or $task.Principal.LogonType.ToString() -ne "Interactive") {
    throw "Scheduled Task did not use an interactive least-privilege principal"
  }
  $taskXml = Export-ScheduledTask -TaskName $taskName
  foreach ($required in @("LeastPrivilege", "InteractiveToken", "run --reason process --json --internal-spike")) {
    if (-not $taskXml.Contains($required)) { throw "Scheduled Task XML is missing $required" }
  }
  foreach ($forbidden in @("HighestAvailable", "<UserId>SYSTEM</UserId>", "powershell", "cmd.exe")) {
    if ($taskXml.Contains($forbidden)) { throw "Scheduled Task XML contains forbidden value $forbidden" }
  }
  Start-ScheduledTask -TaskName $taskName
  for ($attempt = 0; $attempt -lt 20; $attempt++) {
    Start-Sleep -Milliseconds 250
    $info = Get-ScheduledTaskInfo -TaskName $taskName
    if ($info.LastRunTime.Year -gt 2000 -and (Get-ScheduledTask -TaskName $taskName).State -ne "Running") { break }
  }
  $info = Get-ScheduledTaskInfo -TaskName $taskName
  if ($info.LastTaskResult -ne 0) { throw "Scheduled Guardian trigger returned $($info.LastTaskResult)" }
  Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
  $taskRegistered = $false
  if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
    throw "Scheduled Task remained after unregister"
  }

  $summary = [ordered]@{
    schemaVersion = 1
    scope = "internal-per-user-guardian-feasibility-only"
    platform = "windows"
    formalDistributionReady = $false
    signatureKind = "ephemeral-self-signed"
    authenticodeSignerPresent = $true
    tamperRejected = $true
    nativeInstallUpgradeRollbackUninstall = $true
    perUserRegistrationInstalled = $true
    perUserRegistrationRemoved = $true
    scheduledTaskRunLevel = "Limited"
    scheduledTaskLogonType = "InteractiveToken"
    requiresAdministratorAtRuntime = $false
    networkListener = $false
    arbitraryCommandSurface = $false
    signedSha256 = @(
      (Get-FileHash -Algorithm SHA256 -LiteralPath $v1).Hash.ToLowerInvariant(),
      (Get-FileHash -Algorithm SHA256 -LiteralPath $v2).Hash.ToLowerInvariant()
    )
    limitations = @(
      "self-signed Authenticode does not provide public trust, timestamp, or SmartScreen reputation",
      "the fixed trigger validates packaging only; lifecycle reconcile is deferred to M1-S6-013"
    )
  }
}
finally {
  if ($taskRegistered) {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
  }
  if ($thumbprint) {
    Remove-Item -LiteralPath "Cert:\CurrentUser\My\$thumbprint" -Force -ErrorAction SilentlyContinue
  }
  Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}

if (-not $summary) { throw "Windows Guardian lifecycle summary was not produced" }
if ($thumbprint -and (Test-Path "Cert:\CurrentUser\My\$thumbprint")) { throw "ephemeral Guardian certificate was not removed" }
$resolvedOutput = [System.IO.Path]::GetFullPath($OutputPath)
$outputDirectory = Split-Path -Parent $resolvedOutput
$null = New-Item -ItemType Directory -Path $outputDirectory -Force
$temporaryOutput = Join-Path $outputDirectory ("." + [System.IO.Path]::GetFileName($resolvedOutput) + ".tmp")
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $temporaryOutput -Encoding utf8
Move-Item -LiteralPath $temporaryOutput -Destination $resolvedOutput -Force
$summary | ConvertTo-Json -Depth 6 -Compress
