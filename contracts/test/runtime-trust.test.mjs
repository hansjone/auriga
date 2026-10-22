import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash, createPublicKey, verify } from 'node:crypto';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const read = (name) =>
  JSON.parse(
    readFileSync(new URL('../runtime/' + name, import.meta.url), 'utf8'),
  );
const fixtures = read('signature-vectors.json');
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
const runtimeSchema = ajv.compile(read('release.schema.json'));
const standaloneSchema = ajv.compile(read('standalone.schema.json'));
const clone = structuredClone;
const canon = (v) => {
  if (typeof v === 'string') assert(v.isWellFormed(), 'invalid Unicode');
  if (typeof v === 'number')
    assert(Number.isSafeInteger(v) && v >= 0, 'unsafe integer');
  if (typeof v !== 'object' || v === null) return JSON.stringify(v);
  return (
    '{' +
    Object.keys(v)
      .sort()
      .map((k) => {
        assert(k.isWellFormed());
        return JSON.stringify(k) + ':' + canon(v[k]);
      })
      .join(',') +
    '}'
  );
};
const hash = (s) => createHash('sha256').update(s).digest('hex');
const publicKey = (spki) =>
  createPublicKey({
    key: Buffer.from(spki, 'base64url'),
    type: 'spki',
    format: 'der',
  });
const keyFromRaw = (raw) =>
  createPublicKey({
    key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), raw]),
    type: 'spki',
    format: 'der',
  });
function rawBase64(value, length) {
  assert.equal(typeof value, 'string');
  const raw = Buffer.from(value, 'base64url');
  assert.equal(raw.length, length);
  assert.equal(raw.toString('base64url'), value);
  return raw;
}

test('同完整正文由另一发行key重签，序号与正文摘要保持一致', () => {
  const resigned = fixtures.resigned;
  assert.equal(resigned.canonical, fixtures.cases[0].canonical);
  assert.notEqual(resigned.publicKeySpki, fixtures.cases[0].publicKeySpki);
  assert.equal(hash(resigned.canonical), fixtures.cases[0].sha256);
  assert(
    verify(
      null,
      Buffer.from(resigned.canonical),
      publicKey(resigned.publicKeySpki),
      rawBase64(resigned.signature, 64),
    ),
  );
});

for (const vector of fixtures.cases)
  test('Java签名/JCS/完整正文 ' + vector.name, () => {
    const { signature: nested, ...body } = vector.document;
    const signature = nested?.value ?? vector.signature;
    const validate =
      vector.schema === 'runtime' ? runtimeSchema : standaloneSchema;
    assert.equal(
      validate(vector.document),
      true,
      JSON.stringify(validate.errors),
    );
    assert.equal(canon(body), vector.canonical);
    assert.equal(hash(vector.canonical), vector.sha256);
    const key = publicKey(vector.publicKeySpki);
    const sig = rawBase64(signature, 64);
    assert(verify(null, Buffer.from(canon(body)), key, sig));
    for (const field of Object.keys(body)) {
      const changed = clone(body);
      changed[field] =
        typeof changed[field] === 'number'
          ? changed[field] - 1
          : changed[field] + 'x';
      assert.equal(
        verify(null, Buffer.from(canon(changed)), key, sig),
        false,
        field,
      );
    }
    const bad = Buffer.from(sig);
    bad[0] ^= 1;
    assert.equal(verify(null, Buffer.from(canon(body)), key, bad), false);
  });

