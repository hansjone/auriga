// 合成向量：密钥每次运行在内存中生成，输出仅含公钥/签名，无私钥。
import {
  createHash,
  createPrivateKey,
  createPublicKey,
  sign,
} from 'node:crypto';
import { writeFileSync, readFileSync } from 'node:fs';
import { jcs, digest, purposes } from './msgnet-v2-oracle.mjs';
import { extendVectors } from './msgnet-v2-extra-vectors.mjs';
let counter = 0;
// 明示合成种子，绝不可用于安装/生产；私钥只在内存构造。
function generateKeyPairSync() {
  const seed = createHash('sha256')
    .update('Auriga synthetic MSGNET v2 test only ' + counter++)
    .digest();
  const privateKey = createPrivateKey({
    key: Buffer.concat([
      Buffer.from('302e020100300506032b657004220420', 'hex'),
      seed,
    ]),
    format: 'der',
    type: 'pkcs8',
  });
  return { privateKey, publicKey: createPublicKey(privateKey) };
}
const root = generateKeyPairSync('ed25519'),
  runtime = generateKeyPairSync('ed25519');
const pub = (k) =>
  k.publicKey.export({ format: 'der', type: 'spki' }).toString('base64url');
const auth = (payload, domain, key, kid, root = false) => ({
  payload,
  [root ? 'root_auth' : 'runtime_auth']: {
    alg: 'Ed25519',
    kid,
    signature: sign(
      null,
      Buffer.from(domain + '\0' + jcs(payload)),
      key.privateKey,
    ).toString('base64url'),
  },
});
const scope = { schema_version: 2, tenant_id: 'synthetic-tenant', realm: '1' },
  at = '2026-11-14T00:00:00Z',
  end = '2026-11-15T00:00:00Z';
const authority = auth(
  {
    ...scope,
    authority_id: 'synthetic-authority',
    authority_revision: '1',
    runtime_kid: 'synthetic-runtime',
    public_key_spki: pub(runtime),
    not_before: '2026-01-01T00:00:00Z',
    expiry_policy: 'no_hard_expiry',
    review_period_days: 365,
    capabilities: [
      'keyset',
      'announcement',
      'business_rotation',
      'restart_recovery',
      'history_archive',
      'network_transport',
    ],
    limits: {
      keyset_seconds: 86400,
      business_key_seconds: 691200,
      restart_key_seconds: 604800,
      next_maturity_seconds: 86400,
      overlap_seconds: 360,
      archive_grace_seconds: 360,
    },
    transport_scope: [
      {
        component: 'network',
        identity_id: 'synthetic-projector',
        sans: ['submit.example.com'],
        ekus: ['serverAuth'],
      },
    ],
  },
  'Auriga.NetworkRuntimeAuthority.v2',
  root,
  'synthetic-root',
  true,
);
const control = auth(
  {
    ...scope,
    control_revision: '2',
    issued_at: at,
    allowed_authorities: [
      {
        authority_id: authority.payload.authority_id,
        authority_digest: digest(authority),
      },
    ],
    revoked_authority_ids: [],
    revoked_installation_ids: [],
    minimum_generation: '1',
    minimum_keyset_version: '1',
  },
  'Auriga.NetworkRootControl.v2',
  root,
  'synthetic-root',
  true,
);
const binding = {
  ...scope,
  authority_id: authority.payload.authority_id,
  authority_digest: digest(authority),
  control_revision: '2',
  generation: '1',
};
const keys = purposes.map((purpose, n) => ({
  kid: 'synthetic-business-' + n,
  alg: 'Ed25519',
  purpose,
  state: 'active',
  not_before: '2026-11-12T00:00:00Z',
  not_after: '2026-11-19T00:00:00Z',
  public_key_spki: pub(generateKeyPairSync('ed25519')),
}));
const base = {
  ...binding,
  keyset_version: '10',
  issued_at: at,
  valid_until: end,
  mode: 'refresh',
  keys,
  first_publications: keys.map((k) => ({
    kid: k.kid,
    committed_version: '1',
    committed_at: '2026-11-12T00:00:00Z',
  })),
  archive: {
    before: '2026-11-11T00:00:00Z',
    history_digest: 'sha256:' + '0'.repeat(64),
  },
};
const signed = (k) =>
  auth(k, 'Auriga.NetworkKeyset.v2', runtime, 'synthetic-runtime');
