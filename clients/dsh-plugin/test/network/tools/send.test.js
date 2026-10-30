/* global structuredClone */
import test from 'node:test';
import { Buffer } from 'node:buffer';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createWorld, peer, self } from '../host/fixtures.js';
import {
  createAgentSendTool,
  mapSendReceipt,
} from '../../../src/network/tools/index.js';

const input = () => ({
  target: 'aur1:3f.1',
  body: { subtype: 'text', text: '你好' },
});
const exec = (id = 'native') => ({ agent: { session: { id } } });
const expectedError = (code) => ({ code });

async function setup(t, options = {}) {
  const w = await createWorld();
  t.after(() => w.ledger.close());
  for (const [id, kind] of [
    ['native', 'session'],
    ['child', 'subagent'],
  ]) {
    w.host.addSession(id, {
      cwd: 'D:/example',
      ...(kind === 'subagent' ? { origin: 'subagent' } : {}),
    });
    await w.adapter.bindExistingSession({
      opId: id,
      hostSessionId: id,
      cwd: 'D:/example',
      kind,
    });
  }
  let clock = 1_000;
  let targetCalls = 0;
  const auth = {
    sourceBinding: 'source-native',
    producerEpoch: '1',
    senderKind: 'agent',
    address: self('1'),
    sendAllowed: true,
    expiresAt: 100_000,
  };
  const config = {
    ledger: w.ledger,
    localAddress: w.adapter.localAddress,
    sourceFor: () => structuredClone(auth),
    resolveTarget: (target) => {
      targetCalls++;
      return {
        version: 1,
        ...target,
        type: target.local === '0' ? 'system' : 'session',
      };
    },
    rateLimit: { messages: 3, windowMs: 1_000 },
    releaseResult: async () => {},
    now: () => clock,
    ...options,
  };
  return {
    w,
    auth,
    config,
    tool: createAgentSendTool(config),
    setClock: (value) => {
      clock = value;
    },
    targetCalls: () => targetCalls,
  };
}

test('tool schema exposes only contract target/body/thread', async (t) => {
  const { tool } = await setup(t);
  assert.deepEqual(Object.keys(tool.inputSchema.properties).sort(), [
    'body',
    'target',
    'thread',
  ]);
  assert.equal(tool.inputSchema.additionalProperties, false);
  assert.deepEqual(tool.inputSchema.required, ['target', 'body']);
});

for (const field of [
  'to',
  'from',
  'sender_kind',
  'message_class',
  'authorization_ref',
  'broadcast',
  'reply_to',
  'server_auth',
  'source_context',
  'source',
  'source_binding',
  'human_intent_id',
  'local',
  'sessionId',
  'kind',
  'intent',
  'flags',
  'ext',
  'in_reply_to',
  'idempotency',
  'producer_epoch',
  'first_contact',
  'firstContactAllowed',
]) {
  test(`rejects model-controlled reserved field ${field}`, async (t) => {
    const { tool, targetCalls } = await setup(t);
    await assert.rejects(
      tool.prepare({ ...input(), [field]: 'forged' }, exec()),
      expectedError('network.protocol_error'),
    );
    assert.equal(targetCalls(), 0);
  });
}

test('requires real host execution session and registered live local', async (t) => {
  const { tool } = await setup(t);
  for (const context of [
    undefined,
    {},
    { sessionId: 'native' },
    exec('unknown'),
  ]) {
    await assert.rejects(
      tool.prepare(input(), context),
      expectedError('network.local_registration_required'),
    );
  }
});

for (const state of [
  'unregistered',
  'reserved',
  'archived',
  'closed',
  'deleted',
  'ambiguous',
]) {
  test(`rejects ${state} source binding`, async (t) => {
    const { tool, w } = await setup(t);
    await w.ledger.update((s) => {
      if (state === 'unregistered') {
        s.locals['1'].state = 'reserved';
        s.locals['1'].registrationId = null;
      } else if (state === 'reserved') s.bindings['1'].state = 'reserved';
      else if (state === 'ambiguous') s.bindings['2'].hostSessionId = 'native';
      else s.bindings['1'].lifecycle = state;
    });
    await assert.rejects(
      tool.prepare(input(), exec()),
      expectedError('network.local_registration_required'),
    );
  });
}

for (const change of [
  { address: peer('1') },
  { address: self('2') },
  { address: self('0') },
  { senderKind: 'human' },
  { sendAllowed: false },
  { expiresAt: 1_000 },
  { sourceBinding: '' },
  { producerEpoch: '0' },
]) {
  test(`rejects forged or unusable authorization ${JSON.stringify(change)}`, async (t) => {
    const { tool, auth } = await setup(t);
    Object.assign(auth, change);
    await assert.rejects(
      tool.prepare(input(), exec()),
      expectedError('network.unauthorized'),
    );
  });
}

