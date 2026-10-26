import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import { createNetworkLifecycle } from '../../src/network/lifecycle.js';
import { generateKeyPairSync } from 'node:crypto';
import { platformConfig } from '../../src/local/platform.js';

const turn = () => new Promise((resolve) => setImmediate(resolve));

test('network-only forbidden reason remains diagnostic after a running runtime closes', async () => {
  let input,
    revoked = 0;
  const lifecycle = createNetworkLifecycle({
    directory: 'unused',
    ctx: {},
    getAccess: async () => 'native',
    authorizeLocal: async () => false,
    defaultProvider: {
      kind: 'device-connection',
      async prepare() {
        return { kind: 'device-connection' };
      },
    },
    onRevoked: () => {
      revoked++;
    },
    async open(options) {
      input = options;
      return {
        status: { connected: true },
        async start() {},
        async close() {},
        consentUI() {},
      };
    },
  });
  lifecycle.login();
  try {
    await until(() => lifecycle.status.connected);
    input.onClosed('forbidden');
    await turn();
    assert.equal(lifecycle.status.state, 'unavailable');
    assert.equal(lifecycle.status.reason, 'forbidden');
    assert.equal(revoked, 0);
  } finally {
    await lifecycle.close();
  }
});

for (const point of ['prepare', 'start'])
  for (const error of [
    { status: 403 },
    { status: 403, code: 'network.unauthorized' },
    { status: 401 },
    { status: 403, code: 'network.revoked' },
  ])
    test(
      'official lifecycle ' +
        point +
        ' distinguishes denial ' +
        JSON.stringify(error),
      async () => {
        let revoked = 0;
        const lifecycle = createNetworkLifecycle({
          directory: 'unused',
          ctx: {},
          getAccess: async () => 'native',
          authorizeLocal: async () => false,
          defaultProvider: {
            kind: 'device-connection',
            async prepare() {
              if (point === 'prepare') throw error;
              return { kind: 'device-connection' };
            },
          },
          onRevoked: () => {
            revoked++;
          },
          async open() {
            return {
              async start() {
                throw error;
              },
              async close() {},
              consentUI() {},
            };
          },
        });
        lifecycle.login();
        try {
          await until(() => lifecycle.status.state !== 'connecting');
          const expected =
            error.status === 401 || error.code === 'network.revoked';
          assert.equal(revoked, expected ? 1 : 0);
          assert.equal(
            lifecycle.status.state,
            expected ? 'idle' : 'unavailable',
          );
          if (!expected) assert.equal(lifecycle.status.reason, 'forbidden');
        } finally {
          await lifecycle.close();
        }
      },
    );
test('complete provider detach restores official default after old runtime releases its lock', async () => {
  const calls = [];
  let release;
  const gate = new Promise((r) => {
    release = r;
  });
  const fallback = {
    kind: 'device-connection',
    async prepare() {
      calls.push('default-prepare');
      return { kind: 'device-connection' };
    },
  };
  const lifecycle = createNetworkLifecycle({
    directory: 'unused',
    ctx: {},
    defaultProvider: fallback,
    getAccess: async () => '',
    authorizeLocal: async () => false,
    async open(input) {
      const kind = input.kind ?? 'full';
      calls.push(kind + '-open');
      return {
        status: { connected: true },
        async start() {},
        async close() {
          calls.push(kind + '-close');
          if (kind === 'full') await gate;
        },
        consentUI() {},
      };
    },
  });
  const full = {
    ports: {},
    async prepare() {
      calls.push('full-prepare');
      return {};
    },
  };
  const detach = lifecycle.attach(full);
  lifecycle.login();
  await turn();
  await turn();
  detach();
  await turn();
  assert.equal(calls.includes('default-prepare'), false);
  release();
  await turn();
  await turn();
  assert.equal(calls.includes('default-prepare'), true);
  assert.ok(calls.indexOf('full-close') < calls.indexOf('default-prepare'));
  await lifecycle.close();
});
test('installation alone has zero network side effects; only trusted prepare receives immutable pins', async () => {
  const installation = platformConfig({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
    network: {
      realm: '1',
      wssUrl: 'wss://network.example.com/custom/ws',
      roots: [
        {
          kid: 'root',
          public_key_spki: generateKeyPairSync('ed25519')
            .publicKey.export({ format: 'der', type: 'spki' })
            .toString('base64url'),
        },
      ],
    },
  }).network;
  const calls = {
    access: 0,
    tickets: 0,
    keys: 0,
    send: 0,
    authorization: 0,
    open: 0,
  };
  const options = {
    directory: 'unused',
    installation,
    ctx: {
      tools: {
        register() {
          calls.send++;
        },
      },
      emit() {
        calls.authorization++;
      },
    },
    getAccess: async () => {
      calls.access++;
      return 'native';
    },
    authorizeLocal: async () => true,
    async open() {
      calls.open++;
      throw new Error('unexpected open');
    },
  };
  const lifecycle = createNetworkLifecycle(options);
  options.installation = undefined;
  lifecycle.login();
  await turn();
  assert.equal(lifecycle.status.state, 'unconfigured');
  assert.equal(lifecycle.status.available, false);
  assert.ok(Object.values(calls).every((count) => count === 0));
  assert.equal(
    JSON.stringify(lifecycle.status).includes('public_key_spki'),
    false,
  );
  let received;
  lifecycle.attach({
    ports: {
      prepareTicket() {
        calls.tickets++;
      },
      refreshKeys() {
        calls.keys++;
      },
    },
    prepare(input) {
      received = input;
      return new Promise(() => {});
    },
  });
  await until(() => received);
  assert.equal(received.installation, installation);
  assert.deepEqual(Object.keys(received).sort(), [
    'accessToken',
    'installation',
    'signal',
  ]);
  assert.ok(Object.isFrozen(received.installation.roots[0]));
  assert.equal(calls.access, 1);
  assert.ok(
    Object.entries(calls)
      .filter(([key]) => key !== 'access')
      .every(([, count]) => count === 0),
  );
  await lifecycle.close();
});
async function until(predicate) {
  const deadline = Date.now() + 2000;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error('lifecycle timeout');
    await turn();
  }
}

