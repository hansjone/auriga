import test from 'node:test';
import assert from 'node:assert/strict';
import { natsTraceparent } from '../src/observability/index.js';
test('join request children preserve one correlation trace without changing signed payload bytes', () => {
  const trace = '1234567890abcdef1234567890abcdef';
  for (const suffix of [
    '11111111-1111-4111-8111-111111111111',
    '22222222-2222-4222-8222-222222222222',
  ]) {
    const payload = Buffer.from(
      JSON.stringify({
        request_id: `join-${trace}-${suffix}`,
        type: 'local_register',
      }),
    );
    const original = payload.toString('hex');
    assert.equal(natsTraceparent(payload).slice(3, 35), trace);
    assert.equal(payload.toString('hex'), original);
  }
  assert.notEqual(
    natsTraceparent(
      Buffer.from(
        JSON.stringify({
          request_id:
            'join-' + '0'.repeat(32) + '-11111111-1111-4111-8111-111111111111',
        }),
      ),
    ).slice(3, 35),
    '0'.repeat(32),
  );
});
