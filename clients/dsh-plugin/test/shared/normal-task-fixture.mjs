import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setup } from './message-fixture.mjs';
import { canonical } from '../../src/shared/protocol.js';
import {
  createSharedRuntime,
  placementBinding,
} from '../../src/shared/runtime.js';

export function fixture() {
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
      authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
    }),
    resourceView: async () => ({
      schemaVersion: 1,
      binding: placementBinding(f.binding),
      source: 'platform',
      viewId: randomUUID(),
      authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
      resources: [],
    }),
    config: () => ({
      allowRawAgentToken: state.raw,
      outboundAllowedHosts: state.outboundHosts,
    }),
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
      async refreshCapability(token, sessionId, scope) {
        const claims = tokens.get(token);
        if (
          !claims ||
          !active.has(claims.taskId) ||
          !claims.scopes.includes(scope) ||
          sessionId !== f.binding.sessionId ||
          state.invalidPlatform
        )
          throw new Error('shared.revoked');
        return signed(claims.taskId, claims.scopes);
      },
      taskId: (token) => tokens.get(token).taskId,
      async credentials() {
        state.reads++;
        return {
          empNo: '00001234',
          token: state.material ?? 'synthetic-company-material',
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
