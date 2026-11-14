/* global FormData, Blob */
import { catalogApi as baseCatalogApi } from '../../src/resources/catalog-api.js';
import { catalogService as baseCatalogService } from '../../src/resources/catalog-service.js';
import { catalogRoutes as baseCatalogRoutes } from '../../src/resources/catalog-routes.js';
import { createMemberApi as baseMemberApi } from '../../src/resources/member-api.js';
import {
  createResourceHandler as baseHandler,
  installResourceChannel as baseInstallChannel,
} from '../../src/resources/channel.js';
import { openMemberResources as baseOpen } from '../../src/resources/engine.js';
export { catalogIdentity } from '../../src/resources/catalog-api.js';
export { watchCatalogIdentity } from '../../src/resources/catalog-routes.js';
export {
  memberIdentity,
  installation,
} from '../../src/resources/member-api.js';
export { resourcePath } from '../../src/resources/channel.js';
// Retain the established upstream/host fixtures and all their business assertions,
// placing the synthetic upstream on C's side of the new fixed-operation seam.
const bind = (options) => {
  const runtime = options.runtime ?? options.current?.();
  if (runtime && options.fetchImpl) resourceBroker(runtime, options.fetchImpl);
  return options;
};
export const catalogApi = (options) => baseCatalogApi(bind(options));
export const catalogService = (options) => baseCatalogService(bind(options));
export const catalogRoutes = (options) => baseCatalogRoutes(bind(options));
export const createMemberApi = (options) => baseMemberApi(bind(options));
export const createResourceHandler = (options) => baseHandler(bind(options));
export const installResourceChannel = (ctx, options) =>
  baseInstallChannel(ctx, bind(options));
export const openMemberResources = (options) => baseOpen(bind(options));
import { randomUUID, createHash } from 'node:crypto';
import { operations } from '../broker-fixture.js';
import { parseStrictJson } from '../../src/resources/json.js';

/** Test-only upstream peer behind the fixed broker boundary. Product adapters
 * receive neither this fetcher nor its synthetic server credential. */
export function resourceBroker(runtime, fetcher) {
  const uploads = new Map(),
    streams = new Map();
  runtime.identity = () => {
    const claims = runtime.vault?.snapshot()?.credential?.claims;
    return claims
      ? { tenantId: claims.tid, ownerId: claims.sub, clientId: claims.did }
      : undefined;
  };
  runtime.operations = operations;
  runtime.upload = async (bytes, mediaType) => {
    const uploadId = randomUUID();
    uploads.set(uploadId, { bytes, mediaType });
    return {
      uploadId,
      sizeBytes: bytes.length,
      sha256: createHash('sha256').update(bytes).digest('hex'),
    };
  };
  runtime.readStream = async (id) => {
    const bytes = streams.get(id);
    streams.delete(id);
    return bytes;
  };
  runtime.broker = {
    async call(operation, input) {
      const definition = operations.find(
        (item) => item.operation === operation,
      );
      if (!definition) throw new Error('unknown fixed resource operation');
      let route = definition.route;
      const pathKeys = new Set();
      route = route.replace(/\{([^}]+)\}/g, (_, key) => {
        pathKeys.add(key);
        return input[key];
      });
      const url = new URL(route, runtime.config.platformOrigin);
      for (const [key, value] of Object.entries(input))
        if (!['body', 'headers'].includes(key) && !pathKeys.has(key))
          url.searchParams.set(key, String(value));
      let body = input.body;
      if (body?.manifest?.uploadId) {
        const form = new FormData();
        form.set(
          'metadata',
          new Blob([JSON.stringify(body.metadata)], {
            type: 'application/json',
          }),
          'metadata.json',
        );
        const manifest = uploads.get(body.manifest.uploadId);
        form.set(
          'manifest',
          new Blob([manifest.bytes], { type: manifest.mediaType }),
          'manifest.json',
        );
        for (const ref of body.artifacts) {
          const artifact = uploads.get(ref.uploadId);
          form.append(
            'artifacts',
            new Blob([artifact.bytes], { type: artifact.mediaType }),
            'payload.tgz',
          );
        }
        body = form;
      } else if (body !== undefined) body = JSON.stringify(body);
      const headers = {
        ...(input.headers ?? {}),
        accept: 'application/json',
        ...(body === undefined || body instanceof FormData
          ? {}
          : { 'content-type': 'application/json' }),
        authorization: 'Bearer ' + (await runtime.getAccess()),
      };
      const response = await fetcher(url, {
        method: definition.method.toUpperCase(),
        headers,
        body,
        credentials: 'omit',
        redirect: 'error',
      });
      if (!response.ok)
        throw Object.assign(new Error('remote resource failure'), {
          status: response.status,
          code:
            {
              400: 'common.validation_failed',
              401: 'common.unauthenticated',
              403: 'common.forbidden',
              404: 'common.not_found',
              409: 'common.conflict',
              412: 'common.precondition_failed',
              429: 'common.rate_limited',
            }[response.status] ?? 'common.unavailable',
        });
      const bytes = Buffer.from(await response.arrayBuffer());
      const headersOut = Object.fromEntries(response.headers);
      const isBinary = [
        'UnifiedDownloadArtifact',
        'UnifiedDownloadVersionArtifact',
        'UnifiedDownloadResourceManifest',
        'consumeMemberResourceDownloadTicket',
      ].includes(operation);
      if (isBinary) {
        const streamId = randomUUID();
        streams.set(streamId, bytes);
        return {
          statusCode: response.status,
          headers: headersOut,
          body: { streamId },
        };
      }
      return {
        statusCode: response.status,
        headers: headersOut,
        body: parseStrictJson(bytes, true),
      };
    },
  };
  return runtime;
}
