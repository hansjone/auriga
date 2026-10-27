import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { canonicalize } from '../../../clients/dsh-plugin/src/network/trust/jcs.js';
import { verifyEnvelopeWithRefresh } from '../../../clients/dsh-plugin/src/network/trust/verifier.js';

const body = (request) => canonicalize({ ...request, request_id: null });
const failure = 'typed-local production registration failed';

// Integration driver only. Reinvoke the real public tool after a fully verified
// transient LocalRegister refusal. Never retry a source operation or invent R.
export async function joinWithRegistrationRetry({
  invoke,
  frames,
  getState,
  getContext,
  getTrust,
  assertNative,
  onAttempt = () => {},
  pause = delay,
  now = Date.now,
}) {
  let frozenContext, frozenBody, frozenAllocation, local;
  for (let attempt = 1; attempt <= 5; attempt++) {
    await assertNative();
    const context = getContext();
    if (frozenContext)
      assert.equal(
        canonicalize(context),
        frozenContext,
        'registration retry changed READY context',
      );
    if (frozenAllocation)
      assert.equal(
        canonicalize((await getState()).locals[local]),
        frozenAllocation,
        'registration retry changed durable allocation',
      );
    const start = frames.length;
    const dispatched = await invoke();
    assert.equal(
      dispatched.isError,
      false,
      'real ToolRuntime join dispatch failed',
    );
    await assertNative();
    assert.equal(
      canonicalize(getContext()),
      canonicalize(context),
      'registration tool crossed a READY context change',
    );
    const result = dispatched.value;
    const requests = frames
      .slice(start)
      .filter(
        (event) =>
          event.direction === 'up' && event.frame?.type === 'local_register',
      )
      .map((event) => event.frame);
    if (frozenBody)
      for (const request of requests)
        assert.equal(
          body(request),
          frozenBody,
          'registration retry rewrote original C/R intent',
        );
    if (result?.status === 'registered') {
      onAttempt({
        attempt,
        status: result.status,
        requests: requests.map((request) => ({
          requestId: request.request_id,
          local: request.candidate_local,
          allocationOpId: request.allocation_op_id,
        })),
      });
      return result;
    }
    assert.equal(result?.status, 'unavailable', failure);
    assert.equal(requests.length, 1, failure);
    const request = requests[0];
    assert.equal(
      request.epoch,
      context.epoch,
      'LocalRegister must use current READY epoch',
    );
    assert.equal(
      request.boot_id,
      context.bootId,
      'LocalRegister must use current READY boot',
    );
    const response = frames
      .slice(start)
      .find(
        (event) =>
          event.direction === 'down' &&
          event.frame?.request_id === request.request_id,
      )?.frame;
    const retryable =
      response?.type === 'error' &&
      response.code === 'network.overloaded' &&
      response.scope === 'request' &&
      response.retryable === true &&
      (response.retry_after === null ||
        (Number.isSafeInteger(response.retry_after) &&
          response.retry_after >= 1 &&
          response.retry_after <= 5));
    if (!retryable) {
      onAttempt({
        attempt,
        status: result.status,
        requestId: request.request_id,
        retryVerified: false,
      });
      assert.equal(result.status, 'registered', failure);
    }
    try {
      await verifyEnvelopeWithRefresh(response, {
        trust: getTrust(),
        context: { ...context, requestId: request.request_id },
        now,
      });
    } catch (error) {
      onAttempt({
        attempt,
        status: result.status,
        requestId: request.request_id,
        retryVerified: false,
      });
      throw error;
    }
    await assertNative();
    local = request.candidate_local;
    const allocation = (await getState()).locals[local];
    assert.equal(
      allocation?.state,
      'reserved',
      'retry requires the original unsettled allocation',
    );
    assert.equal(allocation.local, local);
    assert.equal(allocation.opId, request.allocation_op_id);
    assert.equal(
      'sha256:' + allocation.fingerprint,
      request.allocation_fingerprint,
    );
    assert.equal(
      allocation.expectedRegistrationRevision,
      request.expected_registration_revision,
    );
    frozenContext ??= canonicalize(context);
    frozenBody ??= body(request);
    frozenAllocation ??= canonicalize(allocation);
    onAttempt({
      attempt,
      status: result.status,
      requestId: request.request_id,
      local,
      allocationOpId: request.allocation_op_id,
      retryVerified: true,
    });
    if (attempt === 5) assert.equal(result.status, 'registered', failure);
    // Honor a signed server backoff; no new ticket/connection/session/op.
    await pause(Math.max(500, (response.retry_after ?? 0) * 1000));
  }
}
