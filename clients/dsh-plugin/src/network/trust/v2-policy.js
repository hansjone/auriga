import { Buffer } from 'node:buffer';
import {
  createHmac,
  createPublicKey,
  createDecipheriv,
  diffieHellman,
} from 'node:crypto';
import { schema } from './v2-schema.js';
import {
  detach,
  digest,
  equal,
  jcs,
  reject,
  signature,
  structure,
  verifyAnnouncement,
  verifyPublication,
} from './v2.js';
/** @param {any} clock @param {boolean} writeFirst @param {boolean} clockError @param {boolean} stable */
export function checkClock(clock, writeFirst, clockError, stable) {
  const deviation = clock.wall_delta - clock.monotonic_delta;
  if (
    Math.abs(clock.pg_skew) > 60 ||
    deviation > 300 ||
    deviation < -60 ||
    (writeFirst && clockError && !stable)
  )
    reject('clock_error');
}
/** @param {any} context @param {any} announcement @param {boolean} persisted */
export function checkAnnouncementPair(context, announcement, persisted) {
  const p = verifyPublication(context),
    k = p.keyset.payload;
  // Pair-level error precedence follows the shared boundary contract.
  signature(
    announcement,
    'Auriga.NetworkKeysetAnnouncement.v2',
    p.authority.payload.public_key_spki,
  );
  if (
    !p.authority.payload.capabilities.includes('announcement') ||
    announcement.payload.authority_digest !== digest(p.authority) ||
    announcement.payload.realm !== k.realm ||
    announcement.payload.tenant_id !== k.tenant_id
  )
    reject('scope_denied');
  if (!persisted) reject('projection_pending');
  const n = announcement.payload;
  if (
    BigInt(n.generation) > BigInt(k.generation) ||
    (n.generation === k.generation &&
      BigInt(n.keyset_version) > BigInt(k.keyset_version))
  )
    reject('projection_pending');
  if (
    n.generation !== k.generation ||
    n.keyset_version !== k.keyset_version ||
    n.keyset_digest !== digest(p.keyset) ||
    n.valid_until !== k.valid_until
  )
    reject('invalid_chain');
  verifyAnnouncement(
    announcement,
    p.authority,
    context.latest_control,
    context,
  );
}
/** @param {any} input */
export function checkReceipt(input) {
  const { identity_auth, ...body } = input.receipt;
  signature(
    { payload: body, runtime_auth: identity_auth },
    'Auriga.NetworkControlReceipt.v2',
    input.identity_spki,
  );
  const r = input.receipt,
    p = input.request;
  for (const field of [
    'package_id',
    'tenant_id',
    'realm',
    'target_id',
    'challenge',
    'control_digest',
  ])
    if (r[field] !== p[field]) reject('invalid_transition');
  if (r.identity_id !== input.identity_id) reject('scope_denied');
  if (
    r.control_revision !== p.control.payload.control_revision ||
    r.result !== 'installed' ||
    !input.persisted ||
    (input.used_challenge && !input.exact_replay)
  )
    reject('invalid_transition');
  if (input.identity_compromised || !input.new_chain_ready)
    reject('identity_quarantined');
}
/** @param {any} input */
export function checkProjection(input) {
  const { identity_auth, ...body } = input.request;
  signature(
    { payload: body, runtime_auth: identity_auth },
    'Auriga.NetworkProjectionRequest.v2',
    input.identity_spki,
  );
  const q = input.request,
    r = input.response;
  for (const field of [
    'tenant_id',
    'realm',
    'installation_id',
    'component',
    'nonce',
  ])
    if (q[field] !== r[field]) reject('invalid_transition');
  if (input.used_nonce) reject('invalid_transition');
  if (
    q.expected_generation !== r.generation ||
    q.expected_keyset_version !== r.keyset_version
  )
    reject('version_rollback');
  if (
    q.component !== 'network' ||
    input.exported_purposes.some(
      (/** @type {string} */ p) => p !== 'protocol_response',
    )
  )
    reject('scope_denied');
}
/** @param {any} input */
export function checkMigration(input) {
  signature(input.permit, 'Auriga.NetworkMigrationPermit.v2', input.root_spki);
  const p = input.permit.payload;
  if (
    digest(input.members) !== p.queue_commitment_digest ||
    !input.members.some(
      (/** @type {any} */ x) =>
        x.version === input.current_version &&
        x.pair_digest === input.current_digest,
    ) ||
    BigInt(input.current_version) > BigInt(p.maximum_committed_v1_version)
  )
    reject('migration_outside_commitment');
  if (input.frozen_elapsed_seconds > 600 || input.remaining_seconds < 21600)
    reject('migration_expired');
  if (
    BigInt(input.first_v2_version) < BigInt(p.minimum_v2_version) ||
    (input.barrier_committed && !input.legacy_disabled)
  )
    reject('invalid_transition');
}
/** @param {any} input */
export function checkArchive(input) {
  if (
    Date.parse(input.before) >
      Date.parse(input.issued_at) - input.grace_seconds * 1000 ||
    input.removed.some(
      (/** @type {any} */ t) =>
        Date.parse(t.not_after) > Date.parse(input.before),
    )
  )
    reject('invalid_transition');
  for (const key of input.reappeared) {
    const prior = input.tombstones.find(
      (/** @type {any} */ x) => x.kid === key.kid,
    );
    if (prior && (!equal(prior, key) || key.terminal_state === 'active'))
      reject('invalid_transition');
  }
}
/** @param {any} config */
export function checkRenewalConfig(config) {
  structure('RenewalConfig', config, 'invalid_transition');
  // Preserve all ranges in the source schema; enforce cross-field windows here.
  for (const [field, rule] of Object.entries(
    schema.$defs.RenewalConfig.properties,
  )) {
    if (
      rule.type === 'integer' &&
      (!Number.isInteger(config[field]) ||
        config[field] < rule.minimum ||
        config[field] > rule.maximum)
    )
      reject('invalid_transition');
  }
  const reminders = [...config.review_reminder_days].sort((a, b) => b - a);
  if (
    config.review_period_days !== 365 ||
    config.refresh_seconds >= config.keyset_seconds ||
    config.rotation_seconds + config.preannounce_seconds >
      config.business_key_seconds ||
    config.preannounce_seconds < config.maturity_seconds ||
    config.leaf_renew_before_days >= config.leaf_days ||
    config.ca_renew_before_years >= config.ca_years ||
    reminders[0] < 30 ||
    reminders[1] < 7 ||
    reminders[2] < 1
  )
    reject('invalid_transition');
}
/** RFC9180 base receiver: the caller supplies its installed X25519 key, never a
 * seed or key embedded in a received object. @param {any} sealed @param {import('node:crypto').KeyObject} receiver */
