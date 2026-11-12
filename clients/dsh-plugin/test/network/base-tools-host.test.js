import { setImmediate } from 'node:timers';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture } from './host/native/fixtures.js';
import { installBaseToolsHost } from '../../src/network/base-tools-host.js';
import { BASE_TOOL_NAMES } from '../../src/network/base-tools.js';

function setup(t) {
  const f = fixture(),
    agent = f.attach('session-a'),
    tools = new Map(),
    cleanups = [];
  let current;
  f.ctx.tools = {
    register(tool) {
      assert.equal(tools.has(tool.name), false);
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  f.ctx.inject = (_deps, run) => run(f.ctx);
  f.ctx.effect = (run) => cleanups.push(run());
  installBaseToolsHost(f.ctx, () => current);
  const dispose = () => cleanups.splice(0).forEach((fn) => fn());
  t.after(dispose);
  return {
    ...f,
    tools,
    agent,
    dispose,
    set: (port) => {
      current = port;
    },
    call: (name, args = {}) =>
      tools.get('auriga_net_' + name).execute(args, { agent }),
  };
}
for (const mode of ['no headquarters', 'managed without credential file'])
  test(`installation registers tools when ${mode}`, async (t) => {
    const f = setup(t);
    assert.deepEqual([...f.tools.keys()], BASE_TOOL_NAMES);
    const status = await f.call('status');
    assert.equal(status.headquarters.reason, 'unconfigured');
    assert.equal(status.credential.state, 'signed_out');
    assert.equal(status.sending.available, false);
    assert.equal(status.sending.reason, 'unconfigured');
    const identity = await f.call('identity');
    assert.equal(identity.loggedIn, false);
    assert.equal(identity.memberId, null);
    assert.equal((await f.call('diagnose')).segments[1].reason, 'unconfigured');
    await assert.rejects(f.call('inbox'), /network.unauthorized/);
  });
test('same installation registry survives configure/unconfigure and refuses calls after unload', async (t) => {
  const f = setup(t);
  const before = [...f.tools.values()];
  let probes = 0;
  f.set({
    generation: () => 1,
    getState: () => ({
      verified: true,
      reachable: false,
      reason: 'hq_unreachable',
      network: { available: true, connected: false },
    }),
    readLocal: async () => null,
    probe: async () => {
      probes++;
      throw Object.assign(new Error(), { code: 'ECONNREFUSED' });
    },
  });
  assert.equal((await f.call('status')).sending.reason, 'hq_unreachable');
  assert.equal((await f.call('diagnose')).firstFailure, 'ingress');
  assert.equal(probes, 1);
  f.set(undefined);
  assert.equal((await f.call('status')).headquarters.reason, 'unconfigured');
  assert.deepEqual([...f.tools.values()], before);
  f.dispose();
  assert.equal(f.tools.size, 0);
  await assert.rejects(
    before[0].execute({}, { agent: f.agent }),
    /network.unauthorized/,
  );
});
test('retired source cannot publish a pending local read', async (t) => {
  const f = setup(t);
  let release;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  const port = {
    generation: () => 1,
    getState: () => ({ verified: true }),
    readLocal: () => pending,
  };
  f.set(port);
  const call = f.call('identity');
  // ResolveAgent is async; wait for the read to begin without a timing sleep.
  await new Promise((resolve) => setImmediate(resolve));
  f.set(undefined);
  release(null);
  await assert.rejects(call, /network.unauthorized/);
});
