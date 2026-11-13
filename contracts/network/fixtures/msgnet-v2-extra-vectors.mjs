import { sealTest } from './msgnet-v2-hpke.mjs';
import { extendRoundThree } from './msgnet-v2-r3-vectors.mjs';
export function extendVectors(c) {
  const {
    add,
    cases,
    boundary_cases: b,
    issuer_cases,
    root,
    runtime,
    auth,
    pub,
    signed,
    input,
    digest,
    jcs,
    transport,
    generateKeyPairSync,
  } = c;
  const clone = (x) => JSON.parse(JSON.stringify(x)),
    last = () => cases.at(-1).input;
  const rootSign = (p, d) => auth(p, d, root, 'synthetic-root', true);
  const signControl = (i) => {
    i.latest_control = rootSign(
      i.latest_control.payload,
      'Auriga.NetworkRootControl.v2',
    );
  };
  const signCurrent = (i) => {
    i.publication.keyset = signed(i.publication.keyset.payload);
  };
  const authorityPatch = (id, expected, patch) => {
    add(id, expected);
    const i = last(),
      p = i.publication;
    patch(p.authority.payload);
    p.authority = rootSign(
      p.authority.payload,
      'Auriga.NetworkRuntimeAuthority.v2',
    );
    p.keyset.payload.authority_digest = digest(p.authority);
    i.latest_control.payload.allowed_authorities = [
      {
        authority_id: p.authority.payload.authority_id,
        authority_digest: digest(p.authority),
      },
    ];
    signControl(i);
    signCurrent(i);
  };
  add(
    'predecessor-local-ledger-mismatch',
    'invalid_transition',
    (i) => (i.previous_digest = 'sha256:' + 'e'.repeat(64)),
  );
  authorityPatch(
    'authority-not-before-in-future',
    'scope_denied',
    (a) => (a.not_before = '2026-11-15T00:00:00Z'),
  );
  authorityPatch(
    'capability-subset-refresh',
    'accept',
    (a) => (a.capabilities = ['keyset']),
  );
  authorityPatch(
    'authority-keyset-limit-tightened-accepted',
    'accept',
    (a) => (a.limits.keyset_seconds = 86400),
  );
  authorityPatch(
    'authority-keyset-limit-tightened-rejected',
    'invalid_transition',
    (a) => (a.limits.keyset_seconds = 43200),
  );
  authorityPatch(
    'authority-business-lifetime-limit',
    'invalid_transition',
    (a) => (a.limits.business_key_seconds = 86400),
  );
  const restartDenied = clone(
    cases.find((x) => x.id === 'restart-future-active-after-clock-correction'),
  );
  restartDenied.id = 'restart-capability-must-be-root-authorized';
  restartDenied.expected = 'scope_denied';
  {
    const i = restartDenied.input,
      p = i.publication;
    p.authority.payload.capabilities = ['keyset'];
    p.authority = rootSign(
      p.authority.payload,
      'Auriga.NetworkRuntimeAuthority.v2',
    );
    p.keyset.payload.authority_digest = digest(p.authority);
    i.latest_control.payload.allowed_authorities = [
      {
        authority_id: p.authority.payload.authority_id,
        authority_digest: digest(p.authority),
      },
    ];
    signControl(i);
    signCurrent(i);
    cases.push(restartDenied);
  }
  add('first-publication-ledger-unchanged', 'accept');
  add('first-publication-rewritten', 'invalid_transition');
  {
    const i = last();
    i.publication.predecessor.keyset.payload.first_publications[0].committed_at =
      '2026-11-11T00:00:00Z';
    i.publication.predecessor.keyset = signed(
      i.publication.predecessor.keyset.payload,
    );
    i.previous_digest = i.publication.predecessor.keyset_digest = digest(
      i.publication.predecessor.keyset,
    );
    signCurrent(i);
  }
  function transition(id, expected, mutate = () => {}) {
    add(id, expected);
    const i = last(),
      p = i.publication,
      next = generateKeyPairSync('ed25519'),
      a = clone(p.authority.payload);
    a.authority_id = 'synthetic-authority-new';
    a.runtime_kid = 'synthetic-runtime-new';
    a.authority_revision = '2';
    a.public_key_spki = pub(next);
    p.authority = rootSign(a, 'Auriga.NetworkRuntimeAuthority.v2');
    Object.assign(p.keyset.payload, {
      authority_id: a.authority_id,
      authority_digest: digest(p.authority),
      generation: '2',
      keyset_version: '1',
      control_revision: '3',
      mode: 'authority_transition',
    });
    const fresh = p.keyset.payload.keys.map((x, n) => ({
      ...x,
      kid: 'synthetic-new-generation-' + n,
      public_key_spki: pub(generateKeyPairSync('ed25519')),
      not_before: p.keyset.payload.issued_at,
      not_after: '2026-11-21T00:00:00Z',
    }));
    p.keyset.payload.keys.forEach((x) => (x.state = 'revoked'));
    p.keyset.payload.keys.push(...fresh);
    p.keyset.payload.first_publications.push(
      ...fresh.map((x) => ({
        kid: x.kid,
        committed_version: '1',
        committed_at: p.keyset.payload.issued_at,
      })),
    );
    const cp = i.latest_control.payload;
    cp.control_revision = '3';
    cp.minimum_generation = '2';
    cp.minimum_keyset_version = '1';
    cp.revoked_authority_ids = ['synthetic-authority'];
    cp.allowed_authorities = [
      { authority_id: a.authority_id, authority_digest: digest(p.authority) },
    ];
    cp.generation_transition = {
      predecessor_digest: p.predecessor.keyset_digest,
      from_generation: '1',
      to_generation: '2',
      authority_id: a.authority_id,
      authority_digest: digest(p.authority),
      first_version: '1',
    };
    mutate(i);
    signControl(i);
    p.control = clone(i.latest_control);
    p.keyset = auth(
      p.keyset.payload,
      'Auriga.NetworkKeyset.v2',
      next,
      a.runtime_kid,
    );
  }
  // 第3轮在函数尾追加，保留既有确定性生成顺序。
  transition('revocation-new-authority-first-generation-snapshot', 'accept');
  transition('normal-authority-overlap-same-generation', 'accept', (i) => {
    i.publication.keyset.payload.generation = '1';
    i.publication.keyset.payload.keyset_version = '10';
    i.publication.keyset.payload.keys = clone(
      input.publication.keyset.payload.keys,
    );
    i.publication.keyset.payload.first_publications = clone(
      input.publication.keyset.payload.first_publications,
    );
    i.publication.keyset.payload.first_publications
      .filter((x) => x.kid.startsWith('synthetic-new-generation'))
      .forEach((x) => (x.committed_version = '10'));
    i.latest_control.payload.minimum_generation = '1';
    i.latest_control.payload.minimum_keyset_version = '1';
    i.latest_control.payload.revoked_authority_ids = [];
    i.latest_control.payload.allowed_authorities.push({
      authority_id: input.publication.authority.payload.authority_id,
      authority_digest: digest(input.publication.authority),
    });
    delete i.latest_control.payload.generation_transition;
  });
  transition(
    'new-generation-rollback',
    'version_rollback',
    (i) => (i.seen_generation = '3'),
  );
  transition(
    'new-generation-unanchored-predecessor',
    'invalid_transition',
    (i) =>
      (i.latest_control.payload.generation_transition.predecessor_digest =
        'sha256:' + 'f'.repeat(64)),
  );
  transition(
    'revoked-old-runtime-cannot-sign-transition',
    'authority_revoked',
    (i) =>
      i.latest_control.payload.revoked_authority_ids.push(
        'synthetic-authority-new',
      ),
  );
  transition(
    'revoked-original-authority-transition-denied',
    'authority_revoked',
  );
  {
    const i = last();
    i.publication.authority = clone(input.publication.authority);
    i.publication.keyset.payload.authority_id =
      i.publication.authority.payload.authority_id;
    i.publication.keyset.payload.authority_digest = digest(
      i.publication.authority,
    );
    signCurrent(i);
  }
  // 补真实成熟激活的首发账本攻击；不能只命中快照摘要检查。
  const activation = cases.find((x) => x.id === 'mature-next-activation');
  const forged = clone(activation);
  forged.id = 'rewrite-first-publication-to-skip-maturity';
  forged.expected = 'invalid_transition';
  forged.input.first_publication_ledger
    .filter((x) => x.kid.startsWith('synthetic-next'))
    .forEach((x) => (x.committed_at = '2026-11-13T23:00:00Z'));
  cases.push(forged);
  const newKidVersion = clone(
    cases.find((x) => x.id === 'restart-future-active-after-clock-correction'),
  );
  newKidVersion.id = 'new-kid-first-version-must-equal-publication';
  newKidVersion.expected = 'invalid_transition';
  {
    const i = newKidVersion.input;
    i.publication.keyset.payload.first_publications.find((x) =>
      x.kid.startsWith('synthetic-restart'),
    ).committed_version = '9';
    signCurrent(i);
    cases.push(newKidVersion);
  }
  for (const [id, expected, clock, write, stable] of [
    [
      'clock-error-first-publication-not-written',
      'clock_error',
      { pg_skew: 61, wall_delta: 30, monotonic_delta: 30 },
      false,
      false,
    ],
    [
      'clock-recovered-first-publication-reanchored',
      'accept',
      { pg_skew: 0, wall_delta: 30, monotonic_delta: 30 },
      true,
      true,
    ],
    [
      'clock-not-stable-cannot-publish',
      'clock_error',
      { pg_skew: 0, wall_delta: 30, monotonic_delta: 30 },
      true,
      false,
    ],
  ])
    issuer_cases.push({
      id,
      expected,
      input: {
        clock,
        write_first_publication: write,
        clock_error_persisted: true,
        stable_samples: stable,
      },
    });
  const push = (id, operation, expected, input) =>
    b.push({ id, operation, expected, input });
  const payload = clone(input.publication.keyset.payload);
  for (const [id, signDomain, verifyDomain, signer, key, expected] of [
    [
      'v2-domain-accepted',
      'Auriga.NetworkKeyset.v2',
      'Auriga.NetworkKeyset.v2',
      runtime,
      pub(runtime),
      'accept',
    ],
    [
      'v1-keyset-rejected-by-v2',
      'Auriga.NetworkKeyset.v1',
      'Auriga.NetworkKeyset.v2',
      runtime,
      pub(runtime),
      'invalid_chain',
    ],
    [
      'v2-keyset-rejected-by-v1',
      'Auriga.NetworkKeyset.v2',
      'Auriga.NetworkKeyset.v1',
      runtime,
      pub(runtime),
      'invalid_chain',
    ],
    [
      'server-envelope-rejected-by-keyset',
      'Auriga.ServerEnvelope.v1',
      'Auriga.NetworkKeyset.v2',
      runtime,
      pub(runtime),
      'invalid_chain',
    ],
    [
      'root-authority-issued-by-root',
      'Auriga.NetworkRuntimeAuthority.v2',
      'Auriga.NetworkRuntimeAuthority.v2',
      root,
      pub(root),
      'accept',
    ],
    [
      'runtime-self-renew-authority-denied',
      'Auriga.NetworkRuntimeAuthority.v2',
      'Auriga.NetworkRuntimeAuthority.v2',
      runtime,
      pub(root),
      'invalid_chain',
    ],
    [
      'runtime-root-control-delegation-denied',
      'Auriga.NetworkRootControl.v2',
      'Auriga.NetworkRootControl.v2',
      runtime,
      pub(root),
      'invalid_chain',
    ],
  ])
    push(id, 'signature_domain', expected, {
      signed: auth(
        signDomain.includes('RuntimeAuthority')
          ? input.publication.authority.payload
          : signDomain.includes('RootControl')
            ? input.latest_control.payload
            : payload,
        signDomain,
        signer,
        'synthetic-signer',
      ),
      verification_domain: verifyDomain,
      verification_spki: key,
    });
  const announcement = { ...clone(input.publication.keyset.payload) };
  for (const f of [
    'issued_at',
    'mode',
    'keys',
    'first_publications',
    'archive',
  ])
    delete announcement[f];
  announcement.keyset_digest = digest(input.publication.keyset);
  for (const [id, expected, change] of [
    ['announcement-matched-pair', 'accept', () => {}],
    [
      'announcement-higher-version-fence',
      'projection_pending',
      (i) => (i.announcement.payload.keyset_version = '11'),
    ],
    [
      'announcement-failed-persistence-fence',
      'projection_pending',
      (i) => (i.persisted = false),
    ],
    [
      'announcement-wrong-keyset-digest',
      'invalid_chain',
      (i) =>
        (i.announcement.payload.keyset_digest = 'sha256:' + 'f'.repeat(64)),
    ],
  ]) {
    const i = {
      context: clone(input),
      announcement: { payload: clone(announcement) },
      persisted: true,
    };
    change(i);
    i.announcement = auth(
      i.announcement.payload,
      'Auriga.NetworkKeysetAnnouncement.v2',
      runtime,
      'synthetic-runtime',
    );
    push(id, 'announcement', expected, i);
  }
  for (const [id, expected, change] of [
    ['transport-dual-to-leaves', 'accept', () => {}],
    [
      'transport-scope-outside-installation',
      'scope_denied',
      (i) => (i.transport.payload.scope[0].sans = ['foreign.example.com']),
    ],
    [
      'transport-stage-skipped',
      'invalid_transition',
      (i) => (i.transport.payload.phase = 'retire_old'),
    ],
    [
      'transport-unconfirmed-readers',
      'projection_pending',
      (i) => (i.readers_persisted = false),
    ],
  ]) {
    add(id, expected, (i) => {
      transport(i);
      i.previous_transport_phase = 'dual_trust';
      i.readers_persisted = true;
      i.transport.payload.phase = 'new_leaves';
      change(i);
      i.transport = auth(
        i.transport.payload,
        'Auriga.NetworkTransportTrustBundle.v2',
        runtime,
        'synthetic-runtime',
      );
    });
  }
  authorityPatch(
    'network-transport-capability-absent',
    'scope_denied',
    (a) => (a.capabilities = ['keyset']),
  );
  {
    const i = last();
    transport(i);
    i.transport.payload.authority_digest = digest(i.publication.authority);
    i.transport = auth(
      i.transport.payload,
      'Auriga.NetworkTransportTrustBundle.v2',
      runtime,
      'synthetic-runtime',
    );
  }
  const identity = generateKeyPairSync('ed25519'),
    spki = pub(identity),
    nonce = Buffer.alloc(32, 7).toString('base64url');
  const request = {
    package_id: 'synthetic-package',
    tenant_id: 'synthetic-tenant',
    realm: '1',
    control: clone(input.latest_control),
    control_digest: digest(input.latest_control),
    target_id: 'synthetic-target',
    challenge: nonce,
  };
  const receipt = {
    package_id: request.package_id,
    tenant_id: request.tenant_id,
    realm: '1',
    target_id: request.target_id,
    identity_id: 'synthetic-device',
    challenge: nonce,
    control_revision: '2',
    control_digest: request.control_digest,
    result: 'installed',
    persisted_at: input.now,
  };
  for (const [id, expected, change] of [
    ['receipt-confirmed-ready', 'accept', () => {}],
    [
      'receipt-wrong-challenge',
      'invalid_transition',
      (i) => (i.receipt.challenge = Buffer.alloc(32, 8).toString('base64url')),
    ],
    [
      'receipt-replayed-new-request',
      'invalid_transition',
      (i) => (i.used_challenge = true),
    ],
    [
      'receipt-compromised-identity-stays-isolated',
      'identity_quarantined',
      (i) => (i.identity_compromised = true),
    ],
    [
      'receipt-new-chain-not-ready',
      'identity_quarantined',
      (i) => (i.new_chain_ready = false),
    ],
  ]) {
    const i = {
      request: clone(request),
      receipt: clone(receipt),
      identity_spki: spki,
      identity_id: 'synthetic-device',
      persisted: true,
      new_chain_ready: true,
      identity_compromised: false,
      used_challenge: false,
      exact_replay: false,
    };
    change(i);
    i.receipt.identity_auth = auth(
      i.receipt,
      'Auriga.NetworkControlReceipt.v2',
      identity,
      'synthetic-device',
    ).runtime_auth;
    push(id, 'control_receipt', expected, i);
  }
  const q = {
    tenant_id: 'synthetic-tenant',
    realm: '1',
    installation_id: 'synthetic-projector',
    component: 'network',
    gateway_id: 'synthetic-gateway',
    nonce,
    expected_generation: '1',
    expected_keyset_version: '10',
  };
  const transportContext = clone(input);
  transport(transportContext);
  const response = {
    tenant_id: q.tenant_id,
    realm: '1',
    installation_id: q.installation_id,
    component: 'network',
    nonce,
    generation: '1',
    keyset_version: '10',
    publication_digest: digest(input.publication.keyset),
    bundle: transportContext.transport,
    leaves: [],
  };
  for (const [id, expected, change] of [
    ['projection-bound-nonce', 'accept', () => {}],
    [
      'projection-wrong-nonce',
      'invalid_transition',
      (i) => (i.response.nonce = 'other-nonce'),
    ],
    ['projection-replay', 'invalid_transition', (i) => (i.used_nonce = true)],
    [
      'projection-cross-purpose-export',
      'scope_denied',
      (i) => (i.exported_purposes = ['management_state']),
    ],
  ]) {
    const i = {
      request: clone(q),
      response: clone(response),
      identity_spki: spki,
      used_nonce: false,
      exported_purposes: ['protocol_response'],
    };
    change(i);
    i.request.identity_auth = auth(
      i.request,
      'Auriga.NetworkProjectionRequest.v2',
      identity,
      'synthetic-projector',
    ).runtime_auth;
    push(id, 'projection', expected, i);
  }
  const metadata = {
    tenant_id: response.tenant_id,
    realm: response.realm,
    installation_id: response.installation_id,
    component: response.component,
    nonce: response.nonce,
    generation: response.generation,
    keyset_version: response.keyset_version,
    bundle_digest: digest(response.bundle),
    kem: 'DHKEM(X25519,HKDF-SHA256)',
    kdf: 'HKDF-SHA256',
    aead: 'AES-256-GCM',
  };
  const sealed = {
    ...metadata,
    ...sealTest(jcs(metadata), Buffer.from(jcs(response))),
  };
  push('hpke-rfc9180-bound-material', 'hpke', 'accept', {
    sealed,
    expected_plaintext: response,
  });
  const tampered = clone(sealed);
  tampered.nonce = 'different-nonce';
  push('hpke-aad-substitution', 'hpke', 'invalid_chain', {
    sealed: tampered,
    expected_plaintext: response,
  });
  const members = [
    {
      version: '51',
      pair_digest: 'sha256:' + '1'.repeat(64),
      material_digest: 'sha256:' + '2'.repeat(64),
    },
    {
      version: '65',
      pair_digest: 'sha256:' + '3'.repeat(64),
      material_digest: 'sha256:' + '4'.repeat(64),
    },
  ];
  const permit = {
    schema_version: 2,
    tenant_id: 'synthetic-tenant',
    realm: '1',
    permit_id: 'synthetic-permit',
    ancestor_digest: 'sha256:' + '1'.repeat(64),
    queue_commitment_digest: digest(members),
    maximum_committed_v1_version: '65',
    minimum_v2_version: '66',
    minimum_generation: '1',
  };
  for (const [id, expected, change] of [
    ['migration-queue-advanced-within-commitment', 'accept', () => {}],
    [
      'migration-uncommitted-batch',
      'migration_outside_commitment',
      (i) => (i.current_version = '66'),
    ],
    [
      'migration-freeze-exceeds-600-seconds',
      'migration_expired',
      (i) => (i.frozen_elapsed_seconds = 601),
    ],
    [
      'migration-600-second-boundary',
      'accept',
      (i) => (i.frozen_elapsed_seconds = 600),
    ],
  ]) {
    const i = {
      permit: rootSign(permit, 'Auriga.NetworkMigrationPermit.v2'),
      root_spki: pub(root),
      members: clone(members),
      current_version: '65',
      current_digest: members[1].pair_digest,
      frozen_elapsed_seconds: 100,
      remaining_seconds: 21600,
      first_v2_version: '66',
      barrier_committed: false,
      legacy_disabled: false,
    };
    change(i);
    push(id, 'migration', expected, i);
  }
  const tombstone = {
    kid: 'synthetic-retired',
    identity_digest: 'sha256:' + '5'.repeat(64),
    not_before: '2026-11-01T00:00:00Z',
    not_after: '2026-11-08T00:00:00Z',
    terminal_state: 'retired',
  };
  for (const [id, expected, change] of [
    ['archive-retains-tombstone', 'accept', () => {}],
    [
      'archive-removes-still-valid-key',
      'invalid_transition',
      (i) => (i.removed[0].not_after = '2026-11-15T00:00:00Z'),
    ],
    [
      'archive-tombstone-resurrection',
      'invalid_transition',
      (i) => (i.reappeared[0].terminal_state = 'active'),
    ],
  ]) {
    const i = {
      issued_at: input.now,
      before: '2026-11-13T23:54:00Z',
      grace_seconds: 360,
      removed: [clone(tombstone)],
      tombstones: [clone(tombstone)],
      reappeared: [clone(tombstone)],
    };
    change(i);
    push(id, 'archive', expected, i);
  }
  const config = {
    enabled: true,
    kek_secret_file: '/run/secrets/msgnet-kek',
    kek_id: 'synthetic-kek',
    authority_source: 'installation',
    control_source: 'installation',
    keyset_seconds: 86400,
    refresh_seconds: 43200,
    check_seconds: 60,
    business_key_seconds: 691200,
    rotation_seconds: 432000,
    preannounce_seconds: 172800,
    maturity_seconds: 86400,
    overlap_seconds: 360,
    allowed_skew_seconds: 60,
    review_period_days: 365,
    review_reminder_days: [30, 7, 1],
    projector_identity_id: 'synthetic-projector',
    projection_directory: '/run/msgnet',
    reload_seconds: 5,
    leaf_days: 90,
    leaf_renew_before_days: 30,
    ca_years: 10,
    ca_renew_before_years: 2,
    forward_jump_seconds: 300,
    backward_jump_seconds: 60,
    clock_retry_seconds: 30,
    migration_margin_seconds: 21600,
    freeze_seconds: 600,
  };
  for (const [id, expected, change] of [
    ['config-defaults', 'accept', () => {}],
    ['config-tightened-feasible', 'accept', (c) => (c.refresh_seconds = 36000)],
    [
      'config-loosened-lifetime',
      'invalid_transition',
      (c) => (c.keyset_seconds = 86401),
    ],
    [
      'config-infeasible-combination',
      'invalid_transition',
      (c) => (c.business_key_seconds = 500000),
    ],
  ]) {
    const value = clone(config);
    change(value);
    push(id, 'config', expected, { config: value });
  }
  extendRoundThree({ ...c, transition, sealTest });
}
