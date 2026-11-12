import {
  existsSync,
  mkdirSync,
  readdirSync,
  renameSync,
  rmdirSync,
  unlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { DesktopGuardian, checkLease } from './guardian.mjs';
import { assertLocalPath, durableJson } from './host-lock.mjs';
import {
  observeLedgers,
  readRecord,
  readBounded,
  verifySnapshot,
  recheckBytes,
  sha,
} from './resource-observer.mjs';
import { canonicalize, parseStrictJson } from './json.mjs';
import { requireSchema } from './schema.mjs';

const identityMatches = (a, b) =>
  ['tenantId', 'ownerId', 'hostId', 'selectionRevision', 'generation'].every(
    (k) => a[k] === b[k],
  );
export class Supervisor {
  constructor(home, tree, trust, options = {}) {
    this.home = home;
    this.tree = tree;
    this.trust = trust;
    this.previousLauncher = tree.record.launcher;
    this.now = options.now ?? Date.now;
    this.guardian = new DesktopGuardian(home, {
      time: options.time,
      stopTree: () => tree.stopTree(),
      isStopped: () => tree.isStopped(),
    });
    this.watermarks = join(this.guardian.directory, 'high-watermarks.json');
    this.startedAt = this.now();
  }
  marks() {
    return existsSync(this.watermarks)
      ? readRecord(this.watermarks, 'UnifiedHighWatermarksFile')
      : {
          schemaVersion: 1,
          revision: 1,
          entries: [],
          migrationMarkers: [],
          consumedDowngradeAuthorizationIds: [],
        };
  }
  async recoverLock() {
    const directory = join(this.guardian.directory, 'host.lock.d');
    assertLocalPath(directory);
    if (!existsSync(directory)) return;
    const ownerFile = join(directory, 'owner.json');
    const owner = readRecord(ownerFile, 'UnifiedHostLockOwner');
    const known = [
      this.previousLauncher,
      this.tree.record.root,
      ...this.tree.record.children,
    ].filter(Boolean);
    if (
      !known.some(
        (p) =>
          p.pid === owner.pid &&
          Math.abs(
            Date.parse(p.createdAt) - Date.parse(owner.processStartedAt),
          ) <= 2000,
      )
    )
      throw new Error('unknown stale lock owner; refusing recovery');
    // Singleton acquired, no DSH/C launch has happened, and all managed writers must stop.
    await this.tree.stopTree();
    if (!(await this.tree.isStopped())) throw new Error('writers remain alive');
    if (readRecord(ownerFile, 'UnifiedHostLockOwner').lockId !== owner.lockId)
      throw new Error('lock owner changed');
    const retired = `${directory}.${randomUUID()}.retired`;
    renameSync(directory, retired);
    // Never recurse through an unknown directory, even after the token check.
    if (readdirSync(retired).some((name) => name !== 'owner.json'))
      throw new Error('unexpected stale lock contents');
    unlinkSync(join(retired, 'owner.json'));
    rmdirSync(retired);
  }
  snapshot() {
    return this.guardian.locked(() => ({
      state: this.guardian.read(),
      ledgers: observeLedgers(this.home),
      marks: this.marks(),
    }));
  }
  async prepare() {
    await this.recoverLock();
    // A launcher crash leaves a trusted process journal; stop it before recovery or updates.
    if (!(await this.tree.isStopped())) await this.tree.stopTree();
    const state = this.guardian.locked(() => this.guardian.read());
    if (
      state?.pendingIsolation ||
      (state && !['active', 'private_only'].includes(state.guardian.state))
    )
      await this.guardian.disable('verification_failed');
    await this.reconcile(true);
  }
  async reconcile(startup = false) {
    const snapshot = this.snapshot();
    const old = snapshot.state;
    const candidates = snapshot.ledgers.filter((s) =>
      s.ledger.installations.some(
        (i) =>
          i.resource.enabled &&
          ['applying', 'applied', 'pending_restart'].includes(i.state),
      ),
    );
    if (!candidates.length) {
      if (old?.guardian.state === 'active' || !this.guardian.cleared()) {
        if (!old) throw new Error('unowned shared view; startup blocked');
        await this.guardian.disable('identity_changed');
      }
      if (old?.pendingIsolation) throw new Error('pending isolation');
      return;
    }
    try {
      if (candidates.length !== 1 || !this.trust)
        throw new Error('ambiguous identity or missing packaged trust roots');
      const candidate = candidates[0];
      if (old?.pendingIsolation)
        throw new Error('pending isolation must recover before activation');
      if (
        old?.lastDisableReceiptId &&
        !candidate.ledger.processedDisableReceiptIds.includes(
          old.lastDisableReceiptId,
        )
      )
        throw new Error('disable receipt not reconciled');
      if (
        old?.guardian.state === 'active' &&
        !identityMatches(old.guardian, candidate.ledger)
      )
        throw new Error('identity_changed');
      if (old?.guardian.state === 'private_only' && !this.guardian.cleared())
        throw new Error('shared content restored without authorization');
      const sample = { ...this.guardian.time(), wallMillis: this.now() };
      if (old?.guardian.state === 'active') {
        const lease = checkLease(old.clock, old.guardian.lease, sample);
        if (!lease.valid) throw new Error(lease.reason);
      }
      const verified = verifySnapshot(
        candidate,
        this.trust,
        snapshot.marks,
        new Date(this.now()).toISOString(),
      );
      if (!verified) throw new Error('empty verification');
      if (
        old?.guardian.state === 'active' &&
        verified.lease.revocationRevision <
          old.guardian.lease.revocationRevision
      )
        throw new Error('revocation revision rollback');
      // Fresh activation/restart across boot must carry a newly signed trusted time.
      // Signed issuance is the trusted source; a wall-clock timestamp from C is never accepted.
      const fresh =
        !old ||
        old.guardian.state !== 'active' ||
        old.clock.bootId !== sample.bootId;
      const issued = Math.min(
        ...candidate.ledger.installations
          .filter(
            (i) =>
              i.resource.enabled &&
              ['applying', 'applied', 'pending_restart'].includes(i.state),
          )
          .map((i) => Date.parse(i.resource.envelope.statement.issuedAt)),
      );
      if (fresh && issued < this.startedAt - (old ? 0 : 120000))
        throw new Error('fresh signed online trust required');
      if (
        fresh &&
        old?.guardian.state === 'private_only' &&
        !(await this.tree.isStopped())
      )
        await this.tree.stopTree();
      this.guardian.locked((lock) => {
        const latest = observeLedgers(this.home);
        if (
          canonicalize(latest) !== canonicalize(snapshot.ledgers) ||
          canonicalize(this.marks()) !== canonicalize(snapshot.marks) ||
          canonicalize(this.guardian.read()) !== canonicalize(old)
        )
          throw new Error('resource snapshot changed');
        recheckBytes(verified);
        const marks = snapshot.marks;
        for (const next of verified.releases) {
          const existing = marks.entries.find(
            (w) =>
              w.tenantId === next.tenantId && w.packageId === next.packageId,
          );
          if (existing) {
            if (next.releaseSequence > existing.releaseSequence)
              Object.assign(existing, next);
          } else marks.entries.push(next);
        }
        marks.revision++;
        requireSchema('UnifiedHighWatermarksFile', marks);
        lock.check();
        durableJson(this.watermarks, marks);
        const state = old ?? { schemaVersion: 1, revision: 1 };
        const effective =
          old?.clock && old.clock.bootId === sample.bootId
            ? Math.max(
                sample.wallMillis,
                Date.parse(old.clock.maxObservedTime) +
                  sample.monotonicMillis -
                  old.clock.lastMonotonicMillis,
              )
            : Math.max(
                sample.wallMillis,
                old ? Date.parse(old.clock.maxObservedTime) : 0,
                old ? Date.parse(old.clock.lastTrustedTime) : 0,
              );
        const deadline =
          sample.monotonicMillis +
          Math.max(0, Date.parse(verified.lease.expiresAt) - effective);
        state.clock = {
          bootId: sample.bootId,
          maxObservedTime: new Date(effective).toISOString(),
          lastTrustedTime: new Date(
            Math.max(
              issued,
              old ? Date.parse(old.clock.lastTrustedTime) : issued,
            ),
          ).toISOString(),
          lastTrustedTimeSource: 'signed_release',
          lastMonotonicMillis: sample.monotonicMillis,
          leaseDeadlineMonotonicMillis:
            old?.guardian.state === 'active' &&
            Date.parse(verified.lease.expiresAt) <=
              Date.parse(old.guardian.lease.expiresAt)
              ? Math.min(old.clock.leaseDeadlineMonotonicMillis, deadline)
              : deadline,
        };
        state.guardian = {
          tenantId: candidate.ledger.tenantId,
          ownerId: candidate.ledger.ownerId,
          hostId: candidate.ledger.hostId,
          selectionRevision: candidate.ledger.selectionRevision,
          generation: candidate.ledger.generation,
          state: 'active',
          reason: 'startup',
          lease: verified.lease,
          processStopped: startup,
          viewCleared: this.guardian.cleared(),
          newSessionRequired: false,
        };
        requireSchema('UnifiedGuardianStateFile', state);
        this.guardian.write(state, lock);
      });
    } catch (error) {
      if (error.revocationList)
        this.guardian.locked((lock) => {
          const list = error.revocationList;
          const directory = join(
            this.guardian.directory,
            'trusted-revocations',
            list.tenantId,
          );
          assertLocalPath(directory);
          mkdirSync(directory, { recursive: true });
          lock.check();
          durableJson(
            join(directory, sha(JSON.stringify(list)).slice(7) + '.json'),
            list,
          );
        });
      if (!old) throw error;
      if (old.guardian.state === 'private_only' && this.guardian.cleared())
        return;
      if (old.guardian.state !== 'private_only' || !this.guardian.cleared())
        await this.guardian.disable(
          [
            'identity_changed',
            'lease_expired',
            'clock_rollback',
            'revoked',
          ].includes(error.message)
            ? error.message
            : 'verification_failed',
        );
      console.error('共享技能已停用，建议新建会话。');
    }
  }
}

export function packagedTrust(dir) {
  const file = join(dir, 'auriga', 'runtime', 'resource-roots.json');
  return existsSync(file) ? parseStrictJson(readBounded(file)) : null;
}
