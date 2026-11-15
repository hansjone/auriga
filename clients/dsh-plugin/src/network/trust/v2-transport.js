// K3 transport verification. Local scope, roots, high-water marks, challenges
// and selector commitments must come from trusted installation/durable ports.
// Policy follows contracts/network/auto-renewal-v2-k3.md; no test oracle imports.
import { digest, jcs, signature, reject as fail, structure } from './v2.js';

/** @param {any} i */
export function verifyTransportTransition(i) {
  structure('SignedRootControl', i.latest_control);
  structure('SignedRuntimeAuthority', i.authority);
  structure('TransportPredecessor', i.predecessor);
  structure('SignedTransportTrustBundle', i.transport);
  const c = i.latest_control.payload,
    a = i.authority.payload,
    p = i.predecessor.bundle.payload,
    t = i.transport.payload;
  signature(i.latest_control, 'Auriga.NetworkRootControl.v2', i.root_spki);
  signature(i.authority, 'Auriga.NetworkRuntimeAuthority.v2', i.root_spki);
  signature(i.predecessor.control, 'Auriga.NetworkRootControl.v2', i.root_spki);
  signature(
    i.predecessor.authority,
    'Auriga.NetworkRuntimeAuthority.v2',
    i.root_spki,
  );
  signature(
    i.predecessor.bundle,
    'Auriga.NetworkTransportTrustBundle.v2',
    i.predecessor.authority.payload.public_key_spki,
  );
  signature(
    i.transport,
    'Auriga.NetworkTransportTrustBundle.v2',
    a.public_key_spki,
  );
  for (const o of [
    c,
    a,
    p,
    t,
    i.predecessor.control.payload,
    i.predecessor.authority.payload,
  ]) {
    if (o.tenant_id !== i.tenant_id) fail('scope_denied');
    if (o.realm !== i.realm) fail('scope_denied');
  }
  if (t.authority_id !== a.authority_id) fail('invalid_chain');
  if (t.authority_digest !== digest(i.authority)) fail('invalid_chain');
  if (i.transport.runtime_auth.kid !== a.runtime_kid) fail('invalid_chain');
  if (p.authority_id !== i.predecessor.authority.payload.authority_id)
    fail('invalid_chain');
  if (p.authority_digest !== digest(i.predecessor.authority))
    fail('invalid_chain');
  if (
    !i.predecessor.control.payload.allowed_authorities.some(
      (/** @type {any} */ x) =>
        x.authority_id === p.authority_id &&
        x.authority_digest === p.authority_digest,
    )
  )
    fail('authority_unknown');
  if (
    i.predecessor.control.payload.revoked_authority_ids.includes(p.authority_id)
  )
    fail('authority_revoked');
  if (
    !c.allowed_authorities.some(
      (/** @type {any} */ x) =>
        x.authority_id === t.authority_id &&
        x.authority_digest === t.authority_digest,
    )
  )
    fail('authority_unknown');
  if (c.revoked_authority_ids.includes(t.authority_id))
    fail('authority_revoked');
  if (BigInt(t.control_revision) > BigInt(c.control_revision))
    fail('control_required');
  if (!a.capabilities.includes('network_transport')) fail('scope_denied');
  if (Date.parse(a.not_before) > Date.parse(t.issued_at)) fail('scope_denied');
  if (
    t.scope.some(
      (/** @type {any} */ s) =>
        !a.transport_scope.some((/** @type {any} */ x) => jcs(x) === jcs(s)),
    )
  )
    fail('scope_denied');
  if (BigInt(t.generation) < BigInt(c.minimum_generation))
    fail('version_rollback');
  if (digest(i.predecessor.bundle) !== t.previous_bundle_digest)
    fail('invalid_transition');
  if (
    i.seen_bundle_digest &&
    i.seen_generation === p.generation &&
    i.seen_bundle_version === p.bundle_version &&
    i.seen_bundle_digest !== digest(i.predecessor.bundle)
  )
    fail('invalid_transition');
  if (
    i.seen_bundle_version &&
    i.seen_generation === p.generation &&
    BigInt(i.seen_bundle_version) > BigInt(p.bundle_version)
  )
    fail('version_rollback');
  if (i.seen_generation && BigInt(t.generation) < BigInt(i.seen_generation))
    fail('version_rollback');
  if (
    i.predecessor.bundle.runtime_auth.kid !==
    i.predecessor.authority.payload.runtime_kid
  )
    fail('invalid_chain');
  const anchor = c.transport_transition;
  if (!anchor) fail('control_required');
  for (const [field, value] of Object.entries({
    predecessor_digest: digest(i.predecessor.bundle),
    from_generation: p.generation,
    to_generation: t.generation,
    authority_id: t.authority_id,
    authority_digest: t.authority_digest,
    first_bundle_version: t.bundle_version,
  })) {
    if (anchor[field] !== value) fail('invalid_transition');
  }
  if (t.phase !== 'dual_trust') fail('invalid_transition');
  if (anchor.mode === 'authority_transition') {
    if (t.generation !== p.generation) fail('invalid_transition');
    if (BigInt(t.bundle_version) <= BigInt(p.bundle_version))
      fail('version_rollback');
    if (p.phase !== 'retire_old') fail('invalid_transition');
    if (c.revoked_authority_ids.includes(p.authority_id))
      fail('authority_revoked');
    if (
      !c.allowed_authorities.some(
        (/** @type {any} */ x) =>
          x.authority_id === p.authority_id &&
          x.authority_digest === p.authority_digest,
      )
    )
      fail('authority_unknown');
    if (jcs(t.scope) !== jcs(p.scope)) fail('scope_denied');
    if (jcs(t.required_reader_ids) !== jcs(p.required_reader_ids))
      fail('invalid_transition');
    if (
      !p.cas.every((/** @type {any} */ old) =>
        t.cas.some((/** @type {any} */ ca) => jcs(ca) === jcs(old)),
      )
    )
      fail('invalid_transition');
    if (
      !t.cas.some(
        (/** @type {any} */ ca) =>
          !p.cas.some((/** @type {any} */ old) => old.ca_id === ca.ca_id),
      )
    )
      fail('invalid_transition');
  } else {
    if (BigInt(t.generation) <= BigInt(p.generation)) fail('version_rollback');
    if (!c.revoked_authority_ids.includes(p.authority_id))
      fail('authority_revoked');
    if (
      t.cas.some((/** @type {any} */ ca) =>
        p.cas.some(
          (/** @type {any} */ old) =>
            old.ca_id === ca.ca_id ||
            old.certificate_der === ca.certificate_der,
        ),
      )
    )
      fail('invalid_transition');
    if (!i.isolation_enforced) fail('identity_quarantined');
  }
}

