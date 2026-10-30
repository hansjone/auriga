/* global structuredClone */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { openTrustStore } from '../../../src/network/trust/store.js';
import {
  parseStrictJson,
  canonicalize,
} from '../../../src/network/trust/jcs.js';
import { verifyEnvelope } from '../../../src/network/trust/verifier.js';
import {
  buildVerifiedPage,
  verifyFetchPage,
} from '../../../src/network/session/page.js';
import { signedKeyset, testRoots } from './keyset-fixtures.js';

const read = async (name) =>
  JSON.parse(
    await readFile(
      new URL(
        '../../../../../contracts/network/fixtures/' + name,
        import.meta.url,
      ),
      'utf8',
    ),
  );
const java = await read('java-signed-samples.json');
const now = () => Date.parse(java.context.now);
function cache() {
  let state;
  return {
    async load() {
      return structuredClone(state);
    },
    async commit(v) {
      state = structuredClone(v);
    },
  };
}
const options = {
  roots: testRoots,
  cache: cache(),
  config: signedKeyset(java.keyset),
  now,
};
const unavailable = (e) => e.code === 'network.key_unavailable';

test('root-signed contract keyset validates and rejects tampering, unknown roots, unsigned payload and invalid fields', async () => {
  const trust = await openTrustStore(options);
  assert.ok(trust.key(java.keyset.keys[0].kid, 'business_delivery', '5'));
  for (const mutate of [
    (v) =>
      (v.keyset.keys[0].public_key_spki = java.keyset.keys[1].public_key_spki),
    (v) => (v.root_auth.kid = 'untrusted'),
    (v) => (v.root_auth.alg = 'none'),
  ]) {
    const bad = structuredClone(options.config);
    mutate(bad);
    await assert.rejects(
      openTrustStore({ ...options, cache: cache(), config: bad }),
      unavailable,
    );
  }
  await assert.rejects(
    openTrustStore({ ...options, cache: cache(), config: java.keyset }),
    unavailable,
  );
  const duplicate = JSON.stringify(options.config).replace(
    '"keyset":',
    '"keyset":{},"keyset":',
  );
  await assert.rejects(
    openTrustStore({ ...options, cache: cache(), config: duplicate }),
    unavailable,
  );
  const serialized = await openTrustStore({
    ...options,
    cache: cache(),
    config: new TextEncoder().encode(JSON.stringify(options.config)),
  });
  assert.ok(serialized.key(java.keyset.keys[0].kid, 'business_delivery', '5'));
  for (const mutate of [
    (v) => (v.schema_version = 2),
    (v) => (v.constructor = 'unexpected'),
    (v) => (v.realm = '18446744073709551616'),
    (v) => (v.keys[0].state = 'unknown'),
    (v) => (v.valid_until = '2026-10-27T00:00:00Z'),
    (v) => (v.keys[0].not_after = '2026-02-30T00:00:00Z'),
  ]) {
    const payload = structuredClone(java.keyset);
    mutate(payload);
    await assert.rejects(
      openTrustStore({
        ...options,
        cache: cache(),
        config: signedKeyset(payload),
      }),
      unavailable,
    );
  }
});

test('next and revoked reject; retired verifies old envelopes; key identities and final states cannot be reused', async () => {
  for (const state of ['active', 'next', 'retired', 'revoked']) {
    const p = structuredClone(java.keyset);
    p.keys[0].state = state;
    const trust = await openTrustStore({
      ...options,
      cache: cache(),
      config: signedKeyset(p),
    });
    if (['active', 'retired'].includes(state)) {
      assert.ok(trust.key(p.keys[0].kid, p.keys[0].purpose, '5'));
      const value = java.frames.delivery_first_contact;
      assert.equal(
        verifyEnvelope(value, {
          trust,
          context: {
            ...java.context,
            nodeIncarnation: java.context.incarnation,
            requestId: value.server_auth.request_id,
          },
          now,
        }),
        true,
      );
    } else
      assert.throws(
        () => trust.key(p.keys[0].kid, p.keys[0].purpose, '5'),
        unavailable,
      );
    if (['retired', 'revoked'].includes(state))
      await assert.rejects(
        trust.update(signedKeyset({ ...java.keyset, keyset_version: '6' })),
        unavailable,
      );
  }
  const trust = await openTrustStore({ ...options, cache: cache() });
  const p = structuredClone(java.keyset);
  p.keyset_version = '6';
  p.keys[0].public_key_spki = p.keys[1].public_key_spki;
  await assert.rejects(trust.update(signedKeyset(p)), unavailable);
});

