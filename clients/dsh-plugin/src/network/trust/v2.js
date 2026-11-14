import {
  verifyTransportTransition,
  verifyTransportCatchup,
  verifyTransportConfirmation,
} from './v2-transport.js';
import { createHash, verify } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { publicKey, timeValue } from './keyset.js';
import { detach, jcs, reject, structure } from './v2-schema.js';
export { detach, jcs, reject, structure } from './v2-schema.js';
export {
  verifyTransportTransition,
  verifyTransportCatchup,
  verifyTransportConfirmation,
} from './v2-transport.js';
/** @param {any} value */
export const digest = (value) =>
  'sha256:' + createHash('sha256').update(jcs(value)).digest('hex');
/** @param {any} a @param {any} b */
export const equal = (a, b) => jcs(a) === jcs(b);
/** @param {string} value */
const seconds = (value) => timeValue(value) / 1000;
const purposes = ['business_delivery', 'protocol_response', 'management_state'];
/** @param {any} signed @param {string} domain @param {string} spki */
export function signature(signed, domain, spki) {
  try {
    const auth = signed.root_auth ?? signed.runtime_auth;
    structure('Signature', auth);
    const bytes = Buffer.from(auth.signature, 'base64url');
    if (
      bytes.length !== 64 ||
      bytes.toString('base64url') !== auth.signature ||
      !verify(
        null,
        Buffer.from(domain + '\0' + jcs(signed.payload)),
        publicKey(spki),
        bytes,
      )
    )
      reject('invalid_chain');
  } catch {
    reject('invalid_chain');
  }
}
/** @param {any} signed @param {Map<string,string>} roots @param {string} type @param {string} domain */
export function rootSignature(signed, roots, type, domain) {
  structure(type, signed);
  const root = roots.get(signed.root_auth.kid);
  if (!root) reject('invalid_chain');
  signature(signed, domain, root);
}
/** Scope and monotonic control are independent of publication versions.
 * @param {any} candidate @param {any} context @param {any} [previous] */
export function verifyControl(candidate, context, previous) {
  rootSignature(
    candidate,
    context.roots,
    'SignedRootControl',
    'Auriga.NetworkRootControl.v2',
  );
  const c = candidate.payload;
  if (c.tenant_id !== context.tenant_id || c.realm !== context.realm)
    reject('scope_denied');
  if (
    new Set(c.allowed_authorities.map((/** @type {any} */ x) => x.authority_id))
      .size !== c.allowed_authorities.length ||
    new Set(c.revoked_authority_ids).size !== c.revoked_authority_ids.length ||
    new Set(c.revoked_installation_ids).size !==
      c.revoked_installation_ids.length ||
    c.allowed_authorities.some((/** @type {any} */ a) =>
      c.revoked_authority_ids.includes(a.authority_id),
    )
  )
    reject('invalid_chain');
  if (previous) {
    const p = previous.payload;
    if (BigInt(c.control_revision) < BigInt(p.control_revision))
      reject('control_rollback');
    if (
      c.control_revision === p.control_revision &&
      !equal(candidate, previous)
    )
      reject('same_version_conflict');
    if (
      p.revoked_authority_ids.some(
        (/** @type {string} */ id) => !c.revoked_authority_ids.includes(id),
      ) ||
      p.revoked_installation_ids.some(
        (/** @type {string} */ id) => !c.revoked_installation_ids.includes(id),
      ) ||
      BigInt(c.minimum_generation) < BigInt(p.minimum_generation) ||
      (c.minimum_generation === p.minimum_generation &&
        BigInt(c.minimum_keyset_version) < BigInt(p.minimum_keyset_version))
    )
      reject('control_rollback');
  }
  return candidate;
}
/** @param {any} authority @param {any} c */
function allowed(authority, c) {
  const a = authority.payload;
  if (c.revoked_authority_ids.includes(a.authority_id))
    reject('authority_revoked');
  if (
    !c.allowed_authorities.some(
      (/** @type {any} */ entry) =>
        entry.authority_id === a.authority_id &&
        entry.authority_digest === digest(authority),
    )
  )
    reject('authority_unknown');
}
/** Authenticate a snapshot, including an expired predecessor used only as proof.
 * @param {any} p @param {any} context @param {any} latest @param {boolean} historical */
