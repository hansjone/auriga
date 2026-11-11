import type { components } from '../../shared/src/generated/resource';
import {
  createResourceReader,
  ResourceError,
  type PanelContext,
  type ResourcePackage,
} from './resources';
export type Distribution = components['schemas']['Distribution'];
export type Action =
  components['schemas']['DistributionInput']['selections'][number]['action'];
type Identity = NonNullable<PanelContext['resourceIdentity']>;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const id = (v: unknown): v is string => typeof v === 'string' && uuid.test(v);
const counter = (v: unknown) => Number.isSafeInteger(v) && Number(v) > 0;
const generation = (v: unknown) => Number.isSafeInteger(v) && Number(v) >= 0;
export function allowedActions(
  pack: ResourcePackage,
  identity: Identity,
): Action[] {
  if (pack.tenantId !== identity.tenantId) return [];
  if (pack.level === 'l0' || pack.level === 'l1') return [];
  if (
    pack.level === 'l4' &&
    (pack.ownerId !== identity.ownerId || pack.visibility !== 'private')
  )
    return [];
  const actions: Action[] =
    pack.level === 'l2'
      ? ['enable', 'disable']
      : ['install', 'enable', 'disable', 'remove'];
  return pack.enabled
    ? actions
    : actions.filter((action) => action === 'disable' || action === 'remove');
}
export function distributionPhase(row: Distribution): Distribution['state'] {
  if (row.state !== 'applied') return row.state;
  return generation(row.generation) &&
    row.generation === row.observedGeneration &&
    row.ready === true
    ? 'applied'
    : 'applying';
}
export function canDistribute(context?: PanelContext): boolean {
  return (
    context?.mode === 'managed' &&
    context.resourceDistributionAvailable === true &&
    context.protocol === 1 &&
    typeof context.fetch === 'function' &&
    !!context.resourceIdentity &&
    Object.values(context.resourceIdentity).every(id)
  );
}
export function createDistributionClient(context: PanelContext) {
  if (!canDistribute(context)) throw new ResourceError('unavailable');
  const identity = { ...context.resourceIdentity! },
    fetch = context.fetch!,
    reader = createResourceReader(context);
  async function send(
    path: string,
    signal: AbortSignal,
    init?: RequestInit,
  ): Promise<Distribution> {
    let response: Response;
    try {
      response = await fetch(path, {
        ...init,
        signal,
        method: init?.method ?? 'GET',
        cache: 'no-store',
        redirect: 'error',
      });
    } catch {
      throw new ResourceError('unavailable');
    }
    if (!response.ok)
      throw new ResourceError(
        response.status === 401
          ? 'auth'
          : response.status === 403
            ? 'forbidden'
            : response.status === 404
              ? 'missing'
              : response.status === 429
                ? 'limited'
                : 'unavailable',
      );
    if (!response.headers.get('content-type')?.startsWith('application/json'))
      throw new ResourceError('invalid');
    let row: Distribution;
    try {
      row = await response.json();
    } catch {
      throw new ResourceError('invalid');
    }
    if (
      !row ||
      !id(row.id) ||
      row.instanceId !== identity.instanceId ||
      row.ownerId !== identity.ownerId ||
      !counter(row.revision) ||
      !counter(row.selectionRevision) ||
      !['requested', 'resolved', 'applying', 'applied', 'failed'].includes(
        row.state,
      ) ||
      (row.generation !== undefined && !generation(row.generation)) ||
      (row.observedGeneration !== undefined &&
        !generation(row.observedGeneration)) ||
      (row.ready !== undefined && typeof row.ready !== 'boolean')
    )
      throw new ResourceError('invalid');
    return row;
  }
  return {
    async request(
      packageId: string,
      versionId: string,
      action: Action,
      key: string,
      signal: AbortSignal,
    ) {
      if (
        !id(packageId) ||
        !id(versionId) ||
        !/^[A-Za-z0-9._-]{1,128}$/.test(key)
      )
        throw new ResourceError('invalid');
      // Re-read authorization metadata immediately before the write. The server
      // remains authoritative for current membership, policy and the exact selection.
      const pack = await reader.detail(packageId, signal);
      const version = await reader.version(packageId, versionId, signal);
      if (
        !allowedActions(pack, identity).includes(action) ||
        (['install', 'enable'].includes(action) &&
          version.state !== 'published')
      )
        throw new ResourceError('forbidden');
      return send('/api/resource/v1/distributions', signal, {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          accept: 'application/json',
          'Idempotency-Key': key,
        },
        body: JSON.stringify({
          instanceId: identity.instanceId,
          selections: [{ packageId, versionId, action }],
        }),
      });
    },
    async status(distributionId: string, signal: AbortSignal) {
      if (!id(distributionId)) throw new ResourceError('invalid');
      const row = await send(
        `/api/resource/v1/distributions/${distributionId}`,
        signal,
      );
      if (row.id !== distributionId) throw new ResourceError('invalid');
      return row;
    },
  };
}
