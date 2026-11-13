import {
  desiredState,
  requireInstallation,
  assertSelectionProgress,
} from './selection-state.js';
import { commitMoves } from './file-commit.js';
import { createHash, randomUUID } from 'node:crypto';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
  openSync,
  fsyncSync,
  closeSync,
} from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import {
  acquireHostLock,
  assertLocalPath,
  durableJson,
  durableRecords,
  hostTime,
  clockBootId,
} from './host-lock.js';
import { requireSchema, schemaValid } from './schema.js';
import { parseStrictJson, canonicalize } from './json.js';
import { refuse, uuid } from './trust.js';
import { unpackSkill } from './skill-package.js';
export const sha = (/** @type {Uint8Array} */ bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
/** @param {string} file @param {string} schema */
function record(file, schema) {
  assertLocalPath(file);
  const stat = lstatSync(file);
  if (!stat.isFile() || stat.size > 2097152)
    refuse('resource.storage_unavailable');
  return requireSchema(schema, parseStrictJson(readFileSync(file), true));
}
/** Deterministic UUID sibling for a transaction's retained prior view.
 * @param {string} id */
const previousId = (id) =>
  id.slice(0, -1) + (parseInt(id.at(-1) ?? '0', 16) ^ 1).toString(16);
/** C owns content transactions; E alone owns guardian state and high watermarks.
 * @param {{home:string,time?:typeof hostTime}} options */
export function desktopSkills({ home, time = hostTime }) {
  assertLocalPath(home);
  const guardian = join(home, 'auriga/guardian'),
    active = join(home, 'auriga/skills-active/current');
  /** @param {any} who */
  function identity(who) {
    if (!['tenantId', 'ownerId', 'hostId'].every((k) => uuid(who?.[k])))
      refuse('common.unauthenticated');
    return { tenantId: who.tenantId, ownerId: who.ownerId, hostId: who.hostId };
  }
  /** @param {any} who */
  const root = (who) =>
    join(
      home,
      'auriga/resources',
      identity(who).tenantId,
      who.ownerId,
      who.hostId,
      'skills',
    );
  /** @param {any} who @param {any} value */
  function same(who, value) {
    return ['tenantId', 'ownerId', 'hostId'].every((k) => who[k] === value[k]);
  }
  /** E owns boot verification. C consumes its active permit and enforces the same persisted lease deadline.
   * @param {any} who @param {any} value @param {any} state */
  function permitted(who, value, state) {
    const g = state.guardian,
      c = state.clock,
      stamp = time(),
      wall = Date.now();
    return (
      clockBootId(stamp.bootId) === c.bootId &&
      !state.pendingIsolation &&
      same(who, g) &&
      g.state === 'active' &&
      g.selectionRevision === value.selectionRevision &&
      g.generation === value.generation &&
      stamp.monotonicMillis >= c.lastMonotonicMillis &&
      stamp.monotonicMillis < c.leaseDeadlineMonotonicMillis &&
      wall >= Date.parse(c.maxObservedTime) - 120000 &&
      Math.max(
        wall,
        Date.parse(c.maxObservedTime) +
          stamp.monotonicMillis -
          c.lastMonotonicMillis,
        Date.parse(c.lastTrustedTime),
      ) < Date.parse(g.lease.expiresAt)
    );
  }
  /** @template T @param {(lock:ReturnType<typeof acquireHostLock>)=>T} work */
  function locked(work) {
    const lock = acquireHostLock(home, 'C', time);
    try {
      return work(lock);
    } finally {
      lock.release();
    }
  }
  /** @param {any} who */
  function ledger(who) {
    const file = join(root(who), 'ledger.json');
    if (!existsSync(file)) return undefined;
    const value = record(file, 'UnifiedLedgerFile');
    if (!same(who, value)) refuse('resource.storage_unavailable');
    for (const row of value.installations) requireInstallation(row);
    return value;
  }
  /** Preserve interrupted owned files outside all discovery/cache readers.
   * @param {any} who @param {string} file */
  function orphan(who, file) {
    assertLocalPath(file);
    if (!lstatSync(file).isFile()) refuse('resource.storage_unavailable');
    const directory = join(root(who), 'staging', randomUUID());
    assertLocalPath(directory);
    mkdirSync(directory, { recursive: true });
    commitMoves([[file, join(directory, 'interrupted-file')]]);
  }
  /** @param {any} who @param {Buffer} bytes @param {[string,string][]} [moves] */
  function cache(who, bytes, moves) {
    const digest = sha(bytes),
      dir = join(root(who), 'cache', digest.slice(7));
    assertLocalPath(dir);
    mkdirSync(dir, { recursive: true });
    if (moves?.some(([, destination]) => destination === join(dir, 'bytes')))
      return;
    for (const name of readdirSync(dir))
      if (/^bytes\.[a-f0-9-]{36}\.tmp$/.test(name))
        orphan(who, join(dir, name));
    const target = join(dir, 'bytes');
    assertLocalPath(target);
    if (existsSync(target)) {
      if (!lstatSync(target).isFile()) refuse('resource.storage_unavailable');
      if (
        lstatSync(target).size === bytes.length &&
        sha(readFileSync(target)) === digest
      )
        return;
      orphan(who, target);
    }
    const temporary = target + '.' + randomUUID() + '.tmp',
      fd = openSync(temporary, 'wx', 0o600);
    try {
      writeFileSync(fd, bytes);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    if (moves) moves.push([temporary, target]);
    else commitMoves([[temporary, target]]);
  }
  /** @param {any} who @param {{check:()=>void}} lock */
  function recoverRecordTemps(who, lock) {
    const directory = root(who);
    assertLocalPath(directory);
    if (!existsSync(directory)) return;
    for (const parent of [directory, join(directory, 'transactions')]) {
      assertLocalPath(parent);
      if (!existsSync(parent)) continue;
      const entries = readdirSync(parent);
      if (entries.length > 8192) refuse('resource.storage_unavailable');
      for (const name of entries)
        if (/^(?:ledger|[a-f0-9-]{36})\.json\.[a-f0-9-]{36}\.tmp$/.test(name)) {
          lock.check();
          orphan(who, join(parent, name));
        }
    }
    const receipts = join(directory, 'receipts');
    assertLocalPath(receipts);
    if (!existsSync(receipts)) return;
    const packages = readdirSync(receipts);
    if (packages.length > 8192) refuse('resource.storage_unavailable');
    for (const packageId of packages) {
      if (!uuid(packageId)) refuse('resource.storage_unavailable');
      const parent = join(receipts, packageId);
      assertLocalPath(parent);
      const names = readdirSync(parent);
      if (names.length > 8192) refuse('resource.storage_unavailable');
      for (const name of names)
        if (/^[a-f0-9-]{36}\.json\.[a-f0-9-]{36}\.tmp$/.test(name)) {
          lock.check();
          orphan(who, join(parent, name));
        }
    }
  }
  /** @param {any} who @param {string} digest */
  function cached(who, digest) {
    if (!/^sha256:[a-f0-9]{64}$/.test(digest))
      refuse('resource.signature_invalid');
    const file = join(root(who), 'cache', digest.slice(7), 'bytes');
    assertLocalPath(file);
    const stat = lstatSync(file);
    if (!stat.isFile() || stat.size > 20971520)
      refuse('resource.storage_unavailable');
    const bytes = readFileSync(file);
    if (sha(bytes) !== digest) refuse('resource.digest_mismatch');
    return bytes;
  }
  /** Reconstruct the complete view from verified immutable cache, never from the old current tree.
   * @param {any} who @param {any[]} rows */
  function tree(who, rows) {
    /** @type {Map<string,Buffer>} */ const files = new Map();
    const names = new Set();
    for (const row of rows.filter(
      (r) =>
        desiredState(r.resource) === 'ENABLED' &&
        ['applying', 'applied', 'pending_restart', 'overridden'].includes(
          r.state,
        ),
    )) {
      requireInstallation(row);
      const ref = row.resource,
        manifest = parseStrictJson(cached(who, ref.identity.manifestDigest));
      if (
        manifest.kind !== 'skill' ||
        names.has(manifest.name) ||
        manifest.name !== ref.envelope.statement.name ||
        manifest.artifacts.length !== 1
      )
        refuse('resource.state_conflict');
      names.add(manifest.name);
      for (const [path, bytes] of Object.entries(
        unpackSkill(cached(who, manifest.artifacts[0].digest), manifest),
      )) {
        if (/~[0-9]/i.test(path)) refuse('common.validation_failed');
        files.set(manifest.name + '/' + path, bytes);
      }
    }
    return files;
  }
  /** @param {string} dir @param {Map<string,Buffer>} expected */
  function checkTree(dir, expected) {
    assertLocalPath(dir);
    const observed = new Map();
    /** @param {string} parent @param {string} prefix */
    function visit(parent, prefix) {
      assertLocalPath(parent);
      for (const entry of readdirSync(parent, { withFileTypes: true })) {
        const path = join(parent, entry.name);
        assertLocalPath(path);
        if (observed.size > 65536) refuse('resource.storage_unavailable');
        if (entry.isDirectory()) visit(path, prefix + entry.name + '/');
        else if (entry.isFile()) {
          const key = prefix + entry.name;
          if (
            !expected.has(key) ||
            lstatSync(path).size !== expected.get(key)?.length
          )
            refuse('resource.state_conflict');
          observed.set(key, readFileSync(path));
        } else refuse('resource.state_conflict');
      }
    }
    visit(dir, '');
    if (
      observed.size !== expected.size ||
      [...observed].some(([k, b]) => !b.equals(expected.get(k)))
    )
      refuse('resource.state_conflict');
  }
  /** @param {any} who @param {any} value @param {{check:()=>void}} lock @returns {void} */
  function save(who, value, lock) {
    requireSchema('UnifiedLedgerFile', value);
    lock.check();
    const file = join(root(who), 'ledger.json');
    if (existsSync(file)) {
      assertLocalPath(file);
      cache(who, readFileSync(file));
    }
    lock.check();
    durableJson(file, value);
  }
  /** Quarantine can belong to an older generation; recover its exact owned view from immutable ledger history.
   * @param {any} who @param {any} receipt @param {any} current */
  function receiptTree(who, receipt, current) {
    const candidates = [current],
      directory = join(root(who), 'cache');
    assertLocalPath(directory);
    const names = readdirSync(directory);
    if (names.length > 8192) refuse('resource.storage_unavailable');
    for (const name of names) {
      const file = join(directory, name, 'bytes');
      assertLocalPath(file);
      if (
        !/^[a-f0-9]{64}$/.test(name) ||
        !existsSync(file) ||
        lstatSync(file).size > 2097152
      )
        continue;
      const bytes = readFileSync(file);
      if (bytes[0] !== 123 || sha(bytes) !== 'sha256:' + name) continue;
      let candidate;
      try {
        candidate = parseStrictJson(bytes, true);
      } catch {
        continue;
      }
      if (
        schemaValid('unified', 'UnifiedLedgerFile', candidate) &&
        same(who, candidate)
      )
        candidates.push(candidate);
    }
    const matching = candidates
      .filter(
        (value) =>
          value.selectionRevision === receipt.selectionRevision &&
          value.generation === receipt.generation &&
          value.installations.some(
            (/** @type {any} */ row) =>
              desiredState(row.resource) === 'ENABLED' &&
              ['applying', 'applied', 'pending_restart', 'overridden'].includes(
                row.state,
              ),
          ),
      )
      .sort((a, b) => b.revision - a.revision);
    if (!matching.length) refuse('resource.state_conflict');
    return tree(who, matching[0].installations);
  }
  /** @param {any} who @param {{check:()=>void}} lock @returns {void} */
  function cleanup(who, lock) {
    const value = ledger(who);
    if (!value) return;
    const receipts = join(guardian, 'disable-receipts');
    assertLocalPath(receipts);
    if (!existsSync(receipts)) return;
    const entries = readdirSync(receipts);
    if (entries.length > 8192) refuse('resource.storage_unavailable');
    value.processedDisableReceiptIds = value.processedDisableReceiptIds.filter(
      (/** @type {string} */ id) => entries.includes(id + '.json'),
    );
    for (const name of entries) {
      if (!/^[a-f0-9-]{36}\.json$/.test(name))
        refuse('resource.storage_unavailable');
      const receipt = record(join(receipts, name), 'UnifiedReceiptFile');
      if (
        receipt.recordType !== 'disable' ||
        !same(who, receipt) ||
        value.processedDisableReceiptIds.includes(receipt.receiptId)
      )
        continue;
      if (
        name !== receipt.receiptId + '.json' ||
        receipt.quarantinePath !==
          'auriga/skills-active/quarantine/' + receipt.transactionId
      )
        refuse('resource.storage_unavailable');
      const tx = {
        schemaVersion: 1,
        ...identity(who),
        transactionId: randomUUID(),
        writer: 'plugin',
        operation: 'cleanup',
        phase: 'prepared',
        expectedLedgerRevision: value.revision,
        selectionRevision: value.selectionRevision,
        generation: value.generation,
        receiptId: receipt.receiptId,
        recordedAt: new Date().toISOString(),
      };
      const file = join(root(who), 'transactions', tx.transactionId + '.json');
      mkdirSync(dirname(file), { recursive: true });
      lock.check();
      durableJson(file, requireSchema('UnifiedTransactionFile', tx));
      const path = resolve(home, receipt.quarantinePath);
      assertLocalPath(path);
      if (existsSync(path)) {
        try {
          checkTree(path, receiptTree(who, receipt, value));
          lock.check();
          rmSync(path, { recursive: true });
        } catch (error) {
          if (
            ![
              'resource.state_conflict',
              'resource.digest_mismatch',
              'ENOENT',
            ].includes(/** @type {any} */ (error).code)
          )
            throw error;
          const parent = join(root(who), 'staging', receipt.transactionId),
            conflict = join(parent, 'conflict-view');
          assertLocalPath(conflict);
          mkdirSync(parent, { recursive: true });
          lock.check();
          commitMoves([[path, conflict]]);
          durableJson(join(parent, 'conflict.json'), {
            receiptId: receipt.receiptId,
            transactionId: receipt.transactionId,
            source: receipt.quarantinePath,
          });
        }
      }
      const transactions = join(root(who), 'transactions');
      for (const name of readdirSync(transactions)) {
        const path = join(transactions, name),
          previous = record(path, 'UnifiedTransactionFile');
        if (
          previous.operation === 'install' &&
          previous.selectionRevision === receipt.selectionRevision &&
          previous.generation === receipt.generation &&
          ['prepared', 'switched'].includes(previous.phase)
        ) {
          lock.check();
          durableJson(path, { ...previous, phase: 'rolled_back' });
        }
      }
      value.processedDisableReceiptIds.push(receipt.receiptId);
      value.revision++;
      if (
        receipt.selectionRevision === value.selectionRevision &&
        receipt.generation === value.generation
      )
        value.installations = value.installations.map(
          (/** @type {any} */ r) => ({
            ...r,
            state:
              desiredState(r.resource) === 'REMOVED'
                ? 'removed'
                : desiredState(r.resource) === 'DISABLED'
                  ? 'disabled'
                  : /** @type {Record<string,string>} */ ({
                      revoked: 'revoked',
                      lease_expired: 'expired',
                      identity_changed: 'superseded',
                      clock_rollback: 'failed',
                      verification_failed: 'failed',
                    })[receipt.reason],
          }),
        );
      save(who, value, lock);
      lock.check();
      durableJson(file, { ...tx, phase: 'committed' });
    }
  }
  /** Recover C's own interrupted identity removals without restoring an old view.
   * @param {any} who @param {{check:()=>void}} lock */
  function recoverIdentity(who, lock) {
    const value = ledger(who);
    if (!value) return;
    const directory = join(root(who), 'transactions');
    if (!existsSync(directory)) return;
    const names = readdirSync(directory);
    if (names.length > 8192) refuse('resource.storage_unavailable');
    for (const name of names) {
      const tx = record(join(directory, name), 'UnifiedTransactionFile');
      if (
        tx.operation !== 'remove' ||
        !['prepared', 'switched'].includes(tx.phase)
      )
        continue;
      if (!same(who, tx) || name !== tx.transactionId + '.json')
        refuse('resource.storage_unavailable');
      const backup = join(root(who), 'staging', tx.transactionId);
      assertLocalPath(backup);
      if (
        existsSync(backup) &&
        existsSync(active) &&
        (!lstatSync(active).isDirectory() || readdirSync(active).length)
      )
        refuse('resource.state_conflict');
      if (!existsSync(backup) && existsSync(active)) {
        lock.check();
        durableJson(join(directory, name), { ...tx, phase: 'rolled_back' });
        continue;
      }
      if (tx.expectedLedgerRevision === value.revision) {
        value.installations = value.installations.map(
          (/** @type {any} */ r) => ({ ...r, state: 'disabled' }),
        );
        value.revision++;
        save(who, value, lock);
      }
      lock.check();
      durableJson(join(directory, name), { ...tx, phase: 'committed' });
    }
  }
  /** Collect only proven unmodified owned backups after a committed transaction. Conflicts are retained.
   * @param {any} who @param {any} keep @param {{check:()=>void}} lock */
  function collectViews(who, keep, lock) {
    const dir = root(who),
      cacheDir = join(dir, 'cache'),
      history = [];
    for (const name of readdirSync(cacheDir)) {
      if (!/^[a-f0-9]{64}$/.test(name)) continue;
      const p = join(cacheDir, name, 'bytes');
      assertLocalPath(p);
      if (!existsSync(p) || lstatSync(p).size > 2097152) continue;
      const bytes = readFileSync(p);
      if (bytes[0] !== 123 || sha(bytes) !== 'sha256:' + name) continue;
      let v;
      try {
        v = parseStrictJson(bytes, true);
      } catch {
        continue;
      }
      if (schemaValid('unified', 'UnifiedLedgerFile', v) && same(who, v))
        history.push(v);
    }
    for (const name of readdirSync(join(dir, 'transactions'))) {
      const tx = record(
        join(dir, 'transactions', name),
        'UnifiedTransactionFile',
      );
      if (
        tx.phase !== 'committed' ||
        !['install', 'remove'].includes(tx.operation) ||
        tx.transactionId === keep.transactionId
      )
        continue;
      const backup = join(
        dir,
        'staging',
        tx.operation === 'install'
          ? previousId(tx.transactionId)
          : tx.transactionId,
      );
      assertLocalPath(backup);
      if (!existsSync(backup)) continue;
      const prior = history.find(
        (v) =>
          v.revision ===
          tx.expectedLedgerRevision - (tx.operation === 'install' ? 1 : 0),
      );
      if (!prior) continue;
      let unchanged = true;
      try {
        checkTree(backup, tree(who, prior.installations));
      } catch {
        unchanged = false;
      }
      lock.check();
      if (unchanged) rmSync(backup, { recursive: true });
      else {
        const parent = join(dir, 'staging', randomUUID());
        mkdirSync(parent);
        commitMoves([[backup, join(parent, 'conflict-view')]]);
        durableJson(join(parent, 'conflict.json'), {
          transactionId: tx.transactionId,
          reason: 'modified-backup',
        });
      }
    }
  }
  return {
    /** @param {any} who @param {Buffer[]} lists */
    acceptRevocations(who, lists) {
      return locked((lock) => {
        const directory = join(root(who), 'cache');
        assertLocalPath(directory);
        mkdirSync(directory, { recursive: true });
        let greatest;
        const names = readdirSync(directory);
        if (names.length > 8192) refuse('resource.storage_unavailable');
        for (const name of names) {
          if (!/^[a-f0-9]{64}$/.test(name)) continue;
          const path = join(directory, name, 'bytes');
          assertLocalPath(path);
          if (!existsSync(path) || lstatSync(path).size > 2097152) continue;
          const bytes = readFileSync(path);
          if (sha(bytes) !== 'sha256:' + name || bytes[0] !== 123) continue;
          let list;
          try {
            list = parseStrictJson(bytes, true);
          } catch {
            continue;
          }
          if (
            schemaValid('signatures', 'RevocationList', list) &&
            list.tenantId === who.tenantId &&
            (!greatest || list.revision > greatest.revision)
          )
            greatest = list;
        }
        /** @type {[string,string][]} */ const moves = [];
        for (const bytes of lists) {
          const list = parseStrictJson(bytes, true);
          if (
            !schemaValid('signatures', 'RevocationList', list) ||
            list.tenantId !== who.tenantId
          )
            refuse('resource.signature_invalid');
          if (
            greatest &&
            (list.revision < greatest.revision ||
              (list.revision === greatest.revision &&
                canonicalize(list) !== canonicalize(greatest)))
          )
            refuse('resource.signature_invalid');
          greatest = list;
          lock.check();
          cache(who, bytes, moves);
        }
        lock.check();
        commitMoves(moves);
      });
    },
    /** Identity publication must not trust E's new identity while old unowned bytes remain.
     * @param {any} who */
    assertIdentityView(who) {
      return locked(() => {
        const state = record(
          join(guardian, 'state.json'),
          'UnifiedGuardianStateFile',
        );
        if (
          !same(who, state.guardian) ||
          !existsSync(active) ||
          !readdirSync(active).length
        )
          return;
        const value = ledger(who);
        if (!value || !permitted(who, value, state))
          refuse('resource.storage_unavailable');
        checkTree(active, tree(who, value.installations));
        for (const row of value.installations.filter(
          (/** @type {any} */ r) => desiredState(r.resource) === 'ENABLED',
        )) {
          if (!['applied', 'pending_restart'].includes(row.state))
            refuse('resource.storage_unavailable');
          const receipt = record(
            join(
              root(who),
              'receipts',
              row.resource.identity.packageId,
              row.resource.identity.versionId + '.json',
            ),
            'UnifiedReceiptFile',
          );
          if (
            receipt.recordType !== 'installation' ||
            !same(who, receipt) ||
            canonicalize(receipt.resource) !== canonicalize(row.resource) ||
            receipt.installationRevision !== row.installationRevision
          )
            refuse('resource.storage_unavailable');
        }
      });
    },
    available() {
      return locked(() => {
        record(join(guardian, 'state.json'), 'UnifiedGuardianStateFile');
        return true;
      });
    },
    startup() {
      return locked((lock) => {
        const state = record(
            join(guardian, 'state.json'),
            'UnifiedGuardianStateFile',
          ),
          who = identity(state.guardian);
        recoverRecordTemps(who, lock);
        recoverIdentity(who, lock);
        try {
          cleanup(who, lock);
        } catch (error) {
          assertLocalPath(active);
          if (
            state.guardian.state !== 'private_only' ||
            !state.guardian.viewCleared ||
            (existsSync(active) &&
              (!lstatSync(active).isDirectory() || readdirSync(active).length))
          )
            throw error;
          return { cleanupPending: true };
        }
        return { cleanupPending: false };
      });
    },
    /** A changed metadata CAS invalidates a prepared permit; retry only the exact current selection.
     * @param {any} who @param {any[]} rows */
    needsPreparation(who, rows) {
      return locked((lock) => {
        if (!rows.length || !rows.some((r) => r.state === 'applying'))
          return false;
        recoverRecordTemps(who, lock);
        const value = ledger(who);
        if (!value) return true;
        if (
          rows.some(
            (r) =>
              r.selectionRevision !== value.selectionRevision ||
              r.generation !== value.generation ||
              !value.installations.some(
                (/** @type {any} */ old) =>
                  old.installationRevision === r.installationRevision &&
                  canonicalize(old.resource.identity) ===
                    canonicalize(r.resource.identity) &&
                  desiredState(old.resource) === desiredState(r.resource),
              ),
          )
        )
          return true;
        if (
          value.installations.some(
            (/** @type {any} */ r) =>
              desiredState(r.resource) === 'ENABLED' &&
              ['expired', 'revoked', 'superseded', 'failed'].includes(r.state),
          )
        )
          return false;
        if (
          !value.installations.some(
            (/** @type {any} */ r) => r.state === 'applying',
          ) &&
          !rows.some(
            (r) =>
              desiredState(r.resource) !== 'ENABLED' && r.state === 'applying',
          )
        )
          return false;
        try {
          tree(who, value.installations);
        } catch (error) {
          if (
            ['resource.digest_mismatch', 'ENOENT'].includes(
              /** @type {any} */ (error).code,
            )
          )
            return true;
          throw error;
        }
        const transactions = readdirSync(join(root(who), 'transactions'));
        if (transactions.length > 8192) refuse('resource.storage_unavailable');
        return !transactions
          .map((name) =>
            record(
              join(root(who), 'transactions', name),
              'UnifiedTransactionFile',
            ),
          )
          .some(
            (t) =>
              t.operation === 'install' &&
              t.selectionRevision === value.selectionRevision &&
              t.generation === value.generation &&
              ((t.phase === 'committed' &&
                t.expectedLedgerRevision + 1 === value.revision &&
                !value.installations.some(
                  (/** @type {any} */ r) => r.state === 'applying',
                )) ||
                (t.phase === 'prepared' &&
                  t.expectedLedgerRevision === value.revision) ||
                (t.phase === 'switched' &&
                  [value.revision, value.revision - 1].includes(
                    t.expectedLedgerRevision,
                  ))),
          );
      });
    },
    /** All expensive transfer/verification happens before this short lock.
     * @param {any} who @param {any[]} rows @param {{manifest:Buffer,artifacts:Buffer[],revocations:Buffer}[]} downloads */
    prepare(who, rows, downloads) {
      identity(who);
      if (
        !rows.length ||
        rows.length > 128 ||
        !existsSync(join(guardian, 'state.json'))
      )
        refuse('resource.storage_unavailable');
      for (const row of rows) {
        requireInstallation(row);
        if (
          row.clientId !== who.hostId ||
          row.resource.identity.tenantId !== who.tenantId ||
          row.selectionRevision !== rows[0].selectionRevision ||
          row.generation !== rows[0].generation
        )
          refuse('resource.state_conflict');
      }
      // Cache is outside all discovery roots; writes still participate in the host lock.
      return locked((lock) => {
        recoverRecordTemps(who, lock);
        recoverIdentity(who, lock);
        cleanup(who, lock);
        assertLocalPath(active);
        const previous = ledger(who),
          transactionId = randomUUID();
        if (!previous && existsSync(active) && readdirSync(active).length)
          refuse('resource.state_conflict');
        assertSelectionProgress(previous, rows);
        const value = {
          schemaVersion: 1,
          ...identity(who),
          revision: (previous?.revision ?? 0) + 1,
          selectionRevision: rows[0].selectionRevision,
          generation: rows[0].generation,
          installations: rows.map((r) => ({
            ...r,
            state:
              desiredState(r.resource) === 'ENABLED'
                ? 'applying'
                : desiredState(r.resource) === 'REMOVED'
                  ? 'removed'
                  : 'disabled',
          })),
          processedDisableReceiptIds:
            previous?.processedDisableReceiptIds ?? [],
        };
        const directory = root(who);
        assertLocalPath(directory);
        mkdirSync(directory, { recursive: true });
        /** @type {[string,string][]} */ const cacheMoves = [];
        for (const download of downloads)
          for (const bytes of [
            download.manifest,
            ...download.artifacts,
            download.revocations,
          ]) {
            lock.check();
            cache(who, bytes, cacheMoves);
          }
        lock.check();
        commitMoves(cacheMoves);
        const staging = join(directory, 'staging', transactionId);
        mkdirSync(staging, { recursive: true });
        for (const [path, bytes] of tree(who, value.installations)) {
          const target = join(staging, path);
          assertLocalPath(target);
          mkdirSync(dirname(target), { recursive: true });
          const fd = openSync(target, 'wx', 0o600);
          try {
            writeFileSync(fd, bytes);
            fsyncSync(fd);
          } finally {
            closeSync(fd);
          }
        }
        const tx = {
          schemaVersion: 1,
          ...identity(who),
          transactionId,
          writer: 'plugin',
          operation: 'install',
          phase: 'prepared',
          expectedLedgerRevision: value.revision,
          selectionRevision: value.selectionRevision,
          generation: value.generation,
          receiptId: randomUUID(),
          recordedAt: new Date().toISOString(),
        };
        mkdirSync(join(directory, 'transactions'), { recursive: true });
        lock.check();
        durableJson(
          join(directory, 'transactions', transactionId + '.json'),
          requireSchema('UnifiedTransactionFile', tx),
        );
        save(who, value, lock);
        return { transactionId, ledgerRevision: value.revision };
      });
    },
    /** Activation permit is checked under the same host lock, after E has released it.
     * @param {any} who @param {()=>boolean} currentIdentity */
    finish(who, currentIdentity) {
      return locked((lock) => {
        recoverRecordTemps(who, lock);
        recoverIdentity(who, lock);
        cleanup(who, lock);
        const value = ledger(who);
        if (!value) return undefined;
        const files = readdirSync(join(root(who), 'transactions'));
        if (files.length > 8192) refuse('resource.storage_unavailable');
        const pending = files
          .map((f) =>
            record(
              join(root(who), 'transactions', f),
              'UnifiedTransactionFile',
            ),
          )
          .filter(
            (t) =>
              t.operation === 'install' &&
              ['prepared', 'switched'].includes(t.phase) &&
              (t.expectedLedgerRevision === value.revision ||
                (t.phase === 'switched' &&
                  t.expectedLedgerRevision + 1 === value.revision)) &&
              t.selectionRevision === value.selectionRevision &&
              t.generation === value.generation,
          );
        if (pending.length === 0) return value;
        if (pending.length !== 1) refuse('resource.state_conflict');
        const tx = pending[0];
        const state = record(
          join(guardian, 'state.json'),
          'UnifiedGuardianStateFile',
        );
        const g = state.guardian;
        const enabled = value.installations.filter(
          (/** @type {any} */ r) => desiredState(r.resource) === 'ENABLED',
        );
        if (
          !currentIdentity() ||
          state.pendingIsolation ||
          !same(who, g) ||
          (enabled.length && !permitted(who, value, state))
        )
          return value;
        if (enabled.length) {
          const lease = enabled
            .map((/** @type {any} */ row) => row.resource.lease)
            .sort(
              (/** @type {any} */ a, /** @type {any} */ b) =>
                Date.parse(a.expiresAt) - Date.parse(b.expiresAt),
            )[0];
          if (canonicalize(lease) !== canonicalize(g.lease)) return value;
          const water = record(
            join(guardian, 'high-watermarks.json'),
            'UnifiedHighWatermarksFile',
          );
          for (const row of enabled) {
            const r = row.resource;
            const w = water.entries.find(
              (/** @type {any} */ e) =>
                e.tenantId === who.tenantId &&
                e.packageId === r.identity.packageId,
            );
            if (
              !w ||
              w.releaseSequence !== r.releaseSequence ||
              w.versionId !== r.identity.versionId ||
              !w.verifiedDigestAliases.includes(r.identity.manifestDigest)
            )
              refuse('resource.signature_invalid');
          }
        }
        const directory = root(who),
          staging = join(directory, 'staging', tx.transactionId),
          expected = tree(who, value.installations);
        if (tx.phase === 'prepared') {
          if (!existsSync(staging) && existsSync(active)) {
            checkTree(active, expected);
            tx.phase = 'switched';
            lock.check();
            durableJson(
              join(directory, 'transactions', tx.transactionId + '.json'),
              tx,
            );
          }
        }
        if (tx.phase === 'prepared') {
          checkTree(staging, expected);
          assertLocalPath(active);
          mkdirSync(dirname(active), { recursive: true });
          if (statSync(staging).dev !== statSync(dirname(active)).dev)
            refuse('resource.storage_unavailable');
          const backup = join(
            directory,
            'staging',
            previousId(tx.transactionId),
          );
          if (existsSync(active)) {
            if (existsSync(backup)) refuse('resource.state_conflict');
            lock.check();
            commitMoves([[active, backup]]);
          }
          try {
            lock.check();
            commitMoves([[staging, active]]);
          } catch (error) {
            if (existsSync(backup) && !existsSync(active))
              commitMoves([[backup, active]]);
            throw error;
          }
          tx.phase = 'switched';
          lock.check();
          durableJson(
            join(directory, 'transactions', tx.transactionId + '.json'),
            tx,
          );
        } else checkTree(active, expected);
        /** @type {[string,any][]} */ const receiptsToCommit = [];
        for (const row of value.installations) {
          if (desiredState(row.resource) === 'ENABLED')
            row.state = 'pending_restart'; // Public host observation, not the permit, decides applied.
          const ref = row.resource,
            path = join(
              directory,
              'receipts',
              ref.identity.packageId,
              ref.identity.versionId + '.json',
            );
          mkdirSync(dirname(path), { recursive: true });
          const receipt = {
            schemaVersion: 1,
            ...identity(who),
            recordType: 'installation',
            writer: 'plugin',
            receiptId: tx.receiptId,
            resource: ref,
            installationRevision: row.installationRevision,
            selectionRevision: value.selectionRevision,
            generation: value.generation,
            recordedAt: new Date().toISOString(),
          };
          lock.check();
          receiptsToCommit.push([
            path,
            requireSchema('UnifiedReceiptFile', receipt),
          ]);
        }
        lock.check();
        durableRecords(receiptsToCommit);
        if (value.revision === tx.expectedLedgerRevision) {
          value.revision++;
          save(who, value, lock);
        }
        lock.check();
        durableJson(
          join(directory, 'transactions', tx.transactionId + '.json'),
          { ...tx, phase: 'committed' },
        );
        collectViews(who, tx, lock);
        return value;
      });
    },
    /** Clear only the old identity's complete owned view; do not grant activation to a new identity.
     * @param {any} next */
    switchIdentity(next) {
      return locked((lock) => {
        const file = join(guardian, 'state.json');
        assertLocalPath(active);
        if (!existsSync(file)) return;
        const state = record(file, 'UnifiedGuardianStateFile'),
          old = identity(state.guardian);
        recoverRecordTemps(old, lock);
        recoverIdentity(old, lock);
        if (next && same(next, old)) return;
        const value = ledger(old);
        if (!existsSync(active)) return;
        if (!value) {
          if (readdirSync(active).length)
            refuse('resource.storage_unavailable');
          return;
        }
        if (!lstatSync(active).isDirectory()) refuse('resource.state_conflict');
        // Preserve the entire owned view, including hand edits, outside discovery. Never delete it here.
        const transactionId = randomUUID(),
          tx = {
            schemaVersion: 1,
            ...old,
            transactionId,
            writer: 'plugin',
            operation: 'remove',
            phase: 'prepared',
            expectedLedgerRevision: value.revision,
            selectionRevision: value.selectionRevision,
            generation: value.generation,
            receiptId: randomUUID(),
            recordedAt: new Date().toISOString(),
          };
        const txFile = join(root(old), 'transactions', transactionId + '.json');
        mkdirSync(dirname(txFile), { recursive: true });
        lock.check();
        durableJson(txFile, requireSchema('UnifiedTransactionFile', tx));
        const backup = join(root(old), 'staging', transactionId);
        mkdirSync(dirname(backup), { recursive: true });
        lock.check();
        commitMoves([[active, backup]]);
        lock.check();
        durableJson(txFile, { ...tx, phase: 'switched' });
        value.installations = value.installations.map(
          (/** @type {any} */ r) => ({ ...r, state: 'disabled' }),
        );
        value.revision++;
        save(old, value, lock);
        lock.check();
        durableJson(txFile, { ...tx, phase: 'committed' });
      });
    },
    /** @param {any} who */ read(who) {
      return locked((lock) => {
        recoverRecordTemps(who, lock);
        recoverIdentity(who, lock);
        cleanup(who, lock);
        return ledger(who);
      });
    },
    /** Validate actual bytes again after public host observation; a path match alone is insufficient.
     * @param {any} who @param {any} row */
    confirm(who, row) {
      return locked(() => {
        const value = ledger(who);
        if (
          !value ||
          !permitted(
            who,
            value,
            record(join(guardian, 'state.json'), 'UnifiedGuardianStateFile'),
          )
        )
          return undefined;
        const local = value.installations.find(
          (/** @type {any} */ i) =>
            i.resource.identity.packageId === row.resource.identity.packageId,
        );
        if (
          !local ||
          local.installationRevision !== row.installationRevision ||
          local.selectionRevision !== row.selectionRevision ||
          local.generation !== row.generation ||
          desiredState(local.resource) !== desiredState(row.resource) ||
          !['pending_restart', 'applied'].includes(local.state)
        )
          return undefined;
        checkTree(active, tree(who, value.installations));
        const manifest = parseStrictJson(
            cached(who, row.resource.identity.manifestDigest),
          ),
          files = unpackSkill(
            cached(who, manifest.artifacts[0].digest),
            manifest,
          );
        const source = files['SKILL.md']
          .toString('utf8')
          .replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, '')
          .trim();
        return { contentDigest: sha(Buffer.from(source)) };
      });
    },
    /** Only successful public observation becomes applied locally; overridden content remains guarded.
     * @param {any} who @param {any} report */
    acknowledge(who, report) {
      return locked((lock) => {
        const value = ledger(who);
        if (!value || report.state !== 'applied') return;
        if (
          !permitted(
            who,
            value,
            record(join(guardian, 'state.json'), 'UnifiedGuardianStateFile'),
          )
        )
          refuse('resource.state_conflict');
        const row = value.installations.find(
          (/** @type {any} */ i) =>
            i.resource.identity.packageId === report.identity.packageId,
        );
        if (
          !row ||
          row.installationRevision !== report.installationRevision ||
          row.selectionRevision !== report.selectionRevision ||
          row.generation !== report.generation
        )
          refuse('common.conflict');
        checkTree(active, tree(who, value.installations));
        row.state = 'applied';
        row.report = report;
        value.revision++;
        save(who, value, lock);
      });
    },
    /** A disabled/removal report needs a matching persisted receipt and an actually cleared view.
     * @param {any} who @param {any} row */
    confirmDisabled(who, row) {
      return locked(() => {
        const value = ledger(who),
          local = value?.installations.find(
            (/** @type {any} */ i) =>
              i.resource.identity.packageId === row.resource.identity.packageId,
          );
        if (
          !local ||
          !['disabled', 'removed'].includes(local.state) ||
          local.installationRevision !== row.installationRevision ||
          local.selectionRevision !== row.selectionRevision ||
          local.generation !== row.generation ||
          desiredState(local.resource) !== desiredState(row.resource)
        )
          return false;
        const ownReceipt = join(
          root(who),
          'receipts',
          row.resource.identity.packageId,
          row.resource.identity.versionId + '.json',
        );
        {
          const cleared = value.processedDisableReceiptIds.some(
            (/** @type {string} */ id) => {
              const path = join(guardian, 'disable-receipts', id + '.json');
              if (!existsSync(path)) return false;
              const r = record(path, 'UnifiedReceiptFile');
              return (
                r.recordType === 'disable' &&
                same(who, r) &&
                r.selectionRevision === row.selectionRevision &&
                r.generation === row.generation
              );
            },
          );
          if (cleared) {
            const expected = tree(who, value.installations);
            if (existsSync(active)) checkTree(active, expected);
            else if (expected.size) return false;
            return true;
          }
        }
        if (!existsSync(ownReceipt)) return false;
        const receipt = record(ownReceipt, 'UnifiedReceiptFile');
        if (
          receipt.recordType !== 'installation' ||
          !same(who, receipt) ||
          receipt.installationRevision !== row.installationRevision ||
          receipt.selectionRevision !== row.selectionRevision ||
          receipt.generation !== row.generation ||
          canonicalize(receipt.resource) !== canonicalize(local.resource)
        )
          return false;
        const expected = tree(who, value.installations);
        if (existsSync(active)) checkTree(active, expected);
        else if (expected.size) return false;
        return true;
      });
    },
  };
}
