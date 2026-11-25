import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { safeDiagnostic, privateKeyFailure } from './diagnostics.mjs';
import { formatCliError, normalizeBuildAliases } from './cli.mjs';
import { launchOfflineBuild } from './build.mjs';
import childProcess from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { syncBuiltinESMExports } from 'node:module';
import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { assertPrivateKey, fingerprint, generateDesktopKeys } from './keys.mjs';
import { verifySignedInventory } from './trust.mjs';

function temporary(t) {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-release-diagnostic-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

const keyPath = 'C:\\secure\\private-name.pem';
const auth = JSON.stringify({
  cookie: 'session=secret-cookie',
  csrf: 'secret-csrf',
});
test('ACL diagnostics preserve exit/spawn classes and bounded sanitized streams without policy bypass', () => {
  const result = {
    status: 1,
    stderr: `AuthorizationManager check failed. UnauthorizedAccess ${keyPath}\n-----BEGIN PRIVATE KEY-----\nprivate-key-bytes\n-----END PRIVATE KEY-----\n${auth}`,
    stdout: 'private ACL required ' + 'x'.repeat(6000),
  };
  const error = privateKeyFailure(result, keyPath, {
    AURIGA_CLIENT_ADMIN_AUTH: auth,
  });
  assert.match(error.message, /exit=1/);
  assert.match(error.message, /stderr=.*AuthorizationManager/);
  assert.match(error.message, /stdout=.*private ACL required/);
  assert.match(error.message, /执行策略/);
  assert.ok(error.message.length < 5000);
  for (const secret of [
    keyPath,
    'private-name',
    'private-key-bytes',
    'secret-cookie',
    'secret-csrf',
  ])
    assert.ok(!error.message.includes(secret), secret);
  assert.match(
    privateKeyFailure(
      {
        status: null,
        error: Object.assign(new Error('spawn failed'), { code: 'ENOENT' }),
      },
      keyPath,
    ).message,
    /spawn=ENOENT/,
  );
  assert.match(
    privateKeyFailure({ status: null, signal: 'SIGTERM' }, keyPath).message,
    /signal=SIGTERM/,
  );
  assert.match(
    privateKeyFailure(
      { status: 1, stderr: 'x'.repeat(3000) + ' PSSecurityException' },
      keyPath,
    ).message,
    /执行策略拒绝/,
  );
});

test('CLI failure keeps root cause and next step while redacting key path and auth material', () => {
  const error = new Error(
    `EPERM: mkdtemp failed; key=${keyPath}; Cookie: session=secret-cookie; csrf=secret-csrf`,
  );
  const message = formatCliError(
    'sign',
    error,
    ['sign', '--key-file', keyPath],
    { AURIGA_CLIENT_ADMIN_AUTH: auth },
  );
  assert.match(message, /EPERM.*mkdtemp/);
  assert.match(message, /下一步/);
  assert.ok(!message.includes(keyPath));
  assert.ok(!message.includes('secret-cookie'));
  assert.ok(!message.includes('secret-csrf'));
  assert.ok(
    !safeDiagnostic(
      '-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----',
    ).includes('secret'),
  );
  assert.match(
    formatCliError(
      'sign',
      new Error('outer', { cause: new Error('inner failure') }),
    ),
    /outer.*原因：inner failure/,
  );
});

test('help distinguishes approval key and site key and documents UAC package location', () => {
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./cli.mjs', import.meta.url)), 'build', '--help'],
    { encoding: 'utf8' },
  );
  assert.equal(result.status, 0, result.stderr);
  for (const text of [
    '--devkit-verify-key',
    '--site-release-public-key',
    '--uac-config-dir',
    'payload/auriga/uac/',
    '套件',
    '站点',
  ])
    assert.ok(result.stdout.includes(text), text);
  assert.deepEqual(
    normalizeBuildAliases({
      'devkit-verify-key': 'approved.pem',
      'site-release-public-key': 'site.pem',
    }),
    { 'verify-key': 'approved.pem', 'release-public-key': 'site.pem' },
  );
  assert.throws(
    () =>
      normalizeBuildAliases({ 'verify-key': 'a', 'devkit-verify-key': 'a' }),
    /互斥/,
  );
  assert.throws(
    () =>
      normalizeBuildAliases({
        'release-public-key': 'a',
        'site-release-public-key': 'b',
      }),
    /互斥/,
  );
});

