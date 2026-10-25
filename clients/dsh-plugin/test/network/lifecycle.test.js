import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import { createNetworkLifecycle } from '../../src/network/lifecycle.js';

const turn = () => new Promise((resolve) => setImmediate(resolve));
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
