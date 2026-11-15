# Stable login entry. Resolve the installed version at the time Windows signs in.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $root '.auriga-desktop-install') -PathType Leaf)) { exit 0 }
$current = (Get-Content -LiteralPath (Join-Path $root 'current.txt') -TotalCount 1).Trim()
if ($current -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]+$') { throw 'Invalid Desktop version pointer.' }
$version = Join-Path $root "versions\$current"
$tray = Join-Path $version 'auriga\tray\AurigaDesktop.exe'
if (Test-Path -LiteralPath $tray -PathType Leaf) {
  Start-Process -FilePath $tray -ArgumentList '/autostart' -WindowStyle Hidden -WorkingDirectory $root
} else {
  $node = Join-Path $version 'upstream\node\node.exe'
  $cli = Join-Path $version 'auriga\runtime\cli.mjs'
  # Older releases have a console launcher, without the tray control protocol.
  Start-Process -FilePath $node -ArgumentList @(('"' + $cli + '"'), 'launch', '--root', ('"' + $root + '"'), '--', '--no-open') -WindowStyle Hidden -WorkingDirectory $root
}
