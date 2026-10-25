/* global structuredClone */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildVerifiedPage } from '../../../src/network/session/page.js';
import { signingFixture } from '../trust/fixtures.js';

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
      return { ...f.config, version: '6' };
    },
  });
  const input = f.pageInput([f.delivery('rotated')]);
  f.seal(input.header, f.context, '6');
  const page = await buildVerifiedPage(input);
  assert.equal(page.verified, true);
  assert.equal(page.items.length, 1);
  assert.equal(calls, 1);
});