test('严格字段、必需空值、序号边界、唯一时间和规范签名编码', () => {
  const fixture = fixtures.cases.find((v) => v.name === 'runtime-0').document;
  assert.equal(fixture.archiveDigest, '');
  for (const field of Object.keys(fixture)) {
    const missing = clone(fixture);
    delete missing[field];
    assert.equal(runtimeSchema(missing), false, field);
    const nil = clone(fixture);
    nil[field] = null;
    assert.equal(runtimeSchema(nil), false, field + '=null');
  }
  for (const mutate of [
    (v) => (v.unknown = 1),
    (v) => (v.signature.unknown = 1),
    (v) => (v.schemaVersion = 2),
    (v) => (v.purpose = 'plugin'),
    (v) => (v.name = 'other'),
    (v) => (v.os = 'any'),
    (v) => (v.arch = 'any'),
    (v) => (v.artifactId = ''),
    (v) => (v.dshVersion = ''),
    (v) => (v.archiveDigest = 'sha256:' + 'A'.repeat(64)),
    (v) => (v.imageDigest = ''),
    (v) => (v.releaseSequence = 0),
    (v) => (v.releaseSequence = -1),
    (v) => (v.releaseSequence = 1.5),
    (v) => (v.releaseSequence = '7'),
    (v) => (v.releaseSequence = 9007199254740992),
    (v) => (v.issuedAt = '2026-10-23T00:00:00.100Z'),
    (v) => (v.issuedAt = '2026-10-23T00:00:00+00:00'),
    (v) => (v.issuedAt = '2026-02-30T00:00:00Z'),
    (v) => (v.issuedAt = '0000-01-01T00:00:00Z'),
    (v) => (v.issuedAt = '2026-12-31T23:59:60Z'),
    (v) => (v.signature.value += '='),
    (v) => (v.signature.value = v.signature.value.slice(0, -1) + 'B'),
    (v) => (v.signature.kid = 'bad/kid'),
    (v) => (v.signature.alg = 'EdDSA'),
  ]) {
    const changed = clone(fixture);
    mutate(changed);
    assert.equal(runtimeSchema(changed), false, JSON.stringify(changed));
  }
  for (const n of [1, 9007199254740991])
    assert(runtimeSchema({ ...fixture, releaseSequence: n }));
  const old = fixtures.cases.find((v) => v.schema === 'standalone').document;
  for (const field of Object.keys(old)) {
    const missing = clone(old);
    delete missing[field];
    assert.equal(standaloneSchema(missing), false);
  }
  assert.equal(standaloneSchema({ ...old, releaseSequence: 7 }), false);
  assert.throws(() => canon({ artifactId: '\ud800' }));
  assert.equal(
    canon({ '\ue000': 'e\u0301', '😀': 'é' }),
    '{"😀":"é","\ue000":"é"}',
  );
});

// Contract reference checks only: no Engine calls, node keyring, disk state or production CAS.
function mapping(envelope, dsh, mode, target, manifest) {
  assert(runtimeSchema(envelope));
  assert.equal(envelope.artifactId, dsh.artifactId);
  assert.equal(envelope.dshVersion, dsh.version);
  assert.equal(envelope.archiveDigest, dsh.digest);
  assert.equal(envelope.os, target.os);
  assert.equal(envelope.arch, target.arch);
  if (mode === 'docker') {
    assert.match(dsh.image, /^[^@\s]+@sha256:[0-9a-f]{64}$/);
    assert.equal(
      dsh.image.slice(dsh.image.lastIndexOf('@') + 1),
      envelope.imageDigest,
    );
    assert.equal(manifest.kind, 'manifest');
    assert.equal(manifest.os, envelope.os);
    assert.equal(manifest.arch, envelope.arch);
    if (!envelope.archiveDigest) assert.equal(dsh.url, '');
  } else {
    assert.equal(dsh.image, '');
    assert.equal(envelope.imageDigest, '');
    assert(envelope.archiveDigest);
  }
  if (dsh.signed) {
    assert(envelope.archiveDigest);
    const old = dsh.signed;
    assert(standaloneSchema(old.document));
    assert(
      verify(
        null,
        Buffer.from(canon(old.document)),
        publicKey(old.publicKeySpki),
        rawBase64(old.signature, 64),
      ),
    );
    for (const [oldName, newName] of Object.entries({
      purpose: 'purpose',
      name: 'name',
      version: 'dshVersion',
      os: 'os',
      arch: 'arch',
      digest: 'archiveDigest',
      issuedAt: 'issuedAt',
    }))
      assert.equal(old.document[oldName], envelope[newName]);
  }
}

