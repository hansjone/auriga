import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setup, iso } from './message-fixture.mjs';
import { createProductionSharedPorts } from '../../src/shared/production-ports.js';
import { placementBinding } from '../../src/shared/runtime.js';
import { canonical } from '../../src/shared/protocol.js';
import { signingInput } from '../../src/network/trust/verifier.js';
import { installSharedMessageTool } from '../../src/shared/message-tool.js';

function fixture() {
  const f = setup();
  const key = generateKeyPairSync('ed25519');
  const keys = new Map([['test', key.publicKey]]);
  const listeners = new Map();
  const active = new Map();
  const events = [];
  f.ctx.on = (name, listener) => {
    listeners.set(name, listener);
    return () => listeners.delete(name);
  };
  f.ctx.agents.currentInitiator = () => f.agent;
  let latest;
  const token = (request) => {
    const now = Math.floor(Date.now() / 1000);
    const text = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-capability+jwt' },
      {
        schemaVersion: 1,
        binding: request.binding,
        taskId: request.taskId,
        audience: request.audience,
        scopes: request.scopes,
        credentialVersion: 1,
        issuedAt: now,
        expiresAt: now + 600,
        jti: randomUUID(),
      },
    ]
      .map((value) => Buffer.from(canonical(value)).toString('base64url'))
      .join('.');
    return (
      text +
      '.' +
      sign(null, Buffer.from(text), key.privateKey).toString('base64url')
    );
  };
  const seal = (value, requestId) => {
    value.server_auth = {
      version: 1,
      purpose: 'protocol_response',
      alg: 'Ed25519',
      kid: 'fixture-response',
      issuer_realm: latest.connection.realm,
      issued_at: iso(f.state.now - 1000),
      not_after: iso(f.state.now + 60_000),
      audience: {
        realm: latest.connection.realm,
        node: latest.connection.node,
        incarnation: latest.connection.incarnation,
        epoch: latest.connection.epoch,
      },
      request_id: requestId,
      channel_nonce: latest.connection.channelNonce,
      keyset_version: '1',
    };
    value.server_auth.signature = sign(
      null,
      signingInput(value),
      f.key.privateKey,
    ).toString('base64url');
    return value;
  };
  const bridge = {
    async call(operation, body, signal) {
      events.push({ operation, body, signal });
      if (operation === 'lookup') return { binding: { ...f.binding } };
      if (operation === 'resolve')
        return {
          state: 'ready',
          binding: placementBinding(f.binding),
          authGeneration: 1,
        };
      if (operation === 'resourceSnapshot')
        return {
          view: {
            schemaVersion: 1,
            binding: placementBinding(f.binding),
            source: 'platform',
            viewId: randomUUID(),
            authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
            resources: [],
          },
        };
      if (operation === 'beginTask') {
        active.set(body.taskId, body);
        return { token: token(body) };
      }
      if (operation === 'issueCapability') {
        const task = active.get(body.taskId);
        assert.ok(task);
        assert.ok(body.scopes.every((scope) => task.scopes.includes(scope)));
        return { token: token(body) };
      }
      if (operation === 'finishTask') {
        active.delete(body.taskId);
        return {};
      }
      if (operation === 'messageContext') {
        latest = await f.call(operation, body, signal);
        return latest;
      }
      if (operation === 'send') {
        await f.state.beforeResult?.(body, signal);
        return (
          f.state.sendResult ?? {
            status: 'platform_stored',
            from: {
              version: 1,
              realm: latest.connection.realm,
              node: latest.connection.node,
              local: latest.registration.local,
              type: 'session',
            },
            to: { version: 1, ...body.target, type: 'session' },
            message_id: randomUUID(),
            thread_id: randomUUID(),
            message_class: 'unicast',
            expires_at: iso(f.state.now + 60_000),
          }
        );
      }
      if (operation === 'receive') {
        const type = {
          attemptBegin: 'attempt_result',
          executionAdmit: 'admit_result',
          receipt: 'receipt_committed',
          receiveSet: 'receive_set_result',
        }[body.operation];
        let result;
        if (body.operation === 'fetch')
          result = {
            app_version: '1.0',
            type: 'fetch_result',
            page: seal(
              {
                app_version: '1.0',
                type: 'fetch_page',
                request_id: body.frame.request_id,
                epoch: latest.connection.epoch,
                manifest: [],
              },
              body.frame.request_id,
            ),
            items: [],
          };
        else
          result = seal(
            {
              app_version: '1.0',
              type,
              request_id: body.frame.request_id,
              result: 'entered',
            },
            body.frame.request_id,
          );
        await f.state.afterReceive?.(result, body);
        return result;
      }
      throw new Error('unexpected fixture operation: ' + operation);
    },
    close() {},
  };
  const ports = createProductionSharedPorts(f.ctx, {
    settings: () => f.settings,
    bridge,
    Peer: class {},
    options: {
      tenantId: f.binding.tenantId,
      physicalInstanceId: f.binding.physicalInstanceId,
      identityKeys: keys,
      capabilityKeys: { 'auriga.shared.message': keys },
      messageTrustStore: f.store,
    },
  });
  const open = async (scope = 'message.send') => {
    if (!events.some((event) => event.operation === 'lookup'))
      await listeners.get('agent/request')(
        { agent: f.agent, turn: 1, step: 1, signal: f.controller.signal },
        async () => {},
      );
    const task = await ports.downstream.taskForSession(
      f.binding,
      f.controller.signal,
      { audience: 'auriga.shared.message', scope },
    );
    const capability = await ports.downstream.issue(
      f.binding,
      task.taskId,
      'auriga.shared.message',
      [scope],
      f.controller.signal,
    );
    return {
      tenantId: f.binding.tenantId,
      sessionId: f.binding.sessionId,
      taskId: task.taskId,
      capability,
      signal: f.controller.signal,
    };
  };
  return {
    ...f,
    ports,
    open,
    events,
    listeners,
    active,
    seal,
    latest: () => latest,
    async close() {
      await ports.close();
      await f.contexts.close();
    },
  };
}

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
    assert.equal(f.active.size, 2);
    assert.deepEqual(
      [...f.active.values()].map((value) => value.scopes),
      [['message.send'], ['message.receive']],
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
