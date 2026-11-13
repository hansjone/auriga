import { verifyTrust } from './resource-trust.mjs';
import { guardianProtocol } from './guardian-protocol.mjs';
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
  trustedClockTime,
} from './host-lock.mjs';

export function checkLease(clock, lease, sample) {
  try {
    verifyTrust(
      'clock',
      {
        clock,
        bootId: sample.bootId,
        monotonicNowMillis: sample.monotonicMillis,
        wallNow: new Date(sample.wallMillis).toISOString(),
        leaseExpiresAt: lease.expiresAt,
      },
      {},
    );
  } catch (error) {
    return { valid: false, reason: error.message };
  }
  const effective = Math.max(
    sample.wallMillis,
    Date.parse(clock.maxObservedTime) +
      sample.monotonicMillis -
      clock.lastMonotonicMillis,
    Date.parse(clock.lastTrustedTime),
  );

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
    this.time = ports.time ?? trustedClockTime;
    this.lockTime = ports.time ?? hostTime;
  }

  locked(action) {
    const lock = acquireHostLock(this.home, 'E', this.lockTime);
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

  move(state, event, facts, lock) {
    state.guardian.state = guardianTransition(
      guardianProtocol,
      state.guardian.state,
      event,
      facts,
    ).to;
    this.write(state, lock);
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
    const stopped = await this.ports.isStopped();
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
        reason:
          recovering && reasons.includes(state.guardian.reason)
            ? state.guardian.reason
            : reason,
        processStopped: stopped,
        viewCleared: false,
        newSessionRequired: true,
      };
      if (
        state.guardian.state === 'active' &&
        [
          'lease_expired',
          'revoked',
          'identity_changed',
          'clock_rollback',
        ].includes(reason)
      )
        this.move(state, reason, {}, lock);
      else {
        if (state.guardian.state !== 'verifying')
          this.move(state, 'restart', {}, lock);
        this.move(
          state,
          'trust_invalid',
          { processRunning: !stopped, processStopped: stopped },
          lock,
        );
      }
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
        state.guardian.processStopped = true;
        if (state.guardian.state === 'stopping')
          this.move(state, 'stop_confirmed', { processStopped: true }, lock);
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
        state.guardian.viewCleared = true;
        delete state.pendingIsolation;
        this.move(
          state,
          'isolation_confirmed',
          {
            processStopped: true,
            viewCleared: true,
            disableReceiptDurable: true,
          },
          lock,
        );
      });
    } catch (error) {
      this.locked((lock) => {
        const state = this.read();
        this.move(
          state,
          state.guardian.state === 'stopping'
            ? 'stop_failed'
            : 'isolation_failed',
          {},
          lock,
        );
      });
      throw error;
    }
  }
}
