import test from 'node:test';
import assert from 'node:assert/strict';
import {
  generateKeyPairSync,
  randomBytes,
  randomUUID,
  createHash,
} from 'node:crypto';
import { signClaims } from './ports.mjs';
import {
  validateProbe,
  signProbe,
  verifyProbe,
  acceptProbeHop,
  acceptProbeResult,
} from './probe-ports.mjs';

function fixture() {
  const w = generateKeyPairSync('ed25519');
  const p = generateKeyPairSync('ed25519');
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'shared',
    epoch: 2,
    revision: 3,
  };
  const request = {
    schemaVersion: 1,
    issuer: 'auriga.workspace',
    audience: 'auriga.shared.probe.chain',
    operationId: randomUUID(),
    binding,
    platformSessionId: randomUUID(),
    challenge: randomBytes(32).toString('base64url'),
    method: 'GET',
    canonicalPath: '/',
    issuedAt: 1000,
    expiresAt: 1060,
    jti: randomUUID(),
  };
  const operation = {
    operationId: request.operationId,
    binding,
    platformSessionId: request.platformSessionId,
    probeJti: request.jti,
    probeChallenge: request.challenge,
    placementState: 'switching',
    state: 'committing',
    receiptsConfirmed: true,
    resourcesReady: true,
    memberActive: true,
    platformSessionActive: true,
  };
  const contentSha256 =
    'sha256:' +
    createHash('sha256').update('<html>normal entry</html>').digest('hex');
  const receipt = {
    schemaVersion: 1,
    issuer: 'auriga.shared.host',
    audience: 'auriga.workspace.probe-result',
    operationId: request.operationId,
    binding,
    platformSessionId: request.platformSessionId,
    challenge: request.challenge,
    requestJti: request.jti,
    method: 'GET',
    canonicalPath: '/',
    statusCode: 200,
    contentType: 'text/html',
    contentSha256,
    observedAt: 1001,
    issuedAt: 1001,
    expiresAt: 1060,
    jti: randomUUID(),
  };
  return {
    w,
    p,
    request,
    operation,
    receipt,
    token: signProbe('ProbeRequest', request, 'workspace-probe', w.privateKey),
    wKeys: new Map([['workspace-probe', w.publicKey]]),
    pKeys: new Map([['host-probe', p.publicKey]]),
  };
}

test('probe crosses three authenticated hops once and W accepts the signed host receipt once', () => {
  const f = fixture();
  validateProbe('ProbeDispatch', { probe: f.token });
  for (const [hop, caller] of [
    ['edge', 'workspace'],
    ['ingress', 'edge'],
    ['host', 'ingress'],
  ]) {
    const context = {
      hop,
      caller,
      authenticated: true,
      operation: f.operation,
      now: 1001,
    };
    const consumed = new Set();
    assert.deepEqual(
      acceptProbeHop(f.token, f.wKeys, context, consumed),
      f.request,
    );
    assert.throws(
      () => acceptProbeHop(f.token, f.wKeys, context, consumed),
      /common.conflict/,
    );
    for (const patch of [
      { authenticated: false },
      { caller: 'browser' },
      { hop: 'public' },
    ])
      assert.throws(() =>
        acceptProbeHop(f.token, f.wKeys, { ...context, ...patch }, new Set()),
      );
  }
  const result = {
    receipt: signProbe('ProbeReceipt', f.receipt, 'host-probe', f.p.privateKey),
    contentSha256: f.receipt.contentSha256,
  };
  const consumed = new Set();
  assert.deepEqual(
    acceptProbeResult(result, f.request, f.pKeys, f.operation, 1002, consumed),
    f.receipt,
  );
  assert.throws(
    () =>
      acceptProbeResult(
        result,
        f.request,
        f.pKeys,
        f.operation,
        1002,
        consumed,
      ),
    /common.conflict/,
  );
});