test('驱动映射向量：image换而tar不变、平台/tag/index/native混用均拒绝', () => {
  const env = fixtures.cases.find((v) => v.name === 'runtime-1').document;
  const dsh = {
    artifactId: env.artifactId,
    version: env.dshVersion,
    digest: env.archiveDigest,
    url: 'https://artifacts.example.com/runtime.tar',
    image: 'registry.example.com/dsh@' + env.imageDigest,
    signed: fixtures.cases.find((v) => v.name === 'standalone-1'),
  };
  const target = { os: env.os, arch: env.arch };
  const manifest = { ...target, kind: 'manifest' };
  mapping(env, dsh, 'docker', target, manifest);
  for (const mutate of [
    (d) => (d.image = 'registry.example.com/dsh@sha256:' + 'c'.repeat(64)),
    (d) => (d.image = 'registry.example.com/dsh:latest'),
    (d) => (d.digest = ''),
    (d) => (d.version = 'other'),
    (d) => (d.artifactId += 'x'),
    (d) => (d.signed.signature = Buffer.alloc(64).toString('base64url')),
    (d) => (d.signed.document.issuedAt = '2026-10-23T00:00:00Z'),
  ]) {
    const changed = clone(dsh);
    mutate(changed);
    assert.throws(() => mapping(env, changed, 'docker', target, manifest));
  }
  for (const changed of [
    { ...target, os: 'windows' },
    { ...target, arch: 'arm64' },
  ])
    assert.throws(() => mapping(env, dsh, 'docker', changed, manifest));
  for (const changed of [
    { ...manifest, kind: 'index' },
    { ...manifest, os: 'windows' },
    { ...manifest, arch: 'arm64' },
  ])
    assert.throws(() => mapping(env, dsh, 'docker', target, changed));
  const only = fixtures.cases.find((v) => v.name === 'runtime-0').document;
  mapping(
    only,
    {
      artifactId: only.artifactId,
      version: only.dshVersion,
      digest: '',
      url: '',
      image: 'registry.example.com/dsh@' + only.imageDigest,
    },
    'docker',
    target,
    manifest,
  );
  assert.throws(() => mapping(env, dsh, 'native', target));
  const native = fixtures.cases.find((v) => v.name === 'runtime-6').document;
  const nativeDsh = {
    artifactId: native.artifactId,
    version: native.dshVersion,
    digest: native.archiveDigest,
    image: '',
  };
  mapping(native, nativeDsh, 'native', { os: native.os, arch: native.arch });
  assert.throws(() =>
    mapping(native, { ...nativeDsh, image: dsh.image }, 'native', {
      os: native.os,
      arch: native.arch,
    }),
  );
});

test('参考Timestamp严格校验全范围并精确保留纳秒', () => {
  for (const [value, expected] of [
    ['0001-01-01T00:00:00Z', -62135596800000000000n],
    ['9999-12-31T23:59:59.999999999Z', 253402300799999999999n],
    ['1970-01-01T00:00:00Z', 0n],
    ['1969-12-31T23:59:59.999999999Z', -1n],
    ['1970-01-01T00:00:00.000000001Z', 1n],
    ['1970-01-01T00:00:00.1Z', 100000000n],
    ['1970-01-01T00:00:00.123Z', 123000000n],
    ['1970-01-01T00:00:00.123456Z', 123456000n],
    ['1970-01-01T00:00:00.123456789Z', 123456789n],
    ['2000-02-29T00:00:00Z', 951782400000000000n],
  ])
    assert.equal(timestampNanos(value), expected, value);
  for (const value of [
    null,
    undefined,
    0,
    '',
    'invalid',
    '0000-01-01T00:00:00Z',
    '10000-01-01T00:00:00Z',
    '1900-02-29T00:00:00Z',
    '2026-02-30T00:00:00Z',
    '2026-00-01T00:00:00Z',
    '2026-13-01T00:00:00Z',
    '2026-01-00T00:00:00Z',
    '2026-04-31T00:00:00Z',
    '2026-01-01T24:00:00Z',
    '2026-01-01T00:60:00Z',
    '2026-01-01T00:00:60Z',
    '2026-01-01T00:00:00.0000000001Z',
    '2026-01-01T00:00:00.Z',
    '2026-01-01T00:00:00+00:00',
    '2026-01-01T00:00:00Z\n',
  ])
    assert.throws(() => timestampNanos(value), String(value));
});

