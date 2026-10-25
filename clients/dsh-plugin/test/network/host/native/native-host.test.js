import assert from 'node:assert/strict';
import test from 'node:test';
import { createHostPortOperations as createPort } from '../../../../src/network/host/native/host-port.js';
import { fixture } from './fixtures.js';

const incompatible = { code: 'network.host_incompatible' };
const unavailable = { code: 'host.session_not_ready' };
const input = {
  requestId: 'recipient-a',
  text: 'untrusted body',
  mode: 'queue',
};

test('cold session lookup never creates/resumes; queue preserves identity, cwd and requestId', async () => {
  const f = fixture();
  const host = await createPort(f);
  assert.deepEqual(await host.getSession('session-a'), {
    id: 'session-a',
    cwd: '/work/project',
  });
  assert.equal(
    f.calls.some(([kind]) => kind === 'resume' || kind === 'create'),
    false,
  );
  await host.prompt('session-a', input);
  const call = f.calls.find(([kind]) => kind === 'prompt');
  assert.deepEqual(call[1], {
    sessionId: 'session-a',
    requestId: input.requestId,
    mode: 'queue',
    content: [{ type: 'text', text: input.text }],
  });
  assert.equal(call[2].aborted, false);
  assert.equal(await host.hasPromptRequest('session-a', input.requestId), true);
  assert.equal(await host.flush('session-a'), true);
  assert.equal(
    f.calls.some(([kind]) => kind === 'create'),
    false,
  );
});

test('missing/archived sessions do not wake or create; read failures are not absence', async () => {
  const f = fixture();
  const host = await createPort(f);
  assert.equal(await host.getSession('missing'), null);
  f.archivedSessionIds.push('session-a');
  assert.equal(await host.getSession('session-a'), null);
  await assert.rejects(host.prompt('session-a', input), unavailable);
  f.seed('other');
  f.ctx.sessionController.inspect = async () => {
    throw new Error('disk failure');
  };
  await assert.rejects(host.getSession('other'), incompatible);
  assert.equal(
    f.calls.some(([kind]) => kind === 'create' || kind === 'prompt'),
    false,
  );
});

test('create uses explicit id/cwd/preset and validates the resulting binding', async () => {
  const f = fixture();
  const host = await createPort(f);
  assert.deepEqual(
    await host.createSession({
      id: 'new',
      cwd: '/other/project',
      preset: 'coding',
    }),
    { id: 'new', cwd: '/other/project' },
  );
  assert.deepEqual(f.calls.find(([kind]) => kind === 'create')[1], {
    sessionId: 'new',
    cwd: '/other/project',
    agentPreset: 'coding',
  });
  assert.equal(
    f.calls.some(([kind]) => kind === 'prompt'),
    false,
  );
  await host.createSession({
    id: 'new',
    cwd: '/other/project',
    preset: 'coding',
  });
  assert.equal(f.calls.filter(([kind]) => kind === 'create').length, 1);
  await assert.rejects(
    host.createSession({ id: 'new', cwd: '/wrong', preset: 'coding' }),
    unavailable,
  );
  await assert.rejects(
    host.createSession({ id: 'new', cwd: '/other/project', preset: 'other' }),
    unavailable,
  );
});

for (const preset of ['missing', 'broken'])
  test(`invalid preset ${preset} cannot create`, async () => {
    const f = fixture();
    const host = await createPort(f);
    await assert.rejects(
      host.createSession({ id: 'new', cwd: '/work/project', preset }),
      incompatible,
    );
    assert.equal(
      f.calls.some(([kind]) => kind === 'create'),
      false,
    );
  });

test('lost create response is queryable by the same id; never creates a replacement', async () => {
  const f = fixture();
  const create = f.ctx.sessionController.create;
  f.ctx.sessionController.create = async (request) => {
    await create(request);
    throw new Error('lost');
  };
  const host = await createPort(f);
  await assert.rejects(
    host.createSession({ id: 'new', cwd: '/work/project', preset: null }),
    incompatible,
  );
  assert.deepEqual(await host.getSession('new'), {
    id: 'new',
    cwd: '/work/project',
  });
  await host.createSession({ id: 'new', cwd: '/work/project', preset: null });
  assert.equal(f.calls.filter(([kind]) => kind === 'create').length, 1);
});

for (const location of ['pending-evidence', 'history'])
  test(`requestId de-duplicates ${location} without invoking prompt`, async () => {
    const f = fixture();
    const source = { kind: 'user', rpcId: input.requestId };
    if (location === 'history')
      f.stored
        .get('session-a')
        .events.push({ type: 'user/message', data: { source } });
    else {
      f.attach('session-a');
      f.pending.set(`session-a:${input.requestId}`, true);
    }
    const host = await createPort(f);
    assert.equal(
      await host.hasPromptRequest('session-a', input.requestId),
      true,
    );
    await host.prompt('session-a', input);
    assert.equal(
      f.calls.some(([kind]) => kind === 'prompt'),
      false,
    );
    assert.equal(await host.hasPromptRequest('session-a', 'other'), false);
  });

test('system sources and matching text do not count as a user rpcId', async () => {
  const f = fixture();
  f.stored.get('session-a').events.push({
    type: 'user/message',
    data: {
      source: { kind: 'system', rpcId: input.requestId },
      text: input.requestId,
    },
  });
  const host = await createPort(f);
  assert.equal(
    await host.hasPromptRequest('session-a', input.requestId),
    false,
  );
});

