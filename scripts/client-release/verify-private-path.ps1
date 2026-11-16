$ErrorActionPreference = 'Stop'
try {
    $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $item = Get-Item -LiteralPath $env:AURIGA_RESOURCE_KEY_PATH -Force
    if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) { throw 'indirect path' }
    $acl = Get-Acl -LiteralPath $item.FullName
    $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    if (-not $acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Equals($sid) -or
        -not $acl.AreAccessRulesProtected -or $rules.Count -ne 1) { throw 'private ACL required' }
    $rule = $rules[0]
    if (-not $rule.IdentityReference.Equals($sid) -or $rule.IsInherited -or
        $rule.AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow -or
        $rule.FileSystemRights -ne [System.Security.AccessControl.FileSystemRights]::FullControl) { throw 'private ACL required' }
} catch { exit 1 }
