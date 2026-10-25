import assert from 'node:assert/strict';
import test from 'node:test';
import { createNativeHostPort } from '../../../../src/network/host/native/index.js';
import { createHostPortOperations } from '../../../../src/network/host/native/host-port.js';
import { fixture } from './fixtures.js';

test('metadata callbacks are never consulted for capability admission', async () => {
  const f = fixture();
  let invoked = false;
  const host = await createNativeHostPort({
    ctx: f.ctx,
    security: {
      async assertIsolated() {
        invoked = true;
        throw new Error('must not run');
      },
    },
  });
  assert.equal(await host.flush('session-a'), true);
  assert.equal(invoked, false);
});

test('existing create cannot succeed when the original header has not crossed a flush barrier', async () => {
  const f = fixture();
  const host = await createHostPortOperations(f);
  f.state.flush = false;
  await assert.rejects(
    host.createSession({ id: 'session-a', cwd: '/work/project', preset: null }),
    { code: 'network.host_incompatible' },
  );
  assert.equal(
    f.calls.some(([kind]) => kind === 'create'),
    false,
  );
});

test('unverified native inbox evidence cannot authorize a new prompt', async () => {
  const f = fixture();
  delete f.attach('session-a').inbox;
  const host = await createHostPortOperations(f);
  await assert.rejects(
    host.prompt('session-a', { requestId: 'r', text: 'body', mode: 'queue' }),
    { code: 'network.host_incompatible' },
  );
  assert.equal(
    f.calls.some(([kind]) => kind === 'prompt'),
    false,
  );
});

test('existing create rechecks provider and flush before returning, including cold adoption', async () => {
  const f = fixture();
  const host = await createHostPortOperations(f);
  f.state.durable = false;
  await assert.rejects(
    host.createSession({ id: 'session-a', cwd: '/work/project', preset: null }),
    { code: 'network.host_incompatible' },
  );
  f.state.durable = true;
  assert.deepEqual(
    await host.createSession({
      id: 'session-a',
      cwd: '/work/project',
      preset: null,
    }),
    { id: 'session-a', cwd: '/work/project' },
  );
  assert.equal(f.calls.filter(([kind]) => kind === 'flush').length, 1);
  assert.equal(
    f.calls.some(([kind]) => kind === 'create'),
    false,
  );
});

test('a parentSession-only fork can adopt, prompt and flush without child ownership', async () => {
  const f = fixture();
  f.seed('lineage', { parentSession: 'parent' });
  const host = await createHostPortOperations(f);
  await host.createSession({
    id: 'lineage',
    cwd: '/work/project',
    preset: null,
  });
  await host.prompt('lineage', { requestId: 'r', text: 'body', mode: 'queue' });
  assert.equal(await host.flush('lineage'), true);
});

test(
  'prompt deadline aborts and frees the lock without re-invoking an unknown request',
  { timeout: 1000 },
  async () => {
    const f = fixture();
    let signal,
      calls = 0;
    f.ctx.sessionController.prompt = (_request, nativeSignal) => {
      signal = nativeSignal;
      calls++;
      return new Promise(() => {});
    };
    const host = await createHostPortOperations({ ...f, promptTimeoutMs: 10 });
    const input = { requestId: 'deadline-r', text: 'body', mode: 'queue' };
    await assert.rejects(host.prompt('session-a', input), {
      code: 'network.host_incompatible',
    });
    assert.equal(signal.aborted, true);
    assert.equal(await host.flush('session-a'), true);
    await assert.rejects(host.prompt('session-a', input), {
      code: 'network.host_incompatible',
    });
    assert.equal(calls, 1);
  },
);

test('unknown pending evidence is not a definite request absence', async () => {
  const f = fixture();
  f.attach('session-a').inbox.nextStep = undefined;
  const host = await createHostPortOperations(f);
  await assert.rejects(host.hasPromptRequest('session-a', 'r'), {
    code: 'network.host_incompatible',
  });
});
