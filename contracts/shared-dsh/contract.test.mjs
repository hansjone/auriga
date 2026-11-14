import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import {
  validate,
  EntryDouble,
  authorizeCapability,
  stepUp,
  confirmReceipt,
  signClaims,
  verifyClaims,
  admitResourceView,
} from './ports.mjs';
const id = () => randomUUID();
const binding = {
  tenantId: id(),
  memberId: id(),
  placementId: id(),
  physicalInstanceId: id(),
  mode: 'shared',
  epoch: 2,
  revision: 3,
};
const session = {
  ...binding,
  sessionId: id(),
  platformSessionId: id(),
  authGeneration: 2,
};
const ticket = {
  schemaVersion: 1,
  binding,
  platformSessionId: session.platformSessionId,
  audience: 'auriga.placement.exchange',
  issuedAt: 1000,
  expiresAt: 1060,
  jti: id(),
};
const capability = {
  schemaVersion: 1,
  binding: session,
  taskId: id(),
  audience: 'auriga.shared.enterprise',
  scopes: ['outbound'],
  credentialVersion: 3,
  issuedAt: 1000,
  expiresAt: 1600,
  jti: id(),
};
const context = {
  now: 1000,
  audience: capability.audience,
  scope: 'outbound',
  taskId: capability.taskId,
  credentialVersion: 3,
  revoked: new Set(),
  allowRawAgentToken: true,
};

