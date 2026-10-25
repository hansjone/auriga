/* global structuredClone */
import { generateKeyPairSync, sign } from 'node:crypto';
import { openTrustStore } from '../../../src/network/trust/store.js';
import { digest, signingInput } from '../../../src/network/trust/verifier.js';
import { IDENTITY, peer, self } from '../host/fixtures.js';

export async function signingFixture() {
  const pairs = Object.fromEntries(
    ['business_delivery', 'protocol_response', 'management_state'].map((p) => [
      p,
      generateKeyPairSync('ed25519'),
    ]),
  );
  const now = () => Date.parse('2026-10-25T00:01:00Z');
  let persisted;
  const trust = await openTrustStore({
    now,
    cache: {
      async load() {
        return persisted;
      },
      async commit(v) {
        persisted = structuredClone(v);
      },
    },
    config: {
      realm: '2',
      version: '5',
      issuedAt: '2026-10-25T00:00:00Z',
      validUntil: '2026-10-26T00:00:00Z',
      keys: Object.entries(pairs).map(([purpose, pair]) => ({
        kid: purpose,
        purpose,
        alg: 'Ed25519',
        publicKeySpki: pair.publicKey
          .export({ format: 'der', type: 'spki' })
          .toString('base64url'),
        status: 'active',
        notBefore: '2026-10-24T00:00:00Z',
        notAfter: '2026-10-27T00:00:00Z',
      })),
    },
  });
  const context = {
    realm: '2',
    node: '64',
    nodeIncarnation: IDENTITY.nodeIncarnation,
    epoch: '7',
    requestId: 'req-fetch-test',
    channelNonce: 'nonce-test',
  };
  function seal(value, ctx = context) {
    const purpose = ['delivery', 'contact_prompt'].includes(value.type)
      ? 'business_delivery'
      : value.type === 'state_update'
        ? 'management_state'
        : 'protocol_response';
    value.server_auth = {
      version: 1,
      purpose,
      alg: 'Ed25519',
      kid: purpose,
      issuer_realm: '2',
      issued_at: '2026-10-25T00:00:00Z',
      not_after: '2026-10-25T00:05:00Z',
      audience: {
        realm: '2',
        node: '64',
        incarnation: ctx.nodeIncarnation,
        epoch: ctx.epoch,
      },
      request_id: ctx.requestId,
      channel_nonce: ctx.channelNonce,
      keyset_version: '5',
    };
    value.server_auth.signature = sign(
      null,
      signingInput(value),
      pairs[purpose].privateKey,
    ).toString('base64url');
    return value;
  }
  function delivery(id) {
    const message = {
      message_id: `msg-${id}`,
      from: peer('418'),
      to: self('0'),
      reply_to: peer('418'),
      thread_id: `thread-${id}`,
      thread_sequence: '1',
      in_reply_to: null,
      kind: 'first_contact',
      message_class: 'unicast',
      sender_kind: 'agent',
      intent: 'request',
      contact_scope: 'conversation',
      body: { subtype: 'text', text: '请求建立对话' },
      created_at: '2026-10-25T00:00:00Z',
      expires_at: '2026-10-26T00:00:00Z',
    };
    return seal({
      app_version: '1.0',
      type: 'delivery',
      epoch: '7',
      delivery_id: `dlv-${id}`,
      mail_seq: '10',
      target_node: { realm: '2', node: '64' },
      message,
      source_context: {
        realm: '2',
        node: '63',
        sender_kind: 'agent',
        message_digest: digest(message),
      },
      recipients: [
        {
          recipient_id: `rcp-${id}`,
          local: '0',
          type: 'system',
          attempt_id: `att-${id}`,
        },
      ],
      message_state_version: '1',
    });
  }
  function pageInput(items) {
    return {
      tenant: IDENTITY.tenant,
      nodeIncarnation: IDENTITY.nodeIncarnation,
      epoch: '7',
      bootId: 'boot-test',
      items,
      trust,
      context,
      now,
      header: seal({
        app_version: '1.0',
        type: 'fetch_page',
        request_id: context.requestId,
        epoch: '7',
        scan_after: '10',
        committed_cursor: '0',
        manifest: items.map((i) => ({
          item_type: i.type,
          mail_seq: i.mail_seq ?? null,
          digest: digest(i),
        })),
      }),
    };
  }
  return { trust, now, context, seal, delivery, pageInput };
}
