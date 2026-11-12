/* global AbortSignal */
import { randomUUID } from 'node:crypto';
import { memberIdentity } from './member-api.js';
import { uuid, refuse } from './trust.js';
import { parseStrictJson, canonicalize } from './json.js';
import { requireSchema } from './schema.js';
/** @param {any} runtime */
export function catalogIdentity(runtime) {
  if (runtime?.resourceIdentity) {
    const r = runtime.resourceIdentity;
    if (![r.tenantId, r.ownerId, r.instanceId].every(uuid))
      refuse('common.unauthenticated');
    return { tenantId: r.tenantId, ownerId: r.ownerId, hostId: r.instanceId };
  }
  const r = memberIdentity(runtime);
  return { tenantId: r.tenantId, ownerId: r.ownerId, hostId: r.clientId };
}
/** @param {{runtime:any,current:()=>any,fetchImpl?:typeof fetch}} options */
export function catalogApi({ runtime, current, fetchImpl = fetch }) {
  const who = catalogIdentity(runtime),
    origin = runtime.config.platformOrigin;
  function check() {
    if (
      current() !== runtime ||
      canonicalize(catalogIdentity(runtime)) !== canonicalize(who)
    )
      refuse('common.unauthenticated');
  }
  /** @param {string} path @param {{body?:any,etag?:string,key?:string,binary?:boolean,max?:number,form?:FormData}} [options] */
  async function request(
    path,
    { body, etag, key, binary = false, max = 2097152, form } = {},
  ) {
    if (
      !path.startsWith('/api/resource/v1/') ||
      /[%\\]/.test(path.split('?')[0]) ||
      path.includes('#') ||
      path.includes('//') ||
      path.split('/').some((p) => p === '..' || p === '.')
    )
      refuse('common.validation_failed');
    check();
    const token = await runtime.getAccess();
    check();
    const response = await fetchImpl(new URL(path, origin), {
      method: body === undefined && !form ? 'GET' : 'POST',
      credentials: 'omit',
      redirect: 'error',
      signal: AbortSignal.timeout(30000),
      headers: {
        authorization: 'Bearer ' + token,
        accept: binary ? 'application/octet-stream' : 'application/json',
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        ...(body === undefined && !form
          ? {}
          : {
              'idempotency-key': key ?? randomUUID(),
              'if-match': etag ?? '"0"',
            }),
      },
      body: form ?? (body === undefined ? undefined : JSON.stringify(body)),
    });
    check();
    if (!response.ok) {
      await response.body?.cancel();
      refuse(
        {
          400: 'common.validation_failed',
          401: 'common.unauthenticated',
          403: 'common.forbidden',
          404: 'common.not_found',
          409: 'common.conflict',
          412: 'common.precondition_failed',
          429: 'common.rate_limited',
        }[/** @type {400} */ (response.status)] ?? 'common.unavailable',
      );
    }
    if (
      !binary &&
      !response.headers.get('content-type')?.startsWith('application/json')
    ) {
      await response.body?.cancel();
      refuse('common.validation_failed');
    }
    const parts = [];
    let length = 0;
    if (!response.body) refuse('common.unavailable');
    const reader = response.body.getReader();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        if ((length += value.length) > max) {
          await reader.cancel();
          refuse('common.validation_failed');
        }
        parts.push(value);
      }
    } finally {
      reader.releaseLock();
    }
    check();
    const bytes = Buffer.concat(parts);
    return {
      value: binary ? bytes : parseStrictJson(bytes, true),
      etag: response.headers.get('etag'),
      status: response.status,
    };
  }
  const base = '/api/resource/v1/catalog';
  return {
    who,
    check,
    request,
    async list() {
      const rows = [];
      let cursor;
      const seen = new Set();
      for (let p = 0; p < 1000; p++) {
        const query = new URLSearchParams({
          clientId: who.hostId,
          kind: 'skill',
          limit: '200',
          ...(cursor ? { cursor } : {}),
        });
        const { value } = await request(
          base + '/member/installations?' + query,
        );
        requireSchema('UnifiedInstallationPage', value);
        for (const row of value.items) {
          if (
            row.clientId !== who.hostId ||
            row.resource.identity.tenantId !== who.tenantId ||
            row.resource.identity.kind !== 'skill' ||
            seen.has(row.resource.identity.packageId)
          )
            refuse('common.validation_failed');
          seen.add(row.resource.identity.packageId);
          rows.push(row);
        }
        if (!value.nextCursor) return rows;
        if (value.nextCursor === cursor) refuse('common.validation_failed');
        cursor = value.nextCursor;
      }
      refuse('common.validation_failed');
    },
    /** @param {any} selection @param {number} revision @param {string} key */
    async select(selection, revision, key) {
      requireSchema('UnifiedSelection', selection);
      if (
        selection.identity.tenantId !== who.tenantId ||
        selection.identity.kind !== 'skill'
      )
        refuse('common.forbidden');
      const result = await request(base + '/member/installations', {
        body: { clientId: who.hostId, selection },
        etag: '"' + revision + '"',
        key,
      });
      const row = requireSchema('UnifiedInstallation', result.value);
      if (
        canonicalize(row.resource.identity) !==
          canonicalize(selection.identity) ||
        row.clientId !== who.hostId ||
        row.resource.enabled !==
          ['install', 'enable'].includes(selection.action)
      )
        refuse('common.validation_failed');
      return row;
    },
    /** @param {any} row */
    async download(row) {
      requireSchema('UnifiedInstallation', row);
      const { value: ticket } = await request(
        base + '/member/download-tickets',
        {
          body: {
            clientId: who.hostId,
            identity: row.resource.identity,
            installationRevision: row.installationRevision,
          },
          etag: '"' + row.installationRevision + '"',
        },
      );
      requireSchema('UnifiedDownloadTicket', ticket);
      if (
        ticket.clientId !== who.hostId ||
        canonicalize(ticket.resource) !== canonicalize(row.resource) ||
        Date.parse(ticket.expiresAt) <= Date.now() ||
        Date.parse(ticket.expiresAt) > Date.now() + 300000
      )
        refuse('resource.signature_invalid');
      const ref = ticket.resource,
        id = ref.identity;
      if (
        ref.manifestDownloadPath !==
        base +
          '/packages/' +
          id.packageId +
          '/versions/' +
          id.versionId +
          '/manifest'
      )
        refuse('resource.signature_invalid');
      const manifest = /** @type {Buffer} */ (
        (await request(ref.manifestDownloadPath, { binary: true })).value
      );
      const artifacts = [];
      for (const artifact of ref.artifacts) {
        const { value } = await request(base + '/member/downloads', {
          body: {
            clientId: who.hostId,
            ticket: ticket.ticket,
            artifactId: artifact.artifactId,
          },
          etag: '"' + row.installationRevision + '"',
          binary: true,
          max: 5242880,
        });
        artifacts.push(/** @type {Buffer} */ (value));
      }
      return {
        manifest,
        artifacts,
        revocations: Buffer.from(JSON.stringify(ticket.revocations)),
        ref,
      };
    },
    /** @param {any} report */
    async report(report) {
      requireSchema('UnifiedInstallationReport', report);
      return request(base + '/member/installation-reports', {
        body: report,
        etag: '"' + report.installationRevision + '"',
      });
    },
  };
}
