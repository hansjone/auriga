/* global structuredClone */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildVerifiedPage } from '../../../src/network/session/page.js';
import { signingFixture } from '../trust/fixtures.js';
import { digest } from '../../../src/network/trust/verifier.js';

test('mixed signed page rejects only the bad delivery and retains auditable index/reason', async () => {
  const f = await signingFixture();
  const good = f.delivery('good');
  const bad = f.delivery('bad');
  bad.message.body.text = 'tampered';
  const input = f.pageInput([bad, good]);
  const page = await buildVerifiedPage(input);
  assert.equal(page.verified, true);
  assert.equal(page.items.length, 1);
  assert.equal(page.items[0].deliveryId, 'dlv-good');
  assert.deepEqual(page.rejections, [
    { index: 0, code: 'network.signature_invalid', reason: 'bad signature' },
  ]);
  assert.equal((await buildVerifiedPage(f.pageInput([bad]))).verified, true);
});

test('signed manifest rejects truncation, reorder, replacement and header tampering', async () => {
  const f = await signingFixture();
  const input = f.pageInput([f.delivery('one'), f.delivery('two')]);
  for (const change of [
    (x) => x.items.pop(),
    (x) => x.items.reverse(),
    (x) => (x.items[0].mail_seq = '99'),
    (x) => (x.header.scan_after = '99'),
  ]) {
    const altered = {
      ...input,
      header: structuredClone(input.header),
      items: structuredClone(input.items),
    };
    change(altered);
    await assert.rejects(
      () => buildVerifiedPage(altered),
      (e) => e.code === 'network.signature_invalid',
    );
  }
});

test('newer page header awaits injected keyset refresh before accepting signed delivery', async () => {
  let calls = 0;
  const f = await signingFixture({
    refresh: async () => {
      calls++;
      return { ...f.config, keyset_version: '6' };
    },
  });
  const input = f.pageInput([f.delivery('rotated')]);
  f.seal(input.header, f.context, '6');
  const page = await buildVerifiedPage(input);
  assert.equal(page.verified, true);
  assert.equal(page.items.length, 1);
  assert.equal(calls, 1);
});

test('signed gap for an oversized item coexists with a valid delivery; do not invent a receipt for the gap', async () => {
  const f = await signingFixture();
  const gap = f.seal({
    app_version: '1.0',
    type: 'gap',
    epoch: '7',
    from_seq: '9',
    to_seq: '9',
    reason: 'quarantined',
  });
  const page = await buildVerifiedPage({
    ...f.pageInput([gap, f.delivery('fits')]),
    maxBytes: 4096,
  });
  assert.equal(page.verified, true);
  assert.equal(page.items.length, 1);
  assert.equal(page.items[0].deliveryId, 'dlv-fits');
  assert.deepEqual(page.rejections, []);
});

test('signed envelopes cannot exceed the aggregate/negotiated frame budget; an oversize body only rejects its own item', async () => {
  const f = await signingFixture();
  const bad = f.delivery('large-body');
  bad.message.body.text = 'x'.repeat(16384);
  bad.source_context.message_digest = digest(bad.message);
  f.seal(bad);
  const page = await buildVerifiedPage(f.pageInput([bad, f.delivery('good')]));
  assert.equal(page.items.length, 1);
  assert.equal(page.rejections[0].code, 'network.protocol_error');
  const large = f.pageInput(
    Array.from({ length: 64 }, (_, i) => f.delivery(String(i))),
  );
  await assert.rejects(
    buildVerifiedPage(large),
    (e) => e.code === 'network.protocol_error',
  );
  const negotiated = f.pageInput([bad]);
  await assert.rejects(
    buildVerifiedPage({ ...negotiated, maxBytes: 4096 }),
    (e) => e.code === 'network.protocol_error',
  );
});