function chain(p, context, latest, historical) {
  rootSignature(
    p.control,
    context.roots,
    'SignedRootControl',
    'Auriga.NetworkRootControl.v2',
  );
  rootSignature(
    p.authority,
    context.roots,
    'SignedRuntimeAuthority',
    'Auriga.NetworkRuntimeAuthority.v2',
  );
  const k = p.keyset.payload,
    a = p.authority.payload,
    c = p.control.payload;
  allowed(p.authority, historical ? c : latest);
  if (k.control_revision !== c.control_revision) reject('invalid_chain');
  if (
    !historical &&
    BigInt(k.control_revision) > BigInt(latest.control_revision)
  )
    reject('control_required');
  if (
    !historical &&
    BigInt(latest.control_revision) <
      BigInt(context.seen_control_revision ?? '0')
  )
    reject('control_rollback');
  if (
    a.expiry_policy !== 'no_hard_expiry' ||
    !a.capabilities.includes('keyset') ||
    (k.mode === 'restart_recovery' &&
      !a.capabilities.includes('restart_recovery'))
  )
    reject('scope_denied');
  if (
    [a, c, k].some(
      (x) => x.tenant_id !== context.tenant_id || x.realm !== context.realm,
    ) ||
    k.authority_id !== a.authority_id ||
    k.authority_digest !== digest(p.authority) ||
    seconds(a.not_before) > seconds(historical ? k.issued_at : context.now)
  )
    reject('scope_denied');
  if (
    historical &&
    (BigInt(k.generation) < BigInt(c.minimum_generation) ||
      BigInt(k.keyset_version) < BigInt(c.minimum_keyset_version))
  )
    reject('invalid_chain');
  if (p.keyset.runtime_auth.kid !== a.runtime_kid) reject('scope_denied');
}
/** Pure production consumer policy; all local facts are supplied by the durable
 * store, never by the received publication. @param {any} context */
