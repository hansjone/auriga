import { resourceRequest } from './broker-transport.js';
/* global AbortSignal */
import { randomUUID } from 'node:crypto';
import { parseStrictJson } from './json.js';
import { counter, digest, uuid, refuse, instant } from './trust.js';
/** @typedef {import('../generated/resource.js').components['schemas']['MemberInstallation']} Installation */

/** Identity comes from the locally verified DEVICE grant, never a browser body.
 * @param {any} runtime */
export function memberIdentity(runtime) {
  const who = runtime?.identity?.();
  if (!uuid(who?.tenantId) || !uuid(who?.ownerId) || !uuid(who?.clientId))
    return refuse('common.unauthenticated');
  return Object.freeze({
    tenantId: who.tenantId,
    ownerId: who.ownerId,
    clientId: who.clientId,
  });
}
/** @param {any} row @param {string} clientId @returns {Installation} */
export function installation(row, clientId) {
  const allowed = [
    'clientId',
    'packageId',
    'versionId',
    'digest',
    'releaseSequence',
    'level',
    'desired',
    'state',
    'revision',
    'reportSource',
    'reportedRevision',
    'reportedAt',
    'errorCode',
  ];
  if (
    !row ||
    Object.keys(row).some((key) => !allowed.includes(key)) ||
    row.clientId !== clientId ||
    !uuid(row.packageId) ||
    !uuid(row.versionId) ||
    !digest(row.digest) ||
    !counter(row.releaseSequence) ||
    !counter(row.revision) ||
    !['l1', 'l2', 'l3', 'l4'].includes(row.level) ||
    !['enabled', 'disabled', 'removed'].includes(row.desired) ||
    !['applying', 'applied', 'failed'].includes(row.state) ||
    row.reportSource !== 'member' ||
    (row.reportedAt !== undefined &&
      (typeof row.reportedAt !== 'string' || row.reportedAt.length > 64)) ||
    (row.errorCode !== undefined &&
      row.errorCode !== 'resource.apply_failed') ||
    (row.reportedRevision !== undefined &&
      (!counter(row.reportedRevision) || row.reportedRevision > row.revision))
  )
    refuse('common.validation_failed');
  return row;
}
/** Bounded reads also apply to chunked responses; no redirected locator is used.
 * @param {Response} response @param {number} max */
