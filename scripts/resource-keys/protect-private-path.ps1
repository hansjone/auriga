param([Parameter(Mandatory = $true)][ValidateSet('directory', 'file', 'verify')][string]$Kind)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
trap {
    [Console]::Error.WriteLine(('{0}: {1}' -f $_.Exception.GetBaseException().GetType().Name, $_.Exception.Message))
    exit 1
}
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User

function Assert-PrivatePath($info) {
    if ($info.Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
        throw "Indirect resource key path: $($info.FullName)"
    }
    $acl = Get-Acl -LiteralPath $info.FullName
    if (-not $acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Equals($sid)) {
        throw "Resource key owner must be the current user: $($info.FullName)"
    }
    $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    if (-not $acl.AreAccessRulesProtected -or $rules.Count -ne 1) {
        throw "Resource key ACL must be protected with one current-user rule: $($info.FullName)"
    }
    $rule = $rules[0]
    if (-not $rule.IdentityReference.Equals($sid) -or
        $rule.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow -or
        $rule.FileSystemRights -ne [System.Security.AccessControl.FileSystemRights]::FullControl -or
        $rule.IsInherited) {
        throw "Resource key ACL must grant only the current user full control: $($info.FullName)"
    }
}

$path = $env:AURIGA_RESOURCE_KEY_PATH
if ([string]::IsNullOrEmpty($path)) {
    throw 'Resource key path is required'
}
$info = Get-Item -LiteralPath $path -Force
if ($Kind -eq 'verify') {
    foreach ($relative in @('', 'platform', 'offline', 'platform/release.pk8', 'platform/revocation.pk8', 'offline/downgrade.pk8', 'resource-roots.json', 'platform.properties')) {
        Assert-PrivatePath (Get-Item -LiteralPath (Join-Path $path $relative) -Force)
    }
    exit 0
}
if (($info.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -or
    ($Kind -eq 'directory' -and (-not $info.PSIsContainer -or @(Get-ChildItem -LiteralPath $path -Force).Count -ne 0)) -or
    ($Kind -eq 'file' -and ($info.PSIsContainer -or $info.Length -ne 0))) {
    throw "A new empty physical resource key $Kind is required"
}
$security = if ($info.PSIsContainer) {
    New-Object System.Security.AccessControl.DirectorySecurity
} else {
    New-Object System.Security.AccessControl.FileSecurity
}
$security.SetAccessRuleProtection($true, $false)
$inheritance = if ($info.PSIsContainer) {
    [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
} else {
    [System.Security.AccessControl.InheritanceFlags]::None
}
$rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
    $sid,
    [System.Security.AccessControl.FileSystemRights]::FullControl,
    $inheritance,
    [System.Security.AccessControl.PropagationFlags]::None,
    [System.Security.AccessControl.AccessControlType]::Allow
)
$security.AddAccessRule($rule)
# The caller exclusively reserved this empty path. Protect it before private
# bytes are written, including when the token's default owner is Administrators.
$info.SetAccessControl($security)
# Harden the empty object first. Its default Administrators owner may have
# WRITE_DAC while an inherited deny still prevents WRITE_OWNER.
$owner = $info.GetAccessControl([System.Security.AccessControl.AccessControlSections]::Owner).GetOwner([System.Security.Principal.SecurityIdentifier])
if (-not $owner.Equals($sid)) {
    $ownerSecurity = if ($info.PSIsContainer) {
        New-Object System.Security.AccessControl.DirectorySecurity
    } else {
        New-Object System.Security.AccessControl.FileSecurity
    }
    $ownerSecurity.SetOwner($sid)
    try {
        $info.SetAccessControl($ownerSecurity)
    } catch {
        throw "Cannot set resource key $Kind owner to the current user: $($_.Exception.Message)"
    }
}
Assert-PrivatePath $info
