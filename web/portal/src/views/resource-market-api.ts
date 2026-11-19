import type { ApiClient } from '@auriga/shared';
import type { components } from '../../../shared/src/generated/resource';
import { placementRequestKey } from './placement-api';

type Schemas = components['schemas'];
export type Installation = Schemas['PlacementInstallation'];
export type InstallationPage = Schemas['PlacementInstallationPage'];
export type ResourcePackage = Schemas['UnifiedPackage'];
export type ResourceVersion = Schemas['UnifiedVersion'];
export type Selection = Schemas['UnifiedSelection'];
const base = '/api/resource/v1/catalog';
const page = (cursor?: string) =>
  '?limit=200' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');

export function createResourceMarketApi(client: ApiClient) {
  return {
    package: (id: string, signal: AbortSignal) =>
      client.get<ResourcePackage>(
        `${base}/packages/${encodeURIComponent(id)}`,
        { signal, cache: 'no-store' },
      ),
    async installations(placementId: string, signal: AbortSignal) {
      const path = `${base}/placements/${encodeURIComponent(placementId)}/installations`;
      const first = await client.get<InstallationPage>(path + page(), {
        signal,
        cache: 'no-store',
      });
      const result = { ...first, items: [...first.items] };
      let cursor = first.nextCursor;
      const seen = new Set<string>();
      while (cursor) {
        if (seen.has(cursor))
          throw new Error('Resource pagination did not advance');
        seen.add(cursor);
        const next = await client.get<InstallationPage>(path + page(cursor), {
          signal,
          cache: 'no-store',
        });
        if (
          next.placementId !== first.placementId ||
          next.viewId !== first.viewId ||
          next.epoch !== first.epoch ||
          next.revision !== first.revision ||
          next.mode !== first.mode ||
          next.nodeObserved !== first.nodeObserved
        )
          throw new Error('Resource placement changed; refresh');
        result.items.push(...next.items);
        cursor = next.nextCursor;
      }
      return result;
    },
    packages: (
      kind: 'plugin' | 'skill',
      signal: AbortSignal,
      cursor?: string,
    ) =>
      client.get<Schemas['UnifiedPackagePage']>(
        base + '/packages' + page(cursor) + '&kind=' + kind,
        {
          signal,
          cache: 'no-store',
        },
      ),
    async latest(pack: ResourcePackage, signal: AbortSignal) {
      let cursor: string | undefined;
      let latest: ResourceVersion | undefined;
      const seen = new Set<string>();
      do {
        const result = await client.get<Schemas['UnifiedVersionPage']>(
          `${base}/packages/${encodeURIComponent(pack.id)}/versions` +
            page(cursor),
          { signal, cache: 'no-store' },
        );
        for (const version of result.items) {
          if (
            version.state === 'published' &&
            version.releaseSequence &&
            (!latest || version.releaseSequence > (latest.releaseSequence ?? 0))
          )
            latest = version;
        }
        cursor = result.nextCursor;
        if (cursor && seen.has(cursor))
          throw new Error('Resource pagination did not advance');
        if (cursor) seen.add(cursor);
      } while (cursor);
      return latest;
    },
    select: (
      placementId: string,
      selection: Selection,
      revision: number,
      signal: AbortSignal,
    ) =>
      client.post<Installation>(
        `${base}/placements/${encodeURIComponent(placementId)}/installations`,
        { selection },
        {
          signal,
          headers: {
            'Idempotency-Key': placementRequestKey(),
            'If-Match': `"${revision}"`,
          },
        },
      ),
  };
}

export function versionIdentity(
  pack: ResourcePackage,
  version: ResourceVersion,
): Selection['identity'] {
  return {
    tenantId: pack.tenantId,
    packageId: pack.id,
    versionId: version.id,
    kind: version.kind,
    version: version.version,
    manifestDigest: version.digest,
  };
}
