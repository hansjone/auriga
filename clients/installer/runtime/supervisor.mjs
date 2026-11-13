import {
  existsSync,
  mkdirSync,
  readdirSync,
  renameSync,
  rmdirSync,
  unlinkSync,
  lstatSync,
} from 'node:fs';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
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
  installationEnabled,
  acceptedView,
} from './resource-observer.mjs';
import { canonicalize, parseStrictJson } from './json.mjs';
import { requireSchema } from './schema.mjs';

const identityMatches = (a, b) =>
  ['tenantId', 'ownerId', 'hostId'].every((k) => a[k] === b[k]);
const retryError = (message) =>
  Object.assign(new Error(message), { code: 'RESOURCE_CHANGED' });
export function resourceStamp(home) {
  const found = [];
  const walk = (path) => {
    assertLocalPath(path);
    if (!existsSync(path)) {
      found.push([path]);
      return;
    }
    const info = lstatSync(path, { bigint: true });
    found.push([
      path,
      info.size.toString(),
      info.mtimeNs.toString(),
      info.ctimeNs.toString(),
      info.ino.toString(),
    ]);
    if (found.length > 50000) throw new Error('resource inventory limit');
    if (info.isDirectory())
      for (const name of readdirSync(path).sort()) walk(join(path, name));
  };
  for (const part of [
    'resources',
    'skills-active/current',
    'guardian/trusted-revocations',
  ])
    walk(join(home, 'auriga', part));
  return canonicalize(found);
}
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
    this.retryDelay = options.retryDelay ?? 25;
    this.authorizedLedger = join(
      this.guardian.directory,
      'authorized-ledger.json',
    );
    this.privateOnly = false;
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
    assertLocalPath(ownerFile);
    const names = readdirSync(directory).sort();
    const owner = existsSync(ownerFile)
      ? readRecord(ownerFile, 'UnifiedHostLockOwner')
      : null;
    if (
      !owner &&
      names.some(
        (n) =>
          !/^owner\.json\.[0-9a-f-]{36}\.tmp$/.test(n) ||
          !lstatSync(join(directory, n)).isFile(),
      )
    )
      throw new Error('unknown incomplete lock contents');
    const known = [
      this.previousLauncher,
      this.tree.record.root,
      ...this.tree.record.children,
      ...(this.tree.stoppedWriters ?? []),
    ].filter(Boolean);
    if (
      owner &&
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
    if (
      owner
        ? readRecord(ownerFile, 'UnifiedHostLockOwner').lockId !== owner.lockId
        : existsSync(ownerFile) ||
          canonicalize(readdirSync(directory).sort()) !== canonicalize(names)
    )
      throw retryError('lock owner changed');
    const retired = `${directory}.${randomUUID()}.retired`;
    renameSync(directory, retired);
    if (canonicalize(readdirSync(retired).sort()) !== canonicalize(names))
      throw new Error('stale lock changed');
    for (const name of names) {
      assertLocalPath(join(retired, name));
      unlinkSync(join(retired, name));
    }
    rmdirSync(retired);
  }

  snapshot() {
    return this.guardian.locked(() => ({
      state: this.guardian.read(),
      ledgers: observeLedgers(this.home),
      marks: this.marks(),
    }));
  }
  privateResult(error) {
    this.privateOnly = true;
    this.privateReason = error?.message;
    // Failed shared activation must not hash an unchanged, large staging set
    // every second while private Desktop is running. Any file change retries now.
    try {
      this.privateRejection = {
        stamp: resourceStamp(this.home),
        trust: this.trust,
        until: this.now() + 30000,
      };
    } catch {
      this.privateRejection = undefined;
    }
    return { privateOnly: true };
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
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        const result = await this.reconcileOnce(startup);
        this.busySince = undefined;
        return result;
      } catch (error) {
        if (error.code === 'EEXIST' || error.code === 'RESOURCE_CHANGED') {
          const state = this.lastState;
          const busyNow = this.guardian.lockTime().monotonicMillis;
          this.busySince ??= busyNow;
          if (
            state?.guardian.state === 'active' &&
            !checkLease(state.clock, state.guardian.lease, {
              ...this.guardian.time(),
              wallMillis: this.now(),
            }).valid
          ) {
            await this.tree.stopTree();
            await this.recoverLock();
            await this.guardian.disable('lease_expired');
            this.privateOnly = true;
            return { privateOnly: true, restart: true };
          }
          if (error.code === 'EEXIST' && busyNow - this.busySince > 30000) {
            let owner;
            try {
              owner = readRecord(
                join(this.guardian.directory, 'host.lock.d/owner.json'),
                'UnifiedHostLockOwner',
              );
            } catch {
              return { deferred: true };
            }
            const clock = this.guardian.lockTime();
            if (
              owner.bootId === clock.bootId &&
              clock.monotonicMillis - owner.monotonicMillis >
                owner.leaseDurationMillis
            ) {
              await this.recoverLock();
              this.restartPending = true;
              this.busySince = undefined;
              continue;
            }
          }
          if (attempt < 2) {
            await delay(this.retryDelay * (attempt + 1));
            continue;
          }
          return { deferred: true, restart: Boolean(this.restartPending) };
        }
        let old;
        try {
          old = this.guardian.locked(() => this.guardian.read());
        } catch (check) {
          if (check.code !== 'EEXIST') throw check;
          if (attempt < 2) {
            await delay(this.retryDelay);
            continue;
          }
          return { deferred: true };
        }
        if (
          (!old || old.guardian.state === 'private_only') &&
          this.guardian.cleared()
        ) {
          return this.privateResult(error);
        }
        if (!old) throw error;
        await this.guardian.disable('verification_failed');
        this.privateOnly = true;
        return { privateOnly: true };
      }
    }
  }
  async reconcileOnce(startup) {
    const snapshot = this.snapshot(),
      old = snapshot.state;
    this.lastState = old;
    if (
      (!old || old.guardian.state === 'private_only') &&
      this.guardian.cleared() &&
      this.privateRejection &&
      this.privateRejection.trust === this.trust &&
      this.now() < this.privateRejection.until &&
      this.privateRejection.stamp === resourceStamp(this.home)
    ) {
      this.privateOnly = true;
      return { privateOnly: true };
    }
    const candidates = snapshot.ledgers.filter((s) =>
      s.ledger.installations.some(installationEnabled),
    );
    if (!candidates.length) {
      if (old?.guardian.state === 'active' || !this.guardian.cleared()) {
        if (!old) throw new Error('unowned shared view; startup blocked');
        await this.guardian.disable('identity_changed');
      }
      this.privateOnly = Boolean(old);
      return { privateOnly: true };
    }
    let stoppedForRestart = false;
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
      const advancing =
        old?.guardian.state === 'active' &&
        (candidate.ledger.selectionRevision !==
          old.guardian.selectionRevision ||
          candidate.ledger.generation !== old.guardian.generation);
      if (advancing) {
        if (
          candidate.ledger.selectionRevision < old.guardian.selectionRevision ||
          candidate.ledger.generation < old.guardian.generation
        )
          throw new Error('resource revision rollback');
        if (
          !candidate.transactions.some(
            (t) =>
              t.phase === 'prepared' &&
              t.expectedLedgerRevision === candidate.ledger.revision &&
              t.selectionRevision === candidate.ledger.selectionRevision &&
              t.generation === candidate.ledger.generation,
          )
        )
          throw new Error('new revision requires prepared transaction');
      }
      if (old?.guardian.state === 'private_only' && !this.guardian.cleared())
        throw new Error('shared content restored without authorization');
      const sample = { ...this.guardian.time(), wallMillis: this.now() };
      if (old?.guardian.state === 'active') {
        const lease = checkLease(old.clock, old.guardian.lease, sample);
        if (!lease.valid) throw new Error(lease.reason);
      }
      const stamp = resourceStamp(this.home);
      if (
        !startup &&
        !advancing &&
        old?.guardian.state === 'active' &&
        this.lastStamp === stamp &&
        this.lastTrust === this.trust &&
        this.now() - this.lastVerifiedAt < 30000 &&
        Date.parse(old.guardian.lease.expiresAt) - this.now() > 30000 &&
        sample.wallMillis <=
          Date.parse(old.clock.maxObservedTime) +
            sample.monotonicMillis -
            old.clock.lastMonotonicMillis +
            1000
      )
        return { active: true };
      let previous = this.activeExpected ?? new Map();
      if (existsSync(this.authorizedLedger)) {
        const ledger = readRecord(this.authorizedLedger, 'UnifiedLedgerFile');
        if (identityMatches(ledger, candidate.ledger))
          previous = acceptedView(
            { directory: candidate.directory, ledger },
            this.trust,
            new Date(this.now()).toISOString(),
          );
      }
      const verified = verifySnapshot(
        candidate,
        this.trust,
        snapshot.marks,
        new Date(this.now()).toISOString(),
        previous,
      );
      if (!verified) throw new Error('empty verification');
      if (
        old?.guardian.state === 'active' &&
        verified.lease.revocationRevision <
          old.guardian.lease.revocationRevision
      )
        throw new Error('revocation revision rollback');
      const fresh =
        !old ||
        old.guardian.state !== 'active' ||
        old.clock.bootId !== sample.bootId;
      const issued = Math.min(
        ...candidate.ledger.installations
          .filter(installationEnabled)
          .map((i) => Date.parse(i.resource.envelope.statement.issuedAt)),
      );
      if (fresh && issued < this.startedAt - (old ? 0 : 120000))
        throw new Error('fresh signed online trust required');
      const effective = Math.max(
        sample.wallMillis,
        old
          ? Date.parse(old.clock.maxObservedTime) +
              (old.clock.bootId === sample.bootId
                ? sample.monotonicMillis - old.clock.lastMonotonicMillis
                : 0)
          : 0,
        old ? Date.parse(old.clock.lastTrustedTime) : 0,
      );
      if (effective >= Date.parse(verified.lease.expiresAt))
        throw new Error('lease_expired');
      if (
        fresh &&
        old?.guardian.state === 'private_only' &&
        !(await this.tree.isStopped())
      ) {
        await this.tree.stopTree();
        stoppedForRestart = true;
        await this.recoverLock();
      }
      this.guardian.locked((lock) => {
        if (
          canonicalize(observeLedgers(this.home)) !==
            canonicalize(snapshot.ledgers) ||
          canonicalize(this.marks()) !== canonicalize(snapshot.marks) ||
          canonicalize(this.guardian.read()) !== canonicalize(old)
        )
          throw retryError('resource snapshot changed');
        recheckBytes(verified);
        if (
          Math.max(
            this.now(),
            effective +
              this.guardian.time().monotonicMillis -
              sample.monotonicMillis,
          ) >= Date.parse(verified.lease.expiresAt)
        )
          throw new Error('lease_expired');
        const marks = snapshot.marks;
        let marksChanged = false;
        for (const next of verified.releases) {
          const existing = marks.entries.find(
            (w) =>
              w.tenantId === next.tenantId && w.packageId === next.packageId,
          );
          if (!existing) {
            marks.entries.push(next);
            marksChanged = true;
          } else if (next.releaseSequence > existing.releaseSequence) {
            Object.assign(existing, next);
            marksChanged = true;
          }
        }
        if (marksChanged || !existsSync(this.watermarks)) {
          marks.revision++;
          requireSchema('UnifiedHighWatermarksFile', marks);
          lock.check();
          durableJson(this.watermarks, marks);
        }
        const deadline =
          sample.monotonicMillis +
          Date.parse(verified.lease.expiresAt) -
          effective;
        const state = old ?? { schemaVersion: 1, revision: 1 };
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
        const was = old?.guardian.state;
        state.guardian = {
          tenantId: candidate.ledger.tenantId,
          ownerId: candidate.ledger.ownerId,
          hostId: candidate.ledger.hostId,
          selectionRevision: candidate.ledger.selectionRevision,
          generation: candidate.ledger.generation,
          state: was ?? 'verifying',
          reason: 'startup',
          lease: verified.lease,
          processStopped: startup || stoppedForRestart,
          viewCleared: this.guardian.cleared(),
          newSessionRequired: false,
        };
        if (was === 'private_only')
          this.guardian.move(
            state,
            'retry_shared',
            { onlineVerified: true, processStopped: true, viewCleared: true },
            lock,
          );
        else if (was) this.guardian.move(state, 'restart', {}, lock);
        else this.guardian.write(state, lock);
        this.guardian.move(
          state,
          'trust_valid',
          {
            onlineVerified: true,
            leaseValid: true,
            clockValid: true,
            identityMatches: true,
            verificationDurable: true,
            noPendingIsolation: !state.pendingIsolation,
          },
          lock,
        );
        if (
          candidate.ledger.installations
            .filter(installationEnabled)
            .every((i) => i.state !== 'applying')
        ) {
          if (
            !existsSync(this.authorizedLedger) ||
            canonicalize(
              readRecord(this.authorizedLedger, 'UnifiedLedgerFile'),
            ) !== canonicalize(candidate.ledger)
          )
            durableJson(this.authorizedLedger, candidate.ledger);
          this.activeExpected = verified.expected;
        } else if (!this.activeExpected && this.guardian.cleared())
          this.activeExpected = verified.expected;
        this.lastState = state;
      });
      this.privateOnly = false;
      this.privateRejection = undefined;
      this.lastStamp = stamp;
      this.lastTrust = this.trust;
      this.lastVerifiedAt = this.now();
      return { active: true, restart: stoppedForRestart };
    } catch (error) {
      if (error.code === 'EEXIST' || error.code === 'RESOURCE_CHANGED') {
        if (stoppedForRestart) this.restartPending = true;
        throw error;
      }
      if (error.revocationList)
        this.guardian.locked((lock) => {
          const list = error.revocationList,
            directory = join(
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
      if (!old) {
        if (!this.guardian.cleared()) throw error;
        return this.privateResult(error);
      }
      if (old.guardian.state === 'private_only' && this.guardian.cleared()) {
        return { ...this.privateResult(error), restart: stoppedForRestart };
      }
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
      this.privateOnly = true;
      return { privateOnly: true, restart: true };
    }
  }
}

export function packagedTrust(dir) {
  const file = join(dir, 'auriga', 'runtime', 'resource-roots.json');
  return existsSync(file) ? parseStrictJson(readBounded(file)) : null;
}
