/* global Response */
import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { join } from 'node:path';
import { existsSync, writeFileSync, readFileSync, readdirSync } from 'node:fs';
import { canonical, verifyRelease, RELEASE_KID } from '../runtime/release.mjs';
import { createUpdater, platformUrl } from '../runtime/update.mjs';
import {
  install,
  readState,
  profileDir,
  desktopSession,
} from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const keys = generateKeyPairSync('ed25519');
const now = Date.parse('2026-11-12T00:00:00Z');
function signed(changes = {}) {
  const body = {
    schema: 1,
    product: 'Auriga Desktop',
    version: '2.0.0',
    packageId: 'package-2',
    components: {
      dshVersion: '0.2.1-rc.3',
      nodeVersion: '24.0.0',
      pluginVersion: '2.0.0',
    },
    channel: 'stable',
    platform: 'win-x64',
    sizeBytes: 3,
    sha256: createHash('sha256').update('zip').digest('hex'),
    minimumVersion: '1.0.0',
    publishedAt: '2026-11-11T00:00:00Z',
    notAfter: '2026-12-01T00:00:00Z',
    sequence: 2,
    notes: '新版本',
    ...changes,
  };
  return {
    ...body,
    signature: {
      alg: 'Ed25519',
      kid: RELEASE_KID,
      value: sign(null, Buffer.from(canonical(body)), keys.privateKey).toString(
        'base64url',
      ),
    },
  };
}
const verify = (value, options = {}) =>
  verifyRelease(value, {
    publicKey: keys.publicKey,
    now,
    currentVersion: '1.0.0',
    ...options,
  });
test('签名、篡改、降级、到期、未来、序号重放均拒绝', () => {
  assert.equal(verify(signed()).version, '2.0.0');
  assert.throws(() => verify({ ...signed(), notes: '篡改' }), /signature/);
  assert.throws(
    () =>
      verify(signed(), { publicKey: generateKeyPairSync('ed25519').publicKey }),
    /signature/,
  );
  assert.throws(() => verify(signed({ version: '0.9.0' })), /downgrade/);
  assert.throws(() => verify(signed({ minimumVersion: '1.5.0' })), /minimum/);
  assert.throws(
    () => verify(signed({ notAfter: '2026-11-11T01:00:00Z' })),
    /expired/,
  );
  assert.throws(
    () => verify(signed({ publishedAt: '2026-11-13T00:00:00Z' })),
    /future/,
  );
  assert.throws(() => verify(signed(), { highestSequence: 3 }), /replay/);
  assert.throws(
    () =>
      verify(signed(), { highestSequence: 2, acceptedDigest: '0'.repeat(64) }),
    /replay/,
  );
  assert.equal(
    verify(signed(), { highestSequence: 2, acceptedDigest: signed().sha256 })
      .sequence,
    2,
  );
  assert.throws(() => verifyRelease(signed()), /untrusted/);
});
test('更新只连接固定 HTTPS origin', () => {
  assert.equal(
    platformUrl('https://example.com', 'latest'),
    'https://example.com/api/resource/v1/desktop/latest',
  );
  for (const origin of [
    'http://example.com',
    'https://example.com/path',
    'https://alice@example.com',
    'https://example.com/',
  ])
    assert.throws(() => platformUrl(origin, 'latest'));
});

test('同一安装目录拒绝同时启动第二个 DSH，退出后可重开', (t) => {
  const base = tempBase(t);
  const session = desktopSession(base);
  assert.throws(() => desktopSession(base), /已在运行/);
  session.child(process.pid);
  session.close();
  const reopened = desktopSession(base);
  reopened.close();
});

function fixture(
  t,
  {
    badSha = false,
    badId = false,
    interrupted = false,
    smokeFails = false,
  } = {},
) {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const old = buildPayload(base, { name: 'old' }),
    next = buildPayload(base, { name: 'new', pluginVersion: '2.0.0' });
  install({ root, dataHome, payload: old.dir, smoke: () => {} });
  writeFileSync(join(profileDir(dataHome), 'user-data.txt'), 'keep');
  const manifest = signed({
    packageId: badId ? 'wrong-id' : next.id,
    sha256: badSha ? '0'.repeat(64) : signed().sha256,
  });
  let attempts = 0;
  const updater = createUpdater({
    root,
    defer: false,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
    now: () => now,
    request: async (url) => {
      if (url.endsWith('/latest'))
        return new Response(JSON.stringify(manifest));
      attempts++;
      if (interrupted && attempts === 1) return new Response('zi');
      return new Response('zip');
    },
    extract: () => next.dir,
    smoke: () => {
      if (smokeFails) throw new Error('smoke failed');
    },
  });
  return { updater, root, dataHome, old, next };
}
test('成功更新保留数据并提示重启', async (t) => {
  const f = fixture(t);
  await f.updater.check();
  assert.equal(f.updater.status().available.version, '2.0.0');
  assert.equal((await f.updater.update()).restartRequired, true);
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(
    readFileSync(join(profileDir(f.dataHome), 'user-data.txt'), 'utf8'),
    'keep',
  );
});
for (const bad of ['badSha', 'badId', 'smokeFails'])
  test(bad + ' 拒绝并保持旧版且不留新版本目录', async (t) => {
    const f = fixture(t, { [bad]: true });
    await f.updater.check();
    await assert.rejects(f.updater.update());
    assert.equal(readState(f.root).current, f.old.id);
    assert.equal(existsSync(join(f.root, 'versions', f.next.id)), false);
    assert.equal(
      readdirSync(f.root).some((n) => n.startsWith('.desktop-download-')),
      false,
    );
  });
test('下载中断后重试成功；关闭检查后不请求', async (t) => {
  const f = fixture(t, { interrupted: true });
  await f.updater.check();
  await assert.rejects(f.updater.update(), /interrupted/);
  await f.updater.update();
  assert.equal(readState(f.root).current, f.next.id);
  f.updater.configure(false);
  assert.equal(f.updater.status().enabled, false);
  await f.updater.check();
  assert.equal(f.updater.status().available, null);
});
