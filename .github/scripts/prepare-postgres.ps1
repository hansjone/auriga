$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
. "$PSScriptRoot/download-artifact.ps1"

if ($env:RUNNER_ARCH -ne 'X64') { throw 'PostgreSQL CI cache supports only X64 runners' }
$platform = switch ($env:RUNNER_OS) {
    'Windows' { 'windows-amd64' }
    'Linux' { 'linux-amd64' }
    default { throw "Unsupported PostgreSQL CI platform: $env:RUNNER_OS" }
}
if (-not $env:AURIGA_PG_CACHE_DIR) { throw 'AURIGA_PG_CACHE_DIR is required' }
$manifest = Get-Content -Raw "$PSScriptRoot/postgres-artifacts.json" | ConvertFrom-Json
$name = "embedded-postgres-binaries-$platform-$($manifest.version)"
$jar = Join-Path $env:AURIGA_PG_CACHE_DIR "$name.jar"
$url = "https://repo.maven.apache.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-$platform/$($manifest.version)/$name.jar"
Get-VerifiedArtifact -Url $url -Path $jar -Sha256 $manifest.sha256.$platform

# Re-extract from the verified jar even on cache hits. Never trust a cached txz alone.
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::OpenRead($jar)
try {
    $entries = @($zip.Entries | Where-Object { $_.Name.EndsWith('.txz') })
    if ($entries.Count -ne 1) { throw 'Expected exactly one PostgreSQL txz in the verified jar' }
    [IO.Compression.ZipFileExtensions]::ExtractToFile(
        $entries[0], (Join-Path $env:AURIGA_PG_CACHE_DIR "$name.txz"), $true)
} finally {
    $zip.Dispose()
}