test('probe request rejects public ticket, signature, purpose, binding, time and stale attempt', () => {
  const f = fixture();
  const context = {
    hop: 'edge',
    caller: 'workspace',
    authenticated: true,
    operation: f.operation,
    now: 1001,
  };
  const accept = (token, patch = {}, keys = f.wKeys) =>
    acceptProbeHop(token, keys, { ...context, ...patch }, new Set());
  for (const patch of [
    { operationId: randomUUID() },
    { platformSessionId: randomUUID() },
    { binding: { ...f.request.binding, epoch: 1 } },
    { jti: randomUUID() },
    { challenge: randomBytes(32).toString('base64url') },
    { issuedAt: 1002 },
    { expiresAt: 1061 },
  ])
    assert.throws(() =>
      accept(
        signProbe(
          'ProbeRequest',
          { ...f.request, ...patch },
          'workspace-probe',
          f.w.privateKey,
        ),
      ),
    );
  for (const patch of [
    { state: 'draining' },
    { placementState: 'ready' },
    { memberActive: false },
    { platformSessionActive: false },
    { receiptsConfirmed: false },
    { resourcesReady: false },
    { probeJti: randomUUID() },
  ])
    assert.throws(() =>
      accept(f.token, { operation: { ...f.operation, ...patch } }),
    );
  for (const now of [999, 1060]) assert.throws(() => accept(f.token, { now }));
  assert.throws(() => accept(f.token, {}, new Map()));
  assert.throws(() =>
    accept(
      signProbe('ProbeRequest', f.request, 'workspace-probe', f.p.privateKey),
    ),
  );
  assert.throws(() => accept(f.token + '\n'));
  assert.throws(() => accept([f.token]));
  const publicToken = signClaims(
    { alg: 'EdDSA', kid: 'workspace-probe', typ: 'auriga-shared-ticket+jwt' },
    f.request,
    f.w.privateKey,
  );
  assert.throws(() => accept(publicToken));
  for (const patch of [
    { audience: 'auriga.placement.exchange' },
    { issuer: 'auriga.edge' },
    { method: 'POST' },
    { canonicalPath: '/health' },
    { owner: true },
    { challenge: 'bad' },
  ])
    assert.throws(() =>
      validateProbe('ProbeRequest', { ...f.request, ...patch }),
    );
});

test('probe receipt rejects mismatched challenge/digest/attempt, redirects, stale state and replay', () => {
  const f = fixture();
  const resultFor = (patch) => ({
    receipt: signProbe(
      'ProbeReceipt',
      { ...f.receipt, ...patch },
      'host-probe',
      f.p.privateKey,
    ),
    contentSha256: f.receipt.contentSha256,
  });
  const accept = (result, op = f.operation, now = 1002, keys = f.pKeys) =>
    acceptProbeResult(result, f.request, keys, op, now, new Set());
  for (const patch of [
    { operationId: randomUUID() },
    { requestJti: randomUUID() },
    { platformSessionId: randomUUID() },
    { challenge: randomBytes(32).toString('base64url') },
    { binding: { ...f.request.binding, tenantId: randomUUID() } },
    { contentSha256: 'sha256:' + 'a'.repeat(64) },
    { expiresAt: 1061 },
    { observedAt: 999 },
    { observedAt: 1002 },
  ])
    assert.throws(() => accept(resultFor(patch)));
  for (const patch of [
    { statusCode: 302 },
    { contentType: 'application/json' },
    { canonicalPath: '/health' },
  ])
    assert.throws(() =>
      validateProbe('ProbeReceipt', { ...f.receipt, ...patch }),
    );
  assert.throws(() =>
    accept(resultFor({}), { ...f.operation, probeJti: randomUUID() }),
  );
  assert.throws(() => accept(resultFor({}), f.operation, 1060));
  assert.throws(() => accept(resultFor({}), f.operation, 1002, f.wKeys));
  assert.throws(() => accept({ ...resultFor({}), cookie: 'unexpected' }));
  assert.throws(() =>
    accept({ ...resultFor({}), contentSha256: 'sha256:' + 'b'.repeat(64) }),
  );
  assert.throws(() => verifyProbe('ProbeReceipt', f.token, f.wKeys, 1002));
});