test('produces detached contract SubmitRequest, binds source and optional thread', async (t) => {
  const { tool } = await setup(t);
  const modelInput = { ...input(), thread: 'thread-1' };
  const request = await tool.prepare(modelInput, exec());
  assert.deepEqual(request.to, peer('1'));
  assert.deepEqual(request.source, { source_binding: 'source-native' });
  assert.equal(request.idempotency.producer_epoch, '1');
  assert.match(request.idempotency.key, /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/);
  assert.equal(request.kind, 'message');
  assert.equal(request.intent, 'information');
  assert.equal(request.durability, 'durable');
  assert.equal(request.thread_id, 'thread-1');
  for (const key of [
    'from',
    'sender_kind',
    'message_class',
    'authorization_ref',
    'human_intent_id',
  ])
    assert.equal(Object.hasOwn(request, key), false);
  assert.deepEqual(JSON.parse(JSON.stringify(request)), request);
  modelInput.body.text = 'tampered';
  assert.equal(request.body.text, '你好');
});

test('subagent source needs its own exact agent authorization', async (t) => {
  const { tool, auth } = await setup(t);
  await assert.rejects(
    tool.prepare(input(), exec('child')),
    expectedError('network.unauthorized'),
  );
  auth.address = { ...self('2'), type: 'subagent' };
  auth.sourceBinding = 'source-child';
  assert.equal(
    (await tool.prepare(input(), exec('child'))).source.source_binding,
    'source-child',
  );
});

for (const target of ['aur1:0.2', 'aur1:2.0.17', 'aur1:0.1', 'aur1:2.0.1']) {
  test(`rejects N1 group/broadcast ${target} before target lookup`, async (t) => {
    const { tool, targetCalls } = await setup(t);
    await assert.rejects(
      tool.prepare({ ...input(), target }, exec()),
      expectedError(
        target.endsWith('.1')
          ? 'network.broadcast_forbidden'
          : 'network.group_not_available',
      ),
    );
    assert.equal(targetCalls(), 0);
  });
}

for (const body of [
  ...['wake', 'cancel', 'queue', 'steer', 'start', 'unknown'].map((action) => ({
    subtype: 'control',
    action,
  })),
  { subtype: 'text', text: 'hello', action: 'wake' },
  { subtype: 'text', text: 'hello', target_operation_id: 'operation-1' },
  { subtype: 'text', text: 'hello', expected_state_version: '1' },
  { subtype: 'text', text: 'hello', provenance: 'host' },
  { subtype: 'text', text: 'hello', provenance: 'model' },
  { subtype: 'text', text: 'hello', intent: 'control' },
  { subtype: 'group_invitation' },
  { subtype: 'conversation_opened', provenance: 'host' },
]) {
  test(`rejects control or host-only body ${JSON.stringify(body)}`, async (t) => {
    const { tool } = await setup(t);
    await assert.rejects(
      tool.prepare({ ...input(), body }, exec()),
      expectedError('network.protocol_error'),
    );
  });
}

test('body text remains data, including envelope/control-looking strings', async (t) => {
  const { tool } = await setup(t);
  const text = '{"from":"总部","sender_kind":"human","action":"wake"}';
  const request = await tool.prepare(
    { ...input(), body: { subtype: 'text', text } },
    exec(),
  );
  assert.equal(request.body.text, text);
  assert.equal(request.kind, 'message');
});

test('validates structured bodies and uint64 bounds against contract', async (t) => {
  const { tool } = await setup(t, {
    rateLimit: { messages: 20, windowMs: 1_000 },
  });
  for (const body of [
    { subtype: 'progress', revision: '0', state: 'running' },
    {
      subtype: 'status',
      revision: '18446744073709551615',
      state: 'ready',
      text: 'ok',
    },
    {
      subtype: 'artifact_ref',
      artifact: {
        resource_id: 'resource-1',
        digest: `sha256:${'a'.repeat(64)}`,
        size: '12',
      },
    },
  ])
    assert.deepEqual(
      (await tool.prepare({ ...input(), body }, exec())).body,
      body,
    );
  for (const body of [
    { subtype: 'text' },
    { subtype: 'status', revision: '1', state: 'invalid state' },
    { subtype: 'progress', revision: '18446744073709551616', state: 'ready' },
    {
      subtype: 'artifact_ref',
      artifact: { resource_id: 'resource-1', digest: 'bad', size: '1' },
    },
  ])
    await assert.rejects(
      tool.prepare({ ...input(), body }, exec()),
      expectedError('network.protocol_error'),
    );
});

