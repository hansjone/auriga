$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
trap {
    [Console]::Error.WriteLine(('{0}: {1}' -f $_.Exception.GetBaseException().GetType().Name, $_.Exception.Message))
    exit 1
}
$directory = $env:AURIGA_RESOURCE_KEY_DIRECTORY
if ([string]::IsNullOrEmpty($directory)) {
    throw 'A new empty directory is required'
}
$info = Get-Item -LiteralPath $directory -Force
if (-not $info.PSIsContainer -or
    ($info.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -or
    @(Get-ChildItem -LiteralPath $directory -Force).Count -ne 0) {
    throw 'A new empty physical directory is required'
}
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
$owner = $info.GetAccessControl([System.Security.AccessControl.AccessControlSections]::Owner).GetOwner([System.Security.Principal.SecurityIdentifier])
if (-not $owner.Equals($sid)) {
    $ownerSecurity = New-Object System.Security.AccessControl.DirectorySecurity
    $ownerSecurity.SetOwner($sid)
    try {
        $info.SetAccessControl($ownerSecurity)
    } catch {
        throw "Cannot set resource key directory owner to the current user: $($_.Exception.Message)"
    }
}
$security = New-Object System.Security.AccessControl.DirectorySecurity
$security.SetAccessRuleProtection($true, $false)
$rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
    $sid,
    [System.Security.AccessControl.FileSystemRights]::FullControl,
    [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
    [System.Security.AccessControl.PropagationFlags]::None,
    [System.Security.AccessControl.AccessControlType]::Allow
)
$security.AddAccessRule($rule)
# The Node caller exclusively reserved this empty directory. Protect it before
# any private bytes are written and use the current SID, never OWNER RIGHTS.
$info.SetAccessControl($security)
if (-not (Get-Acl -LiteralPath $directory).AreAccessRulesProtected) {
    throw 'Private directory ACL protection failed'
}
if (-not (Get-Acl -LiteralPath $directory).GetOwner([System.Security.Principal.SecurityIdentifier]).Equals($sid)) {
    throw 'Private directory owner must be the current user'
}
