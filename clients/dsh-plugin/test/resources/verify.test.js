// @ts-nocheck
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { canonicalize } from '../../src/network/trust/jcs.js';
import { verifyResourceArchive } from '../../src/resources/verify.js';
const id = (n) => `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const now = Date.parse('2026-11-11T00:00:00Z');
function fixture() {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const archive = Buffer.from('test archive bytes');
  const statement = {
    purpose: 'plugin',
    name: 'example-plugin',
    version: '1.0.0',
    os: 'any',
    arch: 'any',
    digest: 'sha256:' + createHash('sha256').update(archive).digest('hex'),
    issuedAt: '2026-11-10T23:00:00Z',
  };
  const envelope = {
    schemaVersion: 1,
    tenantId: id(1),
    packageId: id(2),
    versionId: id(3),
    releaseSequence: 2,
    notAfter: '2026-11-11T01:00:00Z',
    statement,
  };
  const expected = {
    tenantId: id(1),
    packageId: id(2),
    versionId: id(3),
    releaseSequence: 2,
    statement,
    sizeBytes: archive.length,
  };
  const options = {
    expected,
    releaseKeys: new Map([['release', publicKey]]),
    now,
    highWater: undefined,
  };
  const wire = () =>
    Buffer.from(
      JSON.stringify({
        ...envelope,
        signature: {
          alg: 'Ed25519',
          kid: 'release',
          value: sign(
            null,
            Buffer.from(canonicalize(envelope)),
            privateKey,
          ).toString('base64url'),
        },
      }),
    );
  return { archive, envelope, options, wire };
}
test('resource signature binds the complete envelope and exact archive bytes', () => {
  const f = fixture();
  assert.equal(
    verifyResourceArchive(f.wire(), f.archive, f.options).versionId,
    id(3),
  );
  assert.throws(
    () => verifyResourceArchive(f.wire(), Buffer.from('changed'), f.options),
    { code: 'resource.digest_mismatch' },
  );
});
test('resource signature refuses forgery, unknown roots and ambiguous JSON', () => {
  const f = fixture();
  const original = f.wire();
  const forged = JSON.parse(original);
  forged.statement.name = 'changed';
  for (const bytes of [
    Buffer.from(JSON.stringify(forged)),
    Buffer.from(
      original
        .toString()
        .replace('"schemaVersion":1', '"schemaVersion":1,"schemaVersion":1'),
    ),
    Buffer.from([0xff]),
  ])
    assert.throws(() => verifyResourceArchive(bytes, f.archive, f.options), {
      code: 'resource.signature_invalid',
    });
  assert.throws(
    () =>
      verifyResourceArchive(original, f.archive, {
        ...f.options,
        releaseKeys: new Map(),
      }),
    { code: 'resource.signature_invalid' },
  );
});
test('resource signature rejects unknown fields, invalid calendar dates, future, expired and excessive validity', () => {
  for (const change of [
    (e) => {
      e.extra = true;
    },
    (e) => {
      e.statement.extra = true;
    },
    (e) => {
      e.notAfter = '2026-02-30T00:00:00Z';
    },
    (e) => {
      e.notAfter = '2026-11-11T00:00:00Z';
    },
    (e) => {
      e.notAfter = '2026-11-12T01:00:00Z';
    },
    (e) => {
      e.statement.issuedAt = '2026-11-11T00:00:00.000000001Z';
    },
    (e) => {
      e.notAfter = '2026-11-11T01:00:00+00:00';
    },
  ]) {
    const f = fixture();
    change(f.envelope);
    assert.throws(() => verifyResourceArchive(f.wire(), f.archive, f.options), {
      code: 'resource.signature_invalid',
    });
  }
});
test('resource signature binds trusted expected tenant, version and legacy statement', () => {
  for (const key of [
    'tenantId',
    'packageId',
    'versionId',
    'releaseSequence',
    'statement',
  ]) {
    const f = fixture();
    f.options.expected = {
      ...f.options.expected,
      [key]:
        key === 'statement'
          ? { ...f.options.expected.statement, os: 'windows' }
          : key === 'releaseSequence'
            ? 3
            : id(9),
    };
    assert.throws(() => verifyResourceArchive(f.wire(), f.archive, f.options), {
      code: 'resource.signature_invalid',
    });
  }
});
test('resource signature never permits rollback or same-sequence byte/version substitution', () => {
  const f = fixture();
  const highWater = {
    releaseSequence: 2,
    versionId: id(3),
    digest: f.envelope.statement.digest,
  };
  assert.doesNotThrow(() =>
    verifyResourceArchive(f.wire(), f.archive, { ...f.options, highWater }),
  );
  for (const row of [
    { ...highWater, releaseSequence: 3 },
    { ...highWater, versionId: id(9) },
    { ...highWater, digest: 'sha256:' + '0'.repeat(64) },
    { ...highWater, releaseSequence: NaN },
  ])
    assert.throws(
      () =>
        verifyResourceArchive(f.wire(), f.archive, {
          ...f.options,
          highWater: row,
        }),
      { code: 'resource.signature_invalid' },
    );
});
// @ts-nocheck
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { openResourceGate } from '../../src/resources/gate.js';

test('resource gate persists tenant/package high-water across restart and never lowers it', async () => {
  const f = fixture();
  const store = createMemoryLedgerStore();
  let gate = await openResourceGate(await store.open());
  await gate.admit(f.wire(), f.archive, f.options);
  await gate.close();
  gate = await openResourceGate(await store.open());
  f.envelope.releaseSequence = 1;
  f.options.expected.releaseSequence = 1;
  await assert.rejects(gate.admit(f.wire(), f.archive, f.options), {
    code: 'resource.signature_invalid',
  });
  await gate.close();
});
test('resource gate refuses corrupt storage and fails closed after uncertain persistence', async () => {
  const f = fixture();
  const corrupt = createMemoryLedgerStore();
  corrupt.restore({ wrong: true });
  await assert.rejects(openResourceGate(await corrupt.open()));
  let commits = 0;
  const store = createMemoryLedgerStore({
    beforeCommit: () => {
      commits++;
      throw new Error('disk unavailable');
    },
  });
  const gate = await openResourceGate(await store.open());
  await assert.rejects(gate.admit(f.wire(), f.archive, f.options));
  await assert.rejects(gate.admit(f.wire(), f.archive, f.options));
  assert.equal(commits, 1);
  await gate.close();
});
test('resource signature compares legacy issuedAt as an instant while preserving signed text', () => {
  const f = fixture();
  f.options.expected.statement = {
    ...f.envelope.statement,
    issuedAt: '2026-11-10T23:00:00.000Z',
  };
  assert.doesNotThrow(() =>
    verifyResourceArchive(f.wire(), f.archive, f.options),
  );
});
test('resource signature rejects signature extensions, noncanonical encoding and algorithms', () => {
  const f = fixture();
  for (const change of [
    (s) => {
      s.extra = 1;
    },
    (s) => {
      s.value += '=';
    },
    (s) => {
      s.alg = 'RSA';
    },
    (s) => {
      s.kid = '../key';
    },
  ]) {
    const e = JSON.parse(f.wire());
    change(e.signature);
    assert.throws(
      () =>
        verifyResourceArchive(
          Buffer.from(JSON.stringify(e)),
          f.archive,
          f.options,
        ),
      { code: 'resource.signature_invalid' },
    );
  }
});
test('resource gate uses the production file store across close/reopen and excludes concurrent writers', async () => {
  const { mkdtemp, rm } = await import('node:fs/promises');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { createFileLedgerStore } =
    await import('../../src/network/host/store.js');
  const dir = await mkdtemp(join(tmpdir(), 'auriga-resource-'));
  let gate;
  try {
    const store = createFileLedgerStore({ dir });
    gate = await openResourceGate(await store.open());
    await assert.rejects(store.open());
    const f = fixture();
    await gate.admit(f.wire(), f.archive, f.options);
    await gate.close();
    gate = await openResourceGate(await store.open());
    f.envelope.releaseSequence = 1;
    f.options.expected.releaseSequence = 1;
    await assert.rejects(gate.admit(f.wire(), f.archive, f.options), {
      code: 'resource.signature_invalid',
    });
  } finally {
    await gate?.close();
    await rm(dir, { recursive: true, force: true });
  }
});
test('resource JCS accepts alternate safe-integer JSON spellings', () => {
  const f = fixture();
  const wire = Buffer.from(
    f
      .wire()
      .toString()
      .replace('"schemaVersion":1', '"schemaVersion":1.0')
      .replace('"releaseSequence":2', '"releaseSequence":2e0'),
  );
  assert.doesNotThrow(() => verifyResourceArchive(wire, f.archive, f.options));
});
