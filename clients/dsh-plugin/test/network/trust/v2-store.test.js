import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, sign, verify } from 'node:crypto';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import { detach, digest, jcs } from '../../../src/network/trust/v2.js';

const vectors = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const date = (milliseconds) =>
  new Date(milliseconds).toISOString().replace('.000Z', 'Z');
const spki = (key) =>
  key.export({ type: 'spki', format: 'der' }).toString('base64url');
export function fixture() {
  const root = generateKeyPairSync('ed25519'),
    runtime = generateKeyPairSync('ed25519');
  let now = Date.parse('2026-11-14T00:00:00Z');
  const auth = (payload, pair, kid, domain, field) => ({
    payload,
    [field]: {
      alg: 'Ed25519',
      kid,
      signature: sign(
        null,
        Buffer.from(domain + '\0' + jcs(payload)),
        pair.privateKey,
      ).toString('base64url'),
    },
  });
  const rootSign = (payload, domain) =>
    auth(payload, root, 'test-root', domain, 'root_auth');
  const runtimeSign = (payload, domain) =>
    auth(payload, runtime, 'test-runtime', domain, 'runtime_auth');
  const authorityBody = detach(
    vectors.cases[0].input.publication.authority.payload,
  );
  Object.assign(authorityBody, {
    runtime_kid: 'test-runtime',
    public_key_spki: spki(runtime.publicKey),
  });
  const authority = rootSign(
    authorityBody,
    'Auriga.NetworkRuntimeAuthority.v2',
  );
  let control = makeControl(2, false);
  function makeControl(revision, revoked) {
    const body = detach(vectors.cases[0].input.latest_control.payload);
    Object.assign(body, {
      control_revision: String(revision),
      allowed_authorities: revoked
        ? []
        : [
            {
              authority_id: authorityBody.authority_id,
              authority_digest: digest(authority),
            },
          ],
      revoked_authority_ids: revoked ? [authorityBody.authority_id] : [],
      revoked_installation_ids: [],
    });
    return rootSign(body, 'Auriga.NetworkRootControl.v2');
  }
  let counter = 0,
    publication;
  function publish(recover = false) {
    const previous = publication;
    const version = previous
      ? String(BigInt(previous.keyset.payload.keyset_version) + 1n)
      : '1';
    const keys =
      !previous || recover
        ? ['business_delivery', 'protocol_response', 'management_state'].map(
            (purpose) => ({
              kid: purpose + '-' + counter++,
              alg: 'Ed25519',
              purpose,
              state: 'active',
              not_before: date(now),
              not_after: date(now + 7 * 86400000),
              public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
            }),
          )
        : previous.keyset.payload.keys;
    const payload = {
      ...detach(vectors.cases[0].input.publication.keyset.payload),
      authority_id: authorityBody.authority_id,
      authority_digest: digest(authority),
      control_revision: control.payload.control_revision,
      keyset_version: version,
      issued_at: date(now),
      valid_until: date(now + 86400000),
      mode: previous ? (recover ? 'restart_recovery' : 'refresh') : 'bootstrap',
      keys,
      first_publications:
        !previous || recover
          ? keys.map((k) => ({
              kid: k.kid,
              committed_version: version,
              committed_at: date(now),
            }))
          : previous.keyset.payload.first_publications,
      archive: {
        before: date(now - 360000),
        history_digest: 'sha256:' + '0'.repeat(64),
      },
    };
    publication = {
      keyset: runtimeSign(payload, 'Auriga.NetworkKeyset.v2'),
      authority,
      control,
      ...(previous
        ? {
            predecessor: {
              keyset: previous.keyset,
              authority: previous.authority,
              control: previous.control,
              keyset_digest: digest(previous.keyset),
            },
          }
        : {}),
    };
    return publication;
  }
  function announcement(version) {
    const k = publication.keyset.payload;
    const payload = {
      ...detach(
        vectors.boundary_cases.find((x) => x.id === 'announcement-matched-pair')
          .input.announcement.payload,
      ),
      ...Object.fromEntries(
        [
          'tenant_id',
          'realm',
          'authority_id',
          'authority_digest',
          'control_revision',
          'generation',
          'keyset_version',
          'valid_until',
        ].map((f) => [f, k[f]]),
      ),
      keyset_digest: digest(publication.keyset),
    };
    if (version) payload.keyset_version = version;
    return runtimeSign(payload, 'Auriga.NetworkKeysetAnnouncement.v2');
  }
  let state,
    fail = false;
  const cache = {
    load: async () => state && detach(state),
    commit: async (value) => {
      if (fail) throw Error('disk failed');
      state = detach(value);
    },
  };
  const options = {
    cache,
    realm: '1',
    tenant_id: 'synthetic-tenant',
    roots: [{ kid: 'test-root', public_key_spki: spki(root.publicKey) }],
    now: () => now,
  };
  return {
    options,
    cache,
    root,
    rootSign,
    authority,
    publish,
    announcement,
    control: (revision, revoked = false) => {
      control = makeControl(revision, revoked);
      return control;
    },
    advance: (days) => {
      now += days * 86400000;
    },
    fail: (value) => {
      fail = value;
    },
    state: () => state,
  };
}
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
