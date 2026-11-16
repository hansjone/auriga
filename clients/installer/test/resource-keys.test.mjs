import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createPrivateKey, createPublicKey, sign, verify } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { tempBase } from './helpers.mjs';
import { generateKeys } from '../../../scripts/resource-keys/generate.mjs';
import { validateResourceRoots } from '../scripts/build.mjs';

const windowsPowerShell = join(
  process.env.SystemRoot ?? 'C:\\Windows',
  'System32',
  'WindowsPowerShell',
  'v1.0',
  'powershell.exe',
);

function windowsPowerShellEnvironment() {
  return Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) => name.toLowerCase() !== 'psmodulepath',
    ),
  );
}

test(
  'Windows resource key generator isolates inherited PowerShell module paths',
  {
    skip: process.platform !== 'win32',
  },
  (t) => {
    const base = tempBase(t);
    const modules = join(base, 'pwsh7 modules');
    const security = join(modules, 'Microsoft.PowerShell.Security');
    mkdirSync(security, { recursive: true });
    writeFileSync(
      join(security, 'Microsoft.PowerShell.Security.psd1'),
      "@{ RootModule = 'Security.psm1'; ModuleVersion = '1.0.0'; FunctionsToExport = @('Get-Acl') }",
    );
    writeFileSync(
      join(security, 'Security.psm1'),
      "function Get-Acl { throw 'PSModulePath contamination reached ACL helper' }\nExport-ModuleMember -Function Get-Acl\n",
    );
    const environment = {
      ...windowsPowerShellEnvironment(),
      PSModulePath: modules,
    };
    const control = spawnSync(
      windowsPowerShell,
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        'Get-Acl -LiteralPath $env:TEMP',
      ],
      { env: environment, encoding: 'utf8' },
    );
    assert.notEqual(control.status, 0);
    assert.match(
      control.stderr,
      /PSModulePath contamination reached ACL helper/,
    );
    const script = fileURLToPath(
      new URL('../../../scripts/resource-keys/generate.mjs', import.meta.url),
    );
    for (const [index, name] of ['PSModulePath', 'psmodulepath'].entries()) {
      const output = join(base, `keys-${index}`);
      const result = spawnSync(
        process.execPath,
        [script, '--out', output, '--kid-prefix', 'isolated'],
        {
          env: { ...windowsPowerShellEnvironment(), [name]: modules },
          encoding: 'utf8',
        },
      );
      assert.equal(result.status, 0, result.stderr);
      assert.match(
        JSON.parse(result.stdout).resourceRootsSha256,
        /^[a-f0-9]{64}$/,
      );
      assert.equal(
        (result.stdout + result.stderr).includes('contamination'),
        false,
      );
      const trust = JSON.parse(
        readFileSync(join(output, 'resource-roots.json')),
      );
      assert.doesNotThrow(() => validateResourceRoots(trust));
    }
  },
);

test(
  'Windows resource key generation succeeds when the parent denies WRITE_OWNER',
  {
    skip: process.platform !== 'win32',
  },
  (t) => {
    const parent = join(tempBase(t), 'restricted parent');
    mkdirSync(parent);
    const setup = spawnSync(
      windowsPowerShell,
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `
    $ErrorActionPreference = 'Stop'
    $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = New-Object System.Security.AccessControl.DirectorySecurity
    $security.SetAccessRuleProtection($true, $false)
    $inherit = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
    $security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'Modify', $inherit, 'None', 'Allow')))
    $security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'TakeOwnership', $inherit, 'None', 'Deny')))
    (Get-Item -LiteralPath $env:RESOURCE_TEST_PARENT).SetAccessControl($security)
    $blocked = New-Item -ItemType Directory -Path (Join-Path $env:RESOURCE_TEST_PARENT 'blocked')
    $blockedSecurity = New-Object System.Security.AccessControl.DirectorySecurity
    $blockedSecurity.SetAccessRuleProtection($true, $false)
    $blockedSecurity.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'Modify', $inherit, 'None', 'Allow')))
    $blockedSecurity.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'ChangePermissions', $inherit, 'None', 'Deny')))
    $ownerRights = New-Object System.Security.Principal.SecurityIdentifier('S-1-3-4')
    $blockedSecurity.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($ownerRights, 'ChangePermissions', $inherit, 'None', 'Deny')))
    $blocked.SetAccessControl($blockedSecurity)
    $probe = New-Item -ItemType Directory -Path (Join-Path $env:RESOURCE_TEST_PARENT 'probe')
    if ((Get-Acl -LiteralPath $probe.FullName).GetOwner([System.Security.Principal.SecurityIdentifier]).Value -ne $sid.Value) { throw 'Probe owner must be the current user' }
    $ownerOnly = New-Object System.Security.AccessControl.DirectorySecurity
    $ownerOnly.SetOwner($sid)
    try {
      $probe.SetAccessControl($ownerOnly)
    } catch [System.UnauthorizedAccessException] {
      [Console]::WriteLine('WRITE_OWNER denied as required')
      exit 0
    }
    [Console]::WriteLine('WRITE_OWNER deny bypassed by the current Windows token')
  `,
      ],
      {
        encoding: 'utf8',
        env: {
          ...windowsPowerShellEnvironment(),
          RESOURCE_TEST_PARENT: parent,
        },
      },
    );
    assert.equal(setup.status, 0, setup.stderr);
    if (
      setup.stdout.includes(
        'WRITE_OWNER deny bypassed by the current Windows token',
      )
    ) {
      t.skip(
        'The current Windows token can set the owner despite the deny ACL, so WRITE_OWNER restrictions cannot be exercised',
      );
      return;
    }
    assert.match(setup.stdout, /WRITE_OWNER denied as required/);
    const { directory, trust } = generateKeys(
      join(parent, 'keys'),
      'restricted',
    );
    assert.deepEqual(
      JSON.parse(readFileSync(join(directory, 'resource-roots.json'))),
      trust,
    );
    for (const role of ['release', 'revocation', 'downgrade']) {
      const file = join(
        directory,
        role === 'downgrade' ? 'offline' : 'platform',
        `${role}.pk8`,
      );
      assert.equal(
        createPrivateKey({
          key: readFileSync(file),
          type: 'pkcs8',
          format: 'der',
        }).asymmetricKeyType,
        'ed25519',
      );
    }
    const blockedOutput = join(parent, 'blocked', 'keys');
    assert.throws(
      () => generateKeys(blockedOutput, 'blocked'),
      /Cannot protect new key directory:[\s\S]*UnauthorizedAccessException/,
    );
    assert.equal(existsSync(join(blockedOutput, 'platform')), false);
  },
);

