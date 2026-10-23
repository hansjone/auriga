import type { components } from '../../shared/src/generated/resource';

export type ResourcePackage = components['schemas']['Package'];
export type ResourceVersion = components['schemas']['Version'];
export type PackagePage = components['schemas']['PackagePage'];
export type VersionPage = components['schemas']['VersionPage'];

// The host owns authentication. Never fall back to window.fetch or apiBase.
export interface PanelContext {
  mode?: 'managed' | 'connect' | 'standalone';
  protocol?: number;
  locale?: string;
  theme?: 'dark' | 'light';
  apiBase?: string;
  fetch?: (path: string, init?: RequestInit) => Promise<Response>;
}

export type Failure =
  'unavailable' | 'auth' | 'forbidden' | 'missing' | 'limited' | 'invalid';
export class ResourceError extends Error {
  constructor(public readonly reason: Failure) {
    super(reason);
  }
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const oneOf = (value: unknown, choices: string[]) =>
  typeof value === 'string' && choices.includes(value);
const id = (value: unknown): value is string =>
  typeof value === 'string' && uuid.test(value);
const shortText = (value: unknown, max: number): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= max;
const revision = (value: unknown) =>
  Number.isSafeInteger(value) && Number(value) > 0;

function isPackage(value: unknown): value is ResourcePackage {
  return (
    record(value) &&
    id(value.id) &&
    id(value.tenantId) &&
    id(value.ownerId) &&
    shortText(value.name, 214) &&
    value.kind === 'plugin' &&
    oneOf(value.level, ['l0', 'l1', 'l2', 'l3', 'l4']) &&
    oneOf(value.visibility, ['tenant', 'members', 'groups', 'private']) &&
    Array.isArray(value.groupIds) &&
    value.groupIds.every(id) &&
    revision(value.revision) &&
    typeof value.createdAt === 'string' &&
    Number.isFinite(Date.parse(value.createdAt)) &&
    typeof value.enabled === 'boolean' &&
    (value.defaultVersionId === null || id(value.defaultVersionId))
  );
}

function isVersion(value: unknown): value is ResourceVersion {
  return (
    record(value) &&
    id(value.id) &&
    id(value.packageId) &&
    shortText(value.version, 128) &&
    typeof value.digest === 'string' &&
    /^sha256:[0-9a-f]{64}$/.test(value.digest) &&
    revision(value.sizeBytes) &&
    Number(value.sizeBytes) <= 67108864 &&
    value.mediaType === 'application/gzip' &&
    revision(value.revision) &&
    oneOf(value.state, [
      'draft',
      'pending_review',
      'approved',
      'rejected',
      'published',
      'deprecated',
      'withdrawn',
    ]) &&
    typeof value.createdAt === 'string' &&
    Number.isFinite(Date.parse(value.createdAt))
  );
}

function page<T>(
  value: unknown,
  valid: (item: unknown) => item is T,
): { items: T[]; nextCursor?: string } {
  if (
    !record(value) ||
    !Array.isArray(value.items) ||
    value.items.length > 200 ||
    !value.items.every(valid) ||
    (value.nextCursor !== undefined && typeof value.nextCursor !== 'string')
  ) {
    throw new ResourceError('invalid');
  }
  return {
    items: value.items,
    // The response contract permits an empty string for the terminal page.
    nextCursor: value.nextCursor === '' ? undefined : value.nextCursor,
  };
}

export function canReadResources(context?: PanelContext): boolean {
  return (
    context?.mode === 'connect' &&
    context.protocol === 1 &&
    typeof context.fetch === 'function'
  );
}

export function createResourceReader(context: PanelContext) {
  if (!canReadResources(context)) throw new ResourceError('unavailable');
  const hostFetch = context.fetch!;
  async function read(path: string, signal: AbortSignal): Promise<unknown> {
    let response: Response;
    try {
      response = await hostFetch(path, {
        method: 'GET',
        signal,
        cache: 'no-store',
        redirect: 'error',
        headers: { accept: 'application/json' },
      });
    } catch {
      throw new ResourceError('unavailable');
    }
    if (!response.ok) {
      const reasons: Record<number, Failure> = {
        401: 'auth',
        403: 'forbidden',
        404: 'missing',
        429: 'limited',
      };
      throw new ResourceError(reasons[response.status] ?? 'unavailable');
    }
    if (
      !response.headers
        .get('content-type')
        ?.toLowerCase()
        .startsWith('application/json')
    )
      throw new ResourceError('invalid');
    try {
      return await response.json();
    } catch {
      throw new ResourceError('invalid');
    }
  }
  const base = '/api/resource/v1/packages';
  const query = (cursor?: string) =>
    `?${new URLSearchParams({ limit: '50', ...(cursor ? { cursor } : {}) })}`;
  function packagePath(packageId: string) {
    if (!id(packageId)) throw new ResourceError('invalid');
    return `${base}/${packageId}`;
  }
  return {
    async list(signal: AbortSignal, cursor?: string): Promise<PackagePage> {
      return page(await read(base + query(cursor), signal), isPackage);
    },
    async detail(
      packageId: string,
      signal: AbortSignal,
    ): Promise<ResourcePackage> {
      const value = await read(packagePath(packageId), signal);
      if (!isPackage(value) || value.id !== packageId)
        throw new ResourceError('invalid');
      return value;
    },
    async versions(
      packageId: string,
      signal: AbortSignal,
      cursor?: string,
    ): Promise<VersionPage> {
      const value = page(
        await read(
          packagePath(packageId) + '/versions' + query(cursor),
          signal,
        ),
        isVersion,
      );
      if (value.items.some((item) => item.packageId !== packageId))
        throw new ResourceError('invalid');
      return value;
    },
  };
}
