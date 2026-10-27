param([string]$WorktreeRoot = (Get-Location).Path)
$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSEdition -ne 'Desktop' -or
    $PSVersionTable.PSVersion.Major -ne 5 -or $PSVersionTable.PSVersion.Minor -ne 1) {
  throw 'Run this regression under Windows PowerShell 5.1'
}
if (-not (Test-Path -LiteralPath (Join-Path $WorktreeRoot 'mvnw.cmd'))) { throw 'Supply worktree root' }
. (Join-Path $PSScriptRoot 'verify-durable-send.ps1') -FunctionsOnly
$savedOutput = $OutputEncoding
$savedConsole = [Console]::OutputEncoding
try {
  # Reproduce the root reviewer's inherited profile. A PS native pipeline would
  # prefix the first filename with EF BB BF, so Git could not open .editorconfig.
  $OutputEncoding = New-Object Text.UTF8Encoding($true)
  [Console]::OutputEncoding = New-Object Text.UTF8Encoding($true)
  $paths = @('.editorconfig', 'contracts/network/durable-send.md',
    'services/collab/impl/src/test/resources/network-durable-send/verify-durable-send.ps1')
  $expected = @()
  foreach ($path in $paths) {
    $value = & git -C $WorktreeRoot hash-object -- $path
    if ($LASTEXITCODE -ne 0) { throw 'Cannot compute expected Git blob' }
    $expected += $value
  }
  $actual = @(Get-SendArchiveBlobs $WorktreeRoot $paths)
  if ($actual.Count -ne $paths.Count) { throw 'Inventory count differs' }
  for ($i = 0; $i -lt $paths.Count; $i++) {
    if ($actual[$i] -cne $expected[$i]) { throw "Wrong blob or BOM in inventory at index $i" }
  }
  $relative = 'target/send-rework1/' + [Guid]::NewGuid().ToString('N') + '/space-' + [char]0x6d4b + [char]0x8bd5 + '.txt'
  $unicodePath = Join-Path $WorktreeRoot $relative
  New-Item -ItemType Directory -Path (Split-Path -Parent $unicodePath) | Out-Null
  [IO.File]::WriteAllText($unicodePath, 'utf8 filename regression', (New-Object Text.UTF8Encoding($false)))
  $expectedUnicode = & git -C $WorktreeRoot hash-object -- $relative
  if ($LASTEXITCODE -ne 0) { throw 'Cannot compute expected Unicode Git blob' }
  $actualUnicode = @(Get-SendArchiveBlobs $WorktreeRoot @($relative))
  if ($actualUnicode.Count -ne 1 -or $actualUnicode[0] -cne $expectedUnicode) { throw 'Unicode or space in filename changed on stdin' }
  $rejected = $false
  try { Get-SendArchiveBlobs $WorktreeRoot @('target/send-rework1/does-not-exist') | Out-Null }
  catch { $rejected = $_.Exception.Message -like 'Cannot hash archive inventory:*' }
  if (-not $rejected) { throw 'Native Git failure was accepted as a valid inventory' }
  if ($OutputEncoding.GetPreamble().Length -ne 3 -or [Console]::OutputEncoding.GetPreamble().Length -ne 3) {
    throw 'Hash helper changed inherited output encodings'
  }
  Write-Output 'PASS: first filename has no BOM; all three blobs match under Windows PowerShell 5.1 with BOM output encodings'
  Write-Output 'PASS: UTF-8/space filename preserved; native Git failure rejected; inherited encodings unchanged'
} finally {
  $OutputEncoding = $savedOutput
  [Console]::OutputEncoding = $savedConsole
}
