// Regenerates contracts/network/fixtures/{frames,signature-vectors}.json:
//   node contracts/test/network-gen.mjs
// Keys are derived from public TEST-ONLY labels; never use them outside fixtures.
import { writeFileSync } from 'node:fs';
import { createHash, createPublicKey } from 'node:crypto';
import {
  digest,
  jcs,
  privateKeyFromSeed,
  seal,
  signedBody,
} from './network-lib.mjs';

const out = (name) => new URL('../network/fixtures/' + name, import.meta.url);
const seedOf = (label) => createHash('sha256').update(label).digest('hex');
const KEYS = {
  business_delivery: 'test-r2-business-1',
  protocol_response: 'test-r2-protocol-1',
  management_state: 'test-r2-management-1',
};
const keys = Object.fromEntries(
  Object.entries(KEYS).map(([purpose, kid]) => {
    const label = 'auriga-network-v1 TEST ONLY ' + kid;
    const key = privateKeyFromSeed(seedOf(label));
    const spki = createPublicKey(key)
      .export({ format: 'der', type: 'spki' })
      .toString('base64url');
    return [
      purpose,
      { kid, purpose, label, seedHex: seedOf(label), key, spki },
    ];
  }),
);
const ctx = {
  realm: '2',
  node: '64',
  incarnation: 'inc-64-1',
  epoch: '42',
  channelNonce: 'nonce-ready-0001',
  requestId: 'req-fetch-0001',
  now: '2026-10-25T00:01:00Z',
};
const addr = (type, node, local) => ({
  version: 1,
  realm: '2',
  type,
  node,
  local,
});
const A1 = addr('session', '63', '418');
const A_SYS = addr('system', '63', '0');
const B_SYS = addr('system', '64', '0');
const B5 = addr('session', '64', '5');
const GROUP = addr('group', '0', '23');
const BROADCAST = addr('group', '0', '1');
const T0 = '2026-10-25T00:00:00Z';
const T1 = '2026-10-26T00:00:00Z';
const PLACEHOLDER_SIG = 'A'.repeat(86);
const auth = (purpose, requestId = ctx.requestId) => ({
  version: 1,
  purpose,
  alg: 'Ed25519',
  kid: KEYS[purpose],
  issuer_realm: '2',
  issued_at: T0,
  not_after: '2026-10-25T00:05:00Z',
  audience: {
    realm: '2',
    node: ctx.node,
    incarnation: ctx.incarnation,
    epoch: ctx.epoch,
  },
  request_id: requestId,
  channel_nonce: ctx.channelNonce,
  keyset_version: '5',
  signature: PLACEHOLDER_SIG,
});
const agentRef = {
  source_binding: 'binding-a1',
  source_revision: '3',
  policy_revision: '8',
  broadcast_grant_id: null,
};
const msg = (over) => ({
  message_id: 'msg-0001',
  from: A1,
  to: B5,
  reply_to: A1,
  thread_id: 'thread-0001',
  thread_sequence: '2',
  in_reply_to: null,
  kind: 'message',
  durability: 'durable',
  flags: [],
  ttl: 86400,
  ext: { e2e_signature: null },
  body: { subtype: 'text', text: '对话内消息示例' },
  sender_kind: 'agent',
  message_class: 'unicast',
  intent: 'request',
  authorization_ref: agentRef,
  created_at: T0,
  expires_at: T1,
  ...over,
});
const sourceContext = (kind = 'agent', node = '63') => ({
  realm: '2',
  node,
  user: 'user-0193a1',
  message_digest: 'sha256:' + '0'.repeat(64),
  sender_kind: kind,
  display_label: kind === 'human' ? '通知发送者' : '协作会话',
});
const delivery = (message, recipients, extra = {}) => ({
  app_version: '1.0',
  type: 'delivery',
  epoch: ctx.epoch,
  delivery_id: 'dlv-' + message.message_id,
  mail_seq: '1205',
  ...extra.head,
  target_node: { realm: '2', node: ctx.node },
  recipients,
  source_context: sourceContext(message.sender_kind, message.from.node),
  message,
  ...extra.tail,
  message_state_version: '1',
  server_auth: auth('business_delivery'),
});
const proto = (type, body, requestId = 'req-op-0001') => ({
  app_version: '1.0',
  type,
  request_id: requestId,
  ...body,
  server_auth: auth('protocol_response', requestId),
});