const records = Object.fromEntries(fixtures.keys.map((k) => [k.kid, k]));
function record(k) {
  const { kid, publicKey, notAfter, endorsement, endorsedByKid } = k;
  return { kid, publicKey, notAfter, endorsement, endorsedByKid };
}
// Contract reference only: Gregorian UTC seconds and nanos, never millisecond Date parsing.
function timestampNanos(value) {
  assert.equal(typeof value, 'string', 'invalid Timestamp');
  const parts =
    /^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,9}))?Z$/.exec(
      value,
    );
  assert(parts && parts[0] === value, 'invalid Timestamp');
  const [year, month, day, hour, minute, second] = parts
    .slice(1, 7)
    .map(Number);
  assert(
    year >= 1 && year <= 9999 && month >= 1 && month <= 12,
    'invalid Timestamp',
  );
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const months = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  assert(
    day >= 1 &&
      day <= months[month - 1] &&
      hour < 24 &&
      minute < 60 &&
      second < 60,
    'invalid Timestamp',
  );
  const previousYears = BigInt(year - 1);
  const days =
    365n * previousYears +
    previousYears / 4n -
    previousYears / 100n +
    previousYears / 400n +
    BigInt(
      months.slice(0, month - 1).reduce((sum, days) => sum + days, 0) + day - 1,
    ) -
    719162n;
  const seconds = days * 86400n + BigInt(hour * 3600 + minute * 60 + second);
  const nanos = BigInt((parts[7] ?? '').padEnd(9, '0'));
  return seconds * 1000000000n + nanos;
}
function evaluate(scenario) {
  const now = timestampNanos(scenario.now);
  const old = new Map(
    scenario.initial.map((name) => [
      name,
      {
        ...record(records[name]),
        localNotAfter:
          scenario.initialDeadlines?.[name] ?? records[name].notAfter,
      },
    ]),
  );
  const incoming = scenario.incoming.map((name) => record(records[name]));
  for (const patch of scenario.patches ?? [])
    incoming[patch.index][patch.field] = patch.value;
  const candidates = new Map();
  for (const key of incoming) {
    assert.match(key.kid, /^[A-Za-z0-9._-]{1,128}$/);
    rawBase64(key.publicKey, 32);
    assert(timestampNanos(key.notAfter) > now);
    assert(!candidates.has(key.kid), 'duplicate kid');
    candidates.set(key.kid, key);
    if (old.has(key.kid))
      assert.deepEqual(key, record(old.get(key.kid)), 'immutable kid');
    assert.equal(
      key.endorsement === '',
      key.endorsedByKid === '',
      'half endorsement',
    );
  }
  const merged = new Map(old);
  const active = new Set();
  let roots = 0;
  function resolve(kid) {
    assert(!active.has(kid), 'cycle');
    if (merged.has(kid)) {
      assert(
        timestampNanos(merged.get(kid).localNotAfter) > now,
        'expired parent',
      );
      return merged.get(kid);
    }
    const key = candidates.get(kid);
    assert(key, 'unknown parent');
    active.add(kid);
    let localNotAfter = key.notAfter;
    if (!key.endorsedByKid) {
      assert(scenario.bootstrap, 'new root');
      roots++;
    } else {
      const parent = resolve(key.endorsedByKid);
      const raw = rawBase64(key.publicKey, 32);
      const bytes = Buffer.concat([Buffer.from(key.kid, 'utf8'), raw]);
      assert(
        verify(
          null,
          bytes,
          keyFromRaw(rawBase64(parent.publicKey, 32)),
          rawBase64(key.endorsement, 64),
        ),
        'endorsement',
      );
      localNotAfter =
        timestampNanos(parent.localNotAfter) < timestampNanos(key.notAfter)
          ? parent.localNotAfter
          : key.notAfter;
    }
    const accepted = { ...key, localNotAfter };
    active.delete(kid);
    merged.set(kid, accepted);
    return accepted;
  }
  for (const key of incoming) resolve(key.kid);
  if (scenario.bootstrap) assert(roots > 0, 'missing root');
  return Object.fromEntries(
    [...merged].map(([kid, value]) => [kid, value.localNotAfter]),
  );
}

for (const scenario of read('keyring-scenarios.json').cases)
  test('背书/信任契约参考向量 ' + scenario.name, () => {
    if (scenario.expected === 'reject') assert.throws(() => evaluate(scenario));
    else assert.deepEqual(evaluate(scenario), scenario.deadlines);
  });

for (const key of fixtures.keys.filter((k) => k.endorsement))
  test('背书v1精确字节 ' + key.kid, () => {
    const input = Buffer.concat([
      Buffer.from(key.kid),
      rawBase64(key.publicKey, 32),
    ]);
    assert.equal(input.length, key.inputLength);
    assert.equal(input.toString('hex'), key.inputHex);
    const sig = rawBase64(key.endorsement, 64);
    const parent = publicKey(key.parentPublicKeySpki);
    assert(verify(null, input, parent, sig));
    for (const bytes of [
      Buffer.concat([Buffer.from('auriga/v1/'), input]),
      Buffer.concat([
        Buffer.from(key.kid + '\0'),
        rawBase64(key.publicKey, 32),
      ]),
      Buffer.from(input.toString('hex')),
    ])
      assert.equal(verify(null, bytes, parent, sig), false);
    assert.equal(verify(null, input, publicKey(key.publicKeySpki), sig), false);
  });
