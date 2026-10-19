# M1 smoke helpers (does not start long-running servers).
# Prerequisites: ctl start, platform-app --spring.profiles.active=combined, optional edge.
param(
  [string]$PortalBase = "http://127.0.0.1:8081",
  [string]$HostHeader = "portal.example.com"
)

$ErrorActionPreference = "Stop"
$headers = @{
  "Host" = $HostHeader
  "X-Auriga-Client-IP" = "192.0.2.10"
}

Write-Host "GET $PortalBase/actuator/health"
$health = Invoke-WebRequest -Uri "$PortalBase/actuator/health" -Headers $headers -UseBasicParsing
if ($health.StatusCode -ne 200) { throw "health $($health.StatusCode)" }

Write-Host "GET $PortalBase/api/iam/v1/login/providers"
$providers = Invoke-WebRequest -Uri "$PortalBase/api/iam/v1/login/providers" -Headers $headers -UseBasicParsing
if ($providers.StatusCode -ne 200) { throw "providers $($providers.StatusCode)" }
if ($providers.Content -notmatch "team-account") { throw "missing team-account provider" }
if ($providers.Content -notmatch "emergency") { throw "missing emergency provider" }

Write-Host "OK: health + login providers"
if (Test-Path ".dev/emergency-enrollment.json") {
  Write-Host "Enrollment file present (credentials not printed)."
} else {
  Write-Host "WARN: .dev/emergency-enrollment.json missing — seed may not have run."
}
