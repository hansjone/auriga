import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash, generateKeyPairSync } from 'node:crypto';
import { createManagedNetwork } from '../../src/network/managed-device.js';

const ids = {
  tenantId: '00000000-0000-4000-8000-000000000001',
  ownerId: '00000000-0000-4000-8000-000000000002',
  instanceId: '00000000-0000-4000-8000-000000000003',
  deviceId: '00000000-0000-4000-8000-000000000004',
};
const installation = () => ({
  realm: '9',
  wssUrl: 'wss://portal.example.com/net/v1/ws',
  roots: [
    {
      kid: 'root',
      public_key_spki: generateKeyPairSync('ed25519')
        .publicKey.export({ format: 'der', type: 'spki' })
        .toString('base64url'),
    },
  ],
});
const material = () => ({
  ...ids,
  credential: 'a'.repeat(43),
  expiresAt: new Date(Date.now() + 600000).toISOString(),
  platformOrigin: 'https://portal.example.com',
  issuer: 'https://portal.example.com',
  network: installation(),
});

async function fixture(t, change = (x) => x) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-managed-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, 'device.json');
  const value = change(material());
  await writeFile(file, JSON.stringify(value), { mode: 0o600 });
  const calls = [];
  let requestHook;
  let lifecycleInput,
    invalid = false;
  const network = createManagedNetwork({
    directory,
    file,
    ...ids,
    ctx: {},
    createPlatform() {
      return {
        async request(path, options) {
          calls.push({ path, options });
          await requestHook?.();
          if (invalid)
            throw Object.assign(new Error('revoked'), { status: 403 });
          return {
            access_token: 'device-access',
            token_type: 'Bearer',
            scope: 'auriga.connect',
            expires_in: 300,
          };
        },
        async verifyDevice(_token, expected) {
          return {
            ...expected,
            exp: Math.floor(Date.now() / 1000) + 300,
          };
        },
      };
    },
    createLifecycle(input) {
      lifecycleInput = input;
      return {
        login() {
          calls.push('login');
        },
        logout() {
          calls.push('logout');
        },
        attach() {
          calls.push('attach');
          return () => {};
        },
        close() {
          calls.push('close');
        },
        get status() {
          return { connected: true };
        },
      };
    },
  });
  t.after(() => network.close());
  return {
    network,
    calls,
    file,
    value,
    get input() {
      return lifecycleInput;
    },
    revoke() {
      invalid = true;
    },
    intercept(fn) {
      requestHook = fn;
    },
  };
}

test('managed credential uses the common device provider and lifecycle, never instance bearer', async (t) => {
  const f = await fixture(t);
  await f.network.refresh();
  assert.equal(f.input.defaultProvider.kind, 'device-connection');
  assert.ok(f.calls.includes('login'));
  const exchange = f.calls.find((x) => typeof x === 'object');
  assert.equal(exchange.path, '/oauth2/token');
  assert.equal(exchange.options.body.credential, f.value.credential);
  assert.equal(exchange.options.body.instance_id, ids.instanceId);
  assert.equal(exchange.options.token, undefined);
  f.network.attach({ prepare() {} });
  assert.ok(f.calls.includes('attach'));
});

test('file rotation closes old tools before admitting the new device; revocation closes tools', async (t) => {
  const f = await fixture(t);
  await f.network.refresh();
  await writeFile(
    f.file,
    JSON.stringify({
      ...f.value,
      deviceId: '00000000-0000-4000-8000-000000000005',
      credential: 'b'.repeat(43),
    }),
  );
  await f.network.refresh();
  assert.ok(f.calls.indexOf('close') < f.calls.lastIndexOf('login'));
  assert.ok(f.input.directory.endsWith('00000000-0000-4000-8000-000000000005'));
  f.revoke();
  await f.network.refresh(true);
  assert.equal(f.calls.at(-1), 'logout');
  assert.equal(f.network.status.connected, false);
});

