# Per-user RunOnce job for files that remained in use during uninstall.
$ErrorActionPreference = 'Stop'
$plan = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'cleanup.json') | ConvertFrom-Json
$guard = Join-Path $plan.root '.auriga-desktop-uninstall-pending'
if ((Test-Path -LiteralPath $guard) -and (Get-Content -Raw -LiteralPath $guard) -eq $plan.id) {
  foreach ($path in $plan.paths) {
    if ([IO.Directory]::Exists($path)) {
      # Directory.Delete removes NTFS junctions without following their targets.
      [IO.Directory]::Delete($path, $true)
    } elseif ([IO.File]::Exists($path)) {
      [IO.File]::Delete($path)
    }
  }
  [IO.File]::Delete($guard)
  if ([IO.Directory]::GetFileSystemEntries($plan.root).Length -eq 0) {
    [IO.Directory]::Delete($plan.root)
  }
}
[IO.Directory]::Delete($PSScriptRoot, $true)
