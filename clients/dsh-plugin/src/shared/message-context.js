// @ts-nocheck
import { createSharedMessageNative } from './message-native.js';
import { canonical, fail, validate } from './protocol.js';
import {
  assertFact,
  assertRegistration,
  assertSourceResult,
  id,
  sameRegistration,
  sourceObservationDeadline,
  u64,
} from '../network/source-control/frames.js';
import { timeValue } from '../network/trust/store.js';

/**
 * The socket peer is the trusted Node composition, not a public JSON resolver.
 * Node supplies the current K association; Host independently verifies the
 * original msgnet envelope through the installed, durable v2 root/keyset chain.
 */
export function createSharedMessageContext(
  ctx,
  { call, trust, now = Date.now },
) {
  const native = createSharedMessageNative(ctx, { now });
  const observations = new Map();
  let closed = false;
  return {
    async prepare(request, task, signal) {
      if (closed || signal?.aborted || task.execution.signal?.aborted) fail();
      const binding = task.binding;
      validate('SessionBinding', binding);
      if (
        request.tenantId !== binding.tenantId ||
        request.sessionId !== binding.sessionId ||
        request.taskId !== task.id
      )
        fail('shared.binding_mismatch');
      await trust.open();
      const proof = await native.capture(task.execution, binding);
      const outgoing = {
        tenantId: binding.tenantId,
        sessionId: binding.sessionId,
        taskId: task.id,
        capability: request.capability,
        operationId: proof.operationId,
        bootId: proof.bootId,
        nativeWitness: proof.witness,
      };
      proof.check();
      const response = await call('messageContext', outgoing, signal);
      const check = () => {
        if (closed || signal?.aborted || task.execution.signal?.aborted) fail();
        proof.check();
      };
      check();
      validate('SessionBinding', response?.binding);
      if (
        canonical(response.binding) !== canonical(binding) ||
        response.taskId !== task.id ||
        !id(response.contextId) ||
        !u64(response.producerEpoch)
      )
        fail('shared.binding_mismatch');
      const connection = response.connection;
      if (
        connection?.transport !== 'shared_node_http' ||
        !u64(connection.realm) ||
        !u64(connection.node) ||
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
          connection.incarnation,
        ) ||
        !u64(connection.epoch) ||
        connection.bootId !== proof.bootId ||
        !id(connection.channelNonce) ||
        connection.requestId !== proof.witness.witness_id ||
        !(
          timeValue(connection.validUntil) <=
          timeValue(proof.witness.valid_until)
        ) ||
        !(timeValue(connection.validUntil) > now())
      )
        fail('shared.binding_mismatch');
      const registration = response.registration;
      assertRegistration(registration);
      if (
        registration.realm !== connection.realm ||
        registration.node !== connection.node
      )
        fail('shared.binding_mismatch');
      const source = response.sourceGet;
      if (
        source?.type !== 'source_get_result' ||
        !/^1\.(?:0|[1-9][0-9]{0,3})$/.test(source.app_version ?? '') ||
        source.request_id !== proof.witness.witness_id ||
        source.source?.state !== 'active' ||
        !sameRegistration(source.source.registration, registration)
      )
        fail('shared.revoked');
      assertFact(
        source.source,
        {
          registration,
          scope: 'durable_unicast_send',
          predecessor_binding: source.source.predecessor_binding,
        },
        {
          bindingId: proof.witness.binding_id,
          bindingRevision: proof.witness.binding_revision,
        },
      );
      assertSourceResult(
        source,
        {
          type: 'source_get',
          request_id: source.request_id,
          native_witness: proof.witness,
        },
        now(),
      );
      const auth = source.server_auth;
      const audience = auth?.audience;
      if (
        auth?.version !== 1 ||
        auth.purpose !== 'protocol_response' ||
        auth.alg !== 'Ed25519' ||
        !id(auth.kid) ||
        !u64(auth.keyset_version) ||
        auth.issuer_realm !== connection.realm ||
        audience?.realm !== connection.realm ||
        audience.node !== connection.node ||
        audience.incarnation !== connection.incarnation ||
        audience.epoch !== connection.epoch ||
        auth.request_id !== source.request_id ||
        !id(auth.channel_nonce) ||
        !/^[A-Za-z0-9_-]{85}[AQgw]$/.test(auth.signature ?? '')
      )
        fail('shared.signature_invalid');
      const issued = timeValue(auth.issued_at);
      const expiry = timeValue(auth.not_after);
      const clock = now();
      if (
        !Number.isFinite(issued) ||
        !Number.isFinite(expiry) ||
        issued > clock ||
        expiry <= clock ||
        expiry <= issued ||
        expiry - issued > 300_000
      )
        fail('shared.expired');
      await trust.verify(
        source,
        connection,
        proof.witness.witness_id,
        response.messageTrust,
        check,
      );
      await proof.bindLocal(registration.local);
      check();
      // Bootstrap is a K-session observation only. Actual business requests
      // receive a NEW witness produced after the real typed-local resolver.
      const businessProof = await proof.refresh();
      if (businessProof.bootstrap) fail('shared.binding_mismatch');
      check();
      const old = observations.get(proof.operationId);
      if (
        old &&
        (old.contextId !== response.contextId ||
          old.realm !== connection.realm ||
          old.node !== connection.node ||
          old.incarnation !== connection.incarnation ||
          old.bootId !== connection.bootId ||
          BigInt(connection.epoch) < BigInt(old.epoch) ||
          BigInt(response.producerEpoch) < BigInt(old.producerEpoch) ||
          BigInt(source.policy_revision) < BigInt(old.policyRevision) ||
          BigInt(registration.registration_revision) <
            BigInt(old.registrationRevision) ||
          (source.source.source_binding === old.sourceBinding &&
            BigInt(source.source.revision) < BigInt(old.sourceRevision)) ||
          (source.source.source_binding !== old.sourceBinding &&
            BigInt(connection.epoch) <= BigInt(old.epoch) &&
            BigInt(response.producerEpoch) <= BigInt(old.producerEpoch)))
      )
        fail('shared.revoked');
      const validUntil = Math.min(
        sourceObservationDeadline(source, proof.witness),
        timeValue(connection.validUntil),
      );
      const observation = {
        contextId: response.contextId,
        ...connection,
        producerEpoch: response.producerEpoch,
        policyRevision: source.policy_revision,
        registrationRevision: registration.registration_revision,
        sourceBinding: source.source.source_binding,
        sourceRevision: source.source.revision,
      };
      observations.set(proof.operationId, observation);
      return Object.freeze({
        contextId: response.contextId,
        bootId: proof.bootId,
        nativeWitness: businessProof.witness,
        connection: Object.freeze({ ...connection }),
        address: Object.freeze({
          realm: connection.realm,
          node: connection.node,
          local: registration.local,
        }),
        check() {
          check();
          businessProof.check();
          trust.assertUsable();
          const latest = observations.get(proof.operationId);
          if (
            !latest ||
            [
              'contextId',
              'realm',
              'node',
              'incarnation',
              'bootId',
              'epoch',
              'producerEpoch',
              'policyRevision',
              'registrationRevision',
              'sourceBinding',
              'sourceRevision',
            ].some((key) => latest[key] !== observation[key])
          )
            fail('shared.revoked');
          if (!(now() < validUntil)) fail('shared.expired');
        },
      });
    },
    async close() {
      closed = true;
      native.close();
      observations.clear();
      await trust.close();
    },
  };
}
