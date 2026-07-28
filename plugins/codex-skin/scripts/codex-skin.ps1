$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
  [Console]::Error.WriteLine("Codex Skin Helper root is unavailable.")
  exit 80
}

$helperPath = Join-Path $env:LOCALAPPDATA "CodexSkin\recovery\engine\codex-skin.exe"
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