const previous = signed({
  ...base,
  keyset_version: '9',
  issued_at: '2026-11-13T00:00:00Z',
  valid_until: at,
});
const input = {
  root_spki: pub(root),
  tenant_id: scope.tenant_id,
  realm: '1',
  now: at,
  latest_control: control,
  seen_control_revision: '2',
  seen_generation: '1',
  seen_version: '9',
  seen_digest: digest(previous),
  previous_digest: digest(previous),
  first_publication_ledger: base.first_publications,
  publication: {
    keyset: signed(base),
    authority,
    control,
    predecessor: {
      keyset: previous,
      keyset_digest: digest(previous),
      authority,
      control,
    },
  },
};
const cases = [];
function add(id, expected, change = () => {}, resign = true) {
  const i = JSON.parse(JSON.stringify(input));
  change(i);
  if (resign) {
    const k = i.publication.keyset.payload;
    for (const key of k.keys.filter(
      (x) =>
        !i.publication.predecessor?.keyset.payload.keys.some(
          (y) => y.kid === x.kid,
        ),
    )) {
      if (!k.first_publications.some((x) => x.kid === key.kid))
        k.first_publications.push({
          kid: key.kid,
          committed_version: k.keyset_version,
          committed_at: k.issued_at,
        });
    }
    i.publication.keyset = signed(i.publication.keyset.payload);
    i.latest_control = auth(
      i.latest_control.payload,
      'Auriga.NetworkRootControl.v2',
      root,
      'synthetic-root',
      true,
    );
  }
  cases.push({ id, expected, input: i });
}
add('refresh-valid', 'accept');
add('review-past-365-days-still-authorized', 'accept', (i) => {
  const shift = (x) => {
    for (const [name, value] of Object.entries(x)) {
      if (value && typeof value === 'object') shift(value);
      else if (typeof value === 'string' && /^2026-.*Z$/.test(value))
        x[name] = new Date(Date.parse(value) + 400 * 86400000)
          .toISOString()
          .replace('.000Z', 'Z');
    }
  };
  shift(i.publication.keyset.payload);
  shift(i.publication.predecessor.keyset.payload);
  shift(i.first_publication_ledger);
  i.now = i.publication.keyset.payload.issued_at;
  i.publication.predecessor.keyset = signed(
    i.publication.predecessor.keyset.payload,
  );
  i.previous_digest = i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
});
add(
  'control-ahead-old-authority-still-allowed',
  'accept',
  (i) => (i.latest_control.payload.control_revision = '3'),
);
add(
  'revoked-current',
  'authority_revoked',
  (i) =>
    (i.latest_control.payload.revoked_authority_ids = ['synthetic-authority']),
);
add('revoked-arbitrarily-high-version', 'authority_revoked', (i) => {
  i.latest_control.payload.revoked_authority_ids = ['synthetic-authority'];
  i.publication.keyset.payload.keyset_version = '18446744073709551615';
});
add(
  'presigned-authority-not-allowed',
  'authority_unknown',
  (i) => (i.latest_control.payload.allowed_authorities = []),
);
add(
  'binding-requires-new-control',
  'control_required',
  (i) => (i.latest_control.payload.control_revision = '1'),
);
add(
  'local-control-rollback',
  'control_rollback',
  (i) => (i.seen_control_revision = '3'),
);
add('version-rollback', 'version_rollback', (i) => (i.seen_version = '11'));
add(
  'generation-rollback',
  'version_rollback',
  (i) => (i.seen_generation = '2'),
);
add(
  'same-version-changed-bytes',
  'same_version_conflict',
  (i) => (i.seen_version = '10'),
);
add(
  'wrong-realm',
  'scope_denied',
  (i) => (i.publication.keyset.payload.realm = '2'),
);
add(
  'wrong-tenant',
  'scope_denied',
  (i) => (i.publication.keyset.payload.tenant_id = 'another-tenant'),
);
add(
  'runtime-kid-mismatch',
  'scope_denied',
  (i) => (i.publication.keyset.runtime_auth.kid = 'business-key'),
  false,
);
add(
  'tampered-runtime-signature',
  'invalid_chain',
  (i) => (i.publication.keyset.payload.valid_until = '2026-11-14T23:00:00Z'),
  false,
);
add(
  'future-issued-at',
  'invalid_transition',
  (i) => (i.publication.keyset.payload.issued_at = '2026-11-15T00:00:00Z'),
);
add(
  'over-24h',
  'invalid_transition',
  (i) => (i.publication.keyset.payload.valid_until = '2026-11-16T00:00:00Z'),
);
add(
  'missing-predecessor',
  'predecessor_required',
  (i) => delete i.publication.predecessor,
);
add(
  'predecessor-digest-conflict',
  'invalid_transition',
  (i) => (i.publication.predecessor.keyset_digest = 'sha256:' + 'f'.repeat(64)),
);
add(
  'unnecessary-restart',
  'invalid_transition',
  (i) => (i.publication.keyset.payload.mode = 'restart_recovery'),
);
add(
  'clock-forward-day',
  'clock_error',
  (i) => (i.clock = { pg_skew: 0, wall_delta: 86400, monotonic_delta: 30 }),
);
add(
  'clock-forward-five-years',
  'clock_error',
  (i) => (i.clock = { pg_skew: 0, wall_delta: 157680000, monotonic_delta: 30 }),
);
add(
  'clock-backward',
  'clock_error',
  (i) => (i.clock = { pg_skew: 0, wall_delta: -31, monotonic_delta: 30 }),
);
add(
  'clock-pg-skew',
  'clock_error',
  (i) => (i.clock = { pg_skew: 61, wall_delta: 30, monotonic_delta: 30 }),
);
add(
  'clock-boundaries',
  'accept',
  (i) => (i.clock = { pg_skew: 60, wall_delta: 330, monotonic_delta: 30 }),
);
function restart(i) {
  const k = i.publication.keyset.payload;
  k.mode = 'restart_recovery';
  const old = i.publication.predecessor.keyset.payload;
  old.keys.forEach((x) => (x.not_before = '2026-11-15T00:00:00Z'));
  old.keys.forEach((x) => (x.not_after = '2026-11-20T00:00:00Z'));
  i.publication.predecessor.keyset = signed(old);
  i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
  i.previous_digest = i.publication.predecessor.keyset_digest;
  k.keys = keys.map((x, n) => ({
    ...x,
    kid: 'synthetic-restart-' + n,
    public_key_spki: pub(generateKeyPairSync('ed25519')),
    not_before: at,
    not_after: '2026-11-21T00:00:00Z',
  }));
  k.keys.push(...old.keys.map((x) => ({ ...x, state: 'retired' })));
}
add('restart-future-active-after-clock-correction', 'accept', restart);
add('restart-over-seven-days', 'invalid_transition', (i) => {
  restart(i);
  i.publication.keyset.payload.keys[0].not_after = '2026-11-21T00:00:01Z';
});
add('restart-missing-purpose', 'invalid_transition', (i) => {
  restart(i);
  i.publication.keyset.payload.keys.shift();
});
add('restart-earlier-issued-higher-version', 'accept', (i) => {
  restart(i);
  i.publication.predecessor.keyset.payload.issued_at = '2026-11-15T00:00:00Z';
  i.publication.predecessor.keyset.payload.valid_until = '2026-11-16T00:00:00Z';
  i.publication.predecessor.keyset = signed(
    i.publication.predecessor.keyset.payload,
  );
  i.previous_digest = i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
});
function activation(i) {
  const p = i.publication.predecessor.keyset.payload;
  const k = i.publication.keyset.payload;
  k.mode = 'activate';
  p.keys = p.keys.map((x) => ({ ...x, not_after: '2026-11-14T00:10:00Z' }));
  const next = p.keys.map((x, n) => ({
    ...x,
    kid: 'synthetic-next-' + n,
    state: 'next',
    not_before: '2026-11-13T00:00:00Z',
    not_after: '2026-11-20T00:00:00Z',
    public_key_spki: pub(generateKeyPairSync('ed25519')),
  }));
  p.keys.push(...next);
  p.first_publications.push(
    ...next.map((x) => ({
      kid: x.kid,
      committed_version: '8',
      committed_at: '2026-11-12T00:00:00Z',
    })),
  );
  i.publication.predecessor.keyset = signed(p);
  i.previous_digest = i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
  k.keys = p.keys.map((x) => ({
    ...x,
    state: x.state === 'next' ? 'active' : 'retired',
  }));
  k.first_publications = p.first_publications;
  i.first_publication_ledger = structuredClone(p.first_publications);
}
add('mature-next-activation', 'accept', activation);
add('mature-next-restart-unnecessary', 'invalid_transition', (i) => {
  activation(i);
  i.publication.keyset.payload.mode = 'restart_recovery';
});
add('immature-next-activation-denied', 'invalid_transition', (i) => {
  activation(i);
  const q = i.publication.predecessor.keyset.payload;
  q.first_publications.forEach((x) => (x.committed_at = at));
  i.publication.predecessor.keyset = signed(q);
  i.previous_digest = i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
});
add('normal-activation-overlap-insufficient', 'invalid_transition', (i) => {
  activation(i);
  const q = i.publication.predecessor.keyset.payload;
  q.keys
    .filter((x) => x.state === 'active')
    .forEach((x) => (x.not_after = '2026-11-14T00:05:59Z'));
  i.publication.keyset.payload.keys
    .filter((x) => x.state === 'retired')
    .forEach((x) => (x.not_after = '2026-11-14T00:05:59Z'));
  i.publication.predecessor.keyset = signed(q);
  i.previous_digest = i.publication.predecessor.keyset_digest = digest(
    i.publication.predecessor.keyset,
  );
});
add('refresh-cannot-invent-active', 'invalid_transition', (i) => {
  activation(i);
  i.publication.keyset.payload.mode = 'refresh';
});
const certificates = JSON.parse(
  readFileSync(new URL('msgnet-transport-public.json', import.meta.url)),
);
const transport = (i) => {
  i.seen_bundle_version = '1';
  i.transport = auth(
    {
      ...binding,
      bundle_version: '2',
      issued_at: at,
      phase: 'dual_trust',
      cas: certificates.map((x) => ({
        ca_id: x.ca_id,
        role: 'server',
        certificate_der: x.ca_der,
        not_before: '2026-11-13T23:55:00Z',
        not_after: '2036-11-14T00:00:00Z',
      })),
      scope: structuredClone(authority.payload.transport_scope),
      required_reader_ids: ['synthetic-projector'],
      previous_bundle_digest: 'sha256:' + '0'.repeat(64),
    },
    'Auriga.NetworkTransportTrustBundle.v2',
    runtime,
    'synthetic-runtime',
  );
};
add('internal-ca-trust-renewal-chain', 'accept', transport);
add('internal-ca-trust-revoked-signer', 'authority_revoked', (i) => {
  transport(i);
  i.latest_control.payload.revoked_authority_ids = ['synthetic-authority'];
});
add('internal-ca-trust-rollback', 'version_rollback', (i) => {
  transport(i);
  i.seen_bundle_version = '2';
});
const signatures = [];
const issuer_cases = [];
for (let n = cases.length - 1; n >= 0; n--)
  if (cases[n].input.clock) {
    const v = cases.splice(n, 1)[0];
    issuer_cases.unshift({
      id: v.id,
      expected: v.expected,
      input: { clock: v.input.clock, write_first_publication: false },
    });
  }
