import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import { createNetworkLifecycle } from '../../src/network/lifecycle.js';

const turn = () => new Promise((resolve) => setImmediate(resolve));
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