/** @param {any} i */
export function verifyTransportCatchup(i) {
  structure('SignedRootControl', i.control);
  structure('SignedTransportTrustBundle', i.current);
  verifyTransportTransition(i.transition);
  const start = i.transition.transport.payload,
    current = i.current.payload,
    control = i.control.payload,
    a = i.transition.authority.payload;
  signature(i.control, 'Auriga.NetworkRootControl.v2', i.transition.root_spki);
  signature(
    i.current,
    'Auriga.NetworkTransportTrustBundle.v2',
    a.public_key_spki,
  );
  for (const o of [control, current]) {
    if (o.tenant_id !== i.transition.tenant_id) fail('scope_denied');
    if (o.realm !== i.transition.realm) fail('scope_denied');
  }
  if (
    BigInt(control.control_revision) <
    BigInt(i.transition.latest_control.payload.control_revision)
  )
    fail('control_rollback');
  if (control.revoked_authority_ids.includes(a.authority_id))
    fail('authority_revoked');
  if (
    !control.allowed_authorities.some(
      (/** @type {any} */ x) =>
        x.authority_id === a.authority_id &&
        x.authority_digest === digest(i.transition.authority),
    )
  )
    fail('authority_unknown');
  for (const field of ['authority_id', 'authority_digest', 'generation']) {
    if (current[field] !== start[field]) fail('invalid_chain');
  }
  if (BigInt(current.control_revision) > BigInt(control.control_revision))
    fail('control_required');
  if (BigInt(current.generation) < BigInt(control.minimum_generation))
    fail('version_rollback');
  if (BigInt(current.bundle_version) < BigInt(start.bundle_version))
    fail('version_rollback');
  if (i.seen_version && BigInt(current.bundle_version) < BigInt(i.seen_version))
    fail('version_rollback');
  if (
    current.bundle_version === i.seen_version &&
    digest(i.current) !== i.seen_digest
  )
    fail('same_version_conflict');
  if (
    current.scope.some(
      (/** @type {any} */ s) =>
        !a.transport_scope.some((/** @type {any} */ x) => jcs(x) === jcs(s)),
    )
  )
    fail('scope_denied');
  if (i.current.runtime_auth.kid !== a.runtime_kid) fail('invalid_chain');
  if (Date.parse(a.not_before) > Date.parse(current.issued_at))
    fail('scope_denied');
  if (current.phase !== 'dual_trust') {
    try {
      if (
        !i.confirmation ||
        jcs(i.confirmation.control) !== jcs(i.control) ||
        jcs(i.confirmation.authority) !== jcs(i.transition.authority) ||
        i.confirmation.root_spki !== i.transition.root_spki ||
        i.confirmation.tenant_id !== i.transition.tenant_id ||
        i.confirmation.realm !== i.transition.realm
      )
        fail('invalid_chain');
      verifyTransportConfirmation(i.confirmation);
    } catch {
      fail('projection_pending');
    }
    if (digest(i.confirmation.bundle) !== current.previous_bundle_digest)
      fail('invalid_chain');
    const expected =
      current.phase === 'new_leaves' ? 'dual_trust' : 'new_leaves';
    if (i.confirmation.bundle.payload.phase !== expected)
      fail('invalid_transition');
    if (
      i.confirmation.required_phase !==
      (current.phase === 'new_leaves' ? 'loaded' : 'handshake_verified')
    )
      fail('projection_pending');
  }
}

