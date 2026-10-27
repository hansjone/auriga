/* global structuredClone */
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import { writeFile } from 'node:fs/promises';
import { waitUntil } from './assembly-process.mjs';
import { assertSourceScenarioUnblocked } from './assembly-scenario.mjs';

// No business fixture: commands only arm real RPC/JDBC fault boundaries and
// call generated Read/Fence/Get with the exact requests saved by production.
export async function criticalMatrix({
  endpoint: e,
  control,
  snapshot,
  checks,
  artifacts,
  onPhase = () => {},
}) {
  const java = (command) => control({ op: 'java', command });
  const trace = async () => (await java({ op: 'trace' })).events;
  const arm = (name, method, phase, mode, extra = {}) =>
    java({ op: 'arm', name, method, phase, mode, ...extra });
  const clear = () => java({ op: 'clear' });
  const hit = async (name, stage = 'fault-hit') =>
    waitUntil(
      async () =>
        (await trace()).find((v) => v.stage === stage && v.bytes === name),
      `missing real ${stage}: ${name}`,
    );
  const rows = (db, name) => db[name] ?? [];
  const iamFacts = (db) =>
    Object.fromEntries(
      Object.entries(db).filter(([key]) => key.startsWith('iam.')),
    );
  const registerOp = (db, id) =>
    rows(db, 'iam.network_source_operation').find(
      (v) => v.method === 'Register' && v.operation_id === id,
    );
  const forSession = (state, id) =>
    Object.values(state.sourceControl.entries).find(
      (v) => v.mapping.hostSessionId === id,
    );
  const currentC = async () => {
    const state = await e.ledger();
    const records = (await control({ op: 'trace' })).records;
    return records
      .filter(
        (v) =>
          v.method === 'Ready' &&
          v.connection.node === state.identity.node &&
          v.code === 'OK',
      )
      .at(-1).connection;
  };
  async function probe(method, original, extra = {}) {
    const start = await control({
      op: 'source-start',
      method,
      original,
      ...extra,
    });
    return waitUntil(async () => {
      const r = (await control({ op: 'source-result', call: start.call }))
        .result;
      return r.done ? r : false;
    }, `real ${method} did not finish`);
  }
  function noReceipt(result, code) {
    assert.equal(result.code, code);
    assert.equal(result.hasSource, false);
    assert.equal(
      'response' in result,
      false,
      'transport/auth denial must not leak a receipt',
    );
  }
  async function fresh(label, before = async () => {}) {
    const state = await e.ledger();
    assertSourceScenarioUnblocked(
      await snapshot(`${label}-preconditions`),
      state.identity,
      label,
    );
    const local = (BigInt(state.localHighWater) + 1n).toString();
    await before(local);
    const id = `${label}-${randomUUID()}`;
    const agent = await e.native.create(id);
    await e.join(agent);
    const value = await waitUntil(async () => {
      const s = await e.ledger(),
        entry = forSession(s, id);
      return entry?.operationId
        ? {
            state: s,
            entry,
            operation: s.sourceControl.operations[entry.operationId],
          }
        : false;
    }, 'new native source operation not durably recorded');
    assert.equal(
      value.entry.local,
      local,
      'fault must target the actual newly registered local',
    );
    return { ...value, id, agent, local };
  }
  async function closeNative(value) {
    await e.native.dispose(value.id);
  }
  try {
    onPhase('critical/live-C miss and pending outcome');
    // Avoid unrelated natural-expiry writes during pure Read/Fence assertions.
    await waitUntil(
      async () => {
        const observation = (await e.ledger()).sourceControl.entries['1']
          .observation;
        return (
          observation?.source.state === 'active' &&
          Date.parse(observation.source.expires_at) > Date.now() + 60_000
        );
      },
      'critical probes require a real stable live source',
      90_000,
    );
    let db = await snapshot('critical-live-before');
    let ledger = await e.ledger();
    const base = structuredClone(
      registerOp(db, ledger.sourceControl.entries['1'].operationId)
        .request_decoded,
    );
    base.connection = await currentC();
    base.operation_id = `missing-${randomUUID()}`;
    const socketCount = e.sockets.length;
    const read = await probe('ReadAgentSourceReceipt', base, {
      requireLive: true,
    });
    assert.equal(read.live, true, 'live-C miss must first pass real Resolve');
    noReceipt(read, 'Unavailable');
    const fence = await probe('FenceAgentSourceReceipt', base);
    noReceipt(fence, 'PermissionDenied');
    assert.equal(fence.reason, 'common.forbidden');
    assert.equal(e.sockets.length, socketCount);
    assert.equal(e.runtime.status.connected, true);
    assert.deepEqual(
      iamFacts(await snapshot('critical-live-after')),
      iamFacts(db),
      'pure Read/live-C Fence must write zero IAM source facts',
    );

    // Deadlines and explicit cancellation are real client/context events; the
    // hook merely keeps business dispatch outside the boundary until they occur.
    onPhase('critical/deadline cancellation and old service');
    const timeoutRequest = { ...base, operation_id: `timeout-${randomUUID()}` };
    await arm('deadline-read', 'ReadAgentSourceReceipt', 'before', 'hold', {
      operation: timeoutRequest.operation_id,
    });
    db = await snapshot('critical-cancel-before');
    noReceipt(
      await probe('ReadAgentSourceReceipt', timeoutRequest, {
        timeoutMillis: 700,
      }),
      'DeadlineExceeded',
    );
    await hit('deadline-read', 'fault-cancelled');
    const cancelRequest = { ...base, operation_id: `cancel-${randomUUID()}` };
    await arm('cancel-read', 'ReadAgentSourceReceipt', 'before', 'hold', {
      operation: cancelRequest.operation_id,
    });
    const call = await control({
      op: 'source-start',
      method: 'ReadAgentSourceReceipt',
      original: cancelRequest,
    });
    await hit('cancel-read');
    assert.equal(
      (await control({ op: 'source-cancel', call: call.call })).cancelled,
      true,
    );
    const cancelled = await waitUntil(async () => {
      const r = (await control({ op: 'source-result', call: call.call }))
        .result;
      return r.done ? r : false;
    }, 'explicitly cancelled source RPC did not finish');
    noReceipt(cancelled, 'Canceled');
    await hit('cancel-read', 'fault-cancelled');
    assert.deepEqual(
      iamFacts(await snapshot('critical-cancel-after')),
      iamFacts(db),
      'deadline/cancelled Read must write zero business facts',
    );
    const oldRequest = { ...base, operation_id: `old-service-${randomUUID()}` };
    await arm(
      'old-service',
      'ReadAgentSourceReceipt',
      'before',
      'unimplemented',
      { operation: oldRequest.operation_id },
    );
    noReceipt(
      await probe('ReadAgentSourceReceipt', oldRequest),
      'Unimplemented',
    );
    await clear();
    checks.push(
      'critical: real deadline, explicit cancellation and old Read service return no receipt and zero business writes',
    );

    // Actual JDBC COMMIT succeeds; only acknowledgement is lost. The complete
    // IAM receipt exists while collab is pending and its saved response is null.
    onPhase('critical/commit acknowledgement unknown and receipt recovery');
    const lost = await fresh('critical-commit', async (local) => {
      await arm('commit-unknown', 'RegisterAgentSource', 'commit', 'ack-lost', {
        local,
      });
      await arm('recovery-read', 'ReadAgentSourceReceipt', 'before', 'hold', {
        local,
        maxHits: 16,
      });
    });
    const commitHit = await hit('commit-unknown');
    assert.equal(commitHit.phase, 'commit-ack-lost');
    assert.equal(commitHit.method, 'RegisterAgentSource');
    assert.equal(commitHit.local, lost.local);
    assert.equal(commitHit.operation, lost.operation.id);
    db = await snapshot('critical-commit-unknown');
    const committed = registerOp(db, lost.operation.id);
    assert.ok(
      committed?.response_decoded.source,
      'commit acknowledgement loss must retain a genuine IAM source/receipt',
    );
    const frozen = rows(db, 'collab.network_source_operation').find(
      (v) => v.operation_id === lost.operation.id,
    );
    assert.equal(frozen.status, 'pending');
    assert.equal(frozen.iam_response, null);
    assert.equal(
      (await e.ledger()).sourceControl.operations[lost.operation.id].receipt,
      null,
    );
    const originalRequest = committed.request,
      originalResponse = committed.response;
    await hit('recovery-read');
    const beforeReconnect = e.sockets.length;
    e.sockets.at(-1).terminate();
    await waitUntil(
      () => e.sockets.length > beforeReconnect && e.runtime.status.connected,
      'real C replacement after commit unknown failed',
    );
    await hit('recovery-read', 'fault-cancelled');
    await clear();
    ledger = await waitUntil(async () => {
      const s = await e.ledger();
      return forSession(s, lost.id)?.observation?.source.state === 'active'
        ? s
        : false;
    }, 'same-op receipt recovery/Get did not activate');
    db = await snapshot('critical-commit-recovered');
    assert.equal(registerOp(db, lost.operation.id).request, originalRequest);
    assert.equal(registerOp(db, lost.operation.id).response, originalResponse);
    const recovered = rows(db, 'collab.network_source_operation').find(
      (v) => v.operation_id === lost.operation.id,
    );
    assert.equal(recovered.iam_response, committed.response);
    assert.equal(recovered.original_connection, frozen.original_connection);
    const inner = (await trace()).filter(
      (v) => v.operation === lost.operation.id && v.phase === 'delegate',
    );
    assert.equal(
      inner.filter((v) => v.method === 'RegisterAgentSource').length,
      1,
      'unknown COMMIT must never reissue Register',
    );
    assert.ok(inner.some((v) => v.method === 'ReadAgentSourceReceipt'));
    assert.equal(
      inner.filter((v) => v.method === 'FenceAgentSourceReceipt').length,
      0,
    );
    assert.notEqual(
      (await currentC()).ingress,
      committed.request_decoded.connection.ingress,
    );
    checks.push(
      'critical: actual JDBC commit acknowledgement unknown, collab pending, new C same-op pure Read/Get recovery with immutable full receipt',
    );

    // Hold an actual ACTIVE IAM Get result outside its completed transaction.
    // The independent revoke lane must stop first; only then release the late result.
    onPhase('critical/late ACTIVE Get after committed revoke');
    const active = forSession(ledger, lost.id),
      binding = active.head;
    await arm('late-get', 'GetAgentSource', 'after', 'hold', {
      operation: binding,
    });
    const held = await hit('late-get');
    assert.ok(
      (await trace()).some(
        (v) =>
          v.method === 'GetAgentSource' &&
          v.operation === binding &&
          v.phase === 'produced' &&
          v.seq < held.seq &&
          v.code === 'NETWORK_AGENT_SOURCE_STATE_ACTIVE',
      ),
      'late boundary must hold a real ACTIVE IAM response',
    );
    await closeNative(lost);
    await waitUntil(
      async () => forSession(await e.ledger(), lost.id).stopped,
      'native stop must be durable before revoke',
    );
    db = await waitUntil(async () => {
      const s = await snapshot('critical-stop-before-late');
      const source = rows(s, 'iam.network_source').find(
        (v) => v.id === binding,
      );
      return source?.state === '2' &&
        rows(s, 'iam.network_source_chain').some(
          (v) => v.id === source.chain_id && v.stopped,
        )
        ? s
        : false;
    }, 'reserved Revoke did not commit while Get was held');
    await java({ op: 'release', name: 'late-get' });
    const released = await hit('late-get', 'fault-released');
    assert.ok(
      released.seq > held.seq,
      'late Get must actually be released after stop',
    );
    const late = await waitUntil(
      async () =>
        (await control({ op: 'trace' })).records.find(
          (v) =>
            v.method === 'SourceGet' &&
            v.request.source_binding === binding &&
            v.code === 'OK' &&
            v.response?.type === 'source_get_result' &&
            v.response.source.state === 'stopped',
        ),
      'late ACTIVE Get must be masked to exact stopped fact by collab final CAS',
    );
    assert.equal('valid_until' in late.response, false);
    await waitUntil(
      () =>
        e.frames.some(
          (v) =>
            v.direction === 'down' &&
            v.frame.request_id === late.request.request_id &&
            v.frame.type === 'source_get_result' &&
            v.frame.source.state === 'stopped' &&
            !('valid_until' in v.frame),
        ),
      'signed stopped Get frame missing',
    );
    assert.equal(forSession(await e.ledger(), lost.id).observation, null);
    const finalStop = await snapshot('critical-late-rejected');
    const stoppedBinding = rows(
      finalStop,
      'collab.network_source_binding',
    ).find((v) => v.source_binding === binding);
    assert.equal(
      stoppedBinding.valid_until,
      null,
      'late Get must not restore collab active validity',
    );
    await clear();
    checks.push(
      'critical: genuine late ACTIVE Get released after committed Revoke becomes exact signed stopped fact and cannot revive plugin/collab validity',
    );

    // Leave this node-wide pending case last among mutation scenarios. The
    // production guard intentionally forbids a new local bypassing its outcome.
    onPhase('critical/live-C pending outcome');
    const beforeMissingFacts = iamFacts(
      await snapshot('critical-pending-before'),
    );
    // Genuine outer pending: block only the initial real Register before it
    // executes, then let actual pure Read miss and no-create live-C Fence fail.
    const missing = await fresh('critical-missing', (local) =>
      arm('live-register', 'RegisterAgentSource', 'before', 'unavailable', {
        local,
      }),
    );
    const missingOp = missing.operation.id;
    await waitUntil(
      async () =>
        (await trace()).some(
          (v) =>
            v.method === 'FenceAgentSourceReceipt' &&
            v.operation === missingOp &&
            v.phase === 'delegate',
        ),
      'pending live-C miss did not reach the real no-create Fence',
    );
    db = await snapshot('critical-pending-miss');
    assert.deepEqual(
      iamFacts(db),
      beforeMissingFacts,
      'pending live-C Read/Fence must not write IAM anchor/op/source/chain facts',
    );
    assert.equal(registerOp(db, missingOp), undefined);
    const pending = rows(db, 'collab.network_source_operation').find(
      (v) => v.operation_id === missingOp,
    );
    assert.equal(pending.status, 'pending');
    assert.equal(pending.iam_response, null);
    assert.ok(
      rows(db, 'collab.network_source_outbox').some(
        (v) => v.operation_id === pending.id && !v.completed,
      ),
    );
    const unknown = await waitUntil(
      () =>
        e.frames.find(
          (v) =>
            v.direction === 'down' &&
            v.frame.type === 'error' &&
            v.frame.code === 'network.outcome_unknown' &&
            e.frames.some(
              (r) =>
                r.direction === 'up' &&
                r.frame.operation_id === missingOp &&
                r.frame.request_id === v.frame.request_id,
            ),
        ),
      'live-C miss must return precise signed outcome_unknown',
    );
    assert.equal(unknown.frame.retryable, false);
    assert.equal('source' in unknown.frame, false);
    assert.equal('failure_evidence' in unknown.frame, false);
    const missingTrace = (await trace()).filter(
      (v) => v.method === 'RegisterAgentSource' && v.operation === missingOp,
    );
    assert.equal(
      missingTrace.filter((v) => v.phase === 'call').length,
      1,
      'pending recovery must not fall back to Register',
    );
    assert.equal(missingTrace.filter((v) => v.phase === 'delegate').length, 0);
    await closeNative(missing);
    await clear();
    checks.push(
      'critical: live-C miss uses real Read/Fence, zero issuance/fence writes, exact UNKNOWN with original pending/outbox',
    );

    // The source originated in the full chain. Query it through real generated
    // IAM both before and after real OrgService role removal; no JWT-perms fixture.
    onPhase('critical/Org revocation and forbidden receipt');
    ledger = await e.ledger();
    db = await snapshot('critical-org-before');
    const liveEntry = ledger.sourceControl.entries['1'];
    const getOriginal = structuredClone(
      registerOp(db, liveEntry.operationId).request_decoded,
    );
    getOriginal.connection = await currentC();
    const positive = await probe('GetAgentSource', getOriginal, {
      binding: liveEntry.head,
    });
    assert.equal(positive.code, 'OK');
    assert.equal(
      positive.response.source.state,
      'NETWORK_AGENT_SOURCE_STATE_ACTIVE',
    );
    assert.equal((await java({ op: 'revoke-external' })).revoked, true);
    const denied = await probe('GetAgentSource', getOriginal, {
      binding: liveEntry.head,
    });
    noReceipt(denied, 'PermissionDenied');
    assert.equal(denied.reason, 'common.forbidden');
    await waitUntil(
      async () => !(await e.ledger()).sourceControl.entries['1'].observation,
      'Org revocation must retire the local observation',
    );
    db = await snapshot('critical-org-after');
    assert.equal(
      registerOp(db, missingOp),
      undefined,
      'late recovery must never issue the uncommitted live-C operation',
    );
    assert.equal(registerOp(db, lost.operation.id).response, originalResponse);
    checks.push(
      'critical: real Org revocation rejects the same typed source Get after a successful positive control, with no source/receipt leakage',
    );
    await writeFile(
      join(artifacts, 'critical-inner-rpc.json'),
      JSON.stringify(await trace(), null, 2),
    );
  } finally {
    await clear();
  }
}