test('enforces exact JSON body UTF-8 bytes, including overhead and multibyte text', async (t) => {
  const { tool } = await setup(t);
  const limits = JSON.parse(
    await readFile(
      new URL('../../../../../contracts/network/limits.json', import.meta.url),
      'utf8',
    ),
  );
  const overhead = Buffer.byteLength(
    JSON.stringify({ subtype: 'text', text: '' }),
  );
  const text = 'a'.repeat(limits.encoding.maxBodyBytes - overhead);
  await tool.prepare({ ...input(), body: { subtype: 'text', text } }, exec());
  for (const value of [text + 'a', '你'.repeat(6_000)]) {
    await assert.rejects(
      tool.prepare(
        { ...input(), body: { subtype: 'text', text: value } },
        exec(),
      ),
      expectedError('network.quota_exceeded'),
    );
  }
});

test('requires explicit trusted rate policy', async (t) => {
  const { config } = await setup(t);
  for (const rateLimit of [
    undefined,
    { messages: 0, windowMs: 10 },
    { messages: 3, windowMs: 0 },
    { messages: 1.5, windowMs: 10 },
  ]) {
    assert.throws(
      () => createAgentSendTool({ ...config, rateLimit }),
      TypeError,
    );
  }
});

test('node-wide sliding rate is atomic under concurrency, shared across tools and clocks cannot reset it', async (t) => {
  const { tool, config, setClock, targetCalls } = await setup(t);
  const sibling = createAgentSendTool(config);
  const results = await Promise.allSettled(
    Array.from({ length: 8 }, (_, i) =>
      (i % 2 ? sibling : tool).prepare(input(), exec()),
    ),
  );
  assert.equal(results.filter((r) => r.status === 'fulfilled').length, 3);
  for (const r of results.filter((r) => r.status === 'rejected'))
    assert.equal(r.reason.code, 'network.quota_exceeded');
  assert.equal(targetCalls(), 3);
  setClock(0);
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.quota_exceeded'),
  );
  setClock(1_999);
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.quota_exceeded'),
  );
  setClock(2_000);
  await tool.prepare(input(), exec());
});

test('target lookup failure consumes rate budget and masks private detail', async (t) => {
  const { tool } = await setup(t, {
    resolveTarget: () => {
      throw new Error('private missing session');
    },
  });
  for (let i = 0; i < 3; i++)
    await assert.rejects(
      tool.prepare(input(), exec()),
      expectedError('network.target_unavailable'),
    );
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.quota_exceeded'),
  );
});

test('revalidates source after asynchronous target lookup', async (t) => {
  const { tool, w } = await setup(t, {
    resolveTarget: async () => {
      await w.adapter.updateBinding('1', { lifecycle: 'closed' });
      return peer('1');
    },
  });
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.local_registration_required'),
  );
});

test('does not infer session vs subagent and refuses resolver redirection', async (t) => {
  const { tool } = await setup(t, { resolveTarget: () => peer('2') });
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.target_unavailable'),
  );
});

test('all target-related receipts have the same model-visible result and no private metadata', () => {
  const expected = { status: 'request_submitted' };
  for (const receipt of [
    ...['platform_stored', 'request_submitted', 'contact_pending'].map(
      (status) => ({
        status,
        message_id: 'secret',
        from: self('1'),
        to: peer('1'),
      }),
    ),
    ...[
      'network.target_unavailable',
      'network.contact_denied',
      'network.contact_expired',
      'network.quota_exceeded',
      'network.host_incompatible',
      'network.revoked',
    ].map((code) => ({ code, reason: 'private', host_request_id: 'secret' })),
  ])
    assert.deepEqual(mapSendReceipt(receipt), expected);
  assert.deepEqual(mapSendReceipt(null), {
    status: 'error',
    code: 'network.outcome_unknown',
  });
});

test('execute only calls injected submit once; transport failure is unknown with no retry', async (t) => {
  const { tool } = await setup(t);
  let calls = 0;
  const result = await tool.execute(input(), exec(), async (request) => {
    calls++;
    assert.equal(request.source.source_binding, 'source-native');
    throw new Error('private transport token');
  });
  assert.deepEqual(result, {
    status: 'error',
    code: 'network.outcome_unknown',
  });
  assert.equal(calls, 1);
});

