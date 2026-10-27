$ErrorActionPreference = 'Stop'
$current = [Security.Principal.WindowsIdentity]::GetCurrent().User
$administrators = [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
$global:betaOwnerUnitTarget = [IO.Path]::GetFullPath('C:\beta-owner-unit')

# Model the filesystem's merge of the supplied Owner/Access sections. No privileged
# filesystem operation is needed to start with an Administrators-owned descriptor.
function Get-Item {
  param([string]$LiteralPath, [switch]$Force)
  if ($LiteralPath -ne $global:betaOwnerUnitTarget) { throw 'Unexpected unit path' }
  return $global:betaOwnerUnitItem
}

foreach ($directory in @($true, $false)) {
  $security = if ($directory) {
    [Security.AccessControl.DirectorySecurity]::new()
  } else {
    [Security.AccessControl.FileSecurity]::new()
  }
  $security.SetOwner($administrators)
  $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators, 'FullControl', 'Allow'))
  $global:betaOwnerUnitItem = [pscustomobject]@{
    PSIsContainer = $directory
    Attributes = if ($directory) { [IO.FileAttributes]::Directory } else { [IO.FileAttributes]::Normal }
    Security = $security
    Writes = 0
  }
  $global:betaOwnerUnitItem | Add-Member ScriptMethod SetAccessControl {
    param($incoming)
    $access = [Security.AccessControl.AccessControlSections]::Access
    if ($incoming.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Audit)) {
      throw 'Must not write an audit descriptor'
    }
    $nextOwner = $incoming.GetOwner([Security.Principal.SecurityIdentifier])
    if ($null -ne $nextOwner) { $this.Security.SetOwner($nextOwner) }
    $this.Security.SetSecurityDescriptorSddlForm($incoming.GetSecurityDescriptorSddlForm($access), $access)
    $this.Writes++
  }
  $global:betaOwnerUnitItem | Add-Member ScriptMethod GetAccessControl {
    param($sections)
    $expected = [Security.AccessControl.AccessControlSections]::Access -bor [Security.AccessControl.AccessControlSections]::Owner
    if ($sections -ne $expected) { throw 'Must only read Access and Owner' }
    return $this.Security
  }

  if ($global:betaOwnerUnitItem.Security.GetOwner([Security.Principal.SecurityIdentifier]) -ne $administrators) {
    throw 'Fixture must start with Administrators ownership'
  }
  & (Join-Path $PSScriptRoot '../../scripts/beta-ui/private.ps1') -Path $global:betaOwnerUnitTarget
  & (Join-Path $PSScriptRoot '../../scripts/beta-ui/private.ps1') -Path $global:betaOwnerUnitTarget -VerifyOnly
  if ($global:betaOwnerUnitItem.Writes -ne 1 -or $global:betaOwnerUnitItem.Security.GetOwner([Security.Principal.SecurityIdentifier]) -ne $current) {
    throw 'Restriction must replace the group owner with the current user'
  }
  $rules = $global:betaOwnerUnitItem.Security.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])
  if ($rules.Count -ne 1 -or $rules[0].IdentityReference -ne $current -or -not $global:betaOwnerUnitItem.Security.AreAccessRulesProtected) {
    throw 'Restriction must install exactly the current user with protected access'
  }
  # Verification must not repair an unsafe path or accept a privileged group owner.
  foreach ($unsafe in @('group-owner', 'inheritance', 'extra-reader')) {
    $global:betaOwnerUnitItem.Security.SetOwner($current)
    $global:betaOwnerUnitItem.Security.SetAccessRuleProtection($true, $false)
    $expected = 'Beta path owner or inheritance verification failed'
    switch ($unsafe) {
      'group-owner' { $global:betaOwnerUnitItem.Security.SetOwner($administrators) }
      'inheritance' { $global:betaOwnerUnitItem.Security.SetAccessRuleProtection($false, $false) }
      'extra-reader' {
        $global:betaOwnerUnitItem.Security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators, 'Read', 'Allow'))
        $expected = 'Beta path ACL verification failed'
      }
    }
    $rejected = $false
    try {
      & (Join-Path $PSScriptRoot '../../scripts/beta-ui/private.ps1') -Path $global:betaOwnerUnitTarget -VerifyOnly
    } catch {
      if ($_.Exception.Message -ne $expected) { throw }
      $rejected = $true
    }
    if (-not $rejected -or $global:betaOwnerUnitItem.Writes -ne 1) { throw 'Verify-only must reject without rewriting ownership' }
  }
}
