param([Parameter(Mandatory=$true)][string]$Path, [switch]$VerifyOnly)
$ErrorActionPreference = 'Stop'
$full = [IO.Path]::GetFullPath($Path)
$item = Get-Item -LiteralPath $full -Force
if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Beta path must not be a reparse point' }
$owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
if ($item.PSIsContainer) {
  $acl = New-Object Security.AccessControl.DirectorySecurity
  $rule = New-Object Security.AccessControl.FileSystemAccessRule($owner, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
} else {
  $acl = New-Object Security.AccessControl.FileSecurity
  $rule = New-Object Security.AccessControl.FileSystemAccessRule($owner, 'FullControl', 'Allow')
}
if (-not $VerifyOnly) {
  $acl.SetOwner($owner)
  $acl.SetAccessRuleProtection($true, $false)
  $acl.AddAccessRule($rule)
  Set-Acl -LiteralPath $full -AclObject $acl
}
$actual = Get-Acl -LiteralPath $full
if ($actual.Owner -ne $owner.Translate([Security.Principal.NTAccount]).Value -or -not $actual.AreAccessRulesProtected) {
  throw 'Beta path owner or inheritance verification failed'
}
foreach ($entry in $actual.Access) {
  if ($entry.AccessControlType -eq 'Allow' -and $entry.IdentityReference.Translate([Security.Principal.SecurityIdentifier]) -ne $owner) {
    throw 'Beta path ACL verification failed'
  }
}
