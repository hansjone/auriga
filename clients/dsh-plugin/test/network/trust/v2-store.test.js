import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, sign, verify } from 'node:crypto';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import { detach, digest, jcs } from '../../../src/network/trust/v2.js';

import { fixture } from './v2-cache-fixture.js';
test('v2 cache is durable before use and survives reopen without renewing expiry', async () => {
  const f = fixture(),
    p = f.publish();
  const store = await openV2TrustStore(f.options);
  await store.update(p);
  assert.throws(() => store.assertUsable(), /announcement/);
  await store.announce(f.announcement());
  store.assertUsable();
  const reopened = await openV2TrustStore(f.options);
  reopened.assertUsable();
  f.advance(2);
  assert.throws(() => reopened.assertUsable(), /unavailable/);
  await reopened.update(f.publish());
  await reopened.announce(f.announcement());
  reopened.assertUsable();
});
test('root control ahead preserves still-allowed authority; revocation fences even during failed commit', async () => {
  const f = fixture(),
    store = await openV2TrustStore(f.options);
  await store.update(f.publish());
  await store.announce(f.announcement());
  await store.installControl(f.control(3));
  store.assertUsable();
  let disconnected = 0;
  store.onIsolation(() => disconnected++);
  f.fail(true);
  const pending = store.installControl(f.control(4, true));
  assert.throws(() => store.assertUsable(), /unavailable/);
  assert.equal(disconnected, 1);
  await assert.rejects(pending, /disk failed/);
  assert.throws(() => store.assertUsable(), /unavailable/);
  f.fail(false);
  await store.installControl(f.control(4, true));
  assert.throws(() => store.assertUsable(), /unavailable/);
  await assert.rejects(store.update(f.publish()), /authority_revoked/);
  const reopened = await openV2TrustStore(f.options);
  assert.throws(() => reopened.assertUsable(), /unavailable/);
  await assert.rejects(
    reopened.installControl(f.control(3)),
    /control_rollback/,
  );
});
test('higher announcement fences immediately and remains blocked after disk failure', async () => {
  const f = fixture(),
    store = await openV2TrustStore(f.options);
  await store.update(f.publish());
  await store.announce(f.announcement());
  const next = f.publish();
  f.fail(true);
  const pending = store.announce(f.announcement('2'));
  assert.throws(() => store.assertUsable(), /announcement/);
  await assert.rejects(pending, /disk failed/);
  assert.throws(() => store.assertUsable(), /announcement/);
  f.fail(false);
  await store.update(next);
  await store.announce(f.announcement());
  store.assertUsable();
});

test('a root minimum-version fence disconnects the old snapshot while a matching new snapshot recovers', async () => {
  const f = fixture(),
    store = await openV2TrustStore(f.options);
  await store.update(f.publish());
  await store.announce(f.announcement());
  let fenced = 0;
  store.onIsolation(() => fenced++);
  const base = f.control(3);
  const control = f.rootSign(
    { ...base.payload, minimum_keyset_version: '2' },
    'Auriga.NetworkRootControl.v2',
  );
  await store.installControl(control);
  assert.equal(fenced, 1);
  assert.throws(() => store.assertUsable(), /unavailable/);
  const publication = f.publish();
  publication.control = control;
  await store.update(publication);
  await store.announce(f.announcement());
  store.assertUsable();
  assert.equal(store.isolated, false);
});
test('offline cross-year recovery retains more than 128 immutable history entries', async () => {
  const f = fixture();
  let store = await openV2TrustStore(f.options);
  await store.update(f.publish());
  await store.announce(f.announcement());
  for (let i = 0; i < 75; i++) {
    f.advance(10);
    store = await openV2TrustStore(f.options);
    assert.throws(() => store.assertUsable(), /unavailable/);
    await store.update(f.publish(true));
    await store.announce(f.announcement());
    store.assertUsable();
  }
  assert.equal(f.state().history.length, 228);
  assert.equal(f.state().first_publication_ledger.length, 228);
  assert.equal(
    f.state().history.filter((k) => k.state === 'retired').length,
    225,
  );
});
test('control package receipt is signed only after durable installation; replay is exact and durable', async () => {
  const f = fixture(),
    store = await openV2TrustStore(f.options),
    identity = generateKeyPairSync('ed25519');
  const control = f.control(3);
  const pack = {
    package_id: 'package-1',
    tenant_id: 'synthetic-tenant',
    realm: '1',
    control,
    control_digest: digest(control),
    target_id: 'device-1',
    challenge: Buffer.alloc(32, 17).toString('base64url'),
  };
  let signed = 0;
  const signer = {
    target_id: 'device-1',
    identity_id: 'installation-1',
    sign: async (bytes) => {
      signed++;
      assert.equal(f.state().control.payload.control_revision, '3');
      return {
        kid: 'installation-1',
        alg: 'Ed25519',
        signature: sign(null, bytes, identity.privateKey).toString('base64url'),
      };
    },
  };
  f.fail(true);
  await assert.rejects(store.importControlPackage(pack, signer), /disk failed/);
  assert.equal(signed, 0);
  f.fail(false);
  const receipt = await store.importControlPackage(pack, signer);
  const { identity_auth, ...body } = receipt;
  assert.ok(
    verify(
      null,
      Buffer.from('Auriga.NetworkControlReceipt.v2\0' + jcs(body)),
      identity.publicKey,
      Buffer.from(identity_auth.signature, 'base64url'),
    ),
  );
  const reopened = await openV2TrustStore(f.options);
  assert.deepEqual(await reopened.importControlPackage(pack, signer), receipt);
  assert.equal(signed, 1);
  await assert.rejects(
    reopened.importControlPackage(
      { ...pack, challenge: Buffer.alloc(32, 18).toString('base64url') },
      signer,
    ),
    /invalid_transition/,
  );
  assert.equal(reopened.pendingControlReceipts().length, 1);
  await reopened.acknowledgeControlReceipt(pack.package_id);
  const confirmed = await openV2TrustStore(f.options);
  assert.equal(confirmed.pendingControlReceipts().length, 0);
});

