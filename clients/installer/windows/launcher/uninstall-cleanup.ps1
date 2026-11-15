# Per-user RunOnce removes only the generated program quarantine.
$ErrorActionPreference = 'Stop'
$plan = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'cleanup.json') | ConvertFrom-Json
$id = [guid]::Parse($plan.id).ToString()
if ($plan.schema -ne 1 -or $plan.PSObject.Properties['paths'] -or
    (Split-Path -Leaf $PSScriptRoot) -ne $id -or
    (Split-Path -Leaf (Split-Path -Parent $PSScriptRoot)) -ne 'AurigaDesktopCleanup') {
  throw 'Invalid program cleanup record.'
}
$root = [IO.Path]::GetFullPath($plan.root)
if ($root -ne $plan.root -or $root -eq [IO.Path]::GetPathRoot($root) -or $root -notmatch '^[A-Z]:\\') {
  throw 'Invalid install root for program cleanup.'
}
function Assert-NotReparse([string]$Path) {
  for ($current = $Path; $current; $current = [IO.Path]::GetDirectoryName($current)) {
    if ((Test-Path -LiteralPath $current) -and
        ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      throw 'Reparse point in cleanup path.'
    }
  }
}
Assert-NotReparse $PSScriptRoot
$guard = Join-Path $root '.auriga-desktop-uninstall-pending'
$trash = Join-Path $root ".uninstall-$id"
Assert-NotReparse $guard
Assert-NotReparse $trash
if ((Test-Path -LiteralPath $guard -PathType Leaf) -and (Get-Content -Raw -LiteralPath $guard) -ceq $id) {
  if ([IO.Directory]::Exists($trash)) {
    # Directory.Delete removes nested NTFS junctions without following their targets.
    [IO.Directory]::Delete($trash, $true)
  }
  [IO.File]::Delete($guard)
}
# Remove only our own job files. Never remove the install root.
[IO.File]::Delete((Join-Path $PSScriptRoot 'cleanup.json'))
[IO.File]::Delete($PSCommandPath)
[IO.Directory]::Delete($PSScriptRoot)
