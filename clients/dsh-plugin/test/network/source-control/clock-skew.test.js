import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertSourceResult,
  sourceObservationDeadline,
} from '../../../src/network/source-control/frames.js';

const local = Date.parse('2026-10-31T08:00:00Z');
const iso = (ms) => new Date(ms).toISOString().replace('.000Z', 'Z');
function fixture(skew) {
  const witness = {
    witness_id: 'w',
    observed_at: iso(local),
    valid_until: iso(local + 30000),
  };
  const request = {
    type: 'source_get',
    request_id: 'r',
    native_witness: witness,
  };
  const result = {
    type: 'source_get_result',
    request_id: 'r',
    policy_revision: '1',
    witness_id: 'w',
    observed_at: iso(local + skew),
    valid_until: iso(Math.min(local + skew + 20000, local + 30000)),
    source: {
      state: 'active',
      not_before: iso(local + skew - 1000),
      expires_at: iso(local + skew + 120000),
    },
  };
  return { request, result, witness };
}
for (const skew of [-15000, 15000])
  test(`source observation accepts platform skew ${skew} with a strict native fence`, () => {
    const { request, result, witness } = fixture(skew);
    assert.doesNotThrow(() => assertSourceResult(result, request, local));
    assert.equal(
      sourceObservationDeadline(result, witness),
      local + (skew > 0 ? 15000 : 20000),
    );
    assert.throws(() =>
      assertSourceResult(
        result,
        request,
        sourceObservationDeadline(result, witness),
      ),
    );
    assert.throws(() => assertSourceResult(result, request, local + 30000));
  });
for (const skew of [-61000, 61000])
  test(`source observation rejects platform beyond tolerance ${skew}`, () => {
    const { request, result } = fixture(skew);
    assert.throws(() => assertSourceResult(result, request, local));
  });
test('same-domain expiry, witness upper bound and policy fences remain strict', () => {
  for (const mutate of [
    (r) => {
      r.valid_until = r.observed_at;
    },
    (r) => {
      r.source.expires_at = iso(local + 1000);
    },
    (r) => {
      r.valid_until = iso(local + 31000);
    },
    (r) => {
      r.witness_id = 'other';
    },
  ]) {
    const { request, result } = fixture(15000);
    mutate(result);
    assert.throws(() => assertSourceResult(result, request, local));
  }
  const { result, witness } = fixture(0);
  assert.equal(
    sourceObservationDeadline(result, {
      ...witness,
      valid_until: iso(local - 1000),
    }),
    local - 1000,
  );
});

import { setup } from './fixtures.js';
import { publicationFixture } from '../trust/keyset-fixtures.js';
for (const skew of [-15000, 15000])
  test(`signed controller uses distinct clocks (${skew})`, async (t) => {
    const signer = publicationFixture();
    const clientNow = signer.now();
    signer.now = () => clientNow + skew;
    signer.payload.issued_at = iso(signer.now());
    signer.payload.keys[0].not_before = iso(signer.now() - 1000);
    const f = await setup(t, {
      signer,
      now: () => clientNow,
      resign: true,
      beforeReply: (request, result) => {
        if (request.type === 'source_get' && result.source.state === 'active')
          result.valid_until = iso(
            Math.min(
              Date.parse(result.valid_until),
              Date.parse(request.native_witness.valid_until),
              Date.parse(result.source.expires_at),
            ),
          );
      },
    });
    await f.control.establish('1');
    await f.control.idle();
    const state = f.ledger.peek().sourceControl,
      observation = state.entries['1'].observation;
    assert.ok(
      observation,
      'signed observation retained after all signature/identity checks',
    );
    const witness =
      state.witnesses[observation.witness_id].request.native_witness;
    assert.ok(sourceObservationDeadline(observation, witness) > clientNow);
    assert.ok(
      sourceObservationDeadline(observation, witness) <=
        Date.parse(witness.valid_until),
    );
  });

import { resolveClockToleranceSeconds } from '../../../src/network/clock.js';
test('shared clock configuration is bounded and never accepts model-selected strings', () => {
  for (const value of [0, 5, 60, 120])
    assert.equal(resolveClockToleranceSeconds(value), value);
  for (const value of [-1, 121, 0.5, NaN, '60'])
    assert.throws(() => resolveClockToleranceSeconds(value));
});
test('a local witness from the future fails without any cross-clock leeway', () => {
  const { request, result } = fixture(15000);
  request.native_witness.observed_at = iso(local + 1000);
  assert.throws(() => assertSourceResult(result, request, local));
});
