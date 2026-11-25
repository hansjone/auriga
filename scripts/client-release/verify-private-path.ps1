$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
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
} catch {
    # Known validation reasons contain no paths or key bytes. Other failures
    # expose only the exception type; Node also sanitizes the process output.
    $reason = if ($_.Exception.Message -in @('indirect path', 'private ACL required')) { $_.Exception.Message } else { $_.Exception.GetBaseException().GetType().Name }
    [Console]::Error.WriteLine($reason)
    exit 1
}