test('cold persisted request evidence can flush through native resume without create or prompt', async () => {
  const f = fixture();
  f.stored.get('session-a').events.push({
    type: 'user/message',
    data: { source: { kind: 'user', rpcId: input.requestId } },
  });
  const host = await createPort(f);
  assert.equal(await host.hasPromptRequest('session-a', input.requestId), true);
  assert.equal(await host.flush('session-a'), true);
  assert.equal(
    f.calls.some(([kind]) => kind === 'resume'),
    true,
  );
  assert.equal(
    f.calls.some(([kind]) => kind === 'create' || kind === 'prompt'),
    false,
  );
});

test('prompt response lost after admission recovers the same inbox request and flushes', async () => {
  const f = fixture();
  const prompt = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async (...args) => {
    await prompt(...args);
    throw new Error('lost response');
  };
  const host = await createPort(f);
  await assert.rejects(host.prompt('session-a', input), incompatible);
  assert.equal(await host.hasPromptRequest('session-a', input.requestId), true);
  assert.equal(await host.flush('session-a'), true);
  assert.equal(f.calls.filter(([kind]) => kind === 'prompt').length, 1);
});

test('concurrent duplicate requests for one session serialize through async prompt admission', async () => {
  const f = fixture();
  const prompt = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async (...args) => {
    await Promise.resolve();
    return prompt(...args);
  };
  const host = await createPort(f);
  await Promise.all([
    host.prompt('session-a', input),
    host.prompt('session-a', input),
  ]);
  assert.equal(f.calls.filter(([kind]) => kind === 'prompt').length, 1);
});

test('a blocked session does not lock another session', async () => {
  const f = fixture();
  f.seed('session-b');
  let release, entered;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const prompt = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async (...args) => {
    if (args[0].sessionId === 'session-a') {
      entered();
      await pending;
    }
    return prompt(...args);
  };
  const host = await createPort(f);
  const first = host.prompt('session-a', input);
  await started;
  try {
    await host.prompt('session-b', input);
  } finally {
    release();
    await first;
  }
  assert.equal(f.calls.filter(([kind]) => kind === 'prompt').length, 2);
});

test('subagents and parent-owned ordinary sessions cannot use the N1 prompt port', async () => {
  const f = fixture();
  f.seed('child', { origin: 'subagent', parentSession: 'session-a' });
  const host = await createPort(f);
  assert.equal((await host.getSession('child')).origin, 'subagent');
  await assert.rejects(host.prompt('child', input), incompatible);
  f.ctx.sessionController.resolveAgent = async () => ({
    error: { code: 'session/subagent-owned' },
  });
  await assert.rejects(host.prompt('session-a', input), incompatible);
  assert.equal(
    f.calls.some(([kind]) => kind === 'prompt'),
    false,
  );
});

test('durable-provider check and awaited flush are mandatory, errors never accept', async () => {
  const f = fixture();
  const host = await createPort(f);
  f.state.durable = false;
  assert.equal(await host.hasDurableProvider('session-a'), false);
  await assert.rejects(host.prompt('session-a', input), incompatible);
  assert.equal(await host.flush('session-a'), false);
  f.state.durable = true;
  await host.prompt('session-a', input);
  f.state.flush = false;
  assert.equal(await host.flush('session-a'), false);
  f.state.flushError = true;
  assert.equal(await host.flush('session-a'), false);
  f.state.flushError = false;
  f.state.flush = true;
  let release, entered;
  const barrierEntered = new Promise((resolve) => {
    entered = resolve;
  });
  f.ctx.sessions.flush = () =>
    new Promise((resolve) => {
      release = () => resolve(true);
      entered();
    });
  let complete = false;
  const flushed = host.flush('session-a').then((result) => {
    complete = true;
    return result;
  });
  await barrierEntered;
  assert.equal(complete, false);
  release();
  assert.equal(await flushed, true);
});

test('identity mismatch, missing history, invalid inputs and non-accepted prompt fail closed', async () => {
  const f = fixture();
  const host = await createPort(f);
  await assert.rejects(
    host.createSession({ id: 'new', cwd: '', preset: null }),
    { code: 'network.protocol_error' },
  );
  await assert.rejects(host.prompt('session-a', { ...input, mode: 'steer' }), {
    code: 'network.protocol_error',
  });
  f.ctx.sessionController.resolveAgent = async () => ({
    agent: { id: 'other' },
  });
  await assert.rejects(host.prompt('session-a', input), incompatible);
  f.ctx.sessionController.resolveAgent = async () => ({
    agent: f.agents.get('session-a') ?? f.attach('session-a'),
  });
  f.ctx.sessionController.prompt = async () => ({ accepted: false });
  await assert.rejects(host.prompt('session-a', input), incompatible);
  f.agents.get('session-a').session.snapshotEvents = undefined;
  await assert.rejects(
    host.hasPromptRequest('session-a', input.requestId),
    incompatible,
  );
});

for (const changed of ['provider', 'session', 'archive'])
  test(`flush refuses native ${changed} replacement during the barrier`, async () => {
    const f = fixture();
    const host = await createPort(f);
    f.attach('session-a');
    f.ctx.sessions.flush = async () => {
      if (changed === 'provider')
        f.ctx.sessionPersistence = { ...f.ctx.sessionPersistence };
      if (changed === 'session') f.sessions.delete('session-a');
      if (changed === 'archive') f.archivedSessionIds.push('session-a');
      return true;
    };
    assert.equal(await host.flush('session-a'), false);
  });
