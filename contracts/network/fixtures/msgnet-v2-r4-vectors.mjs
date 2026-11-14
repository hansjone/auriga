// 单项反例使用有效合成签名，独立改变一条待验约束。
export function extendRoundFour(context) {
  const {
    cases,
    boundary_cases,
    root,
    runtime,
    auth,
    signed,
    pub,
    digest,
    jcs,
    sealTest,
    generateKeyPairSync,
  } = context;
  const clone = (value) => JSON.parse(JSON.stringify(value));
  const rootSign = (payload, domain) =>
    auth(payload, domain, root, 'synthetic-root', true);
  const current = (i) =>
    (i.publication.keyset = signed(i.publication.keyset.payload));
  const previous = (i) => {
    const q = i.publication.predecessor;
    q.keyset = signed(q.keyset.payload);
    i.previous_digest = q.keyset_digest = digest(q.keyset);
  };
  const authority = (i) => {
    const p = i.publication;
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
    i.latest_control = rootSign(
      i.latest_control.payload,
      'Auriga.NetworkRootControl.v2',
    );
  };
  const historicAuthority = (i) => {
    const q = i.publication.predecessor;
    q.authority = rootSign(
      q.authority.payload,
      'Auriga.NetworkRuntimeAuthority.v2',
    );
    q.keyset.payload.authority_digest = digest(q.authority);
    q.control.payload.allowed_authorities = [
      {
        authority_id: q.authority.payload.authority_id,
        authority_digest: digest(q.authority),
      },
    ];
    q.control = rootSign(q.control.payload, 'Auriga.NetworkRootControl.v2');
    previous(i);
  };
  const consumer = (source, id, expected, change) => {
    const v = clone(cases.find((x) => x.id === source));
    v.id = id;
    v.expected = expected;
    change(v.input);
    cases.push(v);
  };
  const boundary = (source, id, expected, change) => {
    const v = clone(boundary_cases.find((x) => x.id === source));
    v.id = id;
    v.expected = expected;
    change(v.input);
    boundary_cases.push(v);
  };
  const signAnnouncement = (i) =>
    (i.announcement = auth(
      i.announcement.payload,
      'Auriga.NetworkKeysetAnnouncement.v2',
      runtime,
      'synthetic-runtime',
    ));
  consumer(
    'refresh-valid',
    'issued-at-must-not-exceed-local-plus-60',
    'invalid_transition',
    (i) => (i.now = '2026-11-13T23:58:59Z'),
  );
  consumer(
    'refresh-valid',
    'generation-must-equal-control-minimum',
    'version_rollback',
    (i) => {
      i.latest_control.payload.minimum_generation = '2';
      i.latest_control = rootSign(
        i.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
    },
  );
  consumer(
    'restart-future-active-after-clock-correction',
    'new-kid-must-have-first-publication',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.first_publications =
        i.publication.keyset.payload.first_publications.filter(
          (x) => !x.kid.endsWith('restart-0'),
        );
      current(i);
    },
  );
  consumer(
    'restart-future-active-after-clock-correction',
    'new-kid-committed-at-must-equal-issued-at',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.first_publications.find((x) =>
        x.kid.endsWith('restart-0'),
      ).committed_at = '2026-11-13T23:59:59Z';
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'committed-version-cannot-exceed-snapshot',
    'invalid_transition',
    (i) => {
      for (const records of [
        i.publication.keyset.payload.first_publications,
        i.publication.predecessor.keyset.payload.first_publications,
        i.first_publication_ledger,
      ])
        records[0].committed_version = '10';
      previous(i);
      current(i);
    },
  );
  consumer(
    'restart-future-active-after-clock-correction',
    'restart-not-before-must-equal-issued-at',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.keys[0].not_before = '2026-11-13T23:59:59Z';
      current(i);
    },
  );
  function addNext(i, mature, reuse) {
    const p = i.publication,
      q = p.predecessor.keyset.payload,
      k = p.keyset.payload;
    const next = {
      ...q.keys[0],
      kid: 'synthetic-r4-next',
      state: 'next',
      public_key_spki: pub(generateKeyPairSync('ed25519')),
      not_before: k.issued_at,
      not_after: '2026-11-21T00:00:00Z',
    };
    const proof = {
      kid: next.kid,
      committed_version: mature ? '8' : '9',
      committed_at: mature ? '2026-11-12T00:00:00Z' : '2026-11-13T23:00:00Z',
    };
    if (!mature) {
      q.issued_at = '2026-11-13T23:00:00Z';
      q.valid_until = '2026-11-14T23:00:00Z';
    }
    q.keys.push(next);
    q.first_publications.push(proof);
    i.first_publication_ledger.push(clone(proof));
    k.first_publications.push(clone(proof));
    if (reuse) {
      const replaced = k.keys[0].kid;
      k.keys[0] = { ...next, state: 'active' };
      k.first_publications = k.first_publications.filter(
        (x) => x.kid !== replaced,
      );
    } else k.keys.push({ ...next, state: 'retired' });
    previous(i);
    current(i);
  }
  consumer(
    'restart-future-active-after-clock-correction',
    'restart-mature-next-requires-activate',
    'invalid_transition',
    (i) => addNext(i, true, false),
  );
  consumer(
    'restart-future-active-after-clock-correction',
    'restart-must-not-reuse-immature-next-kid',
    'invalid_transition',
    (i) => addNext(i, false, true),
  );
  for (const [part, field, value] of [
    ['keyset', 'authority_digest', 'sha256:' + 'a'.repeat(64)],
    ['keyset', 'authority_id', 'synthetic-wrong'],
    ['keyset', 'control_revision', '1'],
    ['keyset', 'realm', '2'],
    ['keyset', 'tenant_id', 'synthetic-wrong-tenant'],
    ['authority', 'realm', '2'],
    ['authority', 'tenant_id', 'synthetic-wrong-tenant'],
    ['control', 'realm', '2'],
    ['control', 'tenant_id', 'synthetic-wrong-tenant'],
  ]) {
    consumer(
      'refresh-valid',
      'predecessor-' + part + '-' + field + '-binding',
      'invalid_chain',
      (i) => {
        const q = i.publication.predecessor;
        q[part].payload[field] = value;
        if (part === 'authority') historicAuthority(i);
        else {
          if (part === 'control')
            q.control = rootSign(
              q.control.payload,
              'Auriga.NetworkRootControl.v2',
            );
          previous(i);
        }
      },
    );
  }
  consumer(
    'refresh-valid',
    'predecessor-runtime-kid-bound',
    'invalid_chain',
    (i) => {
      i.publication.predecessor.keyset.runtime_auth.kid = 'synthetic-wrong';
      i.previous_digest = i.publication.predecessor.keyset_digest = digest(
        i.publication.predecessor.keyset,
      );
    },
  );
  for (const [field, value] of [['not_before', '2026-11-13T00:00:01Z']])
    consumer(
      'refresh-valid',
      'predecessor-authority-' + field + '-effective',
      'invalid_chain',
      (i) => {
        i.publication.predecessor.authority.payload[field] = value;
        historicAuthority(i);
      },
    );
  consumer(
    'refresh-valid',
    'predecessor-positive-validity-required',
    'invalid_chain',
    (i) => {
      i.publication.predecessor.keyset.payload.valid_until =
        '2026-11-13T00:00:00Z';
      previous(i);
    },
  );
  consumer(
    'refresh-valid',
    'predecessor-lifetime-root-limit',
    'invalid_chain',
    (i) => {
      i.publication.predecessor.keyset.payload.valid_until =
        '2026-11-14T00:00:01Z';
      previous(i);
    },
  );
  for (const [field, value] of [
    ['minimum_generation', '2'],
    ['minimum_keyset_version', '10'],
  ])
    consumer(
      'refresh-valid',
      'predecessor-' + field + '-enforced',
      'invalid_chain',
      (i) => {
        const q = i.publication.predecessor;
        q.control.payload[field] = value;
        q.control = rootSign(q.control.payload, 'Auriga.NetworkRootControl.v2');
      },
    );
  consumer(
    'authorized-history-archive',
    'archived-key-original-not-after-bound',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.archive.before = '2026-11-07T23:59:59Z';
      current(i);
    },
  );
  for (const [field, value, expected] of [
    ['authority_digest', 'sha256:' + 'a'.repeat(64), 'scope_denied'],
    ['tenant_id', 'synthetic-wrong-tenant', 'scope_denied'],
    ['generation', '2', 'projection_pending'],
    ['keyset_version', '9', 'invalid_chain'],
    ['valid_until', '2026-11-14T23:59:59Z', 'invalid_chain'],
  ]) {
    boundary(
      'announcement-matched-pair',
      'announcement-' +
        field +
        '-' +
        value.replace(/[^a-z0-9]/g, '').slice(0, 12),
      'accept',
      (i) => {},
    );
    const v = boundary_cases.at(-1);
    v.expected = expected;
    v.input.announcement.payload[field] = value;
    signAnnouncement(v.input);
  }
  boundary(
    'announcement-matched-pair',
    'announcement-lower-generation-binding',
    'invalid_chain',
    (i) => {
      const c = i.context,
        p = c.publication,
        q = p.predecessor;
      p.keyset.payload.generation =
        q.keyset.payload.generation =
        c.seen_generation =
          '2';
      for (const control of [c.latest_control, p.control, q.control])
        control.payload.minimum_generation = '2';
      c.latest_control = rootSign(
        c.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
      p.control = rootSign(p.control.payload, 'Auriga.NetworkRootControl.v2');
      q.control = rootSign(q.control.payload, 'Auriga.NetworkRootControl.v2');
      previous(c);
      current(c);
      i.announcement.payload.keyset_digest = digest(p.keyset);
      signAnnouncement(i);
    },
  );
  boundary(
    'projection-bound-nonce',
    'projection-component-must-be-network',
    'scope_denied',
    (i) => {
      i.request.component = i.response.component = 'edge';
      const key = generateKeyPairSync('ed25519'),
        body = { ...i.request };
      delete body.identity_auth;
      i.identity_spki = pub(key);
      i.request.identity_auth = auth(
        body,
        'Auriga.NetworkProjectionRequest.v2',
        key,
        'synthetic-projector',
      ).runtime_auth;
    },
  );
  boundary(
    'projection-bound-nonce',
    'projection-expected-generation-bound',
    'version_rollback',
    (i) => (i.response.generation = '2'),
  );
  boundary(
    'migration-queue-advanced-within-commitment',
    'migration-queue-commitment-digest-bound',
    'migration_outside_commitment',
    (i) => {
      i.permit.payload.queue_commitment_digest = 'sha256:' + 'a'.repeat(64);
      i.permit = rootSign(i.permit.payload, 'Auriga.NetworkMigrationPermit.v2');
    },
  );
  boundary(
    'migration-queue-advanced-within-commitment',
    'migration-current-member-must-match',
    'migration_outside_commitment',
    (i) => (i.current_digest = 'sha256:' + 'a'.repeat(64)),
  );
  boundary(
    'migration-queue-advanced-within-commitment',
    'migration-current-version-must-not-exceed-permit',
    'migration_outside_commitment',
    (i) => {
      i.permit.payload.maximum_committed_v1_version = '64';
      i.permit = rootSign(i.permit.payload, 'Auriga.NetworkMigrationPermit.v2');
    },
  );
  boundary(
    'migration-queue-advanced-within-commitment',
    'migration-requires-six-hour-margin',
    'migration_expired',
    (i) => (i.remaining_seconds = 21599),
  );
  boundary(
    'migration-queue-advanced-within-commitment',
    'migration-v2-first-version-lower-bound',
    'invalid_transition',
    (i) => (i.first_v2_version = '65'),
  );
  for (const [field, value] of Object.entries({
    keyset_seconds: 86401,
    refresh_seconds: 43201,
    check_seconds: 61,
    business_key_seconds: 691201,
    rotation_seconds: 432001,
    preannounce_seconds: 172799,
    maturity_seconds: 86399,
    overlap_seconds: 359,
    allowed_skew_seconds: 61,
    review_period_days: 366,
    reload_seconds: 6,
    leaf_days: 91,
    leaf_renew_before_days: 29,
    ca_years: 11,
    ca_renew_before_years: 1,
    forward_jump_seconds: 301,
    backward_jump_seconds: 61,
    clock_retry_seconds: 31,
    migration_margin_seconds: 21599,
    freeze_seconds: 601,
  }))
    boundary(
      'config-defaults',
      'config-tightening-' + field,
      'invalid_transition',
      (i) => (i.config[field] = value),
    );
  for (const [id, changes] of [
    ['refresh-before-expiry', { keyset_seconds: 43200 }],
    ['preannounce-covers-maturity', { maturity_seconds: 172801 }],
    ['leaf-renewal-before-expiry', { leaf_days: 30 }],
    ['ca-renewal-before-expiry', { ca_years: 2 }],
    ['review-30-day', { review_reminder_days: [29, 7, 1] }],
    ['review-7-day', { review_reminder_days: [30, 6, 1] }],
    ['review-1-day', { review_reminder_days: [30, 7, 0] }],
    ['integer-configuration', { check_seconds: 1.5 }],
  ])
    boundary(
      'config-defaults',
      'config-combination-' + id,
      'invalid_transition',
      (i) => Object.assign(i.config, changes),
    );
  for (const [field, value] of [
    ['tenant_id', 'synthetic-other-tenant'],
    ['realm', '2'],
  ])
    consumer(
      'refresh-valid',
      'authority-' + field + '-matches-keyset',
      'scope_denied',
      (i) => {
        i.publication.authority.payload[field] = value;
        authority(i);
        current(i);
      },
    );
  for (const [field, value] of [
    ['tenant_id', 'synthetic-other-tenant'],
    ['realm', '2'],
  ])
    consumer(
      'refresh-valid',
      'latest-control-' + field + '-matches-keyset',
      'scope_denied',
      (i) => {
        i.latest_control.payload[field] = value;
        i.latest_control = rootSign(
          i.latest_control.payload,
          'Auriga.NetworkRootControl.v2',
        );
      },
    );
  for (const [field, value] of [
    ['tenant_id', 'synthetic-other-tenant'],
    ['realm', '2'],
  ])
    consumer(
      'refresh-valid',
      'installation-' + field + '-matches-keyset',
      'scope_denied',
      (i) => (i[field] = value),
    );
  consumer(
    'refresh-valid',
    'keyset-authority-id-binding',
    'scope_denied',
    (i) => {
      i.publication.keyset.payload.authority_id = 'synthetic-other-authority';
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'keyset-authority-digest-binding',
    'scope_denied',
    (i) => {
      i.publication.keyset.payload.authority_digest =
        'sha256:' + 'b'.repeat(64);
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'keyset-hard-expiry-at-local-now',
    'invalid_transition',
    (i) => (i.now = '2026-11-15T00:00:00Z'),
  );
  consumer(
    'refresh-valid',
    'keyset-valid-until-strictly-after-issued',
    'invalid_transition',
    (i) => {
      i.now = '2026-11-13T23:59:59Z';
      i.publication.keyset.payload.valid_until =
        i.publication.keyset.payload.issued_at;
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'predecessor-version-strictly-before-successor',
    'invalid_transition',
    (i) => {
      i.publication.predecessor.keyset.payload.keyset_version = '10';
      previous(i);
      i.previous_digest = '';
    },
  );
  context.transition(
    'root-anchored-revocation-requires-transition-mode',
    'invalid_transition',
    (i) => {
      const p = i.publication,
        k = p.keyset.payload,
        q = p.predecessor.keyset.payload;
      k.mode = 'restart_recovery';
      for (const old of q.keys) {
        old.not_before = '2026-11-15T00:00:00Z';
        old.not_after = '2026-11-20T00:00:00Z';
        const copy = k.keys.find((x) => x.kid === old.kid);
        copy.not_before = old.not_before;
        copy.not_after = old.not_after;
      }
      previous(i);
      i.latest_control.payload.generation_transition.predecessor_digest =
        p.predecessor.keyset_digest;
    },
  );
  consumer(
    'refresh-valid',
    'new-next-kids-must-be-unique',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload;
      for (let n = 0; n < 2; n++)
        k.keys.push({
          ...k.keys[n],
          kid: 'synthetic-duplicate-new-kid',
          state: 'next',
          public_key_spki: pub(generateKeyPairSync('ed25519')),
        });
      k.first_publications.push({
        kid: 'synthetic-duplicate-new-kid',
        committed_version: k.keyset_version,
        committed_at: k.issued_at,
      });
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'next-key-lifetime-must-be-positive',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload,
        key = {
          ...k.keys[0],
          kid: 'synthetic-invalid-next-lifetime',
          state: 'next',
          public_key_spki: pub(generateKeyPairSync('ed25519')),
          not_before: '2026-11-16T00:00:00Z',
          not_after: '2026-11-15T00:00:00Z',
        };
      k.keys.push(key);
      k.first_publications.push({
        kid: key.kid,
        committed_version: k.keyset_version,
        committed_at: k.issued_at,
      });
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'active-key-must-not-have-expired',
    'invalid_transition',
    (i) => {
      for (const key of [
        i.publication.keyset.payload.keys[0],
        i.publication.predecessor.keyset.payload.keys[0],
      ]) {
        key.not_before = '2026-11-07T00:00:00Z';
        key.not_after = '2026-11-13T23:59:59Z';
      }
      previous(i);
      current(i);
    },
  );
  consumer(
    'refresh-valid',
    'refresh-cannot-invent-missing-active',
    'invalid_transition',
    (i) => {
      const p = i.publication,
        q = p.predecessor.keyset.payload,
        k = p.keyset.payload,
        kid = q.keys[0].kid;
      q.keys = q.keys.filter((x) => x.kid !== kid);
      q.first_publications = q.first_publications.filter((x) => x.kid !== kid);
      i.first_publication_ledger = i.first_publication_ledger.filter(
        (x) => x.kid !== kid,
      );
      Object.assign(
        k.first_publications.find((x) => x.kid === kid),
        { committed_version: k.keyset_version, committed_at: k.issued_at },
      );
      previous(i);
      current(i);
    },
  );
  consumer(
    'mature-next-activation',
    'activation-must-use-the-mature-next-kid',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload,
        key = k.keys.find(
          (x) => x.purpose === 'business_delivery' && x.state === 'active',
        ),
        oldKid = key.kid;
      key.state = 'retired';
      const replacement = {
        ...key,
        kid: 'synthetic-unadvertised-active',
        state: 'active',
        public_key_spki: pub(generateKeyPairSync('ed25519')),
      };
      k.keys.push(replacement);
      k.first_publications.push({
        kid: replacement.kid,
        committed_version: k.keyset_version,
        committed_at: k.issued_at,
      });
      current(i);
    },
  );
  consumer(
    'mature-next-activation',
    'maturity-requires-first-publication-proof',
    'invalid_transition',
    (i) => {
      const q = i.publication.predecessor.keyset.payload;
      q.first_publications = q.first_publications.filter(
        (x) => x.kid !== 'synthetic-next-0',
      );
      previous(i);
    },
  );
  for (const [field, value] of [
    ['control_revision', '1'],
    ['result', 'rejected'],
  ])
    boundary(
      'receipt-confirmed-ready',
      'receipt-' + field + '-binding',
      'invalid_transition',
      (i) => {
        i.receipt[field] = value;
        const key = generateKeyPairSync('ed25519'),
          body = { ...i.receipt };
        delete body.identity_auth;
        i.identity_spki = pub(key);
        i.receipt.identity_auth = auth(
          body,
          'Auriga.NetworkControlReceipt.v2',
          key,
          i.identity_id,
        ).runtime_auth;
      },
    );
  boundary(
    'archive-retains-tombstone',
    'tombstone-identity-digest-immutable',
    'invalid_transition',
    (i) => (i.reappeared[0].identity_digest = 'sha256:' + 'b'.repeat(64)),
  );
}