test('successful build reports UAC package location or absence without configuration content; failed build never claims inclusion', (t) => {
  const dir = temporary(t),
    source = join(dir, 'src'),
    kit = join(dir, 'kit');
  mkdirSync(source);
  mkdirSync(kit);
  writeFileSync(join(source, 'README.md'), 'fixture');
  const messages = [];
  t.mock.method(console, 'log', (message) => messages.push(message));
  const run = (out, config, status = 0) =>
    launchOfflineBuild(
      {
        offline: true,
        out: join(dir, out),
        ...(config ? { 'uac-config-dir': 'C:\\secret-config' } : {}),
      },
      { directory: kit, manifestSha256: '0'.repeat(64) },
      { root: source, spawn: () => ({ status }) },
    );
  run('with-config', true);
  assert.match(messages[0], /已写入 payload\/auriga\/uac\/.*2 个文件/);
  assert.ok(!messages[0].includes('secret-config'));
  run('without-config', false);
  assert.match(messages[1], /未提供 --uac-config-dir.*不含本地 UAC 配置/);
  assert.throws(() => run('failed-config', true, 1), /离线构建失败/);
  assert.equal(messages.length, 2);
});

test(
  'actual Windows ACL invocation remains fail-closed with -File and no policy bypass',
  { skip: process.platform !== 'win32' },
  (t) => {
    const dir = temporary(t);
    const file = join(dir, 'sensitive-private-name.pem');
    writeFileSync(file, 'not read by ACL verification');
    let invocation;
    t.mock.method(childProcess, 'spawnSync', (_exe, args, options) => {
      if (!args.includes('-File')) return { status: 0 };
      invocation = { args, options };
      return { status: 1, stderr: `private ACL required: ${file}` };
    });
    syncBuiltinESMExports();
    t.after(() => {
      t.mock.restoreAll();
      syncBuiltinESMExports();
    });
    assert.throws(
      () => assertPrivateKey(file),
      (error) => {
        assert.match(error.message, /exit=1.*private ACL required/);
        assert.ok(!error.message.includes('sensitive-private-name'));
        return true;
      },
    );
    assert.ok(invocation.args.includes('-File'));
    assert.ok(
      !invocation.args.some((arg) => /bypass|executionpolicy/i.test(arg)),
    );
    assert.equal(invocation.options.env.AURIGA_RESOURCE_KEY_PATH, file);
    assert.equal(invocation.options.windowsHide, true);
    assert.throws(
      () => generateDesktopKeys(join(dir, 'new-private-directory')),
      /权限校验或保护失败/,
    );
    assert.ok(invocation.args.includes('directory'));
    assert.ok(
      !invocation.args.some((arg) => /bypass|executionpolicy/i.test(arg)),
    );
  },
);

test('signature diagnostics show actual key fingerprint and only independently supplied expectation', (t) => {
  const dir = temporary(t);
  const approved = generateKeyPairSync('ed25519');
  const wrong = generateKeyPairSync('ed25519');
  const key = join(dir, 'approved.pem');
  const wrongKey = join(dir, 'site.pem');
  const approvedPem = approved.publicKey.export({
    type: 'spki',
    format: 'pem',
  });
  const wrongPem = wrong.publicKey.export({ type: 'spki', format: 'pem' });
  writeFileSync(key, approvedPem);
  writeFileSync(wrongKey, wrongPem);
  writeFileSync(join(dir, 'release-public.pem'), wrongPem);
  writeFileSync(join(dir, 'SHA256SUMS'), '0'.repeat(64) + '  metadata.json\n');
  writeFileSync(
    join(dir, 'SHA256SUMS.sig'),
    sign(
      null,
      Buffer.from('0'.repeat(64) + '  metadata.json\n'),
      approved.privateKey,
    ).toString('base64'),
  );
  const expected = fingerprint(approvedPem),
    actual = fingerprint(wrongPem);
  assert.throws(
    () => verifySignedInventory(dir, { verifyKey: wrongKey }),
    (error) => {
      assert.ok(error.message.includes(actual));
      assert.ok(!error.message.includes(expected));
      assert.match(error.message, /未提供独立批准指纹/);
      assert.match(error.message, /--release-public-key/);
      return true;
    },
  );
  assert.throws(
    () =>
      verifySignedInventory(dir, {
        verifyKey: wrongKey,
        trustFingerprint: expected,
      }),
    (error) => {
      assert.ok(error.message.includes(actual));
      assert.ok(error.message.includes(expected));
      return true;
    },
  );
  assert.throws(
    () =>
      verifySignedInventory(dir, {
        verifyKey: key,
        trustFingerprint: 'untrusted text',
      }),
    /64 位小写/,
  );
  assert.equal(
    verifySignedInventory(dir, { verifyKey: key }).fingerprint,
    expected,
  );
});