test('a runtime storage failure becomes unavailable and is rebuilt with backoff without relogin', async () => {
  const opened = [];
  const lifecycle = createNetworkLifecycle({
    directory: 'unused',
    ctx: {},
    getAccess: async () => 'native',
    authorizeLocal: async () => true,
    backoff: { baseMs: 10, maxMs: 20 },
    async open(options) {
      opened.push(options);
      const ordinal = opened.length;
      return {
        status: { connected: true },
        async start() {
          if (ordinal === 1) await new Promise(() => {});
        },
        async close() {
          options.onClosed?.('stopped');
        },
        consentUI: () => ({}),
      };
    },
  });
  lifecycle.attach({ prepare: async () => ({}), ports: {} });
  lifecycle.login();
  await until(() => opened.length === 1);
  assert.equal(typeof opened[0].onClosed, 'function');
  opened[0].onClosed('storage_failure');
  assert.equal(lifecycle.status.connected, false);
  assert.equal(lifecycle.status.state, 'unavailable');
  assert.equal(opened.length, 1);
  await until(() => opened.length === 2 && lifecycle.status.connected);
  await lifecycle.close();
});
function fixture() {
  const calls = [];
  const lifecycle = createNetworkLifecycle({
    directory: 'unused',
    ctx: {},
    getAccess: async () => 'native-token',
    authorizeLocal: async () => true,
    async open(options) {
      calls.push(['open', options.prepared]);
      return {
        status: { connected: true },
        async start() {
          calls.push(['start']);
        },
        async close() {
          calls.push(['close']);
        },
        consentUI: () => ({}),
      };
    },
  });
  return {
    lifecycle,
    calls,
    provider: {
      ports: {},
      async prepare(input) {
        assert.equal(input.installation, undefined);
        calls.push(['prepare', input.accessToken]);
        return { id: 'node' };
      },
    },
  };
}
test('one authenticated DSH shares one runtime; logout/disposal removes it and relogin rebuilds', async () => {
  const { lifecycle, calls, provider } = fixture();
  lifecycle.attach(provider);
  await turn();
  assert.deepEqual(calls, []);
  lifecycle.login();
  lifecycle.login();
  await turn();
  assert.equal(calls.filter(([kind]) => kind === 'start').length, 1);
  lifecycle.logout();
  assert.equal(lifecycle.status.connected, false);
  await turn();
  assert.equal(calls.filter(([kind]) => kind === 'close').length, 1);
  lifecycle.login();
  await turn();
  assert.equal(calls.filter(([kind]) => kind === 'start').length, 2);
  await lifecycle.close();
  assert.throws(() => lifecycle.consentUI({}), /unavailable/);
});
test('logout during prepare ignores late credentials and never opens a runtime', async () => {
  const { lifecycle, calls, provider } = fixture();
  let release;
  provider.prepare = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  lifecycle.login();
  lifecycle.attach(provider);
  await turn();
  lifecycle.logout();
  release({ id: 'stale-node' });
  await turn();
  assert.deepEqual(calls, []);
  await lifecycle.close();
});
test('provider unload retires connection even while native login remains valid', async () => {
  const { lifecycle, calls, provider } = fixture();
  lifecycle.login();
  const detach = lifecycle.attach(provider);
  await turn();
  detach();
  assert.equal(lifecycle.status.connected, false);
  await turn();
  assert.equal(calls.filter(([kind]) => kind === 'close').length, 1);
  assert.equal(lifecycle.status.state, 'unconfigured');
  await lifecycle.close();
});

test('closing never waits for an uncooperative obsolete prepare response', async () => {
  const { lifecycle, calls, provider } = fixture();
  let release;
  provider.prepare = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  lifecycle.attach(provider);
  lifecycle.login();
  await turn();
  await lifecycle.close();
  assert.equal(lifecycle.status.connected, false);
  release({ id: 'obsolete' });
  await turn();
  assert.deepEqual(calls, []);
});
