export function extendK4({ boundary_cases, auth, runtime, digest }) {
  const base = structuredClone(
    boundary_cases.find((x) => x.id === 'k3-confirmation-complete').input,
  );
  const add = (operation, id, input, expected, change) => {
    const i = structuredClone(input);
    change?.(i);
    boundary_cases.push({ id: 'k4-' + id, operation, input: i, expected });
  };
  const isolated = structuredClone(base),
    row = isolated.evidence.payload.readers[0];
  row.phase = 'isolated';
  row.isolation_enforced = true;
  delete row.selector;
  isolated.expected_selectors[row.reader_id] = null;
  isolated.expected_identities = { [row.reader_id]: row.identity_id };
  const ev = (id, expected, change) =>
    add('transport_confirmation', id, isolated, expected, (i) => {
      change?.(i);
      i.evidence = auth(
        i.evidence.payload,
        'Auriga.NetworkTransportConfirmation.v2',
        runtime,
        'synthetic-runtime',
      );
    });
  ev('isolated-no-selector', 'accept');
  ev(
    'isolated-no-selector-not-enforced',
    'identity_quarantined',
    (i) => (i.evidence.payload.readers[0].isolation_enforced = false),
  );
  ev(
    'isolated-selector-omitted-but-authorized',
    'invalid_chain',
    (i) =>
      (i.expected_selectors[row.reader_id] =
        base.expected_selectors[row.reader_id]),
  );
  ev(
    'isolated-missing-ledger-row',
    'invalid_chain',
    (i) => delete i.expected_selectors[row.reader_id],
  );
  ev(
    'isolated-extra-ledger-row',
    'invalid_chain',
    (i) => (i.expected_selectors.other = null),
  );
  ev(
    'isolated-wrong-identity',
    'invalid_chain',
    (i) => (i.expected_identities[row.reader_id] = 'other'),
  );
  ev(
    'isolated-missing-identity',
    'invalid_chain',
    (i) => delete i.expected_identities,
  );
  const b = base.bundle.payload,
    e = base.evidence.payload;
  const response = {
    ...structuredClone(e),
    generation: b.generation,
    control_digest: digest(base.control),
    expected_selectors: structuredClone(base.expected_selectors),
    reader_identities: { [row.reader_id]: row.identity_id },
  };
  delete response.readers;
  const query = {
    tenant_id: e.tenant_id,
    realm: e.realm,
    bundle_version: e.bundle_version,
    bundle_digest: e.bundle_digest,
    challenge: e.challenge,
    control_revision: e.control_revision,
    control_digest: response.control_digest,
  };
  const input = {
    tenant_id: e.tenant_id,
    realm: e.realm,
    authenticated_c_ledger: true,
    now: base.now,
    bundle: base.bundle,
    control: base.control,
    query,
    response,
  };
  const ledger = (id, expected, change) =>
    add('expected_selectors', id, input, expected, change);
  ledger('ledger-complete', 'accept');
  ledger(
    'ledger-null-unassigned',
    'accept',
    (i) => (i.response.expected_selectors[row.reader_id] = null),
  );
  ledger(
    'ledger-untrusted-source',
    'scope_denied',
    (i) => (i.authenticated_c_ledger = false),
  );
  for (const field of ['tenant_id', 'realm'])
    ledger(
      'ledger-domain-' + field,
      'scope_denied',
      (i) => (i.response[field] = field === 'realm' ? '99' : 'other'),
    );
  for (const field of [
    'generation',
    'bundle_version',
    'phase',
    'authority_id',
    'authority_digest',
    'bundle_digest',
  ])
    ledger(
      'ledger-binding-' + field,
      'invalid_chain',
      (i) =>
        (i.response[field] = field.includes('digest')
          ? 'sha256:' + 'f'.repeat(64)
          : field === 'phase'
            ? 'dual_trust'
            : field === 'authority_id'
              ? 'other'
              : '99'),
    );
  ledger(
    'ledger-control-revision',
    'control_required',
    (i) => (i.response.control_revision = '99'),
  );
  ledger(
    'ledger-control-digest',
    'control_required',
    (i) => (i.response.control_digest = 'sha256:' + 'f'.repeat(64)),
  );
  ledger(
    'ledger-challenge',
    'invalid_chain',
    (i) => (i.response.challenge = 'A'.repeat(43)),
  );
  ledger('ledger-age-60', 'accept', (i) => (i.now = '2026-11-14T00:01:00Z'));
  ledger(
    'ledger-age-61',
    'projection_pending',
    (i) => (i.now = '2026-11-14T00:01:01Z'),
  );
  ledger(
    'ledger-future',
    'projection_pending',
    (i) => (i.now = '2026-11-13T23:59:59Z'),
  );
  ledger(
    'ledger-frozen-set',
    'invalid_chain',
    (i) => (i.response.required_reader_ids = []),
  );
  for (const map of ['expected_selectors', 'reader_identities']) {
    ledger(
      'ledger-missing-' + map,
      'invalid_chain',
      (i) => delete i.response[map][row.reader_id],
    );
    ledger(
      'ledger-extra-' + map,
      'invalid_chain',
      (i) =>
        (i.response[map].other = map === 'expected_selectors' ? null : 'other'),
    );
  }
}