/** @param {any} i */
export function verifyTransportConfirmation(i) {
  structure('SignedRootControl', i.control);
  structure('SignedRuntimeAuthority', i.authority);
  structure('SignedTransportTrustBundle', i.bundle);
  structure('SignedTransportConfirmation', i.evidence);
  const e = i.evidence.payload,
    b = i.bundle.payload,
    a = i.authority.payload,
    c = i.control.payload;
  signature(i.control, 'Auriga.NetworkRootControl.v2', i.root_spki);
  signature(i.authority, 'Auriga.NetworkRuntimeAuthority.v2', i.root_spki);
  signature(
    i.bundle,
    'Auriga.NetworkTransportTrustBundle.v2',
    a.public_key_spki,
  );
  signature(
    i.evidence,
    'Auriga.NetworkTransportConfirmation.v2',
    a.public_key_spki,
  );
  if (
    !c.allowed_authorities.some(
      (/** @type {any} */ x) =>
        x.authority_id === a.authority_id &&
        x.authority_digest === digest(i.authority),
    )
  )
    fail('authority_unknown');
  if (c.revoked_authority_ids.includes(a.authority_id))
    fail('authority_revoked');
  if (!a.capabilities.includes('network_transport')) fail('scope_denied');
  for (const o of [a, b, c]) {
    if (o.tenant_id !== i.tenant_id) fail('scope_denied');
    if (o.realm !== i.realm) fail('scope_denied');
  }
  if (b.authority_id !== a.authority_id) fail('invalid_chain');
  if (b.authority_digest !== digest(i.authority)) fail('invalid_chain');
  if (i.evidence.runtime_auth.kid !== a.runtime_kid) fail('invalid_chain');
  if (e.authority_digest !== digest(i.authority)) fail('invalid_chain');
  if (e.authority_id !== a.authority_id) fail('invalid_chain');
  if (e.control_revision !== c.control_revision) fail('control_required');
  if (e.bundle_digest !== digest(i.bundle)) fail('invalid_chain');
  for (const field of [
    'tenant_id',
    'realm',
    'bundle_version',
    'phase',
    'required_reader_ids',
  ]) {
    if (jcs(e[field]) !== jcs(b[field])) fail('invalid_chain');
  }
  if (e.challenge !== i.challenge) fail('invalid_chain');
  if (
    !Number.isFinite(Date.parse(i.now)) ||
    Date.parse(e.issued_at) > Date.parse(i.now)
  )
    fail('projection_pending');
  if (Date.parse(i.now) - Date.parse(e.issued_at) > 60000)
    fail('projection_pending');
  if (
    new Set(e.readers.map((/** @type {any} */ x) => x.reader_id)).size !==
    e.readers.length
  )
    fail('invalid_chain');
  if (e.readers.length !== b.required_reader_ids.length)
    fail('projection_pending');
  for (const id of b.required_reader_ids) {
    const row = e.readers.find((/** @type {any} */ x) => x.reader_id === id);
    if (!row) fail('projection_pending');
    if (row.phase === 'isolated') {
      if (!row.isolation_enforced) fail('identity_quarantined');
    } else {
      if (row.selector.installation_id !== row.identity_id)
        fail('invalid_chain');
      if (row.selector.bundle_digest !== e.bundle_digest) fail('invalid_chain');
      if (row.selector.bundle_version !== e.bundle_version)
        fail('invalid_chain');
      if (i.expected_selectors[id] !== digest(row.selector))
        fail('invalid_chain');
      if (
        i.required_phase === 'handshake_verified' &&
        row.phase !== 'handshake_verified'
      )
        fail('projection_pending');
    }
  }
  if (i.bundle.runtime_auth.kid !== a.runtime_kid) fail('invalid_chain');
  if (Date.parse(a.not_before) > Date.parse(i.now)) fail('scope_denied');
  if (
    b.scope.some(
      (/** @type {any} */ scope) =>
        !a.transport_scope.some(
          (/** @type {any} */ allowed) => jcs(scope) === jcs(allowed),
        ),
    )
  )
    fail('scope_denied');
  if (BigInt(b.control_revision) > BigInt(c.control_revision))
    fail('control_required');
  if (BigInt(b.generation) < BigInt(c.minimum_generation))
    fail('version_rollback');
}
