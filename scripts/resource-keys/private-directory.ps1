$ErrorActionPreference = 'Stop'
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
$security = New-Object System.Security.AccessControl.DirectorySecurity
$security.SetOwner($sid)
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
