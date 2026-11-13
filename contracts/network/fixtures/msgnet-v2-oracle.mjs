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
      a.capabilities.length !== 6
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
      seconds(k.valid_until) - issued > 86400
    )
      err('invalid_transition');
    if (
      i.clock &&
      (Math.abs(i.clock.pg_skew) > 60 ||
        i.clock.wall_delta - i.clock.monotonic_delta > 300 ||
        i.clock.wall_delta - i.clock.monotonic_delta < -60)
    )
      err('clock_error');
    if (k.mode !== 'bootstrap' && k.mode !== 'migration' && !p.predecessor)
      err('predecessor_required');
    let prev = [];
    if (p.predecessor) {
      const q = p.predecessor;
      if (
        digest(q.keyset) !== q.keyset_digest ||
        BigInt(q.keyset.payload.keyset_version) >= BigInt(k.keyset_version)
      )
        err('invalid_transition');
      if (digest(q.authority) !== digest(p.authority)) err('invalid_chain');
      check(q.keyset, 'Auriga.NetworkKeyset.v2', a.public_key_spki);
      prev = q.keyset.payload.keys;
      if (i.previous_digest && i.previous_digest !== q.keyset_digest)
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
        seconds(key.not_after) - seconds(key.not_before) > 691200
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
          seconds(proof.committed_at) <= issued - 86400 &&
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
        k.mode === 'activate' &&
        old?.kid !== next.kid &&
        (!matureNext ||
          matureNext.kid !== next.kid ||
          (old && valid(old) && seconds(old.not_after) - issued < 360))
      )
        err('invalid_transition');
      if (k.mode === 'restart_recovery') {
        if (old && valid(old)) {
          if (next.kid !== old.kid) err('invalid_transition');
        } else if (
          matureNext ||
          prev.some((x) => x.kid === next.kid) ||
          seconds(next.not_before) !== issued ||
          seconds(next.not_after) - issued > 604800
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
    }
    return 'accept';
  } catch (e) {
    return e.message;
  }
}
