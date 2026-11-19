import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { setup, iso } from './message-fixture.mjs';
import { signingInput } from '../../src/network/trust/verifier.js';
import { installSharedMessageTool } from '../../src/shared/message-tool.js';
import { fixture } from './production-message-fixture.mjs';
test('production send registers true native context then forwards only the bound K request and original body', async () => {
  const f = fixture();
  try {
    const request = {
      ...(await f.open()),
      messageId: randomUUID(),
      target: { realm: '2', node: '65', local: '9' },
      body: { subtype: 'text', text: 'hello' },
      owner: 'forged',
    };
    const result = await f.ports.downstream.send(request);
    assert.equal(result.status, 'platform_stored');
    const sent = f.events.find((event) => event.operation === 'send').body;
    assert.equal(sent.capability, request.capability);
    assert.equal(sent.taskId, request.taskId);
    assert.equal(sent.owner, undefined);
    assert.equal(sent.contextId, f.latest().contextId);
    assert.equal(sent.bootId, f.latest().connection.bootId);
    assert.notEqual(
      sent.nativeWitness.witness_id,
      f.latest().sourceGet.witness_id,
    );
    assert.equal(
      sent.nativeWitness.binding_id,
      f.latest().sourceGet.source.native_binding.binding_id,
    );
    assert.deepEqual(sent.body, request.body);
    assert.ok(
      f.events.findIndex((event) => event.operation === 'messageContext') <
        f.events.findIndex((event) => event.operation === 'send'),
    );
  } finally {
    await f.close();
  }
});

test('send and receive have separate live K tasks without widening the first task scopes', async () => {
  const f = fixture();
  try {
    const send = await f.open();
    const receive = await f.open('message.receive');
    assert.notEqual(send.taskId, receive.taskId);
    assert.equal(f.active.size, 3);
    assert.deepEqual(
      [...f.active.values()].map((value) => value.scopes),
      [['task.execute'], ['message.send'], ['message.receive']],
    );
    await f.ports.downstream.send({
      ...send,
      messageId: randomUUID(),
      target: { realm: '2', node: '65', local: '1' },
      body: { subtype: 'text', text: 'hello' },
    });
    for (const operation of [
      'fetch',
      'attemptBegin',
      'executionAdmit',
      'receipt',
      'receiveSet',
    ]) {
      const frame = {
        app_version: '1.0',
        type: {
          fetch: 'fetch',
          attemptBegin: 'attempt_begin',
          executionAdmit: 'execution_admit',
          receipt: 'receipt',
          receiveSet: 'receive_set',
        }[operation],
        request_id: randomUUID(),
        epoch: '3',
        boot_id: f.latest().connection.bootId,
      };
      const value = await f.ports.downstream.receive({
        ...receive,
        operation,
        frame,
      });
      assert.ok(value.type === 'fetch_result' || value.server_auth);
    }
    assert.equal(
      f.events.filter((event) => event.operation === 'receive').length,
      5,
    );
  } finally {
    await f.close();
  }
});

test('expired or absent message trust does not affect creation or model task bookkeeping', async () => {
  const f = fixture();
  try {
    delete f.settings.messageTrust;
    const request = await f.open();
    assert.ok(request.capability);
    await assert.rejects(
      f.ports.downstream.send({
        ...request,
        messageId: randomUUID(),
        target: { realm: '2', node: '65', local: '1' },
        body: { subtype: 'text', text: 'hello' },
      }),
    );
    assert.equal(
      f.events.some((event) =>
        ['messageContext', 'send'].includes(event.operation),
      ),
      false,
    );
  } finally {
    await f.close();
  }
});

test('production rejects forged task material, late host changes, wrong sender and malformed submit results', async (t) => {
  for (const kind of ['token', 'task', 'lateHost', 'sender', 'schema']) {
    await t.test(kind, async () => {
      const f = fixture();
      try {
        const request = {
          ...(await f.open()),
          messageId: randomUUID(),
          target: { realm: '2', node: '65', local: '1' },
          body: { subtype: 'text', text: 'hello' },
        };
        if (kind === 'token') request.capability = 'forged';
        if (kind === 'task') request.taskId = randomUUID();
        if (kind === 'lateHost')
          f.state.beforeResult = () => {
            f.agent.session.header.cwd = '/changed';
          };
        if (kind === 'schema')
          f.state.sendResult = { status: 'platform_stored' };
        if (kind === 'sender')
          f.state.beforeResult = () => {
            f.state.sendResult = {
              status: 'platform_stored',
              from: {
                version: 1,
                realm: '2',
                node: '64',
                local: '999',
                type: 'session',
              },
              to: { version: 1, ...request.target, type: 'session' },
              message_id: randomUUID(),
              thread_id: randomUUID(),
              message_class: 'unicast',
              expires_at: iso(f.state.now + 60_000),
            };
          };
        await assert.rejects(f.ports.downstream.send(request));
      } finally {
        await f.close();
      }
    });
  }
});