test('K: shared schema正例与未知模式/字段/零代次负例', () => {
  validate('Binding', binding);
  for (const patch of [
    { mode: 'future' },
    { epoch: 0 },
    { revision: 0 },
    { owner: id() },
  ])
    assert.throws(() => validate('Binding', { ...binding, ...patch }));
});
test('K: ticket完整归属、60秒、撤销与重放', () => {
  const fresh = () => new EntryDouble(binding, session.platformSessionId);
  const entry = fresh();
  entry.accept(ticket, 'OpenTicket', { now: 1059 });
  assert.throws(() => entry.accept(ticket, 'OpenTicket', { now: 1059 }));
  for (const key of [
    'tenantId',
    'memberId',
    'placementId',
    'physicalInstanceId',
    'epoch',
    'revision',
    'mode',
  ]) {
    const changed = {
      ...binding,
      [key]:
        typeof binding[key] === 'number'
          ? binding[key] + 1
          : key === 'mode'
            ? 'dedicated'
            : id(),
    };
    assert.throws(() =>
      fresh().accept({ ...ticket, binding: changed }, 'OpenTicket', {
        now: 1000,
      }),
    );
  }
  for (const patch of [
    { expiresAt: 1061 },
    { platformSessionId: id() },
    { issuedAt: 1001 },
  ])
    assert.throws(() =>
      fresh().accept({ ...ticket, ...patch }, 'OpenTicket', { now: 1000 }),
    );
  assert.throws(() => fresh().accept(ticket, 'OpenTicket', { now: 1060 }));
  const revoked = fresh();
  revoked.active = false;
  assert.throws(() => revoked.accept(ticket, 'OpenTicket', { now: 1000 }));
});
test('K: identity签名、方法路径、错aud/issuer、多值和伪造拒绝', () => {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const header = {
    alg: 'EdDSA',
    kid: 'runtime-test',
    typ: 'auriga-shared-identity+jwt',
  };
  const claims = {
    schemaVersion: 2,
    issuer: 'auriga.edge',
    audience: 'auriga.shared.ingress',
    binding,
    platformSessionId: session.platformSessionId,
    method: 'GET',
    canonicalPath: '/api/session',
    issuedAt: 1000,
    expiresAt: 1060,
    jti: id(),
  };
  const keys = new Map([[header.kid, publicKey]]);
  const token = signClaims(header, claims, privateKey);
  const verified = verifyClaims(token, 'Identity', header.typ, keys);
  for (const canonicalPath of [
    '/../other',
    '/a//b',
    '/%2e%2e/other',
    '/%2Fother',
    '/bad%',
  ]) {
    assert.throws(() =>
      new EntryDouble(binding, session.platformSessionId).accept(
        { ...verified, canonicalPath },
        'Identity',
        { now: 1000, method: 'GET', canonicalPath },
      ),
    );
  }
  new EntryDouble(binding, session.platformSessionId).accept(
    verified,
    'Identity',
    { now: 1000, method: 'GET', canonicalPath: '/api/session' },
  );
  for (const patch of [
    { method: 'POST', canonicalPath: '/api/session' },
    { method: 'GET', canonicalPath: '/other' },
  ])
    assert.throws(() =>
      new EntryDouble(binding, session.platformSessionId).accept(
        verified,
        'Identity',
        { now: 1000, ...patch },
      ),
    );
  for (const bad of [token + ', ' + token, [token], token.replace(/.$/, '!')])
    assert.throws(() => verifyClaims(bad, 'Identity', header.typ, keys));
  assert.throws(() => verifyClaims(token, 'Identity', header.typ, new Map()));
  for (const patch of [
    { audience: 'pool' },
    { issuer: 'user' },
    { schemaVersion: 1 },
  ])
    assert.throws(() =>
      verifyClaims(
        signClaims(header, { ...claims, ...patch }, privateKey),
        'Identity',
        header.typ,
        keys,
      ),
    );
  const attacker = generateKeyPairSync('ed25519');
  assert.throws(() =>
    verifyClaims(
      signClaims(header, claims, attacker.privateKey),
      'Identity',
      header.typ,
      keys,
    ),
  );
});
test('K: 企业/model/message能力600秒、会话/任务/撤销/raw围栏', () => {
  authorizeCapability(capability, session, { ...context, now: 1599 });
  for (const patch of [
    { now: 1600 },
    { revoked: new Set([capability.jti]) },
    { taskId: id() },
    { credentialVersion: 4 },
    { audience: 'auriga.shared.model' },
  ])
    assert.throws(() =>
      authorizeCapability(capability, session, { ...context, ...patch }),
    );
  for (const key of Object.keys(session)) {
    const value =
      typeof session[key] === 'number'
        ? session[key] + 1
        : key === 'mode'
          ? 'dedicated'
          : id();
    assert.throws(() =>
      authorizeCapability(
        { ...capability, binding: { ...session, [key]: value } },
        session,
        context,
      ),
    );
  }
  assert.throws(() =>
    authorizeCapability({ ...capability, expiresAt: 1601 }, session, context),
  );
  assert.throws(() =>
    authorizeCapability({ ...capability, scopes: ['credentials'] }, session, {
      ...context,
      scope: 'credentials',
      allowRawAgentToken: false,
    }),
  );
  for (const [audience, scope] of [
    ['auriga.shared.model', 'model.invoke'],
    ['auriga.shared.message', 'message.send'],
    ['auriga.shared.message', 'message.receive'],
  ]) {
    authorizeCapability({ ...capability, audience, scopes: [scope] }, session, {
      ...context,
      audience,
      scope,
    });
    assert.throws(() =>
      authorizeCapability(
        { ...capability, audience, scopes: [scope, 'credentials'] },
        session,
        { ...context, audience, scope },
      ),
    );
  }
});
test('K: admin step-up严格600秒并检查活跃管理授权', () => {
  const input = {
    ctx: 'admin',
    permission: 'workspace.placements.write',
    stepupTime: 1000,
    active: true,
  };
  stepUp(input, 1599);
  for (const now of [999, 1600]) assert.throws(() => stepUp(input, now));
  for (const patch of [
    { ctx: 'portal' },
    { ctx: 'device' },
    { ctx: 'instance' },
    { permission: 'read' },
    { active: false },
    { stepupTime: undefined },
  ])
    assert.throws(() => stepUp({ ...input, ...patch }, 1000));
});
test('K: 30天封存/空目录/丢会话/停池未确认blocked及晚ACK', () => {
  const intent = {
    schemaVersion: 1,
    operationId: id(),
    binding,
    action: 'archive-reset',
    cancelWaitSeconds: 30,
    archiveRetentionSeconds: 2592000,
    workspacePolicy: 'empty',
    sessionPolicy: 'discard',
  };
  const receipt = {
    schemaVersion: 1,
    operationId: intent.operationId,
    binding,
    receiptId: id(),
    action: intent.action,
    outcome: 'confirmed',
    observedAt: 1000,
    executionTreeStopped: true,
    sessionsDiscarded: true,
    workspaceEmpty: true,
    archiveId: id(),
    archiveExpiresAt: 2593000,
  };
  assert.equal(confirmReceipt(intent, receipt), 'confirmed');
  for (const patch of [
    { workspaceEmpty: false },
    { archiveExpiresAt: 2592999 },
    { archiveId: undefined },
    { outcome: 'unconfirmed' },
  ])
    assert.equal(confirmReceipt(intent, { ...receipt, ...patch }), 'blocked');
  for (const [action, field] of [
    ['stop-pool', 'executionTreeStopped'],
    ['discard-sessions', 'sessionsDiscarded'],
    ['prepare-empty', 'workspaceEmpty'],
  ])
    assert.equal(
      confirmReceipt(
        { ...intent, action },
        { ...receipt, action, [field]: false },
      ),
      'blocked',
    );
  assert.throws(() =>
    confirmReceipt(intent, { ...receipt, operationId: id() }),
  );
  assert.throws(() =>
    confirmReceipt(intent, { ...receipt, binding: { ...binding, epoch: 1 } }),
  );
  for (const patch of [
    { cancelWaitSeconds: 31 },
    { sessionPolicy: 'restore' },
    { workspacePolicy: 'copy' },
    { archiveRetentionSeconds: 30 },
  ])
    assert.throws(() => validate('RuntimeIntent', { ...intent, ...patch }));
});
test('K: 平台统一资源不取权限并集、到期拒绝', () => {
  const digest = 'sha256:' + 'a'.repeat(64);
  const view = {
    schemaVersion: 1,
    binding,
    viewId: id(),
    authorizationSetDigest: digest,
    source: 'platform',
    resources: [{ resourceId: id(), digest, leaseId: id(), expiresAt: 1060 }],
  };
  admitResourceView(view, binding, digest, 1059);
  assert.throws(() => admitResourceView(view, binding, digest, 1060));
  assert.throws(() =>
    admitResourceView(view, binding, 'sha256:' + 'b'.repeat(64), 1000),
  );
  assert.throws(() =>
    admitResourceView({ ...view, source: 'private' }, binding, digest, 1000),
  );
});
test('K: 原有契约只增保护（针对固定设计基线）', async () => {
  const { execFileSync } = await import('node:child_process');
  const { parse } =
    await import('../../services/contracts/node_modules/yaml/dist/index.js');
  const base = '9a2f328c';
  const original = parse(
    execFileSync('git', ['show', base + ':contracts/openapi/shared-dsh.yaml'], {
      encoding: 'utf8',
    }),
  );
  const current = parse(
    readFileSync(
      new URL('../openapi/shared-dsh.yaml', import.meta.url),
      'utf8',
    ),
  );
  for (const [key, value] of Object.entries(original.paths))
    assert.deepEqual(current.paths[key], value);
  for (const [key, value] of Object.entries(original.components.schemas))
    assert.deepEqual(current.components.schemas[key], value);
});
test('K: 新事件生产映射与错误目录固定', () => {
  const catalog = JSON.parse(
    readFileSync(new URL('../catalog/m3.json', import.meta.url), 'utf8'),
  );
  for (const [name, source, subject, definition] of [
    ['placement.revoked', 'workspace', 'placementId', 'Revocation'],
    [
      'placement.resource_reported',
      'node',
      'binding.placementId',
      'ResourceReport',
    ],
    [
      'placement.guardian_impact',
      'node',
      'physicalInstanceId',
      'GuardianImpact',
    ],
  ]) {
    const entry = catalog.events[name];
    assert.equal(entry.source, 'auriga/' + source);
    assert.equal(entry.subject, subject);
    assert.equal(
      entry.schema,
      '../shared-dsh/shared-dsh.schema.json#/$defs/' + definition,
    );
    assert.equal(entry.publish, 'auriga.evt.' + name);
  }
  for (const code of [
    'binding_mismatch',
    'expired',
    'revoked',
    'signature_invalid',
    'stop_unconfirmed',
  ])
    assert(catalog.errors['shared.' + code].message['zh-CN'].length > 0);
});
