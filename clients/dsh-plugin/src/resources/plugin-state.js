import { randomUUID } from 'node:crypto';
import { existsSync, lstatSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { acquireHostLock, assertLocalPath, durableJson } from './host-lock.js';
import { canonicalize, parseStrictJson } from './json.js';
import { exact, refuse, uuid } from './trust.js';
import { requireSchema } from './schema.js';

// Shared even across module reloads in one DSH process; a real restart changes it.
const instanceKey = Symbol.for('auriga.catalog.plugin-process-instance');
const processState = /** @type {any} */ (process);
const instance = (processState[instanceKey] ??= randomUUID());
/** Local proof of a successful public PluginManager installation, bound to bytes.
 * It never replaces guardian, lease, selection or live host checks.
 * @param {{home:string,who:any}} options */
export function pluginState({ home, who }) {
  if (![who.tenantId, who.ownerId, who.hostId].every(uuid))
    refuse('common.unauthenticated');
  const scope = {
    tenantId: who.tenantId,
    ownerId: who.ownerId,
    hostId: who.hostId,
  };
  /** @param {any} row */
  const proof = (row) => ({
    identity: row.resource.identity,
    artifacts: row.resource.artifacts.map((/** @type {any} */ a) => a.digest),
  });
  /** @param {any} row */
  function file(row) {
    if (!uuid(row.resource.identity.packageId))
      refuse('resource.storage_unavailable');
    return join(
      home,
      'auriga/resources',
      who.tenantId,
      who.ownerId,
      who.hostId,
      'skills/plugin-installations',
      row.resource.identity.packageId + '.json',
    );
  }
  /** @param {any} row @param {string} name */
  function read(row, name) {
    const path = file(row);
    assertLocalPath(path);
    if (!existsSync(path)) return undefined;
    const stat = lstatSync(path);
    if (!stat.isFile() || stat.size > 65536)
      refuse('resource.storage_unavailable');
    const value = parseStrictJson(readFileSync(path), true);
    if (
      !exact(value, [
        'schemaVersion',
        'tenantId',
        'ownerId',
        'hostId',
        'name',
        'proof',
        'installed',
        'pending',
      ]) ||
      value.schemaVersion !== 1 ||
      !Object.keys(scope).every(
        (k) => value[k] === scope[/** @type {keyof typeof scope} */ (k)],
      ) ||
      typeof value.name !== 'string' ||
      typeof value.installed !== 'boolean' ||
      !exact(value.proof, ['identity', 'artifacts']) ||
      !Array.isArray(value.proof.artifacts) ||
      value.proof.artifacts.length !== 1 ||
      !value.proof.artifacts.every((/** @type {any} */ digest) =>
        /^sha256:[a-f0-9]{64}$/.test(digest),
      )
    )
      refuse('resource.storage_unavailable');
    requireSchema('UnifiedResourceIdentity', value.proof.identity);
    if (
      value.pending !== null &&
      (!exact(value.pending, [
        'action',
        'instance',
        'installationRevision',
        'selectionRevision',
        'generation',
      ]) ||
        !['disable', 'install', 'enable', 'remove'].includes(
          value.pending.action,
        ) ||
        !uuid(value.pending.instance) ||
        !['installationRevision', 'selectionRevision', 'generation'].every(
          (k) =>
            Number.isSafeInteger(value.pending[k]) && value.pending[k] >= 0,
        ))
    )
      refuse('resource.storage_unavailable');
    return value.name === name &&
      canonicalize(value.proof) === canonicalize(proof(row))
      ? value
      : undefined;
  }
  return {
    read,
    /** @param {any} value @param {any} row */
    waiting(value, row) {
      return (
        value?.pending?.instance === instance &&
        ['installationRevision', 'selectionRevision', 'generation'].every(
          (k) => value.pending[k] === row[k],
        )
      );
    },
    /** Persist the public operation result before reporting or scheduling another attempt.
     * @param {any} row @param {string} name @param {boolean} installed @param {string|null} [action] */
    write(row, name, installed, action = null) {
      const value = {
        schemaVersion: 1,
        ...scope,
        name,
        proof: proof(row),
        installed,
        pending: action
          ? {
              action,
              instance,
              installationRevision: row.installationRevision,
              selectionRevision: row.selectionRevision,
              generation: row.generation,
            }
          : null,
      };
      const prior = read(row, name);
      if (prior && canonicalize(prior) === canonicalize(value)) return;
      const lock = acquireHostLock(home);
      try {
        const path = file(row);
        assertLocalPath(path);
        mkdirSync(dirname(path), { recursive: true });
        lock.check();
        durableJson(path, value);
      } finally {
        lock.release();
      }
    },
  };
}