test('execute masks unresolved target exactly like accepted submission and never submits', async (t) => {
  const { tool } = await setup(t, { resolveTarget: () => null });
  const result = await tool.execute(input(), exec(), () =>
    assert.fail('must not submit'),
  );
  assert.deepEqual(result, { status: 'request_submitted' });
});

test('authorization cannot coerce a missing binding or non-boolean permission', async (t) => {
  const { tool, auth } = await setup(t, {
    rateLimit: { messages: 10, windowMs: 1_000 },
  });
  for (const value of [undefined, null, { toString: () => 'forged' }]) {
    auth.sourceBinding = value;
    await assert.rejects(
      tool.prepare(input(), exec()),
      expectedError('network.unauthorized'),
    );
  }
  auth.sourceBinding = 'source-native';
  auth.sendAllowed = 'yes';
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.unauthorized'),
  );
});

test('last asynchronous authorization cannot switch native session to another local', async (t) => {
  const { config, auth, w } = await setup(t);
  let calls = 0;
  const tool = createAgentSendTool({
    ...config,
    sourceFor: async () => {
      if (++calls === 2)
        await w.ledger.update((s) => {
          s.bindings['1'].hostSessionId = 'old';
          s.bindings['2'].hostSessionId = 'native';
        });
      return structuredClone(auth);
    },
  });
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.unauthorized'),
  );
});

test('rejects expired or revoked authorization after target resolution', async (t) => {
  const { config, auth } = await setup(t);
  const tool = createAgentSendTool({
    ...config,
    resolveTarget: () => {
      auth.sendAllowed = false;
      return peer('1');
    },
  });
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.unauthorized'),
  );
});

test('input snapshot rejects inherited fields, getters and non-JSON values without evaluating them', async (t) => {
  const { tool } = await setup(t);
  const getter = { ...input() };
  Object.defineProperty(getter, 'from', {
    enumerable: true,
    get() {
      return assert.fail('getter evaluated');
    },
  });
  const nonEnumerable = { ...input() };
  Object.defineProperty(nonEnumerable, 'from', { value: 'forged' });
  const symbolic = { ...input(), [Symbol('from')]: 'forged' };
  for (const value of [
    Object.assign(Object.create({ from: 'forged' }), input()),
    getter,
    nonEnumerable,
    symbolic,
    { ...input(), from: undefined },
    { ...input(), body: new Date() },
  ]) {
    await assert.rejects(
      tool.prepare(value, exec()),
      expectedError('network.protocol_error'),
    );
  }
});

test('short and complete targets normalize identically, subagent kind is authoritative', async (t) => {
  const { tool } = await setup(t, {
    resolveTarget: () => ({ ...peer('1'), type: 'subagent' }),
  });
  for (const target of ['aur1:3f.1', 'aur1:2.3f.1']) {
    const request = await tool.prepare({ ...input(), target }, exec());
    assert.deepEqual(request.to, { ...peer('1'), type: 'subagent' });
    assert.equal(Object.hasOwn(request, 'thread_id'), false);
  }
});

test('bad address forms and invalid thread cannot reach target lookup', async (t) => {
  const { tool, targetCalls } = await setup(t);
  for (const target of [
    'aur1:03f.1',
    'aur1:3F.1',
    'aur1:2.3f.1.2',
    'aur1:2.0.0',
    'aur1:3.3f.1',
    'aur2:3f.1',
  ]) {
    await assert.rejects(tool.prepare({ ...input(), target }, exec()));
  }
  for (const thread of ['', null, 'x'.repeat(129), 'thread with spaces']) {
    await assert.rejects(
      tool.prepare({ ...input(), thread }, exec()),
      expectedError('network.protocol_error'),
    );
  }
  assert.equal(targetCalls(), 0);
});

test('clock rollback cannot revive expired source authorization', async (t) => {
  const { tool, auth, setClock } = await setup(t);
  auth.expiresAt = 2_000;
  setClock(2_000);
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.unauthorized'),
  );
  setClock(0);
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.unauthorized'),
  );
});

test('another registered local cannot reset the node-wide budget', async (t) => {
  const { tool, targetCalls } = await setup(t, {
    rateLimit: { messages: 1, windowMs: 1_000 },
    sourceFor: ({ address }) => ({
      address,
      senderKind: 'agent',
      sourceBinding: `source-${address.local}`,
      producerEpoch: '1',
      sendAllowed: true,
      expiresAt: 100_000,
    }),
  });
  await tool.prepare(input(), exec());
  await assert.rejects(
    tool.prepare(input(), exec('child')),
    expectedError('network.quota_exceeded'),
  );
  assert.equal(targetCalls(), 1);
});

