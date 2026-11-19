import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setup } from './message-fixture.mjs';
import { canonical } from '../../src/shared/protocol.js';
import {
  createSharedRuntime,
  placementBinding,
} from '../../src/shared/runtime.js';
import {
  openSharedExecution,
  openSharedTask,
} from '../../src/shared/task-context.js';

function fixture() {
  const f = setup();
  const pair = generateKeyPairSync('ed25519');
  const keys = new Map([['test', pair.publicKey]]);
  const active = new Map();
  const executions = new WeakMap();
  const tokens = new Map();
  const issued = [];
  const state = { company: false, raw: true, version: 7, reads: 0 };
  const signed = (taskId, scopes) => {
    const now = Math.floor(Date.now() / 1000);
    const claims = {
      schemaVersion: 1,
      binding: { ...f.binding },
      taskId,
      audience: 'auriga.shared.enterprise',
      scopes,
      credentialVersion: scopes.includes('task.execute') ? 100 : state.version,
      issuedAt: now,
      expiresAt: now + 600,
      jti: randomUUID(),
    };
    const raw = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-capability+jwt' },
      claims,
    ]
      .map((part) => Buffer.from(canonical(part)).toString('base64url'))
      .join('.');
    const token =
      raw +
      '.' +
      sign(null, Buffer.from(raw), pair.privateKey).toString('base64url');
    tokens.set(token, claims);
    return token;
  };
  const issue = async (binding, execution, scopes) => {
    assert.deepEqual(binding, f.binding);
    assert.equal(
      executions.has(execution),
      false,
      'company and generic tasks require separate execution handles',
    );
    if (!scopes.includes('task.execute') && !state.company)
      throw new Error('shared.revoked');
    const id = randomUUID();
    executions.set(execution, id);
    active.set(id, scopes);
    const token = signed(id, scopes);
    issued.push({ id, scopes, execution, token });
    return token;
  };
  const ports = {
    lookup: async () => ({ ...f.binding }),
    resolve: async () => ({
      active: true,
      binding: placementBinding(f.binding),
      platformSessionId: f.binding.platformSessionId,
      authGeneration: 1,
    }),
    config: () => ({ allowRawAgentToken: state.raw }),
    helperBase: 'http://127.0.0.1:41234/dsh-team',
    issueExecution: (binding, execution) =>
      issue(binding, execution, ['task.execute']),
    issueTask: issue,
    async finishTask(binding, execution) {
      assert.deepEqual(binding, f.binding);
      active.delete(executions.get(execution));
    },
    async authorizeTask(claims) {
      if (state.invalidPlatform) throw new Error('shared.revoked');
      return {
        active: active.has(claims.taskId),
        credentialVersion: claims.scopes.includes('task.execute')
          ? 100
          : state.version,
        allowRawAgentToken: state.raw,
        enterpriseCredentialState:
          state.status ??
          'SHARED_ENTERPRISE_CREDENTIAL_STATE_' +
            (state.company ? 'AVAILABLE' : 'ABSENT'),
      };
    },
    enterprise: {
      taskId: (token) => tokens.get(token).taskId,
      async credentials() {
        state.reads++;
        return {
          empNo: '00001234',
          token: 'synthetic-company-material',
          credentialVersion: state.version,
        };
      },
    },
  };
  const runtime = createSharedRuntime({
    ports,
    identityKeys: keys,
    capabilityKeys: { 'auriga.shared.enterprise': keys },
    physicalInstanceId: f.binding.physicalInstanceId,
  });
  return { ...f, state, ports, runtime, issued, active, signed };
}

test('team login without company binding authorizes ordinary task.execute with empty UAC variables', async () => {
  const f = fixture();
  const before = process.env.DSH_AURIGA_TOKEN;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.deepEqual(
      f.issued.map((item) => item.scopes),
      [['task.execute']],
    );
    assert.equal(f.state.reads, 0);
    assert.equal(task.env.DSH_AURIGA_ID, '');
    assert.equal(task.env.DSH_AURIGA_TOKEN, '');
    assert.equal(process.env.DSH_AURIGA_TOKEN, before);
    const id = f.ports.enterprise.taskId(task.token);
    await f.runtime.capability(
      task.token,
      f.binding.sessionId,
      id,
      'task.execute',
    );
    await assert.rejects(
      f.runtime.capability(task.token, f.binding.sessionId, id, 'outbound'),
    );
    await assert.rejects(
      f.runtime.capability(task.token, f.binding.sessionId, id, 'credentials'),
    );
    await assert.rejects(
      openSharedTask({
        ctx: f.ctx,
        runtime: f.runtime,
        ports: f.ports,
        execution: { ...f.task.execution },
        raw: true,
      }),
    );
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
  assert.equal(f.active.size, 0);
});

test('available company material uses a separate task ID and keeps task and UAC versions independent', async () => {
  const f = fixture();
  f.state.company = true;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.equal(f.issued.length, 2);
    assert.notEqual(f.issued[0].id, f.issued[1].id);
    assert.notEqual(f.issued[0].execution, f.issued[1].execution);
    assert.deepEqual(f.issued[0].scopes, ['task.execute']);
    assert.deepEqual(f.issued[1].scopes, ['outbound', 'credentials']);
    assert.equal(task.env.DSH_AURIGA_ID, '00001234');
    assert.equal(task.env.DSH_AURIGA_TOKEN, 'synthetic-company-material');
    assert.equal(f.state.reads, 1);
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
  assert.equal(f.active.size, 0);
});

test('raw-disabled company mode retains ordinary authorization and outbound without reading raw material', async () => {
  const f = fixture();
  f.state.company = true;
  f.state.raw = false;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.deepEqual(f.issued[1].scopes, ['outbound']);
    assert.equal(f.state.reads, 0);
    assert.equal(task.env.DSH_AURIGA_TOKEN, '');
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
});

test('unknown typed company state and invalid platform sessions never become ABSENT fallbacks', async (t) => {
  for (const kind of ['missing', 'unspecified', 'invalidPlatform'])
    await t.test(kind, async () => {
      const f = fixture();
      if (kind === 'missing') f.state.status = 0;
      if (kind === 'unspecified')
        f.state.status = 'SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED';
      if (kind === 'invalidPlatform') f.state.invalidPlatform = true;
      await assert.rejects(
        openSharedExecution({
          ctx: f.ctx,
          runtime: f.runtime,
          ports: f.ports,
          execution: f.task.execution,
        }),
      );
      assert.equal(f.active.size, 0);
      assert.equal(f.state.reads, 0);
      f.runtime.close();
      await f.contexts.close();
    });
});
