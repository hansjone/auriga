import { readFileSync } from 'node:fs';
export function extendK3({
  cases,
  boundary_cases,
  root,
  runtime,
  auth,
  pub,
  digest,
  generateKeyPairSync,
}) {
  const clone = structuredClone,
    rootSign = (p, d) => auth(p, d, root, 'synthetic-root', true),
    runtimeSign = (p, k = runtime) =>
      auth(
        p,
        'Auriga.NetworkTransportTrustBundle.v2',
        k,
        p.authority_id === 'synthetic-authority'
          ? 'synthetic-runtime'
          : 'k3-runtime',
      );
  const base = clone(
    cases.find((x) => x.id === 'transport-second-ca-cycle').input,
  );
  const fresh = generateKeyPairSync('ed25519');
  const authority = rootSign(
    {
      ...clone(base.publication.authority.payload),
      authority_id: 'k3-authority',
      runtime_kid: 'k3-runtime',
      public_key_spki: pub(fresh),
    },
    'Auriga.NetworkRuntimeAuthority.v2',
  );
  const t = {
    ...clone(base.transport.payload),
    authority_id: 'k3-authority',
    authority_digest: digest(authority),
  };
  const predecessor = {
    bundle: clone(base.previous_transport),
    authority: clone(base.publication.authority),
    control: clone(base.latest_control),
  };
  const control = clone(base.latest_control.payload);
  control.control_revision = '20';
  control.allowed_authorities.push({
    authority_id: 'k3-authority',
    authority_digest: digest(authority),
  });
  control.transport_transition = {
    mode: 'authority_transition',
    predecessor_digest: digest(predecessor.bundle),
    from_generation: t.generation,
    to_generation: t.generation,
    authority_id: t.authority_id,
    authority_digest: t.authority_digest,
    first_bundle_version: t.bundle_version,
  };
  const normal = {
    root_spki: base.root_spki,
    tenant_id: base.tenant_id,
    realm: base.realm,
    latest_control: rootSign(control, 'Auriga.NetworkRootControl.v2'),
    authority,
    transport: runtimeSign(t, fresh),
    predecessor,
    seen_generation: predecessor.bundle.payload.generation,
    seen_bundle_version: predecessor.bundle.payload.bundle_version,
    seen_bundle_digest: digest(predecessor.bundle),
    isolation_enforced: true,
  };
  const add = (operation, id, source, expected, change = () => {}) => {
    const input = clone(source);
    change(input);
    boundary_cases.push({ id: 'k3-' + id, operation, expected, input });
  };
  const tr = (id, source, expected, change) =>
    add('transport_transition', id, source, expected, (i) => {
      change?.(i);
      i.latest_control = rootSign(
        i.latest_control.payload,
        'Auriga.NetworkRootControl.v2',
      );
      i.transport = runtimeSign(i.transport.payload, fresh);
    });
  tr('normal-authority-transition', normal, 'accept');
  tr('offline-normal-no-local-history', normal, 'accept', (i) => {
    i.seen_bundle_version = '';
    i.seen_bundle_digest = '';
  });
  for (const field of Object.keys(control.transport_transition).filter(
    (x) => x !== 'mode',
  ))
    tr(
      'anchor-' + field,
      normal,
      'invalid_transition',
      (i) =>
        (i.latest_control.payload.transport_transition[field] = field.includes(
          'digest',
        )
          ? 'sha256:' + 'f'.repeat(64)
          : '99'),
    );
  tr(
    'missing-anchor',
    normal,
    'control_required',
    (i) => delete i.latest_control.payload.transport_transition,
  );
  tr('same-bundle-version', normal, 'version_rollback', (i) => {
    i.transport.payload.bundle_version =
      i.predecessor.bundle.payload.bundle_version;
    i.latest_control.payload.transport_transition.first_bundle_version =
      i.transport.payload.bundle_version;
  });
  tr('changed-generation', normal, 'invalid_transition', (i) => {
    i.transport.payload.generation = '2';
    i.latest_control.payload.transport_transition.to_generation = '2';
  });
  tr(
    'new-leaves-as-start',
    normal,
    'invalid_transition',
    (i) => (i.transport.payload.phase = 'new_leaves'),
  );
  tr(
    'old-scope',
    normal,
    'scope_denied',
    (i) => (i.transport.payload.scope = []),
  );
  tr(
    'reader-set',
    normal,
    'invalid_transition',
    (i) => (i.transport.payload.required_reader_ids = []),
  );
  tr(
    'old-ca-lost',
    normal,
    'invalid_transition',
    (i) => (i.transport.payload.cas = i.transport.payload.cas.slice(1)),
  );
  tr(
    'no-new-ca',
    normal,
    'invalid_transition',
    (i) => (i.transport.payload.cas = clone(i.predecessor.bundle.payload.cas)),
  );
  tr(
    'local-digest-conflict',
    normal,
    'invalid_transition',
    (i) => (i.seen_bundle_digest = 'sha256:' + 'f'.repeat(64)),
  );
  tr(
    'local-version-conflict',
    normal,
    'version_rollback',
    (i) => (i.seen_bundle_version = '999'),
  );
  tr(
    'previous-digest-conflict',
    normal,
    'invalid_transition',
    (i) =>
      (i.transport.payload.previous_bundle_digest = 'sha256:' + 'f'.repeat(64)),
  );
  tr(
    'current-control-required',
    normal,
    'control_required',
    (i) => (i.transport.payload.control_revision = '21'),
  );
  tr(
    'current-authority-id',
    normal,
    'invalid_chain',
    (i) => (i.transport.payload.authority_id = 'foreign'),
  );
  tr(
    'current-authority-digest',
    normal,
    'invalid_chain',
    (i) => (i.transport.payload.authority_digest = 'sha256:' + 'f'.repeat(64)),
  );
  tr(
    'new-revoked',
    normal,
    'authority_revoked',
    (i) => (i.latest_control.payload.revoked_authority_ids = ['k3-authority']),
  );
  tr(
    'old-revoked-in-normal',
    normal,
    'authority_revoked',
    (i) =>
      (i.latest_control.payload.revoked_authority_ids = [
        predecessor.authority.payload.authority_id,
      ]),
  );
  tr(
    'old-no-longer-allowed',
    normal,
    'authority_unknown',
    (i) =>
      (i.latest_control.payload.allowed_authorities =
        i.latest_control.payload.allowed_authorities.slice(-1)),
  );
  tr(
    'new-not-allowed',
    normal,
    'authority_unknown',
    (i) =>
      (i.latest_control.payload.allowed_authorities =
        i.latest_control.payload.allowed_authorities.slice(0, 1)),
  );
  for (const field of ['tenant_id', 'realm'])
    tr('scope-' + field, normal, 'scope_denied', (i) => (i[field] = 'foreign'));
  const history = (i) => {
    i.predecessor.bundle = runtimeSign(i.predecessor.bundle.payload);
    i.predecessor.control = rootSign(
      i.predecessor.control.payload,
      'Auriga.NetworkRootControl.v2',
    );
    const d = digest(i.predecessor.bundle);
    i.transport.payload.previous_bundle_digest = d;
    i.latest_control.payload.transport_transition.predecessor_digest = d;
    i.seen_bundle_digest = d;
  };
  const newAuth = (i, change) => {
    change(i.authority.payload);
    i.authority = rootSign(
      i.authority.payload,
      'Auriga.NetworkRuntimeAuthority.v2',
    );
    const d = digest(i.authority);
    i.transport.payload.authority_digest = d;
    i.latest_control.payload.transport_transition.authority_digest = d;
    i.latest_control.payload.allowed_authorities.at(-1).authority_digest = d;
  };
  tr('kid-binding', normal, 'invalid_chain', (i) =>
    newAuth(i, (a) => (a.runtime_kid = 'other')),
  );
  tr('historical-authority-id', normal, 'invalid_chain', (i) => {
    i.predecessor.bundle.payload.authority_id = 'other';
    history(i);
  });
  tr('historical-authority-digest', normal, 'invalid_chain', (i) => {
    i.predecessor.bundle.payload.authority_digest = 'sha256:' + 'f'.repeat(64);
    history(i);
  });
  tr('historical-not-allowed', normal, 'authority_unknown', (i) => {
    i.predecessor.control.payload.allowed_authorities = [];
    history(i);
  });
  tr('historical-revoked', normal, 'authority_revoked', (i) => {
    i.predecessor.control.payload.revoked_authority_ids = [
      i.predecessor.bundle.payload.authority_id,
    ];
    history(i);
  });
  tr('historical-incomplete-cycle', normal, 'invalid_transition', (i) => {
    i.predecessor.bundle.payload.phase = 'new_leaves';
    history(i);
  });
  tr('capability-denied', normal, 'scope_denied', (i) =>
    newAuth(
      i,
      (a) =>
        (a.capabilities = a.capabilities.filter(
          (x) => x !== 'network_transport',
        )),
    ),
  );
  tr('authority-start-one-second-future', normal, 'scope_denied', (i) =>
    newAuth(
      i,
      (a) =>
        (a.not_before = new Date(
          Date.parse(i.transport.payload.issued_at) + 1000,
        )
          .toISOString()
          .replace('.000', '')),
    ),
  );
  tr('authority-start-equal', normal, 'accept', (i) =>
    newAuth(i, (a) => (a.not_before = i.transport.payload.issued_at)),
  );
  tr(
    'control-bind-equal',
    normal,
    'accept',
    (i) =>
      (i.transport.payload.control_revision =
        i.latest_control.payload.control_revision),
  );
  tr(
    'generation-below-floor',
    normal,
    'version_rollback',
    (i) => (i.latest_control.payload.minimum_generation = '2'),
  );
  tr('authority-scope-denied', normal, 'scope_denied', (i) =>
    newAuth(i, (a) => (a.transport_scope = [])),
  );
  add(
    'transport_transition',
    'tampered-signature',
    normal,
    'invalid_chain',
    (i) => (i.transport.runtime_auth.signature = 'A'.repeat(86)),
  );
  tr('long-offline-older-version', normal, 'accept', (i) => {
    i.seen_bundle_version = '1';
    i.seen_bundle_digest = 'sha256:' + 'f'.repeat(64);
  });
  tr(
    'local-generation-ahead',
    normal,
    'version_rollback',
    (i) => (i.seen_generation = '2'),
  );
  tr('long-offline-older-generation', normal, 'accept', (i) => {
    i.seen_generation = '';
    i.seen_bundle_version = '';
    i.seen_bundle_digest = '';
  });
  const emergency = clone(normal);
  emergency.transport.payload.generation = '2';
  emergency.transport.payload.bundle_version = '1';
  emergency.transport.payload.cas = emergency.transport.payload.cas.slice(1);
  emergency.latest_control.payload.revoked_authority_ids = [
    predecessor.authority.payload.authority_id,
  ];
  Object.assign(emergency.latest_control.payload.transport_transition, {
    mode: 'emergency_recovery',
    to_generation: '2',
    first_bundle_version: '1',
  });
  tr('emergency-fresh-generation', emergency, 'accept');
  tr(
    'emergency-no-isolation',
    emergency,
    'identity_quarantined',
    (i) => (i.isolation_enforced = false),
  );
  tr(
    'emergency-no-revocation',
    emergency,
    'authority_revoked',
    (i) => (i.latest_control.payload.revoked_authority_ids = []),
  );
  tr(
    'emergency-old-ca',
    emergency,
    'invalid_transition',
    (i) => (i.transport.payload.cas = clone(predecessor.bundle.payload.cas)),
  );
  tr('emergency-same-generation', emergency, 'version_rollback', (i) => {
    i.transport.payload.generation = '1';
    i.latest_control.payload.transport_transition.to_generation = '1';
  });
  const selector = JSON.parse(
    readFileSync(
      new URL('msgnet-local-projection-vectors.json', import.meta.url),
    ),
  )[0].input.request.selector;
  const bundle = clone(base.transport);
  bundle.payload.required_reader_ids = ['reader-1'];
  bundle.payload.phase = 'new_leaves';
  const signedBundle = runtimeSign(bundle.payload);
  const row = {
    reader_id: 'reader-1',
    identity_id: selector.installation_id,
    selector: {
      ...selector,
      bundle_digest: digest(signedBundle),
      bundle_version: bundle.payload.bundle_version,
    },
    phase: 'handshake_verified',
    isolation_enforced: false,
  };
  const e = {
    tenant_id: base.tenant_id,
    realm: base.realm,
    authority_id: base.publication.authority.payload.authority_id,
    authority_digest: digest(base.publication.authority),
    control_revision: base.latest_control.payload.control_revision,
    bundle_version: bundle.payload.bundle_version,
    bundle_digest: digest(signedBundle),
    phase: 'new_leaves',
    required_reader_ids: ['reader-1'],
    readers: [row],
    challenge: selector.nonce,
    issued_at: '2026-11-14T00:00:00Z',
  };
  const confirmation = {
    tenant_id: base.tenant_id,
    realm: base.realm,
    root_spki: base.root_spki,
    authority: base.publication.authority,
    control: base.latest_control,
    bundle: signedBundle,
    evidence: auth(
      e,
      'Auriga.NetworkTransportConfirmation.v2',
      runtime,
      'synthetic-runtime',
    ),
    challenge: selector.nonce,
    now: e.issued_at,
    required_phase: 'handshake_verified',
    expected_selectors: { 'reader-1': digest(row.selector) },
  };
  const ev = (id, expected, change) =>
    add('transport_confirmation', id, confirmation, expected, (i) => {
      change?.(i);
      i.evidence = auth(
        i.evidence.payload,
        'Auriga.NetworkTransportConfirmation.v2',
        runtime,
        'synthetic-runtime',
      );
    });
  ev('confirmation-complete', 'accept');
  ev(
    'confirmation-without-network-transport-capability',
    'scope_denied',
    (i) => {
      i.authority.payload.capabilities =
        i.authority.payload.capabilities.filter(
          (capability) => capability !== 'network_transport',
        );
      i.authority = rootSign(
        i.authority.payload,
        'Auriga.NetworkRuntimeAuthority.v2',
      );
      const authorityDigest = digest(i.authority);
      i.control.payload.allowed_authorities = [
        {
          authority_id: i.authority.payload.authority_id,
          authority_digest: authorityDigest,
        },
      ];
      i.control = rootSign(i.control.payload, 'Auriga.NetworkRootControl.v2');
      i.bundle.payload.authority_digest = authorityDigest;
      i.bundle = runtimeSign(i.bundle.payload);
      i.evidence.payload.authority_digest = authorityDigest;
      i.evidence.payload.bundle_digest = digest(i.bundle);
      i.evidence.payload.readers[0].selector.bundle_digest = digest(i.bundle);
      i.expected_selectors['reader-1'] = digest(
        i.evidence.payload.readers[0].selector,
      );
    },
  );

  ev('confirmation-age-60', 'accept', (i) => (i.now = '2026-11-14T00:01:00Z'));
  ev(
    'confirmation-age-61',
    'projection_pending',
    (i) => (i.now = '2026-11-14T00:01:01Z'),
  );
  ev(
    'confirmation-future-one-second',
    'projection_pending',
    (i) => (i.now = '2026-11-13T23:59:59Z'),
  );
  for (const field of [
    'authority_digest',
    'authority_id',
    'bundle_digest',
    'tenant_id',
    'realm',
    'bundle_version',
    'phase',
    'challenge',
  ])
    ev(
      'confirmation-' + field,
      'invalid_chain',
      (i) =>
        (i.evidence.payload[field] = field.includes('digest')
          ? 'sha256:' + 'f'.repeat(64)
          : field === 'phase'
            ? 'dual_trust'
            : field === 'challenge'
              ? 'A'.repeat(43)
              : '99'),
    );
  ev(
    'confirmation-control',
    'control_required',
    (i) => (i.evidence.payload.control_revision = '99'),
  );
  ev(
    'confirmation-readers',
    'invalid_chain',
    (i) => (i.evidence.payload.required_reader_ids = []),
  );
  ev('confirmation-duplicate', 'invalid_chain', (i) =>
    i.evidence.payload.readers.push(clone(row)),
  );
  ev(
    'confirmation-missing',
    'projection_pending',
    (i) => (i.evidence.payload.readers = []),
  );
  ev(
    'confirmation-wrong-reader',
    'projection_pending',
    (i) => (i.evidence.payload.readers[0].reader_id = 'other'),
  );
  ev(
    'confirmation-loaded-only',
    'projection_pending',
    (i) => (i.evidence.payload.readers[0].phase = 'loaded'),
  );
  ev('confirmation-isolated', 'accept', (i) => {
    i.evidence.payload.readers[0].phase = 'isolated';
    i.evidence.payload.readers[0].isolation_enforced = true;
  });
  ev(
    'confirmation-isolation-not-enforced',
    'identity_quarantined',
    (i) => (i.evidence.payload.readers[0].phase = 'isolated'),
  );
  for (const field of [
    'installation_id',
    'bundle_version',
    'bundle_digest',
    'nonce',
    'material_revision',
  ])
    ev(
      'confirmation-selector-' + field,
      'invalid_chain',
      (i) =>
        (i.evidence.payload.readers[0].selector[field] = field.includes(
          'digest',
        )
          ? 'sha256:' + 'f'.repeat(64)
          : field === 'nonce'
            ? 'A'.repeat(43)
            : '99'),
    );
  ev('confirmation-loaded-gate', 'accept', (i) => {
    i.required_phase = 'loaded';
    i.evidence.payload.readers[0].phase = 'loaded';
  });
  ev('confirmation-authority-not-allowed', 'authority_unknown', (i) => {
    i.control.payload.allowed_authorities = [];
    i.control = rootSign(i.control.payload, 'Auriga.NetworkRootControl.v2');
  });
  ev('confirmation-authority-revoked', 'authority_revoked', (i) => {
    i.control.payload.revoked_authority_ids = [
      i.authority.payload.authority_id,
    ];
    i.control = rootSign(i.control.payload, 'Auriga.NetworkRootControl.v2');
  });
  ev('confirmation-bundle-digest-only', 'invalid_chain', (i) => {
    i.evidence.payload.bundle_digest = 'sha256:' + 'f'.repeat(64);
    i.evidence.payload.readers[0].selector.bundle_digest =
      i.evidence.payload.bundle_digest;
    i.expected_selectors['reader-1'] = digest(
      i.evidence.payload.readers[0].selector,
    );
  });
  ev('confirmation-extra-reader', 'projection_pending', (i) => {
    const r = clone(row);
    r.reader_id = 'reader-2';
    i.evidence.payload.readers.push(r);
  });
  for (const field of ['installation_id', 'bundle_version', 'bundle_digest'])
    ev('confirmation-independent-selector-' + field, 'invalid_chain', (i) => {
      const r = i.evidence.payload.readers[0];
      r.selector[field] = field.includes('digest')
        ? 'sha256:' + 'f'.repeat(64)
        : '99';
      i.expected_selectors['reader-1'] = digest(r.selector);
    });
  for (const field of ['tenant_id', 'realm'])
    ev(
      'confirmation-installed-' + field,
      'scope_denied',
      (i) => (i[field] = 'other'),
    );
  ev('confirmation-bundle-authority', 'invalid_chain', (i) => {
    i.bundle.payload.authority_id = 'other';
    i.bundle = runtimeSign(i.bundle.payload);
    i.evidence.payload.bundle_digest = digest(i.bundle);
    i.evidence.payload.readers[0].selector.bundle_digest = digest(i.bundle);
    i.expected_selectors['reader-1'] = digest(
      i.evidence.payload.readers[0].selector,
    );
  });
  ev('confirmation-bundle-authority-digest', 'invalid_chain', (i) => {
    i.bundle.payload.authority_digest = 'sha256:' + 'f'.repeat(64);
    i.bundle = runtimeSign(i.bundle.payload);
    i.evidence.payload.bundle_digest = digest(i.bundle);
    i.evidence.payload.readers[0].selector.bundle_digest = digest(i.bundle);
    i.expected_selectors['reader-1'] = digest(
      i.evidence.payload.readers[0].selector,
    );
  });
  ev('confirmation-kid-binding', 'invalid_chain', (i) => {
    i.authority.payload.runtime_kid = 'other';
    i.authority = rootSign(
      i.authority.payload,
      'Auriga.NetworkRuntimeAuthority.v2',
    );
    const d = digest(i.authority);
    i.control.payload.allowed_authorities = [
      { authority_id: i.authority.payload.authority_id, authority_digest: d },
    ];
    i.control = rootSign(i.control.payload, 'Auriga.NetworkRootControl.v2');
    i.bundle.payload.authority_digest = d;
    i.bundle = runtimeSign(i.bundle.payload);
  });
  add(
    'transport_confirmation',
    'confirmation-kid-only',
    confirmation,
    'invalid_chain',
    (i) => (i.evidence.runtime_auth.kid = 'other'),
  );
  const node = {
    authenticated_node_id: 'node-1',
    pin: {
      target_id: 'target-1',
      node_id: 'node-1',
      identity_id: 'node-ed-1',
      signing_spki: pub(runtime),
      identity_revision: '2',
    },
    target_id: 'target-1',
    identity_id: 'node-ed-1',
    identity_revision: '2',
    compromised: false,
    requests_private_material: false,
    independent_enrollment: true,
  };
  add('node_control_binding', 'node-enrolled', node, 'accept');
  for (const field of [
    'authenticated_node_id',
    'target_id',
    'identity_id',
    'identity_revision',
  ])
    add(
      'node_control_binding',
      'node-' + field,
      node,
      field === 'identity_revision' ? 'version_rollback' : 'scope_denied',
      (i) => (i[field] = '1'),
    );
  for (const [field, expected] of [
    ['compromised', 'identity_quarantined'],
    ['requests_private_material', 'scope_denied'],
    ['independent_enrollment', 'identity_quarantined'],
  ])
    add(
      'node_control_binding',
      'node-' + field,
      node,
      expected,
      (i) => (i[field] = !i[field]),
    );
  for (const component of ['edge', 'network', 'broker']) {
    const uid = component === 'edge' ? 65532 : 10001;
    const access = {
      component,
      projector_uid: uid,
      reader_uid: uid,
      peer_uid: uid,
      owner_uid: uid,
      directory_mode: 448,
      file_mode: 384,
      component_private_mount: true,
      cap_drop_all: true,
    };
    add('projection_access', 'access-' + component, access, 'accept');
    for (const field of Object.keys(access).filter((x) => x !== 'component'))
      add(
        'projection_access',
        'access-' + component + '-' + field,
        access,
        'scope_denied',
        (i) =>
          (i[field] = typeof i[field] === 'boolean' ? false : i[field] + 1),
      );
  }
  const catchup = {
    transition: clone(normal),
    current: runtimeSign(
      { ...normal.transport.payload, bundle_version: '100' },
      fresh,
    ),
    control: clone(normal.latest_control),
    seen_version: '50',
    seen_digest: 'sha256:' + 'f'.repeat(64),
  };
  const cp = (id, expected, change) =>
    add('transport_catchup', id, catchup, expected, (i) => {
      change?.(i);
      i.control = rootSign(i.control.payload, 'Auriga.NetworkRootControl.v2');
      i.current = runtimeSign(i.current.payload, fresh);
    });
  cp('catchup-current-after-years', 'accept');
  cp(
    'catchup-control-old',
    'control_rollback',
    (i) => (i.control.payload.control_revision = '1'),
  );
  cp(
    'catchup-revoked',
    'authority_revoked',
    (i) => (i.control.payload.revoked_authority_ids = ['k3-authority']),
  );
  cp(
    'catchup-unknown',
    'authority_unknown',
    (i) => (i.control.payload.allowed_authorities = []),
  );
  cp(
    'catchup-minimum-generation',
    'version_rollback',
    (i) => (i.control.payload.minimum_generation = '2'),
  );
  cp('catchup-before-start', 'version_rollback', (i) => {
    i.current.payload.bundle_version = '1';
    i.seen_version = '';
  });
  cp(
    'catchup-before-local',
    'version_rollback',
    (i) => (i.current.payload.bundle_version = '49'),
  );
  cp(
    'catchup-same-conflict',
    'same_version_conflict',
    (i) => (i.current.payload.bundle_version = '50'),
  );
  cp('catchup-equal-start', 'accept', (i) => {
    i.current = clone(normal.transport);
    i.seen_version = '1';
  });
  cp('catchup-equal-local', 'accept', (i) => {
    i.seen_version = '100';
    i.seen_digest = digest(i.current);
  });
  cp(
    'catchup-newer-control-required',
    'control_required',
    (i) => (i.current.payload.control_revision = '21'),
  );
  cp(
    'catchup-equal-control',
    'accept',
    (i) => (i.current.payload.control_revision = '20'),
  );
  cp(
    'catchup-scope-denied',
    'scope_denied',
    (i) =>
      (i.current.payload.scope = [
        { ...i.current.payload.scope[0], identity_id: 'foreign' },
      ]),
  );
  for (const f of ['tenant_id', 'realm'])
    cp('catchup-' + f, 'scope_denied', (i) => (i.current.payload[f] = '99'));
  for (const f of ['authority_id', 'authority_digest', 'generation'])
    cp(
      'catchup-' + f,
      'invalid_chain',
      (i) =>
        (i.current.payload[f] = f.includes('digest')
          ? 'sha256:' + 'f'.repeat(64)
          : '99'),
    );
  cp(
    'catchup-phase-needs-proof',
    'projection_pending',
    (i) => (i.current.payload.phase = 'new_leaves'),
  );
  const catchupEvidence = clone(confirmation);
  catchupEvidence.authority = clone(normal.authority);
  catchupEvidence.control = clone(normal.latest_control);
  catchupEvidence.bundle = clone(normal.transport);
  const refreshEvidence = (i) => {
    const q = i.confirmation;
    q.bundle = runtimeSign(q.bundle.payload, fresh);
    const e = q.evidence.payload;
    Object.assign(e, {
      authority_id: 'k3-authority',
      authority_digest: digest(normal.authority),
      control_revision: normal.latest_control.payload.control_revision,
      bundle_version: q.bundle.payload.bundle_version,
      bundle_digest: digest(q.bundle),
      phase: q.bundle.payload.phase,
      required_reader_ids: q.bundle.payload.required_reader_ids,
    });
    q.bundle.payload.required_reader_ids = ['reader-1'];
    q.bundle = runtimeSign(q.bundle.payload, fresh);
    e.required_reader_ids = ['reader-1'];
    e.bundle_digest = digest(q.bundle);
    e.readers[0].selector.bundle_digest = e.bundle_digest;
    e.readers[0].selector.bundle_version = e.bundle_version;
    q.expected_selectors['reader-1'] = digest(e.readers[0].selector);
    q.evidence = auth(
      e,
      'Auriga.NetworkTransportConfirmation.v2',
      fresh,
      'k3-runtime',
    );
    i.current.payload.previous_bundle_digest = digest(q.bundle);
  };
  const withProof = (i) => {
    i.current.payload.phase = 'new_leaves';
    i.confirmation = clone(catchupEvidence);
    i.confirmation.required_phase = 'loaded';
    refreshEvidence(i);
  };
  cp('catchup-new-leaves-confirmed', 'accept', withProof);
  cp('catchup-retire-confirmed', 'accept', (i) => {
    withProof(i);
    i.current.payload.phase = 'retire_old';
    i.confirmation.bundle.payload.phase = 'new_leaves';
    i.confirmation.required_phase = 'handshake_verified';
    refreshEvidence(i);
  });
  cp('catchup-invalid-confirmation', 'projection_pending', (i) => {
    withProof(i);
    i.confirmation.evidence.runtime_auth.signature = 'A'.repeat(86);
  });
  cp('catchup-wrong-confirmation-bundle', 'invalid_chain', (i) => {
    withProof(i);
    i.current.payload.previous_bundle_digest = 'sha256:' + 'f'.repeat(64);
  });
  cp('catchup-wrong-confirmation-phase', 'invalid_transition', (i) => {
    withProof(i);
    i.confirmation.bundle.payload.phase = 'new_leaves';
    refreshEvidence(i);
  });
  cp('catchup-wrong-confirmation-level', 'projection_pending', (i) => {
    withProof(i);
    i.confirmation.required_phase = 'handshake_verified';
  });
  // 成熟期恰在24小时与少一秒时，仅变动旧next的终态。
  for (const [age, state, expected] of [
    [86400, 'next', 'accept'],
    [86399, 'next', 'invalid_transition'],
    [86399, 'retired', 'accept'],
    [86399, 'revoked', 'accept'],
  ]) {
    const vector = clone(
      cases.find((entry) => entry.id === 'restart-two-valid-one-recovered'),
    );
    vector.id = `restart-old-next-${age}-${state}`;
    vector.expected = expected;
    const input = vector.input;
    const previous = input.publication.predecessor.keyset.payload;
    const current = input.publication.keyset.payload;
    const committedAt = new Date(Date.parse(current.issued_at) - age * 1000)
      .toISOString()
      .replace('.000Z', 'Z');
    previous.issued_at = committedAt;
    previous.valid_until = new Date(Date.parse(committedAt) + 86400000)
      .toISOString()
      .replace('.000Z', 'Z');
    const next = {
      ...previous.keys[0],
      kid: 'synthetic-k3-restart-old-next',
      public_key_spki: pub(generateKeyPairSync('ed25519')),
      state: 'next',
      not_before: current.issued_at,
    };
    const first = {
      kid: next.kid,
      committed_version: '8',
      committed_at: committedAt,
    };
    previous.keys.push(next);
    previous.first_publications.push(first);
    input.first_publication_ledger.push(clone(first));
    current.first_publications.push(clone(first));
    current.keys.push({ ...next, state });
    input.publication.predecessor.keyset = auth(
      previous,
      'Auriga.NetworkKeyset.v2',
      runtime,
      'synthetic-runtime',
    );
    input.previous_digest = input.publication.predecessor.keyset_digest =
      digest(input.publication.predecessor.keyset);
    input.publication.keyset = auth(
      current,
      'Auriga.NetworkKeyset.v2',
      runtime,
      'synthetic-runtime',
    );
    cases.push(vector);
  }
  const missingFirst = clone(
    cases.find((entry) => entry.id === 'restart-old-next-86400-next'),
  );
  missingFirst.id = 'restart-old-next-missing-first-publication';
  missingFirst.expected = 'invalid_transition';
  const missingInput = missingFirst.input;
  const missingKid = 'synthetic-k3-restart-old-next';
  missingInput.publication.predecessor.keyset.payload.first_publications =
    missingInput.publication.predecessor.keyset.payload.first_publications.filter(
      (entry) => entry.kid !== missingKid,
    );
  missingInput.publication.keyset.payload.first_publications =
    missingInput.publication.keyset.payload.first_publications.filter(
      (entry) => entry.kid !== missingKid,
    );
  missingInput.first_publication_ledger =
    missingInput.first_publication_ledger.filter(
      (entry) => entry.kid !== missingKid,
    );
  missingInput.publication.predecessor.keyset = auth(
    missingInput.publication.predecessor.keyset.payload,
    'Auriga.NetworkKeyset.v2',
    runtime,
    'synthetic-runtime',
  );
  missingInput.previous_digest =
    missingInput.publication.predecessor.keyset_digest = digest(
      missingInput.publication.predecessor.keyset,
    );
  missingInput.publication.keyset = auth(
    missingInput.publication.keyset.payload,
    'Auriga.NetworkKeyset.v2',
    runtime,
    'synthetic-runtime',
  );
  cases.push(missingFirst);
}
