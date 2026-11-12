import { randomUUID } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  renameSync,
} from 'node:fs';
import { join } from 'node:path';
import { requireSchema } from './schema.mjs';
import { parseStrictJson } from './json.mjs';
import {
  acquireHostLock,
  assertLocalPath,
  durableJson,
  hostTime,
} from './host-lock.mjs';

export function checkLease(clock, lease, sample) {
  const maximum = Date.parse(clock.maxObservedTime);
  const trusted = Date.parse(clock.lastTrustedTime);
  const expires = Date.parse(lease.expiresAt);
  if (
    ![
      maximum,
      trusted,
      expires,
      sample.wallMillis,
      sample.monotonicMillis,
      clock.lastMonotonicMillis,
      clock.leaseDeadlineMonotonicMillis,
    ].every(Number.isFinite)
  )
    return { valid: false, reason: 'verification_failed' };
  if (sample.bootId !== clock.bootId)
    return { valid: false, reason: 'verification_failed' };
  if (
    sample.monotonicMillis < clock.lastMonotonicMillis ||
    sample.wallMillis < maximum - 120000
  )
    return { valid: false, reason: 'clock_rollback' };
  const effective = Math.max(
    sample.wallMillis,
    maximum + sample.monotonicMillis - clock.lastMonotonicMillis,
    trusted,
  );
  if (
    effective >= expires ||
    sample.monotonicMillis >= clock.leaseDeadlineMonotonicMillis
  )
    return { valid: false, reason: 'lease_expired' };
  return {
    valid: true,
    clock: {
      ...clock,
      maxObservedTime: new Date(effective).toISOString(),
      lastMonotonicMillis: sample.monotonicMillis,
    },
  };
}

export function guardianTransition(protocol, state, event, facts) {
  const rule = protocol.transitions.find(
    (r) =>
      r.from === state &&
      r.event === event &&
      r.guards.every((guard) => facts[guard] === true),
  );
  if (!rule) throw new Error('guardian transition rejected');
  return rule;
}

export class DesktopGuardian {
  constructor(dataHome, ports) {
    assertLocalPath(dataHome);
    this.home = dataHome;
    this.ports = ports;
    this.directory = join(dataHome, 'auriga', 'guardian');
    this.file = join(this.directory, 'state.json');
    this.current = join(dataHome, 'auriga', 'skills-active', 'current');
    this.time = ports.time ?? hostTime;
  }

  locked(action) {
    const lock = acquireHostLock(this.home, 'E', this.time);
    try {
      lock.check();
      return action(lock);
    } finally {
      lock.release();
    }
  }

  read() {
    assertLocalPath(this.file);
    if (!existsSync(this.file)) return null;
    const state = requireSchema(
      'UnifiedGuardianStateFile',
      parseStrictJson(readFileSync(this.file), true),
    );
    if (
      state.schemaVersion !== 1 ||
      !Number.isSafeInteger(state.revision) ||
      state.revision < 1 ||
      !state.guardian ||
      !state.clock ||
      Object.keys(state).some(
        (k) =>
          ![
            'schemaVersion',
            'revision',
            'guardian',
            'clock',
            'pendingIsolation',
            'lastDisableReceiptId',
          ].includes(k),
      )
    )
      throw new Error('invalid guardian state');
    const uuid =
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
    const g = state.guardian;
    if (
      !['tenantId', 'ownerId', 'hostId'].every((k) => uuid.test(g[k])) ||
      !['selectionRevision', 'generation'].every(
        (k) => Number.isSafeInteger(g[k]) && g[k] > 0,
      ) ||
      ![
        'verifying',
        'active',
        'stopping',
        'isolating',
        'private_only',
        'blocked',
      ].includes(g.state)
    )
      throw new Error('invalid guardian identity or state');
    if (state.pendingIsolation) {
      const p = state.pendingIsolation;
      if (
        !uuid.test(p.transactionId) ||
        p.quarantinePath !==
          `auriga/skills-active/quarantine/${p.transactionId}` ||
        !['stopping', 'rename_pending', 'renamed', 'receipt_written'].includes(
          p.phase,
        ) ||
        Object.keys(p).sort().join(',') !== 'phase,quarantinePath,transactionId'
      )
        throw new Error('invalid pending isolation');
    }
    return state;
  }

  write(state, lock) {
    lock.check();
    state.revision++;
    requireSchema('UnifiedGuardianStateFile', state);
    durableJson(this.file, state);
  }

  cleared() {
    assertLocalPath(this.current);
    return !existsSync(this.current) || readdirSync(this.current).length === 0;
  }

