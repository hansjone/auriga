$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/download-artifact.ps1"

$testRoot = Join-Path $PSScriptRoot ('.artifact-test-' + [guid]::NewGuid())
[void](New-Item -ItemType Directory $testRoot)
$script:calls = 0
$script:failures = 0
function Invoke-WebRequest {
    param($Uri, $OutFile, $TimeoutSec, [switch]$UseBasicParsing)
    $script:calls++
    if ($script:calls -le $script:failures) { throw 'temporary network failure' }
    [IO.File]::WriteAllText($OutFile, 'verified artifact')
}
function Start-Sleep { param($Seconds) }
function Assert-True($Condition, $Message) {
    if (-not $Condition) { throw $Message }
}
try {
    $file = Join-Path $testRoot 'artifact.jar'
    [IO.File]::WriteAllText($file, 'verified artifact')
    $sha = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
    Get-VerifiedArtifact -Url 'https://example.com/artifact.jar' -Path $file -Sha256 $sha
    Assert-True ($script:calls -eq 0) 'cache hit downloaded again'
    Write-Output 'PASS: verified cache hit'

    Remove-Item -LiteralPath $file
    $script:failures = 2
    Get-VerifiedArtifact -Url 'https://example.com/artifact.jar' -Path $file -Sha256 $sha
    Assert-True ($script:calls -eq 3) 'transient failures were not retried exactly twice'
    Assert-True (Test-Path -LiteralPath $file) 'verified download was not published'
    Write-Output 'PASS: transient failure recovery'

    Remove-Item -LiteralPath $file
    $script:calls = 0
    $script:failures = 3
    $failed = $false
    try { Get-VerifiedArtifact -Url 'https://example.com/artifact.jar' -Path $file -Sha256 $sha } catch { $failed = $true }
    Assert-True ($failed -and $script:calls -eq 3) 'retry exhaustion did not fail after three attempts'
    Assert-True (-not (Test-Path -LiteralPath $file)) 'failed download was published'
    Assert-True (-not (Test-Path -LiteralPath "$file.part")) 'partial download was retained'
    Write-Output 'PASS: bounded retries and partial cleanup'

    $script:calls = 0
    $script:failures = 0
    $failed = $false
    try { Get-VerifiedArtifact -Url 'https://example.com/artifact.jar' -Path $file -Sha256 ('0' * 64) } catch { $failed = $true }
    Assert-True ($failed -and $script:calls -eq 1) 'bad download checksum was accepted or retried'
    Assert-True (-not (Test-Path -LiteralPath $file)) 'unverified download was published'
    Write-Output 'PASS: download checksum rejection'

    [IO.File]::WriteAllText($file, 'tampered cache')
    $script:calls = 0
    $failed = $false
    try { Get-VerifiedArtifact -Url 'https://example.com/artifact.jar' -Path $file -Sha256 $sha } catch { $failed = $true }
    Assert-True ($failed -and $script:calls -eq 0) 'bad cache checksum was accepted or silently replaced'
    Write-Output 'PASS: cache checksum rejection'
} finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force
}
