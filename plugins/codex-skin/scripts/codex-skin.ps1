$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$pluginRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
  [Console]::Error.WriteLine("Codex Skin Helper root is unavailable.")
  exit 80
}

$helperPath = Join-Path $env:LOCALAPPDATA "CodexSkin\recovery\engine\codex-skin.exe"

function Ensure-CodexSkinHelper {
  . (Join-Path $PSScriptRoot "bootstrap-pins.ps1")
  if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") {
    [Console]::Error.WriteLine("Codex Skin Bootstrap does not support this Windows architecture.")
    exit 50
  }
  $bootstrapCache = Join-Path $PSScriptRoot ".bootstrap"
  if (Test-Path -LiteralPath $bootstrapCache) {
    $cacheItem = Get-Item -LiteralPath $bootstrapCache -Force
    if (-not $cacheItem.PSIsContainer -or ($cacheItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      [Console]::Error.WriteLine("Codex Skin Bootstrap cache is unsafe.")
      exit 50
    }
  } else {
    New-Item -ItemType Directory -Path $bootstrapCache | Out-Null
  }
  $bootstrapPath = Join-Path $bootstrapCache $bootstrapFilename
  $bootstrapValid = $false
  if (Test-Path -LiteralPath $bootstrapPath) {
    $launcher = Get-Item -LiteralPath $bootstrapPath -Force
    if ($launcher.PSIsContainer -or ($launcher.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      [Console]::Error.WriteLine("Codex Skin Bootstrap launcher is unsafe.")
      exit 50
    }
    $bootstrapValid = (Get-FileHash -LiteralPath $bootstrapPath -Algorithm SHA256).Hash.ToLowerInvariant() -eq $bootstrapSHA256
  }
  if (-not $bootstrapValid) {
    $temporary = Join-Path $bootstrapCache (".bootstrap-download-" + [Guid]::NewGuid().ToString("N"))
    try {
      $bootstrapURL = "https://github.com/yuanjohn01-byte/codex-skin-plugin/releases/download/$bootstrapReleaseTag/$bootstrapFilename"
      Invoke-WebRequest -UseBasicParsing -Uri $bootstrapURL -OutFile $temporary
      $downloadedSHA = (Get-FileHash -LiteralPath $temporary -Algorithm SHA256).Hash.ToLowerInvariant()
      if ($downloadedSHA -ne $bootstrapSHA256) {
        throw "Codex Skin Bootstrap launcher verification failed."
      }
      Move-Item -LiteralPath $temporary -Destination $bootstrapPath -Force
    } finally {
      if (Test-Path -LiteralPath $temporary) {
        Remove-Item -LiteralPath $temporary -Force
      }
    }
  }
  $bootstrapOutput = & $bootstrapPath install --plugin-cache $pluginRoot --json
  $bootstrapStatus = $LASTEXITCODE
  if ($bootstrapStatus -ne 0) {
    $bootstrapOutput | Write-Output
    exit $bootstrapStatus
  }
}

if ($args.Count -ge 2 -and $args[0] -eq "theme" -and $args[1] -eq "apply") {
  Ensure-CodexSkinHelper
}

try {
  $helper = Get-Item -LiteralPath $helperPath -Force
} catch {
  [Console]::Error.WriteLine("Verified Codex Skin Helper is not installed.")
  exit 80
}
if (-not $helper.PSIsContainer -and -not ($helper.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
  & $helper.FullName @args
  exit $LASTEXITCODE
}

[Console]::Error.WriteLine("Verified Codex Skin Helper is not installed.")
exit 80
