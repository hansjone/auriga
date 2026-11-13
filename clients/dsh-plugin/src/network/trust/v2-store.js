import { Buffer } from 'node:buffer';
import {
  publicKey,
  positiveVersion,
  timeValue,
  unavailable,
  validatePublication as legacyPublication,
  validatePublicKey as legacyKey,
  validateAnnouncement as legacyAnnouncement,
} from './keyset.js';
import {
  detach,
  digest,
  equal,
  jcs,
  reject,
  rootSignature,
  structure,
  verifyAnnouncement,
  verifyControl,
  verifyPublication,
} from './v2.js';

/** A single writer owns control, announcement fences, first publication and
 * immutable key history in one atomic cache snapshot. No v1 live verifier.
 * @param {any} options */
export async function openV2TrustStore(options) {
  const {
    cache,
    realm,
    tenant_id,
    now = Date.now,
    refresh,
    requireAnnouncement = true,
  } = options;
  if (!positiveVersion(realm) || typeof tenant_id !== 'string' || !tenant_id)
    unavailable('installation scope required');
  const roots = new Map();
  for (const root of options.roots) {
    publicKey(root.public_key_spki);
    if (roots.has(root.kid)) unavailable('duplicate root');
    roots.set(root.kid, root.public_key_spki);
  }
  if (!roots.size) unavailable('installation roots required');
  const scope = { realm, tenant_id, roots };
  /** @type {any} */ let control;
  /** @type {any} */ let publication;
  /** @type {any} */ let announcement;
  /** @type {any} */ let checkpoint;
  /** @type {any} */ let migrationPermit;
  const history = new Map(),
    first = new Map(),
    receipts = new Map();
  let blocked = false,
    announcementUncertain = false,
    refreshRevision = 0;
  let queue = Promise.resolve();
  /** @type {Promise<void>|undefined} */ let refreshing;
  let nextRefreshAt = -Infinity;
  const listeners = new Set(),
    isolationListeners = new Set();
  const diagnostics = {
    refreshAttempts: 0,
    refreshFailures: 0,
    refreshSuccesses: 0,
  };
  const clockText = () =>
    new Date(now()).toISOString().replace(/\.\d{3}Z$/, 'Z');
  const snapshot = () =>
    Object.fromEntries(
      Object.entries({
        schema_version: 2,
        control,
        publication,
        announcement,
        history: [...history.values()],
        first_publication_ledger: [...first.values()],
        receipts: [...receipts.values()],
        v1_checkpoint: checkpoint,
        migration_permit: migrationPermit,
      }).filter(([, value]) => value !== undefined),
    );
  const context = () => ({
    ...scope,
    now: clockText(),
    latest_control: control,
    seen_control_revision: control?.payload.control_revision ?? '0',
    seen_generation: publication?.keyset.payload.generation ?? '0',
    seen_version: publication?.keyset.payload.keyset_version ?? '0',
    seen_digest: publication ? digest(publication.keyset) : '',
    first_publication_ledger: [...first.values()],
    history: [...history.values()],
    has_history: !!publication || !!checkpoint,
    migration_authorized: !!migrationPermit,
  });
  /** @param {()=>Promise<void>} op */
  const serialize = (op) => {
    const task = queue.then(op);
    queue = task.catch(() => {});
    return task;
  };
  const isolation = () => {
    const a = publication?.authority.payload;
    return (
      !!control &&
      ((!!options.identity_id &&
        control.payload.revoked_installation_ids.includes(
          options.identity_id,
        )) ||
        (!!a &&
          (control.payload.revoked_authority_ids.includes(a.authority_id) ||
            !control.payload.allowed_authorities.some(
              (/** @type {any} */ x) =>
                x.authority_id === a.authority_id &&
                x.authority_digest === digest(publication.authority),
            ))))
    );
  };
  function notifyIsolation() {
    if (isolation())
      for (const listener of isolationListeners) {
        try {
          listener();
        } catch {
          /* Observers cannot affect fencing. */
        }
      }
  }
  /** @param {any} input */
  async function installControl(input) {
    const candidate = detach(input);
    verifyControl(candidate, scope, control);
    // Fence before the first await. A failed write never restores the old chain.
    control = candidate;
    blocked = true;
    notifyIsolation();
    return serialize(async () => {
      const written = control;
      await cache.commit(snapshot());
      if (control === written) blocked = false;
    });
  }
  /** @param {any} input */
  async function installMigrationPermit(input) {
    const permit = detach(input);
    rootSignature(
      permit,
      roots,
      'SignedMigrationPermit',
      'Auriga.NetworkMigrationPermit.v2',
    );
    if (
      permit.payload.realm !== realm ||
      permit.payload.tenant_id !== tenant_id ||
      publication
    )
      reject('scope_denied');
    if (migrationPermit && !equal(migrationPermit, permit))
      reject('invalid_transition');
    migrationPermit = permit;
    blocked = true;
    return serialize(async () => {
      await cache.commit(snapshot());
      blocked = false;
    });
  }
  /** @param {any} input */
  async function update(input) {
    const candidate = detach(input);
    structure('Publication', candidate);
    verifyControl(candidate.control, scope);
    if (
      !control ||
      BigInt(candidate.control.payload.control_revision) >
        BigInt(control.payload.control_revision)
    )
      await installControl(candidate.control);
    else if (
      candidate.control.payload.control_revision ===
        control.payload.control_revision &&
      !equal(candidate.control, control)
    )
      reject('same_version_conflict');
    return serialize(async () => {
      // Re-read local facts after waiting: never validate against a stale control.
      const c = context();
      const same = publication && equal(publication.keyset, candidate.keyset);
      if (same && !blocked) return;
      const checked = verifyPublication({
        ...c,
        publication: candidate,
        has_history: same ? false : c.has_history,
      });
      const payload = checked.keyset.payload;
      if (checkpoint && !publication) {
        const permit = migrationPermit?.payload;
        if (
          !permit ||
          payload.mode !== 'migration' ||
          BigInt(checkpoint.version) >
            BigInt(permit.maximum_committed_v1_version) ||
          BigInt(payload.keyset_version) < BigInt(permit.minimum_v2_version) ||
          payload.generation !== permit.minimum_generation
        )
          reject('invalid_transition');
        for (const old of history.values())
          if (
            old.state === 'active' &&
            now() < timeValue(old.not_after) &&
            !payload.keys.some((/** @type {any} */ key) => equal(key, old))
          )
            reject('invalid_transition');
      }
      // Preserve history even when rename/fsync has an uncertain outcome.
      for (const p of [checked.predecessor?.keyset.payload, payload].filter(
        Boolean,
      )) {
        for (const key of p.keys) {
          const old = history.get(key.kid);
          if (!old || p === payload) history.set(key.kid, key);
        }
        for (const record of p.first_publications) {
          const old = first.get(record.kid);
          if (old && !equal(old, record)) reject('invalid_transition');
          first.set(record.kid, record);
        }
      }
      for (const [kid, key] of history)
        if (!payload.keys.some((/** @type {any} */ x) => x.kid === kid))
          history.set(kid, {
            ...key,
            state: key.state === 'revoked' ? 'revoked' : 'retired',
          });
      publication = checked;
      blocked = true;
      const written = control;
      await cache.commit(snapshot());
      if (control === written) blocked = false;
      announcementUncertain = false;
    });
  }
  /** @param {any} input */
  async function announce(input) {
    if (!publication || !control) unavailable('announcement chain required');
    const next = verifyAnnouncement(
      input,
      publication.authority,
      control,
      scope,
    );
    const p = next.payload,
      old = announcement?.payload;
    if (!Number.isFinite(now()) || now() >= timeValue(p.valid_until))
      unavailable('announcement expired');
    for (const prior of [old, publication.keyset.payload].filter(Boolean)) {
      if (
        BigInt(p.generation) < BigInt(prior.generation) ||
        (p.generation === prior.generation &&
          BigInt(p.keyset_version) < BigInt(prior.keyset_version))
      )
        reject('version_rollback');
    }
    if (
      old &&
      old.generation === p.generation &&
      old.keyset_version === p.keyset_version &&
      !equal(next, announcement)
    )
      reject('same_version_conflict');
    announcement = next;
    announcementUncertain = true;
    return serialize(async () => {
      const written = announcement;
      await cache.commit(snapshot());
      if (announcement === written) announcementUncertain = false;
    });
  }
  const persisted = await cache.load();
  if (persisted !== undefined) {
    if (persisted.schema_version === 2) {
      if (
        !Array.isArray(persisted.history) ||
        !Array.isArray(persisted.first_publication_ledger) ||
        !Array.isArray(persisted.receipts)
      )
        unavailable('invalid v2 cache');
      for (const key of persisted.history) {
        structure('PublicKey', key);
        publicKey(key.public_key_spki);
        if (history.has(key.kid)) reject('invalid_transition');
        history.set(key.kid, key);
      }
      for (const f of persisted.first_publication_ledger) {
        structure('FirstPublication', f);
        if (first.has(f.kid)) reject('invalid_transition');
        first.set(f.kid, f);
      }
      if (persisted.control)
        control = verifyControl(detach(persisted.control), scope);
      publication = persisted.publication && detach(persisted.publication);
      if (publication) {
        structure('Publication', publication);
        try {
          verifyPublication({
            ...context(),
            now: publication.keyset.payload.issued_at,
            publication,
            has_history: false,
          });
        } catch (error) {
          if (
            ![
              'authority_revoked',
              'authority_unknown',
              'version_rollback',
            ].includes(/** @type {Error} */ (error).message)
          )
            throw error;
        }
        if (
          publication.keyset.payload.keys.some(
            (/** @type {any} */ k) => !history.has(k.kid),
          )
        )
          unavailable('incomplete key history');
      }
      announcement = persisted.announcement && detach(persisted.announcement);
      if (announcement && publication && !isolation())
        verifyAnnouncement(announcement, publication.authority, control, scope);
      checkpoint = persisted.v1_checkpoint;
      migrationPermit = persisted.migration_permit;
      if (migrationPermit)
        rootSignature(
          migrationPermit,
          roots,
          'SignedMigrationPermit',
          'Auriga.NetworkMigrationPermit.v2',
        );
      for (const r of persisted.receipts) {
        structure('InstallationReceipt', r);
        receipts.set(r.package_id, r);
      }
    } else {
      // One-time audit import. A v1 snapshot can never satisfy assertUsable.
      const rootKeys = new Map(
        [...roots].map(([kid, spki]) => [kid, publicKey(spki)]),
      );
      const old = legacyPublication(
        persisted.publication,
        rootKeys,
      ).publication;
      if (old.keyset.realm !== realm || !Array.isArray(persisted.history))
        unavailable('invalid migration cache');
      for (const key of persisted.history) {
        legacyKey(key);
        if (history.has(key.kid)) unavailable('duplicate legacy history');
        history.set(key.kid, key);
      }
      if (persisted.announcement)
        legacyAnnouncement(persisted.announcement, rootKeys);
      const high =
        persisted.announcement?.keyset_version ?? old.keyset.keyset_version;
      checkpoint = {
        version:
          BigInt(high) > BigInt(old.keyset.keyset_version)
            ? high
            : old.keyset.keyset_version,
        digest: digest(persisted),
      };
      blocked = true;
      await cache.commit(snapshot());
      blocked = false;
    }
  }
  const store = {
    update,
    announce,
    installControl,
    installMigrationPermit,
    get version() {
      return publication?.keyset.payload.keyset_version ?? '1';
    },
    get generation() {
      return publication?.keyset.payload.generation ?? '0';
    },
    get controlRevision() {
      return control?.payload.control_revision ?? '0';
    },
    get expiresAt() {
      return Math.min(
        publication ? timeValue(publication.keyset.payload.valid_until) : 0,
        announcement ? timeValue(announcement.payload.valid_until) : Infinity,
      );
    },
    get diagnostics() {
      return { ...diagnostics };
    },
    get refreshRevision() {
      return refreshRevision;
    },
    /** @param {()=>void} listener */
    onRefresh(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    /** @param {()=>void} listener */
    onIsolation(listener) {
      isolationListeners.add(listener);
      notifyIsolation();
      return () => isolationListeners.delete(listener);
    },
    assertUsable() {
      const k = publication?.keyset.payload;
      if (
        blocked ||
        !control ||
        !k ||
        isolation() ||
        !Number.isFinite(now()) ||
        timeValue(k.issued_at) > now() + 60000 ||
        now() >= timeValue(k.valid_until) ||
        k.generation !== control.payload.minimum_generation ||
        BigInt(k.keyset_version) <
          BigInt(control.payload.minimum_keyset_version)
      )
        unavailable('v2 keyset unavailable');
      const n = announcement?.payload;
      if (
        (requireAnnouncement || n) &&
        (!n ||
          announcementUncertain ||
          n.generation !== k.generation ||
          n.keyset_version !== k.keyset_version ||
          n.keyset_digest !== digest(publication.keyset) ||
          n.valid_until !== k.valid_until ||
          n.authority_id !== k.authority_id ||
          n.authority_digest !== k.authority_digest ||
          now() >= timeValue(n.valid_until))
      )
        unavailable('announcement requires complete durable v2 keyset');
    },
    /** @param {string} kid @param {string} purpose @param {string} version */
    key(kid, purpose, version) {
      store.assertUsable();
      const key = publication.keyset.payload.keys.find(
        (/** @type {any} */ x) => x.kid === kid,
      );
      if (
        !positiveVersion(version) ||
        BigInt(version) > BigInt(store.version) ||
        !key ||
        !['active', 'retired'].includes(key.state) ||
        now() < timeValue(key.not_before) ||
        now() >= timeValue(key.not_after)
      )
        unavailable('key unavailable');
      if (key.purpose !== purpose) unavailable('key purpose mismatch');
      return {
        publicKey: publicKey(key.public_key_spki),
        realm,
        notBefore: timeValue(key.not_before),
        notAfter: timeValue(key.not_after),
      };
    },
    /** @param {string} kid @param {string} version */
    async refreshFor(kid, version) {
      try {
        store.key(
          kid,
          publication?.keyset.payload.keys.find(
            (/** @type {any} */ x) => x.kid === kid,
          )?.purpose,
          version,
        );
        return;
      } catch {
        /* Refresh only through the fixed installation port. */
      }
      if (refreshing) return refreshing;
      if (
        !refresh ||
        !positiveVersion(version) ||
        !Number.isFinite(now()) ||
        now() < nextRefreshAt
      )
        return;
      nextRefreshAt = now() + 1000;
      diagnostics.refreshAttempts++;
      refreshing = Promise.resolve()
        .then(async () => {
          try {
            const next = await refresh({ realm, minimumVersion: version, kid });
            if (next !== undefined) await update(next);
            store.assertUsable();
            if (
              BigInt(store.version) < BigInt(version) ||
              !publication.keyset.payload.keys.some(
                (/** @type {any} */ x) => x.kid === kid,
              )
            )
              unavailable('unsatisfied key hint');
            nextRefreshAt = -Infinity;
            diagnostics.refreshSuccesses++;
            refreshRevision++;
            for (const listener of listeners) {
              try {
                listener();
              } catch {
                /* Observer only. */
              }
            }
          } catch {
            diagnostics.refreshFailures++;
            unavailable('v2 refresh failed');
          }
        })
        .finally(() => {
          refreshing = undefined;
        });
      return refreshing;
    },
    /** Public package import is independent of the message connection. The
     * identity port signs only after control commit; receipt is itself durable
     * before any transport acknowledgement is attempted. @param {any} input @param {any} identity */
    async importControlPackage(input, identity) {
      const pack = structure('ControlPackage', detach(input));
      if (
        pack.tenant_id !== tenant_id ||
        pack.realm !== realm ||
        pack.target_id !== identity.target_id ||
        digest(pack.control) !== pack.control_digest ||
        Buffer.from(pack.challenge, 'base64url').length < 32 ||
        Buffer.from(pack.challenge, 'base64url').toString('base64url') !==
          pack.challenge
      )
        reject('scope_denied');
      const previous = receipts.get(pack.package_id);
      if (previous) {
        if (
          ['challenge', 'control_digest', 'target_id'].some(
            (f) => previous[f] !== pack[f],
          )
        )
          reject('invalid_transition');
        return detach(previous);
      }
      await installControl(pack.control);
      if (
        control.payload.revoked_installation_ids.includes(identity.identity_id)
      )
        reject('identity_quarantined');
      const body = {
        package_id: pack.package_id,
        tenant_id,
        realm,
        target_id: pack.target_id,
        identity_id: identity.identity_id,
        challenge: pack.challenge,
        control_revision: pack.control.payload.control_revision,
        control_digest: pack.control_digest,
        result: 'installed',
        persisted_at: clockText(),
      };
      const receipt = {
        ...body,
        identity_auth: await identity.sign(
          Buffer.from('Auriga.NetworkControlReceipt.v2\0' + jcs(body)),
        ),
      };
      structure('InstallationReceipt', receipt);
      receipts.set(pack.package_id, receipt);
      await serialize(async () => {
        await cache.commit(snapshot());
      });
      return detach(receipt);
    },
  };
  if (options.control) await installControl(options.control);
  if (options.migrationPermit)
    await installMigrationPermit(options.migrationPermit);
  if (options.config) await update(options.config);
  return store;
}
