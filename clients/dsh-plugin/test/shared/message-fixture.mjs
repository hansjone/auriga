import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { fixture } from '../network/host/native/fixtures.js';
import { createSharedMessageContext } from '../../src/shared/message-context.js';
import { createSharedMessageTrust } from '../../src/shared/message-trust.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { signingInput } from '../../src/network/trust/verifier.js';
import { createV2Signer } from '../network/trust/v2-keyset-fixtures.js';

export const iso = (value) =>
  new Date(value).toISOString().replace('.000Z', 'Z');

export function setup() {
  const f = fixture();
  const sessionId = randomUUID();
  f.seed(sessionId);
  const agent = f.attach(sessionId);
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'shared',
    epoch: 1,
    revision: 1,
    sessionId,
    platformSessionId: randomUUID(),
    authGeneration: 1,
  };
  const controller = new AbortController();
  const task = {
    id: randomUUID(),
    binding,
    execution: { agent, signal: controller.signal },
  };
  const request = {
    tenantId: binding.tenantId,
    sessionId,
    taskId: task.id,
    capability: 'synthetic-capability',
  };
  const contextId = randomUUID();
  const registration = {
    registration_id: randomUUID(),
    allocation_op_id: randomUUID(),
    allocation_fingerprint: 'sha256:' + 'a'.repeat(64),
    registration_revision: '1',
    local: '7',
    address_type: 'session',
  };
  const state = { now: Math.floor(Date.now() / 1000) * 1000, calls: [] };
  const key = generateKeyPairSync('ed25519');
  const root = generateKeyPairSync('ed25519');
  const signer = createV2Signer(root, 'test-root');
  const payload = {
    schema_version: 1,
    realm: '2',
    keyset_version: '1',
    issued_at: iso(state.now - 1000),
    valid_until: iso(state.now + 86_399_000),
    keys: [
      {
        kid: 'fixture-response',
        alg: 'Ed25519',
        purpose: 'protocol_response',
        state: 'active',
        not_before: iso(state.now - 60_000),
        not_after: iso(state.now + 7 * 86_400_000),
        public_key_spki: key.publicKey
          .export({ type: 'spki', format: 'der' })
          .toString('base64url'),
      },
    ],
  };
  const publication = signer.publication(payload, binding.tenantId);
  const material = {
    control: publication.control,
    publication,
    announcement: signer.announcement(payload, {}, binding.tenantId),
  };
  const settings = {
    messageTrust: {
      realm: '2',
      roots: [
        {
          kid: 'test-root',
          public_key_spki: root.publicKey
            .export({ type: 'spki', format: 'der' })
            .toString('base64url'),
        },
      ],
    },
  };
  const incarnation = randomUUID();
  const channelNonce = randomUUID();
  function response(input) {
    const sourceGet = {
      app_version: '1.0',
      type: 'source_get_result',
      request_id: input.nativeWitness.witness_id,
      source: {
        source_binding: 'source-' + contextId,
        registration: { ...registration },
        scope: 'durable_unicast_send',
        revision: '1',
        state: 'active',
        not_before: iso(state.now - 1000),
        expires_at: iso(state.now + 600_000),
        predecessor_binding: '',
        native_binding: {
          binding_id: input.nativeWitness.binding_id,
          binding_revision: input.nativeWitness.binding_revision,
        },
      },
      policy_revision: '1',
      observed_at: iso(state.now),
      valid_until: input.nativeWitness.valid_until,
      witness_id: input.nativeWitness.witness_id,
    };
    sourceGet.server_auth = {
      version: 1,
      purpose: 'protocol_response',
      alg: 'Ed25519',
      kid: 'fixture-response',
      issuer_realm: '2',
      issued_at: iso(state.now - 1000),
      not_after: iso(state.now + 60_000),
      audience: { realm: '2', node: '64', incarnation, epoch: '3' },
      request_id: sourceGet.request_id,
      channel_nonce: channelNonce,
      keyset_version: '1',
    };
    sourceGet.server_auth.signature = sign(
      null,
      signingInput(sourceGet),
      key.privateKey,
    ).toString('base64url');
    return {
      binding: { ...binding },
      taskId: input.taskId,
      contextId,
      producerEpoch: '2',
      connection: {
        transport: 'shared_node_http',
        realm: '2',
        node: '64',
        incarnation,
        epoch: '3',
        bootId: input.bootId,
        channelNonce,
        requestId: input.nativeWitness.witness_id,
        validUntil: input.nativeWitness.valid_until,
      },
      registration: { realm: '2', node: '64', ...registration },
      sourceGet,
      messageTrust: material,
    };
  }
  const call = async (operation, body, signal) => {
    state.calls.push({ operation, body, signal });
    const value = response(body);
    await state.change?.(value, body, signal);
    if (state.resign && value.sourceGet.server_auth)
      value.sourceGet.server_auth.signature = sign(
        null,
        signingInput(value.sourceGet),
        key.privateKey,
      ).toString('base64url');
    return value;
  };
  const store = createMemoryLedgerStore();
  const trust = createSharedMessageTrust({
    settings: () => settings,
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    store,
    now: () => state.now,
  });
  const contexts = createSharedMessageContext(f.ctx, {
    call,
    trust,
    now: () => state.now,
  });
  return {
    ...f,
    agent,
    binding,
    task,
    request,
    controller,
    registration,
    state,
    contexts,
    response,
    call,
    trust,
    settings,
    material,
    root,
    key,
    store,
  };
}