test('removed keys retain immutable identity/final-state history across durable reopen', async () => {
  const disk = cache();
  const p = structuredClone(java.keyset);
  p.keys[0].state = 'revoked';
  const trust = await openTrustStore({
    ...options,
    cache: disk,
    config: signedKeyset(p),
  });
  await trust.update(signedKeyset({ ...p, keyset_version: '6', keys: [] }));
  const restored = await openTrustStore({ roots: testRoots, cache: disk, now });
  await assert.rejects(
    restored.update(signedKeyset({ ...java.keyset, keyset_version: '7' })),
    unavailable,
  );
  const reused = structuredClone(java.keyset);
  reused.keyset_version = '7';
  reused.keys[0].kid = 'replacement';
  await assert.rejects(restored.update(signedKeyset(reused)), unavailable);
});

for (const [name, frame] of Object.entries(java.frames))
  test(`real Java signer -> plugin: ${name}`, async () => {
    const trust = await openTrustStore({ ...options, cache: cache() });
    const context = {
      ...java.context,
      nodeIncarnation: java.context.incarnation,
      requestId: frame.page?.request_id ?? frame.server_auth?.request_id,
    };
    const encoded = new TextEncoder().encode(JSON.stringify(frame));
    const value = parseStrictJson(encoded);
    if (frame.type === 'fetch_result') {
      const input = {
        tenant: 'tenant-test',
        nodeIncarnation: context.nodeIncarnation,
        epoch: context.epoch,
        bootId: 'boot-test',
        items: value.items,
        header: value.page,
        context,
        trust,
        now,
      };
      const page = await verifyFetchPage(input);
      assert.equal(page.verified, true);
      assert.equal(page.rejections.length, 0);
      assert.equal(page.items.length, 2);
      // Java samples precede ATTEMPT_BEGIN and contain no attempt_id. Never invent
      // execution authority merely to make a signature interop test enter the host.
      const hostPage = await buildVerifiedPage(input);
      assert.equal(hostPage.items.length, 0);
      assert.ok(hostPage.rejections.every((r) => r.code === 'missing_attempt'));
      for (const mutate of [
        (v) => v.items.pop(),
        (v) => v.items.reverse(),
        (v) => (v.page.committed_cursor = '999'),
        (v) => (v.items[0].message.body.text = 'tampered'),
      ]) {
        const bad = structuredClone(value);
        mutate(bad);
        await assert.rejects(
          verifyFetchPage({ ...input, items: bad.items, header: bad.page }),
          (e) => e.code === 'network.signature_invalid',
        );
      }
    } else {
      assert.equal(verifyEnvelope(value, { trust, context, now }), true);
      value.app_version = '1.1';
      assert.throws(
        () => verifyEnvelope(value, { trust, context, now }),
        (e) => e.code === 'network.signature_invalid',
      );
    }
  });

for (const c of (await read('raw-boundaries.json')).cases)
  test(`shared strict boundary: ${c.name}`, () => {
    let raw;
    if (c.kind === 'valueTree') {
      const names =
        'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_';
      raw = JSON.stringify(
        Array.from({ length: c.branches }, (_, i) =>
          Object.fromEntries(
            Array.from(
              { length: i === c.branches - 1 ? c.lastMembers : c.members },
              (_, n) => [names[n], {}],
            ),
          ),
        ),
      );
    } else if (c.kind.startsWith('nested'))
      raw =
        '['.repeat(c.containers) +
        (c.kind === 'nestedScalar' ? '0' : '') +
        ']'.repeat(c.containers);
    else {
      const text = c.unit.repeat(c.repeat) + (c.suffix ?? '');
      raw =
        c.kind === 'key'
          ? `{${JSON.stringify(text)}:0}`
          : JSON.stringify({ value: text });
    }
    if (c.accept)
      assert.doesNotThrow(() =>
        canonicalize(parseStrictJson(new TextEncoder().encode(raw))),
      );
    else
      assert.throws(
        () => parseStrictJson(new TextEncoder().encode(raw)),
        (e) => e.code === 'network.protocol_error',
      );
  });
