import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setup, iso } from './message-fixture.mjs';
import { createProductionSharedPorts } from '../../src/shared/production-ports.js';
import { placementBinding } from '../../src/shared/runtime.js';
import { canonical } from '../../src/shared/protocol.js';
import { signingInput } from '../../src/network/trust/verifier.js';

export function fixture() {
  const f = setup();
  const key = generateKeyPairSync('ed25519');
  const keys = new Map([['test', key.publicKey]]);
  const listeners = new Map();
  const active = new Map();
  const issuedTokens = new Map();
  const events = [];
  f.ctx.on = (name, listener) => {
    const previous = listeners.get(name);
    const combined = async (...args) => {
      await previous?.(...args);
      return listener(...args);
    };
    listeners.set(name, combined);
    return () => {
      if (listeners.get(name) === combined) {
        if (previous) listeners.set(name, previous);
        else listeners.delete(name);
      }
    };
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
    const value =
      text +
      '.' +
      sign(null, Buffer.from(text), key.privateKey).toString('base64url');
    issuedTokens.set(value, request);
    return value;
  };
  const seal = (value, requestId) => {
    const business = value.type === 'delivery';
    value.server_auth = {
      version: 1,
      purpose: business ? 'business_delivery' : 'protocol_response',
      alg: 'Ed25519',
      kid: business ? 'fixture-delivery' : 'fixture-response',
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
      business ? f.deliveryKey.privateKey : f.key.privateKey,
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
        if (body.scopes.includes('task.execute'))
          assert.equal(body.parentCapability, undefined);
        else {
          const parent = issuedTokens.get(body.parentCapability);
          assert.ok(
            parent && active.has(parent.taskId),
            'message child requires the actual live ordinary parent',
          );
          assert.deepEqual(parent.scopes, ['task.execute']);
        }
        await f.state.beforeBegin?.();
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
        return { fenced: true, binding: body.binding, taskId: body.taskId };
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
        return (await f.state.afterReceive?.(result, body)) ?? result;
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
      capabilityKeys: {
        'auriga.shared.message': keys,
        'auriga.shared.enterprise': keys,
      },
      messageTrustStore: f.store,
    },
  });
  let requestReady,
    turn = 1;
  const open = async (scope = 'message.send') => {
    requestReady ??= listeners.get('agent/request')(
      { agent: f.agent, turn, step: 1, signal: f.controller.signal },
      async () => {},
    );
    try {
      await requestReady;
    } catch (error) {
      requestReady = undefined;
      turn++;
      throw error;
    }
    const task = await ports.downstream.taskForSession(
      f.binding,
      f.controller.signal,
      {
        audience: 'auriga.shared.message',
        scope,
        nativeSignal: f.controller.signal,
      },
    );
    const capability = await ports.downstream.issue(
      f.binding,
      task.taskId,
      'auriga.shared.message',
      [scope],
      f.controller.signal,
    );
    await task.close();
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
      await listeners.get('agent/status')({ agent: f.agent, status: 'idle' });
      await ports.close();
      await f.contexts.close();
    },
  };
}