export function verifyPublication(context) {
  const p = detach(context.publication);
  // Structural failure is rejected even when signatures happen to be valid.
  // Authority forbidden-field failures have the contract's scope error.
  structure('SignedRuntimeAuthority', p.authority, 'scope_denied');
  structure('Publication', p);
  rootSignature(
    context.latest_control,
    context.roots,
    'SignedRootControl',
    'Auriga.NetworkRootControl.v2',
  );
  const c = context.latest_control.payload,
    k = p.keyset.payload,
    a = p.authority.payload;
  chain(p, context, c, false);
  if (c.tenant_id !== context.tenant_id || c.realm !== context.realm)
    reject('scope_denied');
  if (
    k.generation !== c.minimum_generation ||
    BigInt(k.keyset_version) < BigInt(c.minimum_keyset_version) ||
    BigInt(k.generation) < BigInt(context.seen_generation ?? '0') ||
    (k.generation === context.seen_generation &&
      BigInt(k.keyset_version) < BigInt(context.seen_version))
  )
    reject('version_rollback');
  if (
    k.generation === context.seen_generation &&
    k.keyset_version === context.seen_version &&
    digest(p.keyset) !== context.seen_digest
  )
    reject('same_version_conflict');
  signature(p.keyset, 'Auriga.NetworkKeyset.v2', a.public_key_spki);
  const issued = seconds(k.issued_at),
    now = seconds(context.now);
  if (
    !Number.isFinite(now) ||
    issued > now + 60 ||
    seconds(k.valid_until) <= now ||
    seconds(k.valid_until) <= issued ||
    seconds(k.valid_until) - issued > a.limits.keyset_seconds
  )
    reject('invalid_transition');
  if (!['bootstrap', 'migration'].includes(k.mode) && !p.predecessor)
    reject('predecessor_required');
  if (k.mode === 'bootstrap' && context.has_history)
    reject('invalid_transition');
  if (k.mode === 'migration' && !context.migration_authorized)
    reject('invalid_transition');
  let previous = [];
  const proof = p.predecessor;
  if (proof) {
    const q = proof.keyset.payload;
    if (
      digest(proof.keyset) !== proof.keyset_digest ||
      (q.generation === k.generation &&
        BigInt(q.keyset_version) >= BigInt(k.keyset_version))
    )
      reject('invalid_transition');
    try {
      chain(proof, context, c, true);
    } catch {
      reject('invalid_chain');
    }
    signature(
      proof.keyset,
      'Auriga.NetworkKeyset.v2',
      proof.authority.payload.public_key_spki,
    );
    const qa = proof.authority.payload;
    if (
      seconds(q.valid_until) <= seconds(q.issued_at) ||
      seconds(q.valid_until) - seconds(q.issued_at) > qa.limits.keyset_seconds
    )
      reject('invalid_chain');
    const revoked = c.revoked_authority_ids.includes(qa.authority_id);
    if (revoked || q.generation !== k.generation) {
      const t = c.generation_transition;
      if (
        k.mode !== 'authority_transition' ||
        !t ||
        t.predecessor_digest !== proof.keyset_digest ||
        t.from_generation !== q.generation ||
        t.to_generation !== k.generation ||
        t.authority_id !== a.authority_id ||
        t.authority_digest !== digest(p.authority) ||
        t.first_version !== k.keyset_version ||
        t.first_version !== c.minimum_keyset_version ||
        BigInt(k.generation) <= BigInt(q.generation)
      )
        reject('invalid_transition');
      if (
        revoked &&
        (q.keys
          .filter((/** @type {any} */ x) =>
            ['active', 'next'].includes(x.state),
          )
          .some(
            (/** @type {any} */ old) =>
              !k.keys.some(
                (/** @type {any} */ x) =>
                  x.kid === old.kid && x.state === 'revoked',
              ),
          ) ||
          k.keys
            .filter((/** @type {any} */ x) => x.state === 'active')
            .some((/** @type {any} */ x) =>
              q.keys.some((/** @type {any} */ old) => old.kid === x.kid),
            ))
      )
        reject('invalid_transition');
    } else if (
      k.mode === 'authority_transition' &&
      qa.authority_id === a.authority_id
    )
      reject('invalid_transition');
    if (
      context.previous_digest &&
      context.previous_digest !== proof.keyset_digest
    )
      reject('invalid_transition');
    previous = q.keys;
  }
  const known = new Map(
    (context.history ?? []).map((/** @type {any} */ x) => [x.kid, x]),
  );
  for (const key of previous) {
    const old = known.get(key.kid);
    if (
      old &&
      ['public_key_spki', 'purpose', 'not_before', 'not_after'].some(
        (f) => old[f] !== key[f],
      )
    )
      reject('invalid_transition');
    if (
      [...known.values()].some(
        (/** @type {any} */ old) =>
          old.kid !== key.kid && old.public_key_spki === key.public_key_spki,
      )
    )
      reject('invalid_transition');
  }
  const ledger = new Map(
    (context.first_publication_ledger ?? []).map((/** @type {any} */ x) => [
      x.kid,
      x,
    ]),
  );
  for (const snapshot of [proof?.keyset.payload, k].filter(Boolean)) {
    const seen = new Set();
    for (const first of snapshot.first_publications) {
      if (
        seen.has(first.kid) ||
        (ledger.has(first.kid) && !equal(ledger.get(first.kid), first)) ||
        BigInt(first.committed_version) > BigInt(snapshot.keyset_version) ||
        seconds(first.committed_at) > seconds(snapshot.issued_at)
      )
        reject('invalid_transition');
      seen.add(first.kid);
      ledger.set(first.kid, first);
    }
  }
  const newKeys = k.keys.filter(
    (/** @type {any} */ x) =>
      !previous.some((/** @type {any} */ old) => old.kid === x.kid),
  );
  if (newKeys.length && !a.capabilities.includes('business_rotation'))
    reject('scope_denied');
  const removed = [...previous, ...known.values()].filter(
    (/** @type {any} */ x) =>
      !k.keys.some((/** @type {any} */ y) => y.kid === x.kid),
  );
  if (removed.length && !a.capabilities.includes('history_archive'))
    reject('scope_denied');
  if (
    removed.length &&
    (seconds(k.archive.before) > issued - a.limits.archive_grace_seconds ||
      removed.some(
        (/** @type {any} */ x) =>
          seconds(x.not_after) > seconds(k.archive.before),
      ))
  )
    reject('invalid_transition');
  for (const key of newKeys) {
    const first = k.first_publications.find(
      (/** @type {any} */ x) => x.kid === key.kid,
    );
    if (!first) reject('invalid_transition');
    // Root-permitted migration preserves existing PG first-publication facts.
    if (
      k.mode === 'migration' &&
      context.migration_authorized &&
      known.has(key.kid)
    )
      continue;
    if (
      !first ||
      first.committed_version !== k.keyset_version ||
      first.committed_at !== k.issued_at
    )
      reject('invalid_transition');
  }
  const kids = new Set(),
    material = new Set();
  for (const key of k.keys) {
    publicKey(key.public_key_spki);
    if (
      kids.has(key.kid) ||
      material.has(key.public_key_spki) ||
      key.public_key_spki === a.public_key_spki ||
      [...context.roots.values()].includes(key.public_key_spki)
    )
      reject('invalid_transition');
    kids.add(key.kid);
    material.add(key.public_key_spki);
    if (
      seconds(key.not_after) <= seconds(key.not_before) ||
      seconds(key.not_after) - seconds(key.not_before) >
        a.limits.business_key_seconds
    )
      reject('invalid_transition');
    const old = previous.find((/** @type {any} */ x) => x.kid === key.kid);
    for (const prior of [old, known.get(key.kid)].filter(Boolean)) {
      if (
        ['public_key_spki', 'purpose', 'not_before', 'not_after'].some(
          (f) => prior[f] !== key[f],
        ) ||
        (prior.state === 'revoked' && key.state !== 'revoked') ||
        (prior.state === 'retired' &&
          !['retired', 'revoked'].includes(key.state)) ||
        (prior.state === 'active' && key.state === 'next')
      )
        reject('invalid_transition');
    }
    if (
      [...known.values()].some(
        (/** @type {any} */ old) =>
          old.kid !== key.kid && old.public_key_spki === key.public_key_spki,
      )
    )
      reject('invalid_transition');
  }
  let recovered = false;
  for (const purpose of purposes) {
    const active = k.keys.filter(
      (/** @type {any} */ x) => x.purpose === purpose && x.state === 'active',
    );
    if (active.length !== 1) reject('invalid_transition');
    const current = active[0];
    const valid = (/** @type {any} */ x) =>
      seconds(x.not_before) <= issued && seconds(x.not_after) > issued;
    if (!valid(current)) reject('invalid_transition');
    const old = previous.find(
      (/** @type {any} */ x) => x.purpose === purpose && x.state === 'active',
    );
    const mature = previous.find((/** @type {any} */ x) => {
      const first = proof?.keyset.payload.first_publications.find(
        (/** @type {any} */ f) => f.kid === x.kid,
      );
      return (
        x.purpose === purpose &&
        x.state === 'next' &&
        valid(x) &&
        first &&
        seconds(first.committed_at) <= issued - a.limits.next_maturity_seconds
      );
    });
    if (
      (k.mode === 'refresh' ||
        (k.mode === 'authority_transition' &&
          proof.keyset.payload.generation === k.generation &&
          !c.revoked_authority_ids.includes(
            proof.authority.payload.authority_id,
          ))) &&
      old?.kid !== current.kid
    )
      reject('invalid_transition');
    if (
      k.mode === 'activate' &&
      old?.kid !== current.kid &&
      (!mature ||
        mature.kid !== current.kid ||
        (old &&
          valid(old) &&
          seconds(old.not_after) - issued < a.limits.overlap_seconds))
    )
      reject('invalid_transition');
    if (k.mode === 'restart_recovery') {
      for (const next of previous.filter(
        (/** @type {any} */ x) => x.purpose === purpose && x.state === 'next',
      )) {
        const first = proof.keyset.payload.first_publications.find(
          (/** @type {any} */ f) => f.kid === next.kid,
        );
        if (!first) reject('invalid_transition');
        if (
          seconds(first.committed_at) >
          issued - a.limits.next_maturity_seconds
        ) {
          const terminal = k.keys.find(
            (/** @type {any} */ x) => x.kid === next.kid,
          );
          if (!terminal || !['retired', 'revoked'].includes(terminal.state))
            reject('invalid_transition');
        }
      }
      if (old && valid(old)) {
        if (old.kid !== current.kid) reject('invalid_transition');
      } else {
        recovered = true;
        if (mature) {
          if (current.kid !== mature.kid) reject('invalid_transition');
          continue;
        }
        if (
          previous.some((/** @type {any} */ x) => x.kid === current.kid) ||
          seconds(current.not_before) !== issued ||
          seconds(current.not_after) - issued > a.limits.restart_key_seconds
        )
          reject('invalid_transition');
      }
    }
  }
  if (k.mode === 'restart_recovery' && !recovered) reject('invalid_transition');
  if (context.transport) verifyTransport(context.transport, p, context);
  return p;
}
/** @param {any} bundle @param {any} publication @param {any} context */
export function verifyTransport(bundle, publication, context) {
  const confirmation = context.transport_confirmation && {
    ...context.transport_confirmation,
    root_spki: context.roots.get(context.latest_control.root_auth.kid),
    control: context.latest_control,
    authority: publication.authority,
    tenant_id: context.tenant_id,
    realm: context.realm,
    now: context.now,
  };
  // Proof data carries signed snapshots; installation facts always come from
  // the caller's trusted context, never from the received proof.
  if (context.transport_transition || context.transport_catchup) {
    const catchup = context.transport_catchup;
    const proof = catchup?.transition ?? context.transport_transition;
    const transition = {
      ...proof,
      root_spki: context.roots.get(proof.latest_control.root_auth.kid),
      tenant_id: context.tenant_id,
      realm: context.realm,
      seen_generation: catchup ? undefined : context.seen_transport_generation,
      seen_bundle_version: catchup ? undefined : context.seen_bundle_version,
      seen_bundle_digest: catchup ? undefined : context.seen_bundle_digest,
      isolation_enforced: context.isolation_enforced,
    };
    if (!equal(proof.authority, publication.authority)) reject('invalid_chain');
    if (catchup) {
      const predecessor = proof.predecessor.bundle;
      if (
        context.seen_transport_generation === predecessor.payload.generation &&
        context.seen_bundle_version === predecessor.payload.bundle_version &&
        context.seen_bundle_digest !== digest(predecessor)
      )
        reject('invalid_transition');
      if (
        context.seen_transport_generation &&
        BigInt(bundle.payload.generation) <
          BigInt(context.seen_transport_generation)
      )
        reject('version_rollback');
      verifyTransportCatchup({
        ...catchup,
        transition,
        current: bundle,
        control: context.latest_control,
        seen_version:
          !context.seen_transport_generation ||
          context.seen_transport_generation === bundle.payload.generation
            ? context.seen_bundle_version
            : undefined,
        seen_digest: context.seen_bundle_digest,
        confirmation,
      });
    } else {
      if (
        !equal(proof.latest_control, context.latest_control) ||
        !equal(proof.transport, bundle)
      )
        reject('invalid_chain');
      verifyTransportTransition(transition);
    }
    return;
  }
  if (confirmation) {
    verifyTransportConfirmation(confirmation);
    if (digest(confirmation.bundle) !== bundle.payload.previous_bundle_digest)
      reject('invalid_chain');
    const phase = bundle.payload.phase;
    if (
      confirmation.bundle.payload.phase !==
      (phase === 'new_leaves' ? 'dual_trust' : 'new_leaves')
    )
      reject('invalid_transition');
    if (
      confirmation.required_phase !==
      (phase === 'new_leaves' ? 'loaded' : 'handshake_verified')
    )
      reject('projection_pending');
  }

  const a = publication.authority.payload,
    t = bundle.payload;
  if (!a.capabilities.includes('network_transport')) reject('scope_denied');
  structure('SignedTransportTrustBundle', bundle);
  signature(bundle, 'Auriga.NetworkTransportTrustBundle.v2', a.public_key_spki);
  if (
    t.authority_digest !== digest(publication.authority) ||
    t.scope.some(
      (/** @type {any} */ s) =>
        !a.transport_scope.some((/** @type {any} */ allowed) =>
          equal(s, allowed),
        ),
    )
  )
    reject('scope_denied');
  if (BigInt(t.bundle_version) <= BigInt(context.seen_bundle_version))
    reject('version_rollback');
  const phases = ['dual_trust', 'new_leaves', 'retire_old'];
  const cycle =
    context.previous_transport_phase === 'retire_old' &&
    t.phase === 'dual_trust';
  if (cycle) {
    const previous = context.previous_transport;
    if (!previous) reject('invalid_transition');
    structure('SignedTransportTrustBundle', previous);
    signature(
      previous,
      'Auriga.NetworkTransportTrustBundle.v2',
      a.public_key_spki,
    );
    if (
      [
        'tenant_id',
        'realm',
        'authority_id',
        'authority_digest',
        'generation',
      ].some((f) => previous.payload[f] !== t[f]) ||
      previous.payload.phase !== 'retire_old' ||
      previous.payload.bundle_version !== context.seen_bundle_version ||
      digest(previous) !== t.previous_bundle_digest
    )
      reject('invalid_transition');
    if (
      !previous.payload.cas.every((/** @type {any} */ old) =>
        t.cas.some((/** @type {any} */ ca) => equal(ca, old)),
      ) ||
      !t.cas.some(
        (/** @type {any} */ ca) =>
          !previous.payload.cas.some(
            (/** @type {any} */ old) => old.ca_id === ca.ca_id,
          ),
      )
    )
      reject('invalid_transition');
  }
  if (
    context.previous_transport_phase &&
    !cycle &&
    phases.indexOf(t.phase) !==
      phases.indexOf(context.previous_transport_phase) + 1
  )
    reject('invalid_transition');
  if (
    (t.phase === 'new_leaves' && !context.readers_persisted) ||
    (t.phase === 'retire_old' && !context.handshakes_verified)
  )
    reject('projection_pending');
}
/** Announcement authentication does not require a usable current keyset.
 * @param {any} value @param {any} authority @param {any} control @param {any} context */
export function verifyAnnouncement(value, authority, control, context) {
  const n = detach(value);
  structure('SignedKeysetAnnouncementV2', n);
  rootSignature(
    authority,
    context.roots,
    'SignedRuntimeAuthority',
    'Auriga.NetworkRuntimeAuthority.v2',
  );
  allowed(authority, control.payload);
  const a = authority.payload,
    p = n.payload;
  signature(n, 'Auriga.NetworkKeysetAnnouncement.v2', a.public_key_spki);
  if (
    !a.capabilities.includes('announcement') ||
    n.runtime_auth.kid !== a.runtime_kid ||
    p.tenant_id !== context.tenant_id ||
    p.realm !== context.realm ||
    p.authority_id !== a.authority_id ||
    p.authority_digest !== digest(authority)
  )
    reject('scope_denied');
  if (BigInt(p.control_revision) > BigInt(control.payload.control_revision))
    reject('control_required');
  if (
    p.generation !== control.payload.minimum_generation ||
    BigInt(p.keyset_version) < BigInt(control.payload.minimum_keyset_version)
  )
    reject('version_rollback');
  return n;
}