test('receive verifies original request id, real signature and page manifest, without sending a receipt', async (t) => {
  for (const kind of ['request', 'signature', 'manifest']) {
    await t.test(kind, async () => {
      const f = fixture();
      try {
        const request = await f.open('message.receive');
        f.state.afterReceive = (value) => {
          if (kind === 'request')
            value.page = f.seal(
              { ...value.page, request_id: randomUUID() },
              randomUUID(),
            );
          if (kind === 'signature')
            value.page.server_auth.signature = sign(
              null,
              signingInput(value.page),
              generateKeyPairSync('ed25519').privateKey,
            ).toString('base64url');
          if (kind === 'manifest')
            value.page = f.seal(
              {
                ...value.page,
                manifest: [
                  {
                    item_type: 'delivery',
                    mail_seq: '1',
                    digest: 'sha256:' + 'a'.repeat(64),
                  },
                ],
              },
              value.page.request_id,
            );
        };
        await assert.rejects(
          f.ports.downstream.receive({
            ...request,
            operation: 'fetch',
            frame: {
              app_version: '1.0',
              type: 'fetch',
              request_id: randomUUID(),
              epoch: '3',
            },
          }),
        );
        assert.equal(
          f.events.some((event) => event.body?.operation === 'receipt'),
          false,
        );
      } finally {
        await f.close();
      }
    });
  }
});

test('shared message tool uses the live Host call id and never exposes private submit details', async () => {
  const f = setup();
  let tool;
  const sent = [];
  f.ctx.tools = {
    register(value) {
      tool = value;
      return () => {};
    },
  };
  f.ctx.aurigaShared = {
    downstream: {
      async send(...args) {
        sent.push(args);
        return { status: 'platform_stored', message_id: 'private-message-id' };
      },
    },
  };
  installSharedMessageTool(f.ctx, { settings: () => f.settings });
  const execution = { ...f.task.execution, callId: 'actual-host-call' };
  const input = { to: 'aur1:2.41.9', body: { subtype: 'text', text: 'hello' } };
  assert.deepEqual(await tool.execute(input, execution), {
    status: 'request_submitted',
  });
  await tool.execute(input, execution);
  assert.equal(sent[0][1].messageId, sent[1][1].messageId);
  assert.equal(sent[0][0], f.binding.sessionId);
  assert.deepEqual(sent[0][1].target, { realm: '2', node: '65', local: '9' });
  for (const args of [
    { ...input, owner: 'forged' },
    { ...input, from: 'forged' },
    { ...input, thread: 'not-supported' },
    { ...input, to: 'aur1:2.0.1' },
  ])
    await assert.rejects(tool.execute(args, execution));
  await assert.rejects(
    tool.execute(input, { ...execution, agent: { ...f.agent } }),
  );
  await f.contexts.close();
});

test('concurrent consumers join the same pending Begin ACK before issuing message capabilities', async () => {
  const f = fixture();
  let started, release;
  const ready = new Promise((resolve) => {
    started = resolve;
  });
  const held = new Promise((resolve) => {
    release = resolve;
  });
  f.state.beforeBegin = async () => {
    started();
    await held;
  };
  try {
    const first = f.open();
    await ready;
    const second = f.open();
    assert.equal(
      f.events.some((entry) => entry.operation === 'issueCapability'),
      false,
    );
    release();
    const values = await Promise.all([first, second]);
    assert.equal(values[0].taskId, values[1].taskId);
    assert.equal(
      f.events.filter((entry) => entry.operation === 'beginTask').length,
      2,
    );
  } finally {
    release();
    await f.close();
  }
});

test('idle during a delayed Begin compensates a late committed task and never resurrects its token', async () => {
  const f = fixture();
  f.state.beforeBegin = () =>
    f.listeners.get('agent/status')({ agent: f.agent, status: 'idle' });
  try {
    await assert.rejects(f.open());
    assert.equal(f.active.size, 0);
    assert.ok(
      f.events.filter((entry) => entry.operation === 'finishTask').length >= 1,
    );
  } finally {
    await f.close();
  }
});

test('a failed Begin is not retried implicitly and a later explicit call can prepare a fresh task', async () => {
  const f = fixture();
  let fail = true;
  f.state.beforeBegin = () => {
    if (fail) {
      fail = false;
      throw new Error('synthetic begin failure');
    }
  };
  try {
    await assert.rejects(f.open());
    assert.equal(
      f.events.filter((entry) => entry.operation === 'beginTask').length,
      1,
    );
    const next = await f.open();
    assert.ok(next.taskId);
    assert.equal(f.active.size, 2);
    assert.equal(
      f.events.filter((entry) => entry.operation === 'beginTask').length,
      3,
    );
  } finally {
    await f.close();
  }
});
