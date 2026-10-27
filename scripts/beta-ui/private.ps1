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
  # Only Access is modified: do not read/write SACL or request SeSecurityPrivilege.
  # Use .NET directly so a PowerShell 7 parent's modules cannot replace 5.1 cmdlets.
  $acl.SetAccessRuleProtection($true, $false)
  $acl.AddAccessRule($rule)
  $item.SetAccessControl($acl)
}
$sections = [Security.AccessControl.AccessControlSections]::Access -bor [Security.AccessControl.AccessControlSections]::Owner
$actual = $item.GetAccessControl($sections)
if ($actual.GetOwner([Security.Principal.SecurityIdentifier]) -ne $owner -or -not $actual.AreAccessRulesProtected) {
  throw 'Beta path owner or inheritance verification failed'
}
foreach ($entry in $actual.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
  if ($entry.AccessControlType -eq 'Allow' -and $entry.IdentityReference -ne $owner) {
    throw 'Beta path ACL verification failed'
  }
}