  // No plugin is needed here: stop and verify the whole owned tree before touching current.
  async disable(reason) {
    const reasons = [
      'lease_expired',
      'revoked',
      'identity_changed',
      'clock_rollback',
      'verification_failed',
    ];
    if (!reasons.includes(reason)) throw new Error('invalid disable reason');
    this.locked((lock) => {
      const state = this.read();
      if (!state)
        throw new Error('shared view has no trusted ownership record');
      const recovering = Boolean(state.pendingIsolation);
      if (!state.pendingIsolation) {
        const transactionId = randomUUID();
        state.pendingIsolation = {
          transactionId,
          quarantinePath: `auriga/skills-active/quarantine/${transactionId}`,
          phase: 'stopping',
        };
      }
      state.guardian = {
        ...state.guardian,
        state: 'stopping',
        reason:
          recovering && reasons.includes(state.guardian.reason)
            ? state.guardian.reason
            : reason,
        processStopped: false,
        viewCleared: false,
        newSessionRequired: true,
      };
      this.write(state, lock);
    });
    try {
      await this.ports.stopTree();
      if (!(await this.ports.isStopped()))
        throw new Error('DSH tree still running');
      this.locked((lock) => {
        const state = this.read();
        const pending = state.pendingIsolation;
        const quarantine = join(this.home, pending.quarantinePath);
        assertLocalPath(quarantine);
        state.guardian.state = 'isolating';
        state.guardian.processStopped = true;
        pending.phase = 'rename_pending';
        this.write(state, lock);
        if (existsSync(quarantine) && !this.cleared())
          throw new Error('ambiguous isolation recovery');
        if (!existsSync(quarantine) && existsSync(this.current)) {
          mkdirSync(join(this.home, 'auriga', 'skills-active', 'quarantine'), {
            recursive: true,
          });
          assertLocalPath(quarantine);
          lock.check();
          renameSync(this.current, quarantine);
        }
        if (!this.cleared()) throw new Error('shared view not cleared');
        pending.phase = 'renamed';
        this.write(state, lock);
        const receiptId = pending.transactionId;
        const receipts = join(this.directory, 'disable-receipts');
        mkdirSync(receipts, { recursive: true });
        assertLocalPath(receipts);
        const { tenantId, ownerId, hostId, selectionRevision, generation } =
          state.guardian;
        lock.check();
        const receipt = {
          schemaVersion: 1,
          tenantId,
          ownerId,
          hostId,
          recordType: 'disable',
          writer: 'guardian',
          receiptId,
          transactionId: pending.transactionId,
          selectionRevision,
          generation,
          quarantinePath: pending.quarantinePath,
          reason: state.guardian.reason,
          processStopped: true,
          viewCleared: true,
          newSessionRequired: true,
          recordedAt: new Date().toISOString(),
        };
        const receiptFile = join(receipts, `${receiptId}.json`);
        requireSchema('UnifiedReceiptFile', receipt);
        assertLocalPath(receiptFile);
        if (existsSync(receiptFile)) {
          const existing = requireSchema(
            'UnifiedReceiptFile',
            parseStrictJson(readFileSync(receiptFile), true),
          );
          if (
            Object.keys(receipt).some(
              (k) => k !== 'recordedAt' && receipt[k] !== existing[k],
            )
          )
            throw new Error('disable receipt conflicts with recovery');
        } else durableJson(receiptFile, receipt);
        state.lastDisableReceiptId = receiptId;
        pending.phase = 'receipt_written';
        this.write(state, lock);
        state.guardian = {
          ...state.guardian,
          state: 'private_only',
          viewCleared: true,
        };
        delete state.pendingIsolation;
        this.write(state, lock);
      });
    } catch (error) {
      this.locked((lock) => {
        const state = this.read();
        state.guardian.state = 'blocked';
        this.write(state, lock);
      });
      throw error;
    }
  }

  async tick() {
    const state = this.locked(() => this.read());
    if (state?.guardian.state !== 'active') return;
    const sample = { ...this.time(), wallMillis: Date.now() };
    const lease = checkLease(state.clock, state.guardian.lease, sample);
    let reason = lease.valid ? null : lease.reason;
    if (!reason) {
      // This trusted port must independently verify the complete C snapshot, never a plugin boolean.
      const observation = await this.ports.verifyCurrent(state);
      if (!observation?.valid)
        reason = observation?.reason ?? 'verification_failed';
    }
    if (reason) return this.disable(reason);
    this.locked((lock) => {
      const latest = this.read();
      if (latest.revision !== state.revision)
        throw new Error('guardian snapshot changed');
      latest.clock = lease.clock;
      this.write(latest, lock);
    });
  }
}

// P1 round 2 permits private-only restart after verified quarantine, with a session warning.
export function assertUnmanagedStart(dataHome) {
  assertLocalPath(dataHome);
  const guardian = new DesktopGuardian(dataHome, {});
  guardian.locked(() => {
    const state = guardian.read();
    if (
      (state &&
        (state.guardian.state !== 'private_only' || state.pendingIsolation)) ||
      !guardian.cleared()
    )
      throw new Error(
        '共享资源需完成可信重验与新会话门禁，当前拒绝启动。历史与私有技能已保留。',
      );
  });
}