test('system targets require an explicit trusted first_contact grant before resolution', async (t) => {
  const { tool, auth, targetCalls } = await setup(t, {
    rateLimit: { messages: 10, windowMs: 1_000 },
  });
  for (const firstContactAllowed of [undefined, false, 'true', 1]) {
    auth.firstContactAllowed = firstContactAllowed;
    await assert.rejects(
      tool.prepare({ ...input(), target: 'aur1:3f.0' }, exec()),
      expectedError('network.first_contact_forbidden'),
    );
  }
  assert.equal(targetCalls(), 0);
});

test('verified first_contact grant fixes kind, scope and intent for short and full system targets', async (t) => {
  const { tool, auth } = await setup(t);
  auth.firstContactAllowed = true;
  for (const target of ['aur1:3f.0', 'aur1:2.3f.0']) {
    const request = await tool.prepare({ ...input(), target }, exec());
    assert.equal(request.kind, 'first_contact');
    assert.equal(request.contact_scope, 'conversation');
    assert.equal(request.intent, 'request');
    assert.equal(request.durability, 'durable');
    assert.deepEqual(request.to, peer('0'));
  }
});

test('first_contact grant is rechecked after asynchronous target resolution', async (t) => {
  const { config, auth } = await setup(t);
  auth.firstContactAllowed = true;
  const tool = createAgentSendTool({
    ...config,
    resolveTarget: async () => {
      auth.firstContactAllowed = false;
      return peer('0');
    },
  });
  await assert.rejects(
    tool.prepare({ ...input(), target: 'aur1:3f.0' }, exec()),
    expectedError('network.first_contact_forbidden'),
  );
});

test('unicast to a session never becomes first_contact from an ambient grant', async (t) => {
  const { tool, auth } = await setup(t);
  auth.firstContactAllowed = true;
  const request = await tool.prepare(input(), exec());
  assert.equal(request.kind, 'message');
  assert.equal(Object.hasOwn(request, 'contact_scope'), false);
});

test('unauthorized callers do not spend the authorized node send budget', async (t) => {
  const { tool, auth } = await setup(t);
  auth.sendAllowed = false;
  for (let i = 0; i < 5; i++)
    await assert.rejects(
      tool.prepare(input(), exec()),
      expectedError('network.unauthorized'),
    );
  auth.sendAllowed = true;
  for (let i = 0; i < 3; i++) await tool.prepare(input(), exec());
});

test('local quota rejection preserves aggregate capacity for other registered locals', async (t) => {
  const { tool, targetCalls } = await setup(t, {
    rateLimit: { messages: 3, localMessages: 1, windowMs: 1_000 },
    sourceFor: ({ address }) => ({
      address,
      senderKind: 'agent',
      sourceBinding: `source-${address.local}`,
      producerEpoch: '1',
      sendAllowed: true,
      expiresAt: 100_000,
    }),
  });
  await tool.prepare(input(), exec());
  await assert.rejects(
    tool.prepare(input(), exec()),
    expectedError('network.quota_exceeded'),
  );
  await tool.prepare(input(), exec('child'));
  assert.equal(targetCalls(), 2);
});

test('model result release requires an explicit trusted privacy boundary', async (t) => {
  const { config } = await setup(t);
  assert.throws(
    () => createAgentSendTool({ ...config, releaseResult: undefined }),
    TypeError,
  );
});

test('accepted and unavailable targets use the same result release path without target metadata', async (t) => {
  const releases = [];
  const { config } = await setup(t, {
    rateLimit: { messages: 10, windowMs: 1_000 },
  });
  const releaseResult = async (...args) => {
    releases.push(args);
  };
  for (const resolveTarget of [
    () => peer('1'),
    () => null,
    () => {
      throw new Error('private target state');
    },
  ]) {
    const tool = createAgentSendTool({
      ...config,
      resolveTarget,
      releaseResult,
    });
    assert.deepEqual(
      await tool.execute(input(), exec(), () => ({
        status: 'platform_stored',
      })),
      { status: 'request_submitted' },
    );
  }
  assert.deepEqual(releases, [[1_000], [1_000], [1_000]]);
});

test('result release failure never exposes receipt details', async (t) => {
  const { tool } = await setup(t, {
    releaseResult: async () => {
      throw new Error('private queue state');
    },
  });
  assert.deepEqual(
    await tool.execute(input(), exec(), () => ({
      status: 'platform_stored',
      message_id: 'secret',
    })),
    { status: 'error', code: 'network.outcome_unknown' },
  );
});