const frames = {};
const put = (name, def, value, context = {}) =>
  (frames[name] = { def, context, value });

// ---- deliveries ----
put(
  'delivery_first_contact',
  'envelope#Delivery',
  delivery(
    msg({
      message_id: 'msg-fc-0001',
      to: B_SYS,
      thread_sequence: '1',
      kind: 'first_contact',
      contact_scope: 'conversation',
      body: { subtype: 'text', text: '首次联系示例' },
    }),
    [{ recipient_id: 'rcp-fc-sys', local: '0', type: 'system' }],
    {
      tail: {
        contact: {
          contact_id: 'msg-fc-0001',
          state: 'contact_allowed',
          request_version: '2',
          assigned_address: null,
        },
      },
    },
  ),
);
put(
  'delivery_session_message',
  'envelope#Delivery',
  delivery(msg({}), [
    { recipient_id: 'rcp-0001', local: '5', type: 'session' },
  ]),
);
put(
  'delivery_reply_private',
  'envelope#Delivery',
  delivery(
    msg({
      message_id: 'msg-reply-0001',
      kind: 'reply',
      reply_mode: 'private',
      in_reply_to: 'msg-0000',
      intent: 'information',
    }),
    [{ recipient_id: 'rcp-reply-0001', local: '5', type: 'session' }],
  ),
);
put(
  'delivery_control_wake',
  'envelope#Delivery',
  delivery(
    msg({
      message_id: 'msg-ctl-0001',
      kind: 'control',
      intent: 'control',
      body: { subtype: 'control', action: 'wake' },
    }),
    [{ recipient_id: 'rcp-ctl-0001', local: '5', type: 'session' }],
  ),
);
put(
  'delivery_multicast',
  'envelope#Delivery',
  delivery(
    msg({
      message_id: 'msg-group-0001',
      to: GROUP,
      thread_id: 'thread-group-17',
      thread_sequence: '9',
      dispatch_mode: 'paged',
      intent: 'information',
      message_class: 'multicast',
      body: { subtype: 'text', text: '组通知正文仅此一份' },
    }),
    [
      {
        recipient_id: 'rcp-group-a',
        local: '2',
        type: 'session',
        membership_id: 'join-a-v3',
      },
      {
        recipient_id: 'rcp-group-b',
        local: '3',
        type: 'session',
        membership_id: 'join-b-v1',
      },
    ],
    { head: { mail_seq: '1206', snapshot_id: 'snapshot-17-v8' } },
  ),
);
put(
  'delivery_broadcast',
  'envelope#Delivery',
  delivery(
    msg({
      message_id: 'msg-bcast-0001',
      from: A_SYS,
      reply_to: A_SYS,
      to: BROADCAST,
      thread_id: 'thread-bcast-0001',
      thread_sequence: '1',
      dispatch_mode: 'paged',
      sender_kind: 'human',
      message_class: 'broadcast',
      body: { subtype: 'text', text: '请在方便时确认信息，回复仅回发送者' },
      authorization_ref: {
        human_intent_id: 'intent-0001',
        broadcast_grant_id: 'grant-0001',
        grant_revision: '4',
        subject_type: 'user',
        policy_revision: '8',
      },
      response_deadline: T1,
    }),
    [
      {
        recipient_id: 'rcp-bcast-sys',
        local: '0',
        type: 'system',
        membership_id: 'bcast-member-64',
      },
    ],
    { head: { mail_seq: '1207', snapshot_id: 'snapshot-bcast-0001' } },
  ),
);
// ---- other page items ----
put('item_terminal', 'envelope#TerminalItem', {
  app_version: '1.0',
  type: 'terminal',
  epoch: ctx.epoch,
  delivery_id: 'dlv-expired-0001',
  mail_seq: '1201',
  target_node: { realm: '2', node: ctx.node },
  recipients: [
    {
      recipient_id: 'rcp-expired-0001',
      transport_status: 'expired',
      state_version: '3',
    },
  ],
  server_auth: auth('protocol_response'),
});
put('item_gap', 'envelope#GapItem', {
  app_version: '1.0',
  type: 'gap',
  epoch: ctx.epoch,
  from_seq: '1202',
  to_seq: '1203',
  reason: 'held',
  server_auth: auth('protocol_response'),
});
put('item_contact_prompt', 'envelope#ContactPromptItem', {
  app_version: '1.0',
  type: 'contact_prompt',
  epoch: ctx.epoch,
  delivery_id: 'dlv-fc-pending',
  mail_seq: '1204',
  contact_id: 'msg-fc-pending',
  contact_scope: 'conversation',
  request_version: '1',
  state_version: '1',
  recipient: { recipient_id: 'rcp-fc-pending', local: '0', type: 'system' },
  from: A1,
  source_context: sourceContext(),
  preview: '希望建立对话（预览已限长）',
  expires_at: T1,
  server_auth: auth('business_delivery'),
});
put('item_state_update', 'envelope#StateUpdateItem', {
  app_version: '1.0',
  type: 'state_update',
  epoch: ctx.epoch,
  update_id: 'upd-0001',
  action: 'recall_broadcast',
  message_id: 'msg-bcast-0001',
  message_state_version: '2',
  expires_at: '2026-11-24T00:00:00Z',
  server_auth: auth('management_state'),
});
// ---- protocol responses ----
put('fetch_result', 'protocol#FetchResult', {
  app_version: '1.0',
  type: 'fetch_result',
  page: {
    app_version: '1.0',
    type: 'fetch_page',
    request_id: ctx.requestId,
    epoch: ctx.epoch,
    head_seq: '1207',
    committed_cursor: '1200',
    scan_after: '1205',
    snapshot_head: '1207',
    next_continuation: 'cont-AAAAAAAAAAAAAAAA',
    manifest: [],
    message_states: [
      {
        message_id: 'msg-bcast-0001',
        message_state_version: '1',
        state: 'active',
        sender_frozen: false,
      },
    ],
    server_auth: auth('protocol_response'),
  },
  items: [
    structuredClone(frames.item_terminal.value),
    structuredClone(frames.delivery_session_message.value),
  ],
});
put(
  'ready_result',
  'protocol#ReadyResult',
  proto(
    'ready_result',
    {
      status: 'ready',
      epoch: ctx.epoch,
      node_identity: {
        realm: '2',
        node: ctx.node,
        incarnation: ctx.incarnation,
      },
      head_seq: '1207',
      committed_cursor: '1200',
      retention_low_seq: '1',
      local_high_water: '418',
      registration_revision: '418',
      seen_checkpoint: {
        checkpoint_seq: '1200',
        checkpoint_revision: '77',
        summary_digest: digest('checkpoint-1200'),
      },
      required_features: [
        'server_auth_v1',
        'source_attestation_v1',
        'inbox_only_v1',
      ],
    },
    'req-ready-0001',
  ),
);
put('head', 'protocol#Head', {
  app_version: '1.0',
  type: 'head',
  epoch: ctx.epoch,
  head_seq: '1207',
  server_auth: auth('protocol_response', null),
});
put(
  'receipt_committed',
  'protocol#ReceiptCommitted',
  proto('receipt_committed', {
    operation_id: 'op-receipt-0001',
    delivery_id: 'dlv-msg-group-0001',
    mail_seq: '1206',
    results: [
      {
        recipient_id: 'rcp-group-a',
        transport_status: 'host_accepted',
        state_version: '2',
      },
      {
        recipient_id: 'rcp-group-b',
        transport_status: 'revoked',
        state_version: '2',
      },
    ],
    mail_item_closed: true,
    committed_cursor: '1200',
  }),
);
put(
  'attempt_result',
  'protocol#AttemptResult',
  proto('attempt_result', {
    attempt_id: 'att-0001',
    recipient_id: 'rcp-0001',
    result: 'entered',
    attempt_count: 1,
    max_attempts: 5,
  }),
);
put(
  'admit_result',
  'protocol#AdmitResult',
  proto('admit_result', {
    recipient_id: 'rcp-0001',
    result: 'admitted',
    admission_id: 'adm-0001',
    admitted_epoch: ctx.epoch,
    state_version: '4',
  }),
);
put(
  'decide_result',
  'protocol#DecideResult',
  proto('decide_result', {
    decision_id: 'dec-0001',
    contact_state: 'contact_allowed',
    join_state: null,
    execution_permission: 'pending',
    request_version: '2',
  }),
);
put(
  'assign_result',
  'protocol#AssignResult',
  proto('assign_result', {
    contact_id: 'msg-fc-0001',
    assigned_address: addr('session', '64', '419'),
    creation_attempt_id: 'create-0001',
    result: 'assigned',
    creation_permit: true,
  }),
);
put(
  'register_result',
  'protocol#RegisterResult',
  proto('register_result', {
    local: '419',
    address_type: 'session',
    registration_id: 'reg-419',
    allocation_op_id: 'alloc-419',
    result: 'registered',
    registration_revision: '419',
    local_high_water: '419',
  }),
);
put(
  'status_full',
  'protocol#Status',
  proto('status', {
    visibility: 'full',
    status: 'platform_stored',
    message_id: 'msg-group-0001',
    message_class: 'multicast',
    dispatch_status: 'partial',
    total_members: '100',
    total_nodes: '10',
    receipts: {
      accepted: '97',
      rejected: '1',
      expired: '0',
      canceled: '0',
      skipped: '0',
      unresolved: '0',
      pending: '2',
    },
    quarantined: '1',
    responses: {
      pending: '60',
      responded: '38',
      declined: '1',
      timed_out: '0',
      unavailable: '1',
    },
    message_state: 'active',
  }),
);
put(
  'status_restricted',
  'protocol#Status',
  proto('status', {
    visibility: 'restricted',
    status: 'request_submitted',
    submission_id: 'sub-0001',
    wait_until: T1,
  }),
);
put(
  'error',
  'protocol#Error',
  proto('error', {
    code: 'network.stale_epoch',
    scope: 'connection',
    retryable: false,
    retry_after: null,
    related: { recipient_id: 'rcp-0001' },
  }),
);
// ---- upstream (unsigned; identity comes from the ingress) ----
const up = (type, body, requestId = 'req-up-0001') => ({
  app_version: '1.0',
  type,
  request_id: requestId,
  ...body,
});
put(
  'ready',
  'protocol#Ready',
  up(
    'ready',
    {
      channel_nonce: ctx.channelNonce,
      boot_id: 'boot-0001',
      features: [
        'server_auth_v1',
        'source_attestation_v1',
        'inbox_only_v1',
        'broadcast_grants_v1',
      ],
      adapter_capabilities: ['session_inbox', 'system_inbox'],
      resume_cursor: '1200',
      local_high_water: '418',
      registration_revision: '418',
      receipt_checkpoint: {
        checkpoint_seq: '1200',
        checkpoint_revision: '77',
        summary_digest: digest('checkpoint-1200'),
      },
      keyset_version: '5',
      pending_registration: null,
    },
    'req-ready-0001',
  ),
);
put(
  'fetch',
  'protocol#Fetch',
  up(
    'fetch',
    {
      epoch: ctx.epoch,
      resume_cursor: '1200',
      continuation: null,
      max_items: 128,
      max_bytes: 65536,
      held_ids: ['rcp-0001'],
      paused: [{ thread_id: 'thread-poison', target: B5 }],
      state_barrier: ['msg-bcast-0001'],
    },
    ctx.requestId,
  ),
);
put(
  'receipt',
  'protocol#Receipt',
  up('receipt', {
    operation_id: 'op-receipt-0001',
    epoch: ctx.epoch,
    delivery_id: 'dlv-msg-group-0001',
    mail_seq: '1206',
    results: [
      {
        recipient_id: 'rcp-group-a',
        target: addr('session', '64', '2'),
        message_digest: digest('group-message'),
        host_request_id: 'rcp-group-a',
        status: 'host_accepted',
        expected_state_version: '1',
        accepted_by: 'session',
        acceptance_mode: 'inbox_only',
        execution_status: 'not_applicable',
        evidence_digest: digest('inbox-a'),
      },
      {
        recipient_id: 'rcp-group-b',
        target: addr('session', '64', '3'),
        message_digest: digest('group-message'),
        host_request_id: 'rcp-group-b',
        status: 'rejected',
        code: 'network.target_unavailable',
        expected_state_version: '1',
      },
    ],
  }),
);
put(
  'attempt_begin',
  'protocol#AttemptBegin',
  up('attempt_begin', {
    epoch: ctx.epoch,
    boot_id: 'boot-0001',
    attempt_id: 'att-0001',
    delivery_id: 'dlv-msg-0001',
    recipient_id: 'rcp-0001',
    purpose: 'process',
  }),
);
put(
  'execution_admit',
  'protocol#ExecutionAdmit',
  up('execution_admit', {
    recipient_id: 'rcp-0001',
    target: B5,
    connection_epoch: ctx.epoch,
    host_request_id: 'rcp-0001',
    attempt_id: 'att-0001',
    boot_id: 'boot-0001',
  }),
);
put(
  'contact_decide_conversation',
  'protocol#ContactDecide',
  up('contact_decide', {
    epoch: ctx.epoch,
    decision_scope: 'conversation',
    contact_id: 'msg-fc-0001',
    decision_id: 'dec-0001',
    expected_request_version: '1',
    policy_revision: '12',
    conversation_allowed: true,
    execution_allowed: null,
  }),
);
put(
  'contact_decide_group_join',
  'protocol#ContactDecide',
  up('contact_decide', {
    epoch: ctx.epoch,
    decision_scope: 'group_join',
    contact_id: 'msg-invite-0001',
    decision_id: 'dec-0002',
    expected_request_version: '1',
    policy_revision: '12',
    join: {
      join_allowed: true,
      target: B_SYS,
      membership_instance_id: 'member-0001',
    },
  }),
);
put(
  'contact_decide_execution',
  'protocol#ContactDecide',
  up('contact_decide', {
    epoch: ctx.epoch,
    decision_scope: 'execution',
    authorization_request_id: 'authz-0001',
    decision_id: 'dec-0003',
    expected_request_version: '1',
    policy_revision: '12',
    execution_allowed: false,
  }),
);
put(
  'contact_assign',
  'protocol#ContactAssign',
  up('contact_assign', {
    epoch: ctx.epoch,
    boot_id: 'boot-0001',
    contact_id: 'msg-fc-0001',
    assigned_address: addr('session', '64', '419'),
    registration_id: 'reg-419',
    creation_attempt_id: 'create-0001',
    decision_version: '2',
  }),
);
put(
  'local_register',
  'protocol#LocalRegister',
  up('local_register', {
    epoch: ctx.epoch,
    boot_id: 'boot-0001',
    candidate_local: '419',
    address_type: 'session',
    allocation_op_id: 'alloc-419',
    allocation_fingerprint: digest('alloc-419-nonce'),
    expected_registration_revision: '418',
  }),
);
put(
  'status_query',
  'protocol#StatusQuery',
  up('status_query', { message_id: 'msg-group-0001' }),
);
// ---- send surface ----
put('tool_send', 'send#AgentSendToolInput', {
  target: 'aur1:40.5',
  body: { subtype: 'text', text: '你好' },
  thread: 'thread-0001',
});
put('tool_reply', 'send#AgentReplyToolInput', {
  body: { subtype: 'text', text: '收到' },
});
put('tool_first_contact', 'send#AgentFirstContactToolInput', {
  target: 'aur1:2.40.0',
  text: '想请你协助评审',
});
put('submit_request', 'send#SubmitRequest', {
  kind: 'message',
  to: B5,
  thread_id: 'thread-0001',
  intent: 'request',
  durability: 'durable',
  ttl: 86400,
  body: { subtype: 'text', text: '对话内消息示例' },
  flags: [],
  ext: { e2e_signature: null },
  source: { source_binding: 'binding-a1' },
  idempotency: { producer_epoch: '3', key: 'send-0001' },
});
put('send_result_stored', 'send#SendResult', {
  status: 'platform_stored',
  from: A1,
  to: B5,
  message_id: 'msg-0001',
  thread_id: 'thread-0001',
  message_class: 'unicast',
  expires_at: T1,
});
put('send_result_submitted', 'send#SendResult', {
  status: 'request_submitted',
  from: A1,
  to: B_SYS,
  submission_id: 'sub-0001',
  wait_until: T1,
});

