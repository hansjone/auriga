import assert from 'node:assert/strict';
import test from 'node:test';
import { createNativeHostPort } from '../../../../src/network/host/native/index.js';
import { fixture } from './fixtures.js';

test('public factory needs only capabilities, ignoring version and artifact metadata', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(host.getDiagnostics().capabilities.create.available, true);
  await host.createSession({ id: 'new', cwd: '/work/project', preset: null });
  assert.equal(await host.flush('new'), true);
});

test('missing create API disables only create, without crashing plugin initialization', async () => {
  const f = fixture();
  delete f.ctx.sessionController.create;
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.deepEqual(host.getDiagnostics().capabilities.create.missing, [
    'sessionController.create',
  ]);
  assert.equal((await host.getSession('session-a')).id, 'session-a');
  assert.equal(await host.flush('session-a'), true);
  await assert.rejects(
    host.createSession({ id: 'new', cwd: '/work/project', preset: null }),
    { code: 'network.host_incompatible' },
  );
});

test('empty host remains diagnostic and fails affected operations closed', async () => {
  const host = await createNativeHostPort({ ctx: {} });
  assert.equal(host.getDiagnostics().capabilities.read.available, false);
  assert.equal(await host.flush('session-a'), false);
  await assert.rejects(host.getSession('session-a'), {
    code: 'network.host_incompatible',
  });
});

test('public persistence barrier is awaited and errors cannot acknowledge durability', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  let release, entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  f.ctx.sessionPersistence.flush = () =>
    new Promise((resolve) => {
      entered();
      release = resolve;
    });
  let completed = false;
  const result = host.flush('session-a').then((value) => {
    completed = true;
    return value;
  });
  await started;
  assert.equal(completed, false);
  release();
  assert.equal(await result, true);
  f.ctx.sessionPersistence.flush = async () => {
    throw new Error('private');
  };
  assert.equal(await host.flush('session-a'), false);
  assert.equal(
    host.getDiagnostics().capabilities.durability.lastFailure,
    'public_flush_failed',
  );
});

test('cold queued requests use the public projection without waking an agent', async () => {
  const f = fixture();
  f.coldInbox.set('session-a', {
    'next-turn': [{ source: { kind: 'user', rpcId: 'pending' } }],
    'next-step': [],
  });
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(await host.hasPromptRequest('session-a', 'pending'), true);
  assert.equal(await host.hasPromptRequest('session-a', 'other'), false);
  assert.equal(
    f.calls.some(([kind]) => kind === 'resume'),
    false,
  );
});

test('absent or malformed cold projections are unknown rather than request absence', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  for (const result of [
    null,
    {},
    { asOfSeq: 0, values: {} },
    { asOfSeq: 0, values: { inbox: { 'next-turn': [] } } },
  ]) {
    f.ctx.sessionController.projections = async () => result;
    await assert.rejects(host.hasPromptRequest('session-a', 'r'), {
      code: 'network.host_incompatible',
    });
  }
  assert.equal(
    host.getDiagnostics().capabilities.requests.lastFailure,
    'public_request_read_failed',
  );
});

test('provider identity changes and non-void public flush results fail closed', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  f.ctx.sessionPersistence.flush = async () => true;
  assert.equal(await host.flush('session-a'), false);
  f.ctx.sessionPersistence.flush = async () => {
    f.ctx.sessionPersistence.identity = Symbol('replaced');
  };
  assert.equal(await host.flush('session-a'), false);
});

test('unavailable Cordis service getters are diagnostic without leaking exceptions', async () => {
  const f = fixture();
  Object.defineProperty(f.ctx, 'sessionController', {
    get() {
      throw new Error('private host path');
    },
  });
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(host.getDiagnostics().capabilities.read.available, false);
  assert.equal(
    JSON.stringify(host.getDiagnostics()).includes('private host path'),
    false,
  );
  await assert.rejects(host.getSession('session-a'), {
    code: 'network.host_incompatible',
  });
});

test('a public flush acknowledgement cannot hide a stale stored event prefix', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  f.stored.get('session-a').events.push({
    type: 'user/message',
    data: { source: { kind: 'user', rpcId: 'r' } },
  });
  f.ctx.sessionPersistence.open = async () => ({
    header: { id: 'session-a', cwd: '/work/project' },
    read: async () => ({ events: [] }),
    close: async () => {},
  });
  assert.equal(await host.flush('session-a'), false);
});
