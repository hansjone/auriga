// 每个负例只破坏指定语义，所有签名及首发账本同步保持合法。
export function extendRoundThree(context) {
  const {
    cases,
    boundary_cases,
    input,
    root,
    runtime,
    auth,
    digest,
    jcs,
    pub,
    generateKeyPairSync,
    signed,
    transition,
  } = context;
  const clone = (value) => JSON.parse(JSON.stringify(value));
  const rootSign = (payload, domain) =>
    auth(payload, domain, root, 'synthetic-root', true);
  const refreshProof = (i) => {
    const q = i.publication.predecessor;
    q.keyset = signed(q.keyset.payload);
    i.previous_digest = q.keyset_digest = digest(q.keyset);
  };
  const addFrom = (source, id, expected, modify) => {
    const vector = clone(cases.find((v) => v.id === source));
    vector.id = id;
    vector.expected = expected;
    modify(vector.input);
    cases.push(vector);
  };
  addFrom(
    'mature-next-activation',
    'immature-next-with-consistent-first-publication',
    'invalid_transition',
    (i) => {
      const p = i.publication,
        q = p.predecessor.keyset.payload;
      q.issued_at = '2026-11-13T23:30:00Z';
      q.valid_until = '2026-11-14T23:30:00Z';
      for (const records of [
        q.first_publications,
        p.keyset.payload.first_publications,
        i.first_publication_ledger,
      ])
        for (const proof of records)
          if (proof.kid.startsWith('synthetic-next'))
            proof.committed_at = '2026-11-13T23:00:00Z';
      refreshProof(i);
      p.keyset = signed(p.keyset.payload);
    },
  );
  const adjustSameGeneration = (i) => {
    const k = i.publication.keyset.payload;
    k.generation = '1';
    k.keyset_version = '10';
    k.first_publications
      .filter((p) => p.kid.startsWith('synthetic-new-generation'))
      .forEach((p) => (p.committed_version = '10'));
    i.latest_control.payload.minimum_generation = '1';
    i.latest_control.payload.minimum_keyset_version = '1';
  };
  transition(
    'revoked-predecessor-requires-root-boundary',
    'invalid_transition',
    (i) => {
      adjustSameGeneration(i);
      delete i.latest_control.payload.generation_transition;
    },
  );
  transition(
    'same-generation-authority-cannot-replace-active',
    'invalid_transition',
    (i) => {
      adjustSameGeneration(i);
      i.latest_control.payload.revoked_authority_ids = [];
      i.latest_control.payload.allowed_authorities.push({
        authority_id: input.publication.authority.payload.authority_id,
        authority_digest: digest(input.publication.authority),
      });
      delete i.latest_control.payload.generation_transition;
    },
  );
  addFrom(
    'refresh-valid',
    'same-authority-transition-forbidden',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.mode = 'authority_transition';
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  for (const [field, value] of [
    ['authority_id', 'synthetic-wrong-authority'],
    ['authority_digest', 'sha256:' + 'd'.repeat(64)],
    ['from_generation', '2'],
    ['to_generation', '3'],
  ]) {
    transition(
      'root-anchor-' + field + '-mismatch',
      'invalid_transition',
      (i) => (i.latest_control.payload.generation_transition[field] = value),
    );
  }
  transition(
    'root-anchor-first-version-must-match-keyset',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload;
      k.keyset_version = '2';
      k.first_publications
        .filter((p) => p.kid.startsWith('synthetic-new-generation'))
        .forEach((p) => (p.committed_version = '2'));
    },
  );
  transition(
    'root-anchor-first-version-must-match-minimum',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload;
      k.keyset_version = '2';
      k.first_publications
        .filter((p) => p.kid.startsWith('synthetic-new-generation'))
        .forEach((p) => (p.committed_version = '2'));
      i.latest_control.payload.generation_transition.first_version = '2';
    },
  );
  transition(
    'root-anchor-generation-must-advance',
    'invalid_transition',
    (i) => {
      adjustSameGeneration(i);
      i.latest_control.payload.generation_transition.to_generation = '1';
      i.latest_control.payload.generation_transition.first_version = '10';
      i.latest_control.payload.minimum_keyset_version = '10';
    },
  );
  for (const revoked of [false, true])
    transition(
      'historical-authority-' + (revoked ? 'revoked' : 'not-allowed'),
      'invalid_chain',
      (i) => {
        const q = i.publication.predecessor,
          c = q.control.payload;
        if (revoked)
          c.revoked_authority_ids.push(q.authority.payload.authority_id);
        else c.allowed_authorities = [];
        q.control = rootSign(c, 'Auriga.NetworkRootControl.v2');
      },
    );
  function removeCapability(source, capability, id) {
    addFrom(source, id, 'scope_denied', (i) => {
      const p = i.publication;
      p.authority.payload.capabilities =
        p.authority.payload.capabilities.filter((x) => x !== capability);
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
      p.keyset = signed(p.keyset.payload);
    });
  }
  removeCapability(
    'restart-future-active-after-clock-correction',
    'restart_recovery',
    'restart-specific-capability-required',
  );
  removeCapability(
    'restart-future-active-after-clock-correction',
    'business_rotation',
    'business-rotation-specific-capability-required',
  );
  addFrom('refresh-valid', 'authorized-history-archive', 'accept', (i) => {
    const q = i.publication.predecessor.keyset.payload,
      old = clone(q.keys[0]);
    old.kid = 'synthetic-old-archived';
    old.state = 'retired';
    old.not_before = '2026-11-01T00:00:00Z';
    old.not_after = '2026-11-08T00:00:00Z';
    q.keys.push(old);
    const proof = {
      kid: old.kid,
      committed_version: '1',
      committed_at: old.not_before,
    };
    q.first_publications.push(proof);
    i.first_publication_ledger.push(clone(proof));
    refreshProof(i);
  });
  removeCapability(
    'authorized-history-archive',
    'history_archive',
    'history-archive-specific-capability-required',
  );
  transition(
    'revoked-authority-old-active-preserved',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.keys = clone(
        input.publication.keyset.payload.keys,
      );
      i.publication.keyset.payload.first_publications = clone(
        input.publication.keyset.payload.first_publications,
      );
    },
  );
  transition(
    'revoked-authority-old-active-only-retired',
    'invalid_transition',
    (i) =>
      i.publication.keyset.payload.keys
        .filter((k) => k.state === 'revoked')
        .forEach((k) => (k.state = 'retired')),
  );
  transition(
    'revoked-authority-old-key-missing',
    'invalid_transition',
    (i) =>
      (i.publication.keyset.payload.keys =
        i.publication.keyset.payload.keys.filter((k) => k.state !== 'revoked')),
  );
  transition(
    'revoked-authority-old-next-must-be-revoked',
    'invalid_transition',
    (i) => {
      const p = i.publication,
        q = p.predecessor.keyset.payload;
      const next = {
        ...q.keys[0],
        kid: 'synthetic-old-next',
        state: 'next',
        public_key_spki: pub(generateKeyPairSync('ed25519')),
      };
      q.keys.push(next);
      p.keyset.payload.keys.push(clone(next));
      const proof = {
        kid: next.kid,
        committed_version: '1',
        committed_at: '2026-11-12T00:00:00Z',
      };
      q.first_publications.push(proof);
      p.keyset.payload.first_publications.push(clone(proof));
      i.first_publication_ledger.push(clone(proof));
      refreshProof(i);
      i.latest_control.payload.generation_transition.predecessor_digest =
        p.predecessor.keyset_digest;
    },
  );
  addFrom(
    'refresh-valid',
    'control-binding-must-match-carried-control',
    'invalid_chain',
    (i) => {
      i.publication.keyset.payload.control_revision = '1';
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  removeCapability(
    'refresh-valid',
    'keyset',
    'keyset-specific-capability-required',
  );
  addFrom(
    'refresh-valid',
    'root-minimum-version-enforced',
    'version_rollback',
    (i) => {
      i.latest_control.payload.minimum_keyset_version = '11';
      i.latest_control = rootSign(
        i.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
    },
  );
  addFrom(
    'refresh-valid',
    'predecessor-self-digest-independent-of-local',
    'invalid_transition',
    (i) => {
      i.previous_digest = i.publication.predecessor.keyset_digest =
        'sha256:' + 'f'.repeat(64);
    },
  );
  addFrom(
    'refresh-valid',
    'historical-keyset-capability-required',
    'invalid_chain',
    (i) => {
      const q = i.publication.predecessor;
      q.authority.payload.capabilities =
        q.authority.payload.capabilities.filter((x) => x !== 'keyset');
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
      refreshProof(i);
    },
  );
  addFrom(
    'authorized-history-archive',
    'archive-grace-must-elapse',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.archive.before = '2026-11-13T23:59:00Z';
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  addFrom(
    'refresh-valid',
    'first-publication-must-not-follow-snapshot',
    'invalid_transition',
    (i) => {
      for (const records of [
        i.publication.keyset.payload.first_publications,
        i.publication.predecessor.keyset.payload.first_publications,
        i.first_publication_ledger,
      ])
        records[0].committed_at = '2026-11-13T12:00:00Z';
      refreshProof(i);
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  addFrom(
    'refresh-valid',
    'new-next-spki-must-be-unique',
    'invalid_transition',
    (i) => {
      const k = i.publication.keyset.payload,
        material = pub(generateKeyPairSync('ed25519'));
      for (let n = 0; n < 2; n++) {
        const key = {
          ...k.keys[n],
          kid: 'synthetic-duplicate-next-' + n,
          state: 'next',
          public_key_spki: material,
        };
        k.keys.push(key);
        k.first_publications.push({
          kid: key.kid,
          committed_version: k.keyset_version,
          committed_at: k.issued_at,
        });
      }
      i.publication.keyset = signed(k);
    },
  );
  addFrom(
    'refresh-valid',
    'existing-key-material-is-immutable',
    'invalid_transition',
    (i) => {
      i.publication.keyset.payload.keys[0].public_key_spki = pub(
        generateKeyPairSync('ed25519'),
      );
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  addFrom(
    'refresh-valid',
    'retired-key-cannot-return-as-next',
    'invalid_transition',
    (i) => {
      const q = i.publication.predecessor.keyset.payload,
        k = i.publication.keyset.payload,
        key = {
          ...q.keys[0],
          kid: 'synthetic-terminal',
          public_key_spki: pub(generateKeyPairSync('ed25519')),
          state: 'retired',
        };
      q.keys.push(key);
      k.keys.push({ ...key, state: 'next' });
      const proof = {
        kid: key.kid,
        committed_version: '1',
        committed_at: '2026-11-12T00:00:00Z',
      };
      q.first_publications.push(proof);
      k.first_publications.push(clone(proof));
      i.first_publication_ledger.push(clone(proof));
      refreshProof(i);
      i.publication.keyset = signed(k);
    },
  );
  addFrom(
    'refresh-valid',
    'current-active-must-be-effective',
    'invalid_transition',
    (i) => {
      for (const k of [
        i.publication.keyset.payload.keys[0],
        i.publication.predecessor.keyset.payload.keys[0],
      ]) {
        k.not_before = '2026-11-15T00:00:00Z';
        k.not_after = '2026-11-20T00:00:00Z';
      }
      refreshProof(i);
      i.publication.keyset = signed(i.publication.keyset.payload);
    },
  );
  addFrom(
    'restart-future-active-after-clock-correction',
    'restart-must-preserve-each-valid-purpose',
    'invalid_transition',
    (i) => {
      const q = i.publication.predecessor.keyset.payload,
        k = i.publication.keyset.payload;
      const old = q.keys[0];
      old.not_before = '2026-11-12T00:00:00Z';
      old.not_after = '2026-11-19T00:00:00Z';
      Object.assign(
        k.keys.find((x) => x.kid === old.kid),
        { not_before: old.not_before, not_after: old.not_after },
      );
      refreshProof(i);
      i.publication.keyset = signed(k);
    },
  );
  addFrom(
    'internal-ca-trust-renewal-chain',
    'transport-authority-digest-binding',
    'scope_denied',
    (i) => {
      i.transport.payload.authority_digest = 'sha256:' + 'f'.repeat(64);
      i.transport = auth(
        i.transport.payload,
        'Auriga.NetworkTransportTrustBundle.v2',
        runtime,
        'synthetic-runtime',
      );
    },
  );
  addFrom(
    'transport-dual-to-leaves',
    'transport-retirement-needs-handshake',
    'projection_pending',
    (i) => {
      i.previous_transport_phase = 'new_leaves';
      i.transport.payload.phase = 'retire_old';
      i.handshakes_verified = false;
      i.transport = auth(
        i.transport.payload,
        'Auriga.NetworkTransportTrustBundle.v2',
        runtime,
        'synthetic-runtime',
      );
    },
  );
  transition(
    'generation-change-alone-needs-anchor',
    'invalid_transition',
    (i) => {
      i.latest_control.payload.revoked_authority_ids = [];
      delete i.latest_control.payload.generation_transition;
    },
  );
  const addBoundary = (source, id, expected, change) => {
    const v = clone(boundary_cases.find((x) => x.id === source));
    v.id = id;
    v.expected = expected;
    change(v.input);
    boundary_cases.push(v);
  };
  addBoundary(
    'announcement-matched-pair',
    'announcement-context-revoked',
    'authority_revoked',
    (i) => {
      i.context.latest_control.payload.revoked_authority_ids = [
        'synthetic-authority',
      ];
      i.context.latest_control = rootSign(
        i.context.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
    },
  );
  addBoundary(
    'announcement-matched-pair',
    'announcement-capability-required',
    'scope_denied',
    (i) => {
      const p = i.context.publication;
      p.authority.payload.capabilities =
        p.authority.payload.capabilities.filter((x) => x !== 'announcement');
      p.authority = rootSign(
        p.authority.payload,
        'Auriga.NetworkRuntimeAuthority.v2',
      );
      p.keyset.payload.authority_digest = digest(p.authority);
      i.context.latest_control.payload.allowed_authorities = [
        {
          authority_id: p.authority.payload.authority_id,
          authority_digest: digest(p.authority),
        },
      ];
      i.context.latest_control = rootSign(
        i.context.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
      p.keyset = signed(p.keyset.payload);
      i.announcement.payload.authority_digest = digest(p.authority);
      i.announcement.payload.keyset_digest = digest(p.keyset);
      i.announcement = auth(
        i.announcement.payload,
        'Auriga.NetworkKeysetAnnouncement.v2',
        runtime,
        'synthetic-runtime',
      );
    },
  );
  addBoundary(
    'announcement-matched-pair',
    'announcement-wrong-realm',
    'scope_denied',
    (i) => {
      i.announcement.payload.realm = '2';
      i.announcement = auth(
        i.announcement.payload,
        'Auriga.NetworkKeysetAnnouncement.v2',
        runtime,
        'synthetic-runtime',
      );
    },
  );
  addBoundary(
    'receipt-confirmed-ready',
    'receipt-installation-identity-bound',
    'scope_denied',
    (i) => (i.identity_id = 'synthetic-other-identity'),
  );
  addBoundary(
    'receipt-confirmed-ready',
    'receipt-must-be-persisted',
    'invalid_transition',
    (i) => (i.persisted = false),
  );
  addBoundary(
    'projection-bound-nonce',
    'projection-exact-version-required',
    'version_rollback',
    (i) => (i.response.keyset_version = '9'),
  );
  addBoundary(
    'hpke-rfc9180-bound-material',
    'hpke-plaintext-exact-digest',
    'invalid_chain',
    (i) =>
      (i.expected_plaintext.publication_digest = 'sha256:' + 'f'.repeat(64)),
  );
  addBoundary(
    'hpke-rfc9180-bound-material',
    'hpke-authenticated-scope-still-bound',
    'scope_denied',
    (i) => {
      i.expected_plaintext.tenant_id = 'synthetic-other-tenant';
      const metadata = { ...i.sealed };
      delete metadata.enc;
      delete metadata.ciphertext;
      Object.assign(
        i.sealed,
        context.sealTest(jcs(metadata), Buffer.from(jcs(i.expected_plaintext))),
      );
    },
  );
  addBoundary(
    'migration-queue-advanced-within-commitment',
    'migration-barrier-disables-legacy',
    'invalid_transition',
    (i) => {
      i.barrier_committed = true;
      i.legacy_disabled = false;
    },
  );
  addBoundary(
    'archive-retains-tombstone',
    'archive-boundary-respects-grace',
    'invalid_transition',
    (i) => (i.before = '2026-11-14T00:00:00Z'),
  );
  addBoundary(
    'config-defaults',
    'config-positive-check-interval',
    'invalid_transition',
    (i) => (i.config.check_seconds = 0),
  );
}
