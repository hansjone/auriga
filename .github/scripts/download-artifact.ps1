function Get-VerifiedArtifact {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$Sha256
    )
    if (Test-Path -LiteralPath $Path) {
        if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Sha256) {
            throw "Cached artifact checksum mismatch: $Path"
        }
        return
    }
    [void](New-Item -ItemType Directory -Force (Split-Path -Parent $Path))
    $partial = "$Path.part"
    try {
        for ($attempt = 1; $attempt -le 3; $attempt++) {
            try {
                Invoke-WebRequest -Uri $Url -OutFile $partial -TimeoutSec 120 -UseBasicParsing
                break
            } catch {
                if ($attempt -eq 3) { throw }
                Write-Warning "Artifact download attempt $attempt failed; retrying: $Url"
                Start-Sleep -Seconds (2 * $attempt)
            }
        }
        if ((Get-FileHash -LiteralPath $partial -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Sha256) {
            throw "Downloaded artifact checksum mismatch: $Url"
        }
        Move-Item -LiteralPath $partial -Destination $Path
    } finally {
        if (Test-Path -LiteralPath $partial) { Remove-Item -LiteralPath $partial -Force }
    }
}