test('resource keys are independent PKCS#8 DER with matching SPKI roots and protected storage', (t) => {
  const directory = join(tempBase(t), 'keys with spaces');
  const { trust } = generateKeys(directory, 'dev-example');
  assert.deepEqual(validateResourceRoots(trust), trust);
  assert.deepEqual(
    JSON.parse(readFileSync(join(directory, 'resource-roots.json'))),
    trust,
  );
  for (const role of ['release', 'revocation', 'downgrade']) {
    const folder = join(
      directory,
      role === 'downgrade' ? 'offline' : 'platform',
    );
    const file = join(folder, `${role}.pk8`);
    const bytes = readFileSync(file);
    assert.ok(bytes.length <= 4096);
    const privateKey = createPrivateKey({
      key: bytes,
      type: 'pkcs8',
      format: 'der',
    });
    assert.equal(privateKey.asymmetricKeyType, 'ed25519');
    assert.deepEqual(
      privateKey.export({ type: 'pkcs8', format: 'der' }),
      bytes,
    );
    const publicKey = createPublicKey({
      key: Buffer.from(trust[role].publicKeySpkiBase64, 'base64'),
      type: 'spki',
      format: 'der',
    });
    const message = Buffer.from('resource-key-format-check');
    assert.equal(
      verify(null, message, publicKey, sign(null, message, privateKey)),
      true,
    );
    assert.equal(trust[role].kid, `dev-example-${role}`);
    if (process.platform !== 'win32') {
      assert.equal(statSync(folder).mode & 0o777, 0o700);
      assert.equal(statSync(file).mode & 0o777, 0o600);
    }
  }
  if (process.platform === 'win32') {
    const check = spawnSync(
      windowsPowerShell,
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `
      $ErrorActionPreference = 'Stop'
      $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
      $items = @(Get-Item -LiteralPath $env:RESOURCE_TEST_DIRECTORY) + @(Get-ChildItem -LiteralPath $env:RESOURCE_TEST_DIRECTORY -Recurse)
      foreach ($item in $items) {
        $acl = Get-Acl -LiteralPath $item.FullName
        if ($item.PSIsContainer -and !$acl.AreAccessRulesProtected) { throw 'inherited directory ACL' }
        foreach ($rule in $acl.Access) {
          if ($rule.AccessControlType -eq 'Allow' -and $rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value -ne $sid) { throw 'exposed key ACL' }
        }
      }
    `,
      ],
      {
        encoding: 'utf8',
        env: {
          ...windowsPowerShellEnvironment(),
          RESOURCE_TEST_DIRECTORY: directory,
        },
      },
    );
    assert.equal(check.status, 0, check.stderr);
  }
  const properties = readFileSync(
    join(directory, 'platform.properties'),
    'utf8',
  );
  assert.match(properties, /auriga\.resource\.release-kid=dev-example-release/);
  assert.match(
    properties,
    /auriga\.resource\.revocation-key-file=\/run\/secrets\/auriga-resource\/revocation.pk8/,
  );
  assert.equal(properties.includes('downgrade'), false);
});

test('resource key generation refuses overwrite, invalid kid and indirect parents', (t) => {
  const base = tempBase(t);
  const directory = join(base, 'keys');
  generateKeys(directory, 'one');
  const original = readFileSync(join(directory, 'platform/release.pk8'));
  assert.throws(() => generateKeys(directory, 'two'), /exist/i);
  assert.deepEqual(
    readFileSync(join(directory, 'platform/release.pk8')),
    original,
  );
  assert.throws(() => generateKeys(join(base, 'invalid'), '../bad'), /kid/i);
  const alias = join(base, 'alias');
  symlinkSync(
    directory,
    alias,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.throws(
    () => generateKeys(join(alias, 'other'), 'valid'),
    /indirect|symbolic/i,
  );
});

test('resource key CLI prints only public metadata and refuses an existing output', (t) => {
  const directory = join(tempBase(t), 'cli');
  const script = fileURLToPath(
    new URL('../../../scripts/resource-keys/generate.mjs', import.meta.url),
  );
  const args = [script, '--out', directory, '--kid-prefix', 'cli-example'];
  const result = spawnSync(process.execPath, args, { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.match(JSON.parse(result.stdout).resourceRootsSha256, /^[a-f0-9]{64}$/);
  const combined = result.stdout + result.stderr;
  for (const role of ['release', 'revocation', 'downgrade']) {
    const bytes = readFileSync(
      join(
        directory,
        role === 'downgrade' ? 'offline' : 'platform',
        `${role}.pk8`,
      ),
    );
    assert.equal(combined.includes(bytes.toString('base64')), false);
    assert.equal(combined.includes(bytes.toString('hex')), false);
  }
  assert.equal(spawnSync(process.execPath, args).status, 1);
});