for (const f of Object.values(frames))
  if (
    f.def.startsWith('protocol#') &&
    typeof f.value.request_id === 'string' &&
    f.value.server_auth
  )
    f.context = { requestId: f.value.request_id };

// Typed variants retain the same protocol signing and recovery boundaries.
for (const [name, type] of [['register_result_subagent', 'subagent']]) {
  const value = structuredClone(frames.register_result.value);
  value.address_type = type;
  put(name, 'protocol#RegisterResult', value, { requestId: value.request_id });
}
put('ready_pending_registration', 'protocol#Ready', {
  ...structuredClone(frames.ready.value),
  local_high_water: '419',
  pending_registration: {
    candidate_local: '419',
    allocation_op_id: 'alloc-419',
    allocation_fingerprint: digest('alloc-419-nonce'),
    address_type: 'session',
  },
});

// ---- seal ----
const keyFor = (obj) => keys[obj.server_auth.purpose].key;
for (const f of Object.values(frames)) seal(f.value, keyFor);

const publicKeys = Object.values(keys).map(
  ({ kid, purpose, label, seedHex, spki }) => ({
    kid,
    purpose,
    label,
    testOnlySeedHex: seedHex,
    publicKeySpki: spki,
  }),
);
writeFileSync(
  out('frames.json'),
  JSON.stringify(
    {
      description:
        '已签名的合法帧；由 contracts/test/network-gen.mjs 生成，勿手改。',
      receiver: ctx,
      frames,
    },
    null,
    2,
  ) + '\n',
);
const vector = (name) => {
  const v = frames[name].value;
  return {
    name,
    signingInputPrefix: 'Auriga.ServerEnvelope.v1\\u0000',
    canonical: signedBody(v),
    signature: v.server_auth.signature,
    kid: v.server_auth.kid,
  };
};
writeFileSync(
  out('signature-vectors.json'),
  JSON.stringify(
    {
      description:
        'JCS + Ed25519 已知答案向量；密钥只用于测试，种子由公开标签 SHA-256 得出。签名输入 = UTF-8("Auriga.ServerEnvelope.v1") || 0x00 || JCS(对象去掉最外层 server_auth.signature)。',
      keys: publicKeys,
      digests: [
        {
          name: 'delivery_first_contact.message',
          canonical: jcs(frames.delivery_first_contact.value.message),
          digest: digest(frames.delivery_first_contact.value.message),
        },
      ],
      vectors: [
        'delivery_first_contact',
        'delivery_broadcast',
        'head',
        'error',
        'register_result',
        'register_result_subagent',
      ].map(vector),
    },
    null,
    2,
  ) + '\n',
);
console.log(Object.keys(frames).length + ' frames');
