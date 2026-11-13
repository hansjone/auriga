// 仅契约参考判定器；生产实现必须通过文档定义的adapter独立消费全部数据。
import { createPublicKey, verify } from 'node:crypto';
import { jcs, digest, evaluate } from './msgnet-v2-oracle.mjs';
import { openTest } from './msgnet-v2-hpke.mjs';
import { readFileSync } from 'node:fs';
const configFields = JSON.parse(
  readFileSync(new URL('../auto-renewal-v2.schema.json', import.meta.url)),
).$defs.RenewalConfig.properties;
const err = (x) => {
    throw Error(x);
  },
  eq = (a, b) => jcs(a) === jcs(b);
const check = (s, domain, spki) => {
  if (
    !verify(
      null,
      Buffer.from(domain + '\0' + jcs(s.payload)),
      createPublicKey({
        key: Buffer.from(spki, 'base64url'),
        format: 'der',
        type: 'spki',
      }),
      Buffer.from((s.root_auth ?? s.runtime_auth).signature, 'base64url'),
    )
  )
    err('invalid_chain');
};
export function evaluateIssuer(v) {
  const i = v.input,
    c = i.clock;
  const bad =
    Math.abs(c.pg_skew) > 60 ||
    c.wall_delta - c.monotonic_delta > 300 ||
    c.wall_delta - c.monotonic_delta < -60;
  if (bad) return 'clock_error';
  if (i.write_first_publication && i.clock_error_persisted && !i.stable_samples)
    return 'clock_error';
  return 'accept';
}
export function evaluateBoundary(v) {
  try {
    const i = v.input;
    switch (v.operation) {
      case 'signature_domain':
        check(i.signed, i.verification_domain, i.verification_spki);
        break;
      case 'announcement': {
        const r = evaluate({ input: i.context });
        if (r !== 'accept') err(r);
        const k = i.context.publication.keyset.payload,
          a = i.context.publication.authority.payload,
          n = i.announcement.payload;
        check(
          i.announcement,
          'Auriga.NetworkKeysetAnnouncement.v2',
          a.public_key_spki,
        );
        if (!a.capabilities.includes('announcement')) err('scope_denied');
        if (
          n.authority_digest !== digest(i.context.publication.authority) ||
          n.realm !== k.realm ||
          n.tenant_id !== k.tenant_id
        )
          err('scope_denied');
        if (!i.persisted) err('projection_pending');
        if (
          BigInt(n.generation) > BigInt(k.generation) ||
          (n.generation === k.generation &&
            BigInt(n.keyset_version) > BigInt(k.keyset_version))
        )
          err('projection_pending');
        if (
          n.generation !== k.generation ||
          n.keyset_version !== k.keyset_version ||
          n.keyset_digest !== digest(i.context.publication.keyset) ||
          n.valid_until !== k.valid_until
        )
          err('invalid_chain');
        break;
      }
      case 'control_receipt': {
        const r = i.receipt,
          body = { ...r };
        delete body.identity_auth;
        check(
          { payload: body, runtime_auth: r.identity_auth },
          'Auriga.NetworkControlReceipt.v2',
          i.identity_spki,
        );
        for (const x of [
          'package_id',
          'tenant_id',
          'realm',
          'target_id',
          'challenge',
          'control_digest',
        ])
          if (r[x] !== i.request[x]) err('invalid_transition');
        if (r.identity_id !== i.identity_id) err('scope_denied');
        if (
          r.control_revision !== i.request.control.payload.control_revision ||
          r.result !== 'installed' ||
          !i.persisted
        )
          err('invalid_transition');
        if (i.used_challenge && !i.exact_replay) err('invalid_transition');
        if (i.identity_compromised || !i.new_chain_ready)
          err('identity_quarantined');
        break;
      }
      case 'projection': {
        const q = i.request,
          r = i.response,
          body = { ...q };
        delete body.identity_auth;
        check(
          { payload: body, runtime_auth: q.identity_auth },
          'Auriga.NetworkProjectionRequest.v2',
          i.identity_spki,
        );
        for (const f of [
          'tenant_id',
          'realm',
          'installation_id',
          'component',
          'nonce',
        ])
          if (q[f] !== r[f]) err('invalid_transition');
        if (i.used_nonce) err('invalid_transition');
        if (
          q.expected_generation !== r.generation ||
          q.expected_keyset_version !== r.keyset_version
        )
          err('version_rollback');
        if (
          i.exported_purposes.some((x) => x !== 'protocol_response') ||
          q.component !== 'network'
        )
          err('scope_denied');
        break;
      }
      case 'hpke': {
        const h = { ...i.sealed };
        delete h.enc;
        delete h.ciphertext;
        let clear;
        try {
          clear = openTest(jcs(h), i.sealed);
        } catch {
          err('invalid_chain');
        }
        if (clear !== jcs(i.expected_plaintext)) err('invalid_chain');
        for (const f of [
          'tenant_id',
          'realm',
          'installation_id',
          'component',
          'nonce',
          'generation',
          'keyset_version',
        ])
          if (i.expected_plaintext[f] !== h[f]) err('scope_denied');
        break;
      }
      case 'migration': {
        check(i.permit, 'Auriga.NetworkMigrationPermit.v2', i.root_spki);
        const p = i.permit.payload;
        if (
          digest(i.members) !== p.queue_commitment_digest ||
          !i.members.some(
            (x) =>
              x.version === i.current_version &&
              x.pair_digest === i.current_digest,
          ) ||
          BigInt(i.current_version) > BigInt(p.maximum_committed_v1_version)
        )
          err('migration_outside_commitment');
        if (i.frozen_elapsed_seconds > 600 || i.remaining_seconds < 21600)
          err('migration_expired');
        if (
          BigInt(i.first_v2_version) < BigInt(p.minimum_v2_version) ||
          (i.barrier_committed && !i.legacy_disabled)
        )
          err('invalid_transition');
        break;
      }
      case 'archive': {
        if (
          Date.parse(i.before) >
          Date.parse(i.issued_at) - i.grace_seconds * 1000
        )
          err('invalid_transition');
        for (const t of i.removed)
          if (Date.parse(t.not_after) > Date.parse(i.before))
            err('invalid_transition');
        for (const k of i.reappeared) {
          const t = i.tombstones.find((x) => x.kid === k.kid);
          if (t && (!eq(t, k) || k.terminal_state === 'active'))
            err('invalid_transition');
        }
        break;
      }
      case 'config': {
        const c = i.config;
        for (const [field, rule] of Object.entries(configFields))
          if (
            rule.type === 'integer' &&
            (!Number.isInteger(c[field]) ||
              c[field] < rule.minimum ||
              c[field] > rule.maximum)
          )
            err('invalid_transition');
        const reminders = [...c.review_reminder_days].sort((a, b) => b - a);
        if (
          c.review_period_days !== 365 ||
          c.refresh_seconds >= c.keyset_seconds ||
          c.rotation_seconds + c.preannounce_seconds > c.business_key_seconds ||
          c.preannounce_seconds < c.maturity_seconds ||
          c.leaf_renew_before_days >= c.leaf_days ||
          c.ca_renew_before_years >= c.ca_years ||
          reminders[0] < 30 ||
          reminders[1] < 7 ||
          reminders[2] < 1
        )
          err('invalid_transition');
        break;
      }
      default:
        err('unknown_operation');
    }
    return 'accept';
  } catch (e) {
    return e.message;
  }
}