test('managed provider partitions storage by verified device and preserves the key across renewal', async (t) => {
  const f = await fixture(t);
  await f.network.refresh();
  const signal = new AbortController().signal;
  const first = await f.input.defaultProvider.prepare({ signal });
  assert.equal(
    first.storageKey,
    createHash('sha256')
      .update(JSON.stringify([f.value.issuer, ids.tenantId, ids.deviceId]))
      .digest('hex'),
  );
  await f.network.refresh(true);
  assert.equal(
    (await f.input.defaultProvider.prepare({ signal })).storageKey,
    first.storageKey,
  );
  await writeFile(
    f.file,
    JSON.stringify({
      ...f.value,
      deviceId: '00000000-0000-4000-8000-000000000005',
      credential: 'b'.repeat(43),
    }),
  );
  await f.network.refresh();
  const rotated = await f.input.defaultProvider.prepare({ signal });
  assert.match(rotated.storageKey, /^[a-f0-9]{64}$/);
  assert.notEqual(rotated.storageKey, first.storageKey);
});

test('managed renewal reuses the current lifecycle and file removal revokes its tools', async (t) => {
  const f = await fixture(t);
  await f.network.refresh();
  const initial = f.input;
  await f.network.refresh(true);
  assert.equal(f.input, initial);
  assert.equal(f.calls.filter((x) => typeof x === 'object').length, 2);
  assert.equal(f.calls.filter((x) => x === 'login').length, 1);
  await rm(f.file);
  await f.network.refresh();
  assert.equal(f.calls.at(-1), 'close');
  assert.equal(f.network.status.connected, false);
});

test('managed refresh coalesces slow exchanges and close fences their late results', async (t) => {
  const f = await fixture(t);
  let enter, release;
  const entered = new Promise((r) => {
    enter = r;
  });
  const blocked = new Promise((r) => {
    release = r;
  });
  f.intercept(async () => {
    enter();
    await blocked;
  });
  const attempt = f.network.refresh();
  for (let i = 0; i < 20; i++) assert.equal(f.network.refresh(), attempt);
  await entered;
  await f.network.close();
  release();
  await attempt;
  assert.equal(f.input, undefined);
  assert.equal(f.calls.filter((x) => typeof x === 'object').length, 1);
  assert.equal(f.calls.includes('login'), false);
});

for (const field of [
  'tenantId',
  'ownerId',
  'instanceId',
  'expiresAt',
  'platformOrigin',
])
  test('managed file rejects wrong ' + field, async (t) => {
    const f = await fixture(t, (x) => ({
      ...x,
      [field]:
        field === 'expiresAt'
          ? '2000-01-01T00:00:00Z'
          : field === 'platformOrigin'
            ? 'http://portal.example.com'
            : ids.deviceId,
    }));
    await f.network.refresh();
    assert.equal(f.input, undefined);
    assert.equal(f.network.status.connected, false);
    assert.equal(f.calls.length, 0);
  });

test('429 renewal retains unexpired access and tools, backs off, and closes after expiry', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() });
  const f = await fixture(t);
  await f.network.refresh();
  const initial = f.input;
  t.mock.timers.tick(271000);
  f.intercept(() => {
    throw Object.assign(new Error('rate limited'), { status: 429 });
  });
  await f.network.refresh(true);
  assert.equal(f.network.status.connected, true);
  assert.equal(f.input, initial);
  assert.equal(f.calls.includes('close'), false);
  assert.equal(f.calls.includes('logout'), false);
  const count = f.calls.filter((x) => typeof x === 'object').length;
  await f.network.refresh();
  await initial.getAccess();
  assert.equal(f.calls.filter((x) => typeof x === 'object').length, count);
  t.mock.timers.tick(1001);
  f.intercept(undefined);
  await f.network.refresh();
  assert.equal(f.input, initial);
  assert.equal(f.calls.filter((x) => typeof x === 'object').length, count + 1);
  f.intercept(() => {
    throw Object.assign(new Error('rate limited'), { status: 429 });
  });
  t.mock.timers.tick(271000);
  await f.network.refresh();
  assert.equal(f.network.status.connected, true);
  t.mock.timers.tick(30000);
  await f.network.refresh();
  assert.equal(f.network.status.connected, false);
  assert.ok(f.calls.includes('close'));
});

test('missing optional managed credential starts no network tools and accepts late material', async (t) => {
  const f = await fixture(t);
  await rm(f.file);
  await f.network.refresh();
  assert.equal(f.input, undefined);
  assert.equal(f.calls.length, 0);
  await writeFile(f.file, JSON.stringify(f.value), { mode: 0o600 });
  await f.network.refresh(true);
  assert.ok(f.input);
  assert.equal(f.calls.filter((x) => x === 'login').length, 1);
});
