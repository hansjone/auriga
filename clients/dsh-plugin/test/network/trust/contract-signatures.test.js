/* global structuredClone */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  privateKeyFromSeed,
  seal,
} from '../../../../../contracts/test/network-lib.mjs';
import { openTestTrust as openTrustStore } from './keyset-fixtures.js';
import { verifyEnvelope } from '../../../src/network/trust/verifier.js';
import { buildVerifiedPage } from '../../../src/network/session/page.js';

const read = async (file) =>
  JSON.parse(
    await readFile(
      new URL(
        '../../../../../contracts/network/fixtures/' + file,
        import.meta.url,
      ),
      'utf8',
    ),
  );
const fixtures = await read('frames.json');
const vectors = await read('signature-vectors.json');
const cases = (await read('frame-cases.json')).cases;
const now = () => Date.parse(fixtures.receiver.now);
const trust = await openTrustStore({
  now,
  cache: { async load() {}, async commit() {} },
  config: {
    schema_version: 1,
    realm: '2',
    keyset_version: '5',
    issued_at: '2026-10-24T01:00:00Z',
    valid_until: '2026-10-25T01:00:00Z',
    keys: vectors.keys.map((k) => ({
      kid: k.kid,
      purpose: k.purpose,
      public_key_spki: k.publicKeySpki,
      alg: 'Ed25519',
      state: 'active',
      not_before: '2026-10-24T00:00:00Z',
      not_after: '2026-10-27T00:00:00Z',
    })),
  },
});
const byPurpose = (value) =>
  privateKeyFromSeed(
    vectors.keys.find((k) => k.purpose === value.server_auth.purpose)
      .testOnlySeedHex,
  );
const byKid = (value) =>
  privateKeyFromSeed(
    vectors.keys.find((k) => k.kid === value.server_auth.kid).testOnlySeedHex,
  );
function at(root, path) {
  const parts = path.split('.');
  return [parts.slice(0, -1).reduce((p, k) => p[k], root), parts.at(-1)];
}
function apply(root, ops) {
  for (const op of ops) {
    if ('seal' in op) {
      seal(root, op.seal === 'with_kid_key' ? byKid : byPurpose);
      continue;
    }
    const [p, k] = at(
      root,
      op.set ?? op.remove ?? op.copy ?? op.noncanonical_sig,
    );
    if ('set' in op) p[k] = structuredClone(op.value);
    else if ('remove' in op) {
      if (Array.isArray(p)) p.splice(Number(k), 1);
      else delete p[k];
    } else if ('copy' in op)
      p[k] = structuredClone(fixtures.frames[op.from].value);
    else if ('noncanonical_sig' in op)
      p[k] =
        p[k].slice(0, -1) + String.fromCharCode(p[k].at(-1).charCodeAt(0) + 1);
    else throw Error('unsupported fixture operation');
  }
  return root;
}

for (const c of cases.filter(
  (c) =>
    c.mode === 'downstream' &&
    /network\.(signature_|key_unavailable)/.test(c.expect ?? ''),
)) {
  test(`contract signature rejection: ${c.name}`, async () => {
    const fixture = fixtures.frames[c.base];
    const frame = apply(structuredClone(fixture.value), c.ops);
    const ctx = { ...fixtures.receiver, ...fixture.context, ...c.context };
    const context = { ...ctx, nodeIncarnation: ctx.incarnation };
    const verifyNow = () => Date.parse(ctx.now);
    if (frame.type === 'fetch_result') {
      let page;
      try {
        page = await buildVerifiedPage({
          tenant: 'tenant-fixture',
          nodeIncarnation: ctx.incarnation,
          epoch: ctx.epoch,
          bootId: 'boot-test',
          header: frame.page,
          items: frame.items,
          trust,
          context,
          now: verifyNow,
        });
      } catch (error) {
        assert.equal(error.code, c.expect);
        return;
      }
      // A bad member is isolated; the signed container and other members survive.
      assert.ok(page.rejections.some((r) => r.code === c.expect));
    } else {
      assert.throws(
        () => verifyEnvelope(frame, { trust, context, now: verifyNow }),
        (e) => e.code === c.expect,
      );
    }
  });
}
