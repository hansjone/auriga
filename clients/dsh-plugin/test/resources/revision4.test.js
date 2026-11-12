import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture, id, tarball } from './fixtures.js';
import { validateArchive } from '../../src/resources/archive.js';
import { gunzipSync, gzipSync } from 'node:zlib';
const input = {
  packageId: id(4),
  versionId: id(5),
  action: 'install',
  key: 'first',
};
test('renewal obtains a freshly authorized same-version signature before expiry without reinstalling', async (t) => {
  const f = await fixture(t);
  await f.engine.apply(input);
  f.advance(3300000);
  f.renew();
  await f.engine.monitor();
  assert.equal((await f.engine.list()).items[0].renewalState, 'current');
  assert.equal(
    f.calls.filter((c) => c.path.endsWith('/download-tickets')).length,
    2,
  );
  assert.deepEqual(f.hostCalls, ['install', 'enable']);
});
test('offline renewal keeps installed L1 running beyond admission expiry and refuses new activation', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l1';
  await f.engine.apply(input);
  f.advance(7200000);
  f.offline();
  await f.engine.monitor();
  const result = await f.engine.list();
  assert.equal(result.items[0].localEnabled, true);
  assert.equal(result.items[0].renewalState, 'offline');
  assert.equal(f.hostCalls.includes('disable'), false);
  assert.equal(f.hostCalls.includes('stop'), false);
  await assert.rejects(
    f.engine.apply({ ...input, action: 'enable', key: 'new' }),
  );
});
test('explicit renewal authorization denial disables even L1 without exiting the host', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l1';
  await f.engine.apply(input);
  f.advance(3300000);
  f.denyRenewal();
  await f.engine.monitor();
  assert(f.hostCalls.includes('disable'));
  assert(!f.hostCalls.includes('stop'));
  assert.equal((await f.engine.list()).items[0].renewalState, 'denied');
});
for (const scenario of ['logout', 'switchMember'])
  test(
    'persisted L4 stops across ' +
      scenario +
      ' and restart with no usable original credential',
    async (t) => {
      const f = await fixture(t);
      f.pack.level = 'l4';
      f.pack.visibility = 'private';
      await f.engine.apply(input);
      f[scenario]();
      await f.reopen();
      await f.engine.monitor();
      assert(f.hostCalls.includes('disable'));
      assert(!f.hostCalls.includes('stop'));
      await assert.rejects(f.engine.list(), { code: 'common.unauthenticated' });
    },
  );
test('signed cached revocation is enforced after logout and reopen without network or credentials', async (t) => {
  const f = await fixture(t);
  await f.engine.apply(input);
  f.revocations.revision = 2;
  f.revocations.revokedKids = ['release'];
  await f.engine.monitor();
  f.hostCalls.length = 0;
  await f.host.enable(f.pack.name, true);
  f.logout();
  await f.reopen();
  await f.engine.monitor();
  assert(f.hostCalls.includes('disable'));
});
for (const stage of ['install', 'enable'])
  test(
    'failed upgrade ' +
      stage +
      ' restores verified old version and does not advance release high water',
    async (t) => {
      const f = await fixture(t);
      await f.engine.apply(input);
      f.upgrade();
      const original = f.host[stage];
      let fail = true;
      f.host[stage] = async (...args) => {
        if (fail && (stage === 'install' || args[1] === true)) {
          fail = false;
          throw new Error('upgrade failed');
        }
        return original(...args);
      };
      await assert.rejects(
        f.engine.apply({ ...input, versionId: id(6), key: 'upgrade' }),
      );
      assert.equal((await f.host.observe(f.pack.name)).version, '1.0.0');
      assert.equal((await f.host.observe(f.pack.name)).enabled, true);
      await f.reopen();
      await f.engine.apply({ ...input, action: 'enable', key: 'old-again' });
      assert.equal((await f.host.observe(f.pack.name)).enabled, true);
    },
  );
test('archive layout matches npm stripping and folds case and Unicode before duplicate detection', async () => {
  const manifest = {
    name: 'example',
    version: '1.0.0',
    dsh: { bundle: { patch: './cordis.patch.yml' } },
  };
  for (const extra of [
    { 'outside.js': 'x' },
    { 'package/File.js': 'x', 'package/file.js': 'y' },
    { 'package/\u03c2.js': 'x', 'package/\u03a3.js': 'y' },
    { 'package/caf\u00e9.js': 'x', 'package/cafe\u0301.js': 'y' },
  ])
    await assert.rejects(async () =>
      validateArchive(tarball(manifest, extra), manifest),
    );
  const data = gunzipSync(tarball(manifest));
  // A valid rootless tar must also be refused rather than interpreted as npm layout.
  for (let off = 0; off + 512 < data.length;) {
    const h = data.subarray(off, off + 512);
    if (h.every((b) => b === 0)) break;
    const size = parseInt(
      h.subarray(124, 136).toString().replace(/\0/g, '').trim(),
      8,
    );
    const path = h.subarray(0, 100).toString().split('\0')[0].slice(8);
    h.fill(0, 0, 100);
    h.write(path);
    h.fill(32, 148, 156);
    h.write(
      h
        .reduce((a, b) => a + b, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    off += 512 + Math.ceil(size / 512) * 512;
  }
  await assert.rejects(async () => validateArchive(gzipSync(data), manifest));
});
