# Resolve a compatible maintenance runtime among the installed pair.
# Kept in bin even when the running version rolls back to a legacy release.
function Get-DesktopMaintenanceVersion([string]$Root) {
  $state = Get-Content -Raw -LiteralPath (Join-Path $Root 'install.json') | ConvertFrom-Json
  foreach ($id in @($state.current, $state.previous)) {
    if (-not $id -or $id -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]+$') { continue }
    $version = Join-Path $Root "versions\$id"
    if (Test-Path -LiteralPath (Join-Path $version 'auriga\launcher\maintenance.ps1') -PathType Leaf) {
      return $version
    }
  }
  throw 'Missing compatible Desktop maintenance runtime.'
}