export function openSealedMaterial(sealed, receiver) {
  try {
    structure('SealedMaterial', sealed);
    if (receiver.asymmetricKeyType !== 'x25519') reject('invalid_chain');
    const enc = Buffer.from(sealed.enc, 'base64url');
    const ciphertext = Buffer.from(sealed.ciphertext, 'base64url');
    if (
      enc.length !== 32 ||
      enc.toString('base64url') !== sealed.enc ||
      ciphertext.length < 16 ||
      ciphertext.toString('base64url') !== sealed.ciphertext
    )
      reject('invalid_chain');
    const peer = createPublicKey({
      key: Buffer.concat([Buffer.from('302a300506032b656e032100', 'hex'), enc]),
      type: 'spki',
      format: 'der',
    });
    const receiverPublic = createPublicKey(receiver)
      .export({ type: 'spki', format: 'der' })
      .subarray(-32);
    const kem = Buffer.from('4b454d0020', 'hex'),
      suite = Buffer.from('48504b45002000010002', 'hex');
    /** @param {Buffer} salt @param {Buffer} value */
    const extract = (salt, value) =>
      createHmac('sha256', salt.length ? salt : Buffer.alloc(32))
        .update(value)
        .digest();
    /** @param {Buffer} suiteId @param {Buffer} salt @param {string} label @param {Buffer} value */
    const labeledExtract = (suiteId, salt, label, value) =>
      extract(
        salt,
        Buffer.concat([
          Buffer.from('HPKE-v1'),
          suiteId,
          Buffer.from(label),
          value,
        ]),
      );
    /** All required HPKE outputs fit one SHA256 block. @param {Buffer} suiteId @param {Buffer} key @param {string} label @param {Buffer} info @param {number} length */
    const expand = (suiteId, key, label, info, length) =>
      createHmac('sha256', key)
        .update(
          Buffer.concat([
            Buffer.from([length >> 8, length & 255]),
            Buffer.from('HPKE-v1'),
            suiteId,
            Buffer.from(label),
            info,
            Buffer.from([1]),
          ]),
        )
        .digest()
        .subarray(0, length);
    const empty = Buffer.alloc(0);
    const eae = labeledExtract(
      kem,
      empty,
      'eae_prk',
      diffieHellman({ privateKey: receiver, publicKey: peer }),
    );
    const secret = expand(
      kem,
      eae,
      'shared_secret',
      Buffer.concat([enc, receiverPublic]),
      32,
    );
    const keyContext = Buffer.concat([
      Buffer.from([0]),
      labeledExtract(suite, empty, 'psk_id_hash', empty),
      labeledExtract(
        suite,
        empty,
        'info_hash',
        Buffer.from('Auriga.NetworkSealedMaterial.v2'),
      ),
    ]);
    const keySeed = labeledExtract(suite, secret, 'secret', empty);
    const cipher = createDecipheriv(
      'aes-256-gcm',
      expand(suite, keySeed, 'key', keyContext, 32),
      expand(suite, keySeed, 'base_nonce', keyContext, 12),
    );
    const metadata = { ...sealed };
    delete metadata.enc;
    delete metadata.ciphertext;
    cipher.setAAD(Buffer.from(jcs(metadata)));
    cipher.setAuthTag(ciphertext.subarray(-16));
    const plain = detach(
      Buffer.concat([
        cipher.update(ciphertext.subarray(0, -16)),
        cipher.final(),
      ]),
    );
    for (const field of [
      'tenant_id',
      'realm',
      'installation_id',
      'component',
      'nonce',
      'generation',
      'keyset_version',
    ])
      if (plain[field] !== metadata[field]) reject('scope_denied');
    return plain;
  } catch (error) {
    if (/** @type {Error} */ (error).message === 'scope_denied') throw error;
    reject('invalid_chain');
  }
}