async function bytes(response, max) {
  const parts = [];
  let size = 0;
  if (!response.body) return refuse('common.unavailable');
  const reader = response.body.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > max) {
        await reader.cancel();
        return refuse('common.validation_failed');
      }
      parts.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  return Buffer.concat(parts);
}
/** @param {{runtime:any, alive?:()=>boolean, fetchImpl?:typeof fetch, now?:()=>number}} options */
export function createMemberApi({
  runtime,
  alive = () => true,
  fetchImpl: _fetchImpl,
  now = Date.now,
}) {
  const identity = memberIdentity(runtime),
    _origin = runtime.config.platformOrigin;
  function check() {
    const current = memberIdentity(runtime);
    if (
      !alive() ||
      current.clientId !== identity.clientId ||
      current.tenantId !== identity.tenantId ||
      current.ownerId !== identity.ownerId
    )
      refuse('common.unauthenticated');
  }
  /** @param {string} path @param {any} [input] @param {string} [key] @param {string} [etag] @param {boolean} [binary] */
  async function request(
    path,
    input,
    key,
    etag,
    binary = false,
    timeout = 15000,
  ) {
    check();

    check();
    const response = await resourceRequest(runtime, path, {
      method: input === undefined ? 'GET' : 'POST',
      credentials: 'omit',
      redirect: 'error',
      signal: AbortSignal.timeout(timeout),
      headers: {
        accept: binary ? 'application/gzip' : 'application/json',
        ...(input === undefined
          ? {}
          : {
              'content-type': 'application/json',
              'idempotency-key': key ?? randomUUID(),
            }),
        ...(etag === undefined ? {} : { 'if-match': etag }),
      },
      body: input === undefined ? undefined : JSON.stringify(input),
    });
    check();
    if (!response.ok) {
      await response.body?.cancel();
      const codes = /** @type {Record<number,string>} */ ({
        400: 'common.validation_failed',
        401: 'common.unauthenticated',
        403: 'common.forbidden',
        404: 'common.not_found',
        409: 'common.conflict',
        412: 'common.precondition_failed',
        429: 'common.rate_limited',
      });
      return refuse(codes[response.status] ?? 'common.unavailable');
    }
    if (
      !response.headers
        .get('content-type')
        ?.toLowerCase()
        .startsWith(binary ? 'application/gzip' : 'application/json')
    ) {
      await response.body?.cancel();
      return refuse('common.validation_failed');
    }
    const data = await bytes(response, binary ? 67108864 : 2097152);
    check();
    if (binary) return data;
    const fields = new Map();
    const value = parseStrictJson(data, true, fields);
    if (path.endsWith('/download-tickets'))
      value.envelopeWire = fields.get('resourceEnvelope');
    return value;
  }
  const base = '/api/resource/v1/member';
  /** @param {any} ref */
  async function issue(ref) {
    const ticket = await request(
      `${base}/download-tickets`,
      {
        clientId: identity.clientId,
        packageId: ref.pack.id,
        versionId: ref.version.id,
        digest: ref.version.digest,
      },
      randomUUID(),
    );
    const expiry = instant(ticket.expiresAt),
      clock = BigInt(now()) * 1000000n;
    if (
      ticket.clientId !== identity.clientId ||
      typeof ticket.ticket !== 'string' ||
      !/^[A-Za-z0-9_-]{43}$/.test(ticket.ticket) ||
      expiry <= clock ||
      expiry - clock > 60000000000n ||
      ticket.resourceEnvelope?.tenantId !== identity.tenantId ||
      ticket.resourceEnvelope?.packageId !== ref.pack.id ||
      ticket.resourceEnvelope?.versionId !== ref.version.id ||
      ticket.resourceEnvelope?.statement?.digest !== ref.version.digest
    )
      return refuse('resource.signature_invalid');

    if (!(ticket.envelopeWire instanceof Uint8Array)) refuse();
    return ticket;
  }
  return {
    identity,
    check,
    /** @param {string} packageId @param {string} versionId */
    async reference(packageId, versionId) {
      if (!uuid(packageId) || !uuid(versionId))
        return refuse('common.validation_failed');
      const pack = await request(`/api/resource/v1/packages/${packageId}`);
      const version = await request(
        `/api/resource/v1/packages/${packageId}/versions/${versionId}`,
      );
      if (
        pack.id !== packageId ||
        pack.tenantId !== identity.tenantId ||
        pack.kind !== 'plugin' ||
        typeof pack.name !== 'string' ||
        version.packageId !== packageId ||
        version.id !== versionId ||
        !digest(version.digest) ||
        !counter(version.releaseSequence) ||
        !counter(version.sizeBytes) ||
        version.sizeBytes > 67108864 ||
        typeof version.version !== 'string'
      )
        return refuse('common.validation_failed');
      return { pack, version };
    },
    async list() {
      /** @type {Installation[]} */ const all = [];
      const seen = new Set();
      let after;
      for (let pages = 0; pages < 1000; pages++) {
        const query = new URLSearchParams({
          clientId: identity.clientId,
          limit: '200',
          ...(after ? { afterPackageId: after } : {}),
        });
        const page = await request(`${base}/installations?${query}`);
        if (!Array.isArray(page.items) || page.items.length > 200)
          return refuse('common.validation_failed');
        for (const row of page.items) {
          const value = installation(row, identity.clientId);
          if (seen.has(value.packageId))
            return refuse('common.validation_failed');
          seen.add(value.packageId);
          all.push(value);
        }
        if (!page.nextAfterPackageId) return all;
        if (!uuid(page.nextAfterPackageId) || page.nextAfterPackageId === after)
          return refuse('common.validation_failed');
        after = page.nextAfterPackageId;
      }
      return refuse('common.validation_failed');
    },
    /** Each new attempt has a NEW ticket key. A lost ticket response is never replayed.
     * @param {any} ref */
    async authorize(ref) {
      const ticket = await issue(ref);
      return {
        envelope: ticket.resourceEnvelope,
        wire: ticket.envelopeWire,
        revocations: ticket.revocations,
      };
    },
    /** @param {any} ref */
    async download(ref) {
      const ticket = await issue(ref);
      const archive = await request(
        base + '/downloads',
        { clientId: identity.clientId, ticket: ticket.ticket },
        randomUUID(),
        undefined,
        true,
        Math.min(
          300000,
          30000 + Math.ceil(ref.version.sizeBytes / 262144) * 1000,
        ),
      );
      return {
        archive,
        envelope: ticket.resourceEnvelope,
        wire: ticket.envelopeWire,
        revocations: ticket.revocations,
      };
    },
    revocations: () => request('/api/resource/v1/revocations'),
    /** @param {any} ref @param {string} action @param {number} revision @param {string} key */
    async select(ref, action, revision, key) {
      const row = await request(
        `${base}/installations`,
        {
          clientId: identity.clientId,
          packageId: ref.pack.id,
          versionId: ref.version.id,
          digest: ref.version.digest,
          action,
        },
        key,
        `"${revision}"`,
      );
      const checked = installation(row, identity.clientId);
      if (
        checked.packageId !== ref.pack.id ||
        checked.versionId !== ref.version.id ||
        checked.digest !== ref.version.digest ||
        checked.releaseSequence !== ref.version.releaseSequence ||
        checked.level !== ref.pack.level ||
        checked.desired !==
          (action === 'remove'
            ? 'removed'
            : action === 'disable'
              ? 'disabled'
              : 'enabled') ||
        checked.state !== 'applying'
      )
        return refuse('common.validation_failed');
      return checked;
    },
    /** @param {Installation} row @param {'applied'|'failed'} state @param {string} key */
    async report(row, state, key) {
      const value = installation(
        await request(
          `${base}/installation-reports`,
          {
            clientId: identity.clientId,
            packageId: row.packageId,
            versionId: row.versionId,
            digest: row.digest,
            installationRevision: row.revision,
            state,
          },
          key,
        ),
        identity.clientId,
      );
      if (
        value.packageId !== row.packageId ||
        value.versionId !== row.versionId ||
        value.digest !== row.digest ||
        value.revision !== row.revision ||
        value.reportedRevision !== row.revision ||
        value.state !== state
      )
        return refuse('common.validation_failed');
      return value;
    },
  };
}
