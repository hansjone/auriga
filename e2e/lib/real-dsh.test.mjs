import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { productionInputs } from '../../deploy/compose/real-dsh/inputs.mjs';
import {
  assertDoorRejected,
  assertRealHealth,
  realDshPageOk,
  probeRealDoor,
} from './real-dsh.mjs';

test('real-mode predicate rejects fake pages and unrelated 200 HTML', () => {
  for (const text of [
    'fake-dsh ok',
    '<html>login</html>',
    '<html>platform</html>',
  ]) {
    assert.equal(realDshPageOk({ status: 200, text }), false);
  }
  assert.equal(
    realDshPageOk({
      status: 401,
      text: '<html>__DSH_BOOT__<script type="module"></script></html>',
    }),
    false,
  );
});

test('health rejects activity fields, fake version and missing contract fields', () => {
  const body = {
    status: 'ok',
    ready: true,
    protocol: 1,
    pluginVersion: '0.0.0',
  };
  assertRealHealth({ status: 200, body }, '0.0.0');
  assert.throws(() =>
    assertRealHealth({ status: 200, body: { ...body, activity: {} } }, '0.0.0'),
  );
  assert.throws(() =>
    assertRealHealth(
      { status: 200, body: { ...body, pluginVersion: '0.0.0-fake' } },
      '0.0.0',
    ),
  );
  assert.throws(() => assertRealHealth({ status: 200, body: {} }, '0.0.0'));
});

test('door assertion rejects native 401 without contract code and soft statuses', () => {
  for (const response of [
    { status: 200 },
    { status: 403 },
    { status: 401, body: {} },
  ]) {
    assert.throws(() => assertDoorRejected(response));
  }
});

test('real door probe fails immediately if a host exposes unsigned pages', async () => {
  const request = async (path) =>
    path === '/_auriga/health'
      ? {
          status: 200,
          body: {
            status: 'ok',
            ready: true,
            protocol: 1,
            pluginVersion: '0.0.0',
          },
        }
      : { status: 200, text: 'fake-dsh ok' };
  await assert.rejects(
    () => probeRealDoor(request, () => 'unused', '0.0.0'),
    /door must reject/,
  );
});

test('fixture retains the exact reviewed S4 closure and registry integrity', () => {
  const base = new URL('../../deploy/compose/real-dsh/', import.meta.url);
  const bytes = readFileSync(new URL('package-lock.json', base));
  const versions = JSON.parse(
    readFileSync(new URL('versions.json', base), 'utf8'),
  );
  assert.equal(
    createHash('sha256').update(bytes).digest('hex'),
    versions.lockSha256,
  );
  const lock = JSON.parse(bytes);
  assert.equal(
    lock.packages['node_modules/@deepseek-ai/dsh'].version,
    versions.dshVersion,
  );
  for (const [name, entry] of Object.entries(lock.packages)) {
    if (!name) continue;
    assert.match(entry.resolved, /^https:\/\/registry\.npmjs\.org\//);
    assert.match(entry.integrity, /^sha512-/);
  }
});

test('plugin production lock preserves actual versions/integrities and host entry exports', () => {
  const base = new URL('../../clients/dsh-plugin/', import.meta.url);
  const sourcePackage = JSON.parse(
    readFileSync(new URL('package.json', base), 'utf8'),
  );
  const sourceLock = JSON.parse(
    readFileSync(new URL('package-lock.json', base), 'utf8'),
  );
  const { pkg, lock } = productionInputs(sourcePackage, sourceLock);
  assert.equal(pkg.devDependencies, undefined);
  assert.equal(pkg.exports['./plugin'], './src/plugin.js');
  assert.ok(sourcePackage.devDependencies);
  assert.ok(sourceLock.packages[''].devDependencies);
  for (const [name, version] of Object.entries(sourcePackage.dependencies)) {
    const path = `node_modules/${name}`;
    assert.equal(lock.packages[path].version, version);
    assert.equal(
      lock.packages[path].integrity,
      sourceLock.packages[path].integrity,
    );
    assert.equal(
      lock.packages[path].resolved,
      sourceLock.packages[path].resolved,
    );
  }
  const changed = structuredClone(sourcePackage);
  changed.dependencies.undici = '^7.30.1';
  assert.throws(() => productionInputs(changed, sourceLock), /not pinned/);
  const unlocked = structuredClone(sourceLock);
  delete unlocked.packages['node_modules/undici'].integrity;
  assert.throws(() => productionInputs(sourcePackage, unlocked), /Unlocked/);
});
