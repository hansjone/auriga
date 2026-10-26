/* global structuredClone */
import { sign } from 'node:crypto';
import { fixture as nativeFixture } from '../host/native/fixtures.js';
import { IDENTITY, createFixturePlatform } from '../host/fixtures.js';
import { createHostAdapter } from '../../../src/network/host/adapter.js';
import { createNativeHostPort } from '../../../src/network/host/native/host-port.js';
import { openLedger } from '../../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';
import { createSourceControl } from '../../../src/network/source-control/controller.js';
import { connectionSourceClient } from '../../../src/network/source-control/connection.js';
import { decodeJson, encodeJson } from '../../../src/network/session/frames.js';
import { signingInput, digest } from '../../../src/network/trust/verifier.js';
import { publicationFixture, openTestTrust } from '../trust/keyset-fixtures.js';

export function latch() {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { promise, release };
}
export const iso = (value) =>
  new Date(Math.floor(value / 1000) * 1000).toISOString().replace('.000Z', 'Z');

// Explicit collab/IAM contract peer fixture. No Java, PG or real collab claim.
export function sourcePeer(signer, options = {}) {
  const operations = new Map(),
    facts = new Map(),
    revokes = new Map();
  const requests = [];
  function reply(request, ready) {
    requests.push(structuredClone(request));
    let result;
    if (request.type === 'source_register') {
      result = operations.get(request.operation_id);
      if (!result) {
        if (options.failure || request.mode === 'reconcile') {
          const body = {
            registration: request.registration,
            scope: request.scope,
            expected_revision: request.expected_revision,
            predecessor_binding: request.predecessor_binding,
          };
          const failure = options.failure ?? 'not_accepted';
          result = {
            operation_id: request.operation_id,
            result: failure,
            source: null,
            failure_evidence: {
              authority: failure === 'not_accepted' ? 'collab' : 'iam',
              reason:
                failure === 'not_accepted'
                  ? 'rejected_before_pending'
                  : 'original_connection_invalid_uncommitted',
              request_digest:
                failure === 'not_accepted'
                  ? digest(body)
                  : 'sha256:' + 'b'.repeat(64),
            },
          };
        } else {
          const source = {
            source_binding: 'source-' + (facts.size + 1),
            registration: request.registration,
            scope: request.scope,
            revision: '1',
            state: 'active',
            not_before: iso(signer.now() - 1000),
            expires_at: iso(signer.now() + 3600000),
            predecessor_binding: request.predecessor_binding,
            native_binding: {
              binding_id: request.native_witness.binding_id,
              binding_revision: request.native_witness.binding_revision,
            },
          };
          result = {
            operation_id: request.operation_id,
            result: 'registered',
            source,
            failure_evidence: null,
          };
          facts.set(source.source_binding, structuredClone(source));
        }
        operations.set(request.operation_id, structuredClone(result));
      }
    } else if (request.type === 'source_get') {
      const source = structuredClone(facts.get(request.source_binding));
      result = {
        source,
        policy_revision: '7',
        observed_at: iso(signer.now()),
        witness_id: request.native_witness.witness_id,
      };
      if (source.state === 'active')
        result.valid_until = request.native_witness.valid_until;
    } else if (request.type === 'source_revoke') {
      result = revokes.get(request.operation_id);
      if (!result) {
        const source = facts.get(request.source_binding);
        source.state = source.state === 'expired' ? 'expired' : 'revoked';
        source.revision = String(BigInt(source.revision) + 1n);
        result = {
          operation_id: request.operation_id,
          source_binding: request.source_binding,
          revision: source.revision,
          state: source.state,
          lineage_stopped: true,
        };
        revokes.set(request.operation_id, structuredClone(result));
      }
    } else throw new Error('unexpected source fixture request');
    return seal(
      signer,
      {
        ...structuredClone(result),
        app_version: '1.0',
        type: request.type + '_result',
        request_id: request.request_id,
      },
      ready,
    );
  }
  return { reply, operations, facts, revokes, requests };
}
export function seal(signer, result, ready) {
  const frame = {
    ...result,
    server_auth: {
      version: 1,
      purpose: 'protocol_response',
      alg: 'Ed25519',
      kid: 'response',
      issuer_realm: '2',
      issued_at: iso(signer.now() - 1000),
      not_after: iso(signer.now() + 120000),
      audience: {
        realm: '2',
        node: '64',
        incarnation: 'inc-1',
        epoch: ready.epoch,
      },
      request_id: result.request_id,
      channel_nonce: ready.channelNonce,
      keyset_version: '5',
    },
  };
  frame.server_auth.signature = sign(
    null,
    signingInput(frame),
    signer.pair.privateKey,
  ).toString('base64url');
  return frame;
}

export async function setup(t, options = {}) {
  const native = options.native ?? nativeFixture();
  const agent = native.agents.get('session-a') ?? native.attach('session-a');
  const store =
    options.store ??
    createMemoryLedgerStore({ beforeCommit: options.beforeCommit });
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  const platform = createFixturePlatform();
  const host = await createNativeHostPort({ ctx: native.ctx });
  const adapter = createHostAdapter({
    ledger,
    host,
    network: platform.network,
  });
  if (!ledger.peek().bindings['1'])
    await adapter.bindExistingSession({
      opId: 'join-1',
      hostSessionId: agent.id,
      cwd: agent.session.header.cwd,
      kind: 'session',
    });
  const signer = options.signer ?? publicationFixture();
  const peer = options.peer ?? sourcePeer(signer, options);
  const trust = await openTestTrust({
    cache: { async load() {}, async commit() {} },
    config: signer.payload,
    realm: '2',
    now: signer.now,
  });
  const ready = options.ready ?? {
    epoch: '3',
    bootId: 'boot-a',
    channelNonce: 'nonce-a',
  };
  const listeners = new Set();
  let current = true;
  const abort = new AbortController();
  const transport = {
    epoch: 1,
    state: 'ready',
    onMessage(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    async send(bytes) {
      const request = decodeJson(bytes),
        result = peer.reply(request, ready);
      await options.beforeReply?.(request, result);
      if (options.resign)
        result.server_auth.signature = sign(
          null,
          signingInput(result),
          signer.pair.privateKey,
        ).toString('base64url');
      if (options.drop?.(request)) throw new Error('fixture response lost');
      for (const listener of listeners) listener(encodeJson(result), 1);
    },
    async stop() {},
  };
  const assertCurrent = () => {
    if (!current) throw new Error('old generation');
  };
  const client = connectionSourceClient({
    transport,
    trust,
    identity: IDENTITY,
    ready,
    signal: abort.signal,
    assertCurrent,
    now: signer.now,
  });
  const control = createSourceControl({
    ledger,
    getContext: () => native.ctx,
    ready,
    request: async (...args) => {
      const result = await client(...args);
      await options.afterClientReply?.(...args);
      return result;
    },
    assertCurrent,
    now: signer.now,
  });
  t.after(async () => {
    control.close();
    abort.abort();
    await control.idle();
    await ledger.close();
  });
  return {
    ...native,
    agent,
    ledger,
    store,
    adapter,
    signer,
    peer,
    ready,
    trust,
    transport,
    control,
    requests: peer.requests,
    invalidate() {
      current = false;
    },
    retire() {
      current = false;
      control.close();
      abort.abort();
    },
  };
}
