param([string]$Node, [string]$Repository, [string]$Output)
$ErrorActionPreference = 'Stop'
$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object System.Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    [Console]::WriteLine('SKIP: Administrators default-owner simulation requires an elevated token')
    exit 0
}
Add-Type -Path (Join-Path $PSScriptRoot 'windows-token-owner.cs')
$previousOwner = [ResourceTestTokenOwner]::Get()
try {
    # Set only this test process's token default owner, inherited by its Node child.
    [ResourceTestTokenOwner]::Set('S-1-5-32-544')
    $proof = Join-Path $Output 'default-owner-proof'
    [System.IO.File]::WriteAllText($proof, '')
    if ((Get-Acl -LiteralPath $proof).GetOwner([System.Security.Principal.SecurityIdentifier]).Value -ne 'S-1-5-32-544') {
        throw 'The test did not create an Administrators-owned file'
    }
    [Console]::WriteLine('DefaultOwnerAdministrators=True')
    $keys = Join-Path $Output 'keys'
    & $Node (Join-Path $Repository 'scripts/resource-keys/generate.mjs') --out $keys --kid-prefix administrator-test
    if ($LASTEXITCODE -ne 0) { throw 'Generator failed with the Administrators default owner' }
    $items = @(Get-Item -LiteralPath $keys) + @(Get-ChildItem -LiteralPath $keys -Recurse)
    foreach ($item in $items) {
        $acl = Get-Acl -LiteralPath $item.FullName
        if (-not $acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Equals($identity.User)) {
            throw "The generated path still has the default Administrators owner: $($item.Name)"
        }
        $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
        if (-not $acl.AreAccessRulesProtected -or $rules.Count -ne 1 -or
            -not $rules[0].IdentityReference.Equals($identity.User) -or
            $rules[0].AccessControlType -ne 'Allow' -or $rules[0].FileSystemRights -ne 'FullControl') {
            throw "The generated path has an unexpected ACL: $($item.Name)"
        }
    }
    [Console]::WriteLine('AllGeneratedOwnersAndAclsVerified=True')
} finally {
    [ResourceTestTokenOwner]::Set($previousOwner)
}
