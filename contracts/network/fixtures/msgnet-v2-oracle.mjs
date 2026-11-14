// 仅供共享向量的契约测试，不可被生产代码导入。
import { createHash, createPublicKey, verify } from 'node:crypto';
export const jcs = (x) =>
  JSON.stringify(
    x instanceof Array
      ? x.map((v) => JSON.parse(jcs(v)))
      : x && typeof x === 'object'
        ? Object.fromEntries(
            Object.keys(x)
              .sort()
              .map((k) => [k, JSON.parse(jcs(x[k]))]),
          )
        : x,
  );
export const digest = (x) =>
  'sha256:' + createHash('sha256').update(jcs(x)).digest('hex');
export const purposes = [
  'business_delivery',
  'protocol_response',
  'management_state',
];
const seconds = (s) => Date.parse(s) / 1000;
const err = (x) => {
  throw Error(x);
};
export function evaluate(v) {
  try {
    const { input: i } = v,
      p = i.publication,
      k = p.keyset.payload,
      a = p.authority.payload,
      c = i.latest_control.payload;
    const check = (signed, domain, key) => {
      const auth = signed.root_auth ?? signed.runtime_auth;
      if (
        auth.alg !== 'Ed25519' ||
        !verify(
          null,
          Buffer.from(domain + '\0' + jcs(signed.payload)),
          createPublicKey({
            key: Buffer.from(key, 'base64url'),
            format: 'der',
            type: 'spki',
          }),
          Buffer.from(auth.signature, 'base64url'),
        )
      )
        err('invalid_chain');
    };
    check(i.latest_control, 'Auriga.NetworkRootControl.v2', i.root_spki);
    check(p.control, 'Auriga.NetworkRootControl.v2', i.root_spki);
    check(p.authority, 'Auriga.NetworkRuntimeAuthority.v2', i.root_spki);
    if (c.revoked_authority_ids.includes(a.authority_id))
      err('authority_revoked');
    if (
      !c.allowed_authorities.some(
        (x) =>
          x.authority_id === a.authority_id &&
          x.authority_digest === digest(p.authority),
      )
    )
      err('authority_unknown');
    if (k.control_revision !== p.control.payload.control_revision)
      err('invalid_chain');
    if (BigInt(k.control_revision) > BigInt(c.control_revision))
      err('control_required');
    if (BigInt(c.control_revision) < BigInt(i.seen_control_revision))
      err('control_rollback');
    if (
      a.expiry_policy !== 'no_hard_expiry' ||
      'not_after' in a ||
      'delegate' in a ||
      !a.capabilities.includes('keyset') ||
      (k.mode === 'restart_recovery' &&
        !a.capabilities.includes('restart_recovery'))
    )
      err('scope_denied');
    if (
      a.tenant_id !== k.tenant_id ||
      a.realm !== k.realm ||
      c.tenant_id !== k.tenant_id ||
      c.realm !== k.realm ||
      k.tenant_id !== i.tenant_id ||
      k.realm !== i.realm ||
      k.authority_id !== a.authority_id ||
      k.authority_digest !== digest(p.authority) ||
      seconds(a.not_before) > seconds(i.now)
    )
      err('scope_denied');
    if (
      k.generation !== c.minimum_generation ||
      BigInt(k.keyset_version) < BigInt(c.minimum_keyset_version)
    )
      err('version_rollback');
    if (
      BigInt(k.generation) < BigInt(i.seen_generation) ||
      (k.generation === i.seen_generation &&
        BigInt(k.keyset_version) < BigInt(i.seen_version))
    )
      err('version_rollback');
    if (
      k.generation === i.seen_generation &&
      k.keyset_version === i.seen_version &&
      digest(p.keyset) !== i.seen_digest
    )
      err('same_version_conflict');
    if (p.keyset.runtime_auth.kid !== a.runtime_kid) err('scope_denied');
    check(p.keyset, 'Auriga.NetworkKeyset.v2', a.public_key_spki);
    const now = seconds(i.now),
      issued = seconds(k.issued_at);
    if (
      issued > now + 60 ||
      seconds(k.valid_until) <= now ||
      seconds(k.valid_until) <= issued ||
      seconds(k.valid_until) - issued > a.limits.keyset_seconds
    )
      err('invalid_transition');
    if (k.mode !== 'bootstrap' && k.mode !== 'migration' && !p.predecessor)
      err('predecessor_required');
    let prev = [];
    if (p.predecessor) {
      const q = p.predecessor;
      if (
        digest(q.keyset) !== q.keyset_digest ||
        (q.keyset.payload.generation === k.generation &&
          BigInt(q.keyset.payload.keyset_version) >= BigInt(k.keyset_version))
      )
        err('invalid_transition');
      check(q.control, 'Auriga.NetworkRootControl.v2', i.root_spki);
      check(q.authority, 'Auriga.NetworkRuntimeAuthority.v2', i.root_spki);
      const qa = q.authority.payload,
        qc = q.control.payload,
        qk = q.keyset.payload;
      if (
        qk.authority_digest !== digest(q.authority) ||
        qk.authority_id !== qa.authority_id ||
        qk.control_revision !== qc.control_revision ||
        qk.realm !== k.realm ||
        qk.tenant_id !== k.tenant_id ||
        qa.realm !== k.realm ||
        qa.tenant_id !== k.tenant_id ||
        qc.realm !== k.realm ||
        qc.tenant_id !== k.tenant_id ||
        !qc.allowed_authorities.some(
          (x) =>
            x.authority_id === qa.authority_id &&
            x.authority_digest === digest(q.authority),
        ) ||
        qc.revoked_authority_ids.includes(qa.authority_id) ||
        q.keyset.runtime_auth.kid !== qa.runtime_kid
      )
        err('invalid_chain');
      check(q.keyset, 'Auriga.NetworkKeyset.v2', qa.public_key_spki);
      if (
        !qa.capabilities.includes('keyset') ||
        seconds(qa.not_before) > seconds(qk.issued_at) ||
        seconds(qk.valid_until) <= seconds(qk.issued_at) ||
        seconds(qk.valid_until) - seconds(qk.issued_at) >
          qa.limits.keyset_seconds ||
        BigInt(qk.generation) < BigInt(qc.minimum_generation) ||
        BigInt(qk.keyset_version) < BigInt(qc.minimum_keyset_version)
      )
        err('invalid_chain');
      const boundary =
        qk.generation !== k.generation ||
        c.revoked_authority_ids.includes(qa.authority_id);
      if (boundary) {
        const t = c.generation_transition;
        if (
          k.mode !== 'authority_transition' ||
          !t ||
          t.predecessor_digest !== q.keyset_digest ||
          t.from_generation !== qk.generation ||
          t.to_generation !== k.generation ||
          t.authority_id !== a.authority_id ||
          t.authority_digest !== digest(p.authority) ||
          t.first_version !== k.keyset_version ||
          t.first_version !== c.minimum_keyset_version ||
          BigInt(k.generation) <= BigInt(qk.generation)
        )
          err('invalid_transition');
        if (c.revoked_authority_ids.includes(qa.authority_id)) {
          const oldKeysRevoked = qk.keys
            .filter((key) => ['active', 'next'].includes(key.state))
            .every((old) =>
              k.keys.some(
                (key) => key.kid === old.kid && key.state === 'revoked',
              ),
            );
          const newActiveKids = k.keys
            .filter((key) => key.state === 'active')
            .every((key) => !qk.keys.some((old) => old.kid === key.kid));
          if (!oldKeysRevoked || !newActiveKids) err('invalid_transition');
        }
      } else if (
        k.mode === 'authority_transition' &&
        qa.authority_id === a.authority_id
      )
        err('invalid_transition');
      prev = q.keyset.payload.keys;
      if (i.previous_digest && i.previous_digest !== q.keyset_digest)
        err('invalid_transition');
    }
    const ledger = i.first_publication_ledger ?? [];
    if (
      k.keys.some((x) => !prev.some((y) => y.kid === x.kid)) &&
      !a.capabilities.includes('business_rotation')
    )
      err('scope_denied');
    const removed = prev.filter((x) => !k.keys.some((y) => y.kid === x.kid));
    if (removed.length) {
      if (!a.capabilities.includes('history_archive')) err('scope_denied');
      if (
        seconds(k.archive.before) > issued - a.limits.archive_grace_seconds ||
        removed.some((x) => seconds(x.not_after) > seconds(k.archive.before))
      )
        err('invalid_transition');
    }
    for (const snapshot of [p.predecessor?.keyset.payload, k].filter(Boolean)) {
      for (const proof of snapshot.first_publications) {
        const known = ledger.find((x) => x.kid === proof.kid);
        if (known && jcs(known) !== jcs(proof)) err('invalid_transition');
        if (
          BigInt(proof.committed_version) > BigInt(snapshot.keyset_version) ||
          seconds(proof.committed_at) > seconds(snapshot.issued_at)
        )
          err('invalid_transition');
      }
    }
    for (const key of k.keys.filter(
      (x) => !prev.some((y) => y.kid === x.kid),
    )) {
      const proof = k.first_publications.find((x) => x.kid === key.kid);
      if (
        !proof ||
        proof.committed_version !== k.keyset_version ||
        proof.committed_at !== k.issued_at
      )
        err('invalid_transition');
    }
    const ids = new Set(),
      materials = new Set();
    for (const key of k.keys) {
      if (ids.has(key.kid) || materials.has(key.public_key_spki))
        err('invalid_transition');
      ids.add(key.kid);
      materials.add(key.public_key_spki);
      if (
        seconds(key.not_after) <= seconds(key.not_before) ||
        seconds(key.not_after) - seconds(key.not_before) >
          a.limits.business_key_seconds
      )
        err('invalid_transition');
      const old = prev.find((x) => x.kid === key.kid);
      if (old) {
        for (const f of [
          'public_key_spki',
          'purpose',
          'not_before',
          'not_after',
        ])
          if (old[f] !== key[f]) err('invalid_transition');
        if (
          ['retired', 'revoked'].includes(old.state) &&
          old.state !== key.state
        )
          err('invalid_transition');
      }
    }
    for (const purpose of purposes) {
      const active = k.keys.filter(
        (x) => x.purpose === purpose && x.state === 'active',
      );
      if (active.length !== 1) err('invalid_transition');
      const next = active[0];
      if (
        seconds(next.not_before) > issued ||
        seconds(next.not_after) <= issued
      )
        err('invalid_transition');
      const old = prev.find(
          (x) => x.purpose === purpose && x.state === 'active',
        ),
        valid = (x) =>
          seconds(x.not_before) <= issued && seconds(x.not_after) > issued;
      const mature = (x) => {
        const proof = p.predecessor.keyset.payload.first_publications.find(
          (y) => y.kid === x.kid,
        );
        return (
          proof &&
          seconds(proof.committed_at) <=
            issued - a.limits.next_maturity_seconds &&
          BigInt(proof.committed_version) <=
            BigInt(p.predecessor.keyset.payload.keyset_version)
        );
      };
      const matureNext = prev.find(
        (x) =>
          x.purpose === purpose && x.state === 'next' && valid(x) && mature(x),
      );
      if (k.mode === 'refresh' && (!old || old.kid !== next.kid))
        err('invalid_transition');
      if (
        k.mode === 'authority_transition' &&
        p.predecessor.keyset.payload.generation === k.generation &&
        !c.revoked_authority_ids.includes(
          p.predecessor.authority.payload.authority_id,
        ) &&
        old?.kid !== next.kid
      )
        err('invalid_transition');
      if (
        k.mode === 'activate' &&
        old?.kid !== next.kid &&
        (!matureNext ||
          matureNext.kid !== next.kid ||
          (old &&
            valid(old) &&
            seconds(old.not_after) - issued < a.limits.overlap_seconds))
      )
        err('invalid_transition');
      if (k.mode === 'restart_recovery') {
        if (old && valid(old)) {
          if (next.kid !== old.kid) err('invalid_transition');
        } else if (
          matureNext ||
          prev.some((x) => x.kid === next.kid) ||
          seconds(next.not_before) !== issued ||
          seconds(next.not_after) - issued > a.limits.restart_key_seconds
        )
          err('invalid_transition');
      }
    }
    if (
      k.mode === 'restart_recovery' &&
      purposes.every((purpose) =>
        prev.some(
          (x) =>
            x.purpose === purpose &&
            x.state === 'active' &&
            seconds(x.not_before) <= issued &&
            seconds(x.not_after) > issued,
        ),
      )
    )
      err('invalid_transition');
    if (i.transport) {
      if (!a.capabilities.includes('network_transport')) err('scope_denied');
      const t = i.transport;
      check(t, 'Auriga.NetworkTransportTrustBundle.v2', a.public_key_spki);
      if (
        t.payload.authority_digest !== digest(p.authority) ||
        t.payload.scope.some(
          (s) => !a.transport_scope.some((x) => jcs(x) === jcs(s)),
        )
      )
        err('scope_denied');
      if (BigInt(t.payload.bundle_version) <= BigInt(i.seen_bundle_version))
        err('version_rollback');
      const phase = { dual_trust: 0, new_leaves: 1, retire_old: 2 };
      if (
        i.previous_transport_phase &&
        phase[t.payload.phase] !== phase[i.previous_transport_phase] + 1
      )
        err('invalid_transition');
      if (t.payload.phase === 'new_leaves' && !i.readers_persisted)
        err('projection_pending');
      if (t.payload.phase === 'retire_old' && !i.handshakes_verified)
        err('projection_pending');
    }
    return 'accept';
  } catch (e) {
    return e.message;
  }
}
