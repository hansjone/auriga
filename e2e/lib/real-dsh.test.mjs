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
  realImageDigest,
  assertRealRuntimeImage,
} from './real-dsh.mjs';

const digest = (raw) =>
  `sha256:${createHash('sha256').update(raw).digest('hex')}`;

function runtimeFixture(engineForm = 'config', configFields = {}) {
  const config = Buffer.from(
    JSON.stringify({ os: 'linux', architecture: 'amd64', ...configFields }),
  );
  const manifest = Buffer.from(
    JSON.stringify({
      schemaVersion: 2,
      mediaType: 'application/vnd.oci.image.manifest.v1+json',
      config: {
        mediaType: 'application/vnd.oci.image.config.v1+json',
        digest: digest(config),
        size: config.length,
      },
      layers: [],
    }),
  );
  const metadata = {
    image: `127.0.0.1:5000/auriga/real-dsh@${digest(manifest)}`,
    dshVersion: '0.2.0-rc.2',
    nodeVersion: '22.19.0',
    target: 'linux/amd64',
  };
  const image = {
    Id: digest(engineForm === 'config' ? config : manifest),
    Os: 'linux',
    Architecture: 'amd64',
    RepoDigests: [metadata.image],
  };
  const info = { Image: image.Id, Config: { Image: image.Id } };
  return { metadata, image, info, manifest, config };
}

function checkRuntime({ metadata, image, info, manifest, config }) {
  return assertRealRuntimeImage(metadata, image, info, manifest, config);
}

for (const [version, form] of [
  [27, 'config'],
  [29, 'manifest'],
]) {
  test(`Docker ${version} style ${form} Id is pinned across both container fields`, () => {
    const fixture = runtimeFixture(form);
    assert.equal(checkRuntime(fixture), fixture.image.Id);
    const otherId = digest(
      form === 'config' ? fixture.manifest : fixture.config,
    );
    for (const field of ['Image', 'Config.Image']) {
      const changed = structuredClone(fixture.info);
      if (field === 'Image') changed.Image = otherId;
      else changed.Config.Image = otherId;
      assert.throws(
        () => checkRuntime({ ...fixture, info: changed }),
        /not fixed Engine Id/,
      );
    }
    for (const ref of [
      fixture.metadata.image,
      'real-dsh:w3',
      fixture.image.Id.slice(0, 20),
    ]) {
      assert.throws(
        () =>
          checkRuntime({
            ...fixture,
            info: { Image: ref, Config: { Image: ref } },
          }),
        /not fixed Engine Id/,
      );
    }
  });
}

test('runtime image requires exact repository ownership, platform and original M/C identity', () => {
  const fixture = runtimeFixture();
  for (const patch of [
    { RepoDigests: [] },
    { RepoDigests: [fixture.metadata.image.replace('real-dsh', 'fake-dsh')] },
    { RepoDigests: [fixture.metadata.image + '0'] },
    { Os: 'windows' },
    { Architecture: 'arm64' },
    { Id: `sha256:${'f'.repeat(64)}` },
    { Id: fixture.metadata.image },
  ]) {
    assert.throws(() =>
      checkRuntime({ ...fixture, image: { ...fixture.image, ...patch } }),
    );
  }
  const wrongId = `sha256:${'e'.repeat(64)}`;
  assert.throws(
    () =>
      checkRuntime({
        ...fixture,
        image: { ...fixture.image, Id: wrongId },
        info: { Image: wrongId, Config: { Image: wrongId } },
      }),
    /original M\/C proof/,
  );
});

test('runtime rejects unbound original bytes and descriptors via existing strict proof verifier', () => {
  const fixture = runtimeFixture();
  assert.throws(
    () =>
      checkRuntime({
        ...fixture,
        manifest: Buffer.concat([fixture.manifest, Buffer.from(' ')]),
      }),
    /manifest digest mismatch/,
  );
  assert.throws(
    () =>
      checkRuntime({
        ...fixture,
        config: Buffer.concat([fixture.config, Buffer.from(' ')]),
      }),
    /invalid single-platform OCI proof/,
  );
  for (const patch of [
    {
      config: {
        mediaType: 'application/vnd.oci.image.config.v1+json',
        digest: `sha256:${'e'.repeat(64)}`,
        size: fixture.config.length,
      },
    },
    {
      config: {
        ...JSON.parse(fixture.manifest).config,
        size: fixture.config.length + 1,
      },
    },
  ]) {
    const manifest = Buffer.from(
      JSON.stringify({ ...JSON.parse(fixture.manifest), ...patch }),
    );
    const metadata = {
      ...fixture.metadata,
      image: `127.0.0.1:5000/auriga/real-dsh@${digest(manifest)}`,
    };
    assert.throws(
      () => checkRuntime({ ...fixture, manifest, metadata }),
      /invalid single-platform OCI proof/,
    );
  }
  const config = Buffer.from(
    '{"os":"linux","os":"linux","architecture":"amd64"}',
  );
  const manifest = Buffer.from(
    JSON.stringify({
      ...JSON.parse(fixture.manifest),
      config: {
        ...JSON.parse(fixture.manifest).config,
        digest: digest(config),
        size: config.length,
      },
    }),
  );
  const metadata = {
    ...fixture.metadata,
    image: `127.0.0.1:5000/auriga/real-dsh@${digest(manifest)}`,
  };
  assert.throws(
    () => checkRuntime({ ...fixture, metadata, manifest, config }),
    /duplicate JSON key/,
  );
});

test('matching M/C hashes and Engine platform cannot hide a wrong original config platform', () => {
  for (const form of ['config', 'manifest']) {
    for (const platform of [{ os: 'windows' }, { architecture: 'arm64' }]) {
      assert.throws(
        () => checkRuntime(runtimeFixture(form, platform)),
        /invalid single-platform OCI proof/,
      );
    }
  }
});

test('runtime metadata retains immutable real repository and version/target constraints', () => {
  const { metadata } = runtimeFixture();
  for (const patch of [
    { image: '127.0.0.1:5000/auriga/real-dsh:w3' },
    { image: metadata.image.replace('real-dsh', 'fake-dsh') },
    { image: metadata.image + '0' },
    { dshVersion: '0.2.0' },
    { nodeVersion: '22.20.0' },
    { target: 'linux/arm64' },
  ]) {
    assert.throws(() => realImageDigest({ ...metadata, ...patch }));
  }
});

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