const boundary_cases = [];
extendVectors({
  cases,
  issuer_cases,
  boundary_cases,
  add,
  input,
  root,
  runtime,
  auth,
  pub,
  signed,
  transport,
  digest,
  jcs,
  generateKeyPairSync,
});
for (const v of cases)
  for (const [field, domain] of [
    ['keyset', 'Auriga.NetworkKeyset.v2'],
    ['authority', 'Auriga.NetworkRuntimeAuthority.v2'],
    ['control', 'Auriga.NetworkRootControl.v2'],
  ]) {
    const s = v.input.publication[field];
    signatures.push({
      id: v.id + '-' + field,
      domain,
      canonical: jcs(s.payload),
      public_key_spki:
        field === 'keyset'
          ? v.input.publication.authority.payload.public_key_spki
          : pub(root),
      signature: (s.root_auth ?? s.runtime_auth).signature,
      valid: !(v.id === 'tampered-runtime-signature' && field === 'keyset'),
    });
  }
for (const v of cases)
  if (v.input.transport) {
    const t = v.input.transport;
    signatures.push({
      id: v.id + '-bundle',
      domain: 'Auriga.NetworkTransportTrustBundle.v2',
      canonical: jcs(t.payload),
      public_key_spki: pub(runtime),
      signature: t.runtime_auth.signature,
      valid: true,
    });
  }
const serialized =
  JSON.stringify(
    {
      synthetic: true,
      producer: 'Node Ed25519 runtime generated; no private keys persisted',
      cases,
      issuer_cases,
      boundary_cases,
      signatures,
      certificates,
    },
    null,
    2,
  ) + '\n';
const destination = new URL('auto-renewal-v2-vectors.json', import.meta.url);
if (process.argv.includes('--check')) {
  if (readFileSync(destination, 'utf8') !== serialized)
    throw Error('v2 vectors drift; regenerate with documented commands');
} else writeFileSync(destination, serialized);