test(
  'late publication commit cannot clear a newer announcement whose own commit fails',
  { timeout: 10000 },
  async (t) => {
    const f = fixture(),
      store = await openV2TrustStore(f.options);
    await store.update(f.publish());
    await store.announce(f.announcement());
    const next = f.publish(),
      announce = f.announcement();
    let entered, release;
    const waiting = new Promise((resolve) => {
      entered = resolve;
    });
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    t.after(() => release());
    const original = f.cache.commit;
    f.cache.commit = async (value) => {
      if (
        value.publication?.keyset.payload.keyset_version === '2' &&
        value.announcement?.payload.keyset_version === '1'
      ) {
        entered();
        await gate;
      }
      if (value.announcement?.payload.keyset_version === '2')
        throw Error('announcement disk failure');
      return original(value);
    };
    const update = store.update(next);
    await waiting;
    const announced = store.announce(announce);
    const rejection = assert.rejects(announced, /announcement disk failure/);
    release();
    await update;
    await rejection;
    assert.throws(() => store.assertUsable(), /announcement/);
    f.cache.commit = original;
    await store.announce(announce);
    store.assertUsable();
  },
);

test('local predecessor binds exact digest and rejects older forks; offline higher predecessor recovers', async () => {
  const f = fixture(),
    store = await openV2TrustStore(f.options);
  const one = f.publish();
  await store.update(one);
  await store.announce(f.announcement());
  const two = f.publish();
  const ahead = f.publish();
  await assert.rejects(store.update(ahead), /invalid_transition/);
  const fork = detach(two);
  fork.predecessor.keyset = f.runtimeSign(
    {
      ...one.keyset.payload,
      valid_until: new Date(Date.parse(one.keyset.payload.valid_until) - 1000)
        .toISOString()
        .replace('.000Z', 'Z'),
    },
    'Auriga.NetworkKeyset.v2',
  );
  fork.predecessor.keyset_digest = digest(fork.predecessor.keyset);
  await assert.rejects(store.update(fork), /invalid_transition/);
  assert.equal(store.version, '1');
  await store.update(two);
  await store.announce(f.announcement());
  const three = f.publish();
  const stale = { ...three, predecessor: two.predecessor };
  await assert.rejects(store.update(stale), /invalid_transition/);
  assert.equal(store.version, '2');
  f.advance(2);
  f.publish(); // The server advanced while this installation was offline.
  const latest = f.publish();
  await store.update(latest);
  await store.announce(f.announcement());
  store.assertUsable();
  assert.equal(store.version, '6');
});

test('migration without an installed root permit rejects even on an empty cache', async () => {
  const f = fixture(),
    publication = f.publish();
  publication.keyset = f.runtimeSign(
    { ...publication.keyset.payload, mode: 'migration' },
    'Auriga.NetworkKeyset.v2',
  );
  const store = await openV2TrustStore(f.options);
  await assert.rejects(store.update(publication), /invalid_transition/);
});
