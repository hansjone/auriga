import {
  allPluginPackages,
  validateStar,
} from '../../shared/src/plugin-market';
import {
  ResourceDiagnosticError,
  resourceResponseError,
} from './resource-diagnostics';
import type {
  PanelContext,
  SkillInstallation,
  SkillPackage,
  SkillVersion,
} from './resources';

type HostFetch = NonNullable<PanelContext['fetch']>;
type CatalogScope = NonNullable<PanelContext['catalogSession']>;
type Kind = 'plugin' | 'skill';

export interface InstallationSnapshot {
  identity?: { ownerId: string; tenantId: string };
  items: SkillInstallation[];
  sharedAvailable?: boolean;
  sharedUnavailableReason?: string;
  pendingRestart?: boolean;
  hostBusy?: boolean;
  policyCacheUnavailable?: boolean;
  blockedPersonalPlugins?: string[];
  personalPluginPolicyPendingRestart?: boolean;
  allowPersonalPlugins?: boolean;
}

export function validateInstallations(value: InstallationSnapshot) {
  if (
    !value ||
    [
      'sharedAvailable',
      'pendingRestart',
      'hostBusy',
      'policyCacheUnavailable',
      'personalPluginPolicyPendingRestart',
      'allowPersonalPlugins',
    ].some(
      (key) =>
        key in value &&
        typeof value[key as keyof InstallationSnapshot] !== 'boolean',
    ) ||
    !Array.isArray(value.items) ||
    value.items.length > 10000 ||
    value.items.some(
      (row) =>
        !row?.resource?.identity ||
        !['plugin', 'skill'].includes(row.resource.identity.kind),
    )
  )
    throw new Error('invalid');
  const blocked = value.blockedPersonalPlugins ?? [];
  if (
    !Array.isArray(blocked) ||
    blocked.length > 10000 ||
    blocked.some((name) => typeof name !== 'string' || name.length > 214)
  )
    throw new Error('invalid');
  return value;
}

export async function catalogRequest(
  fetch: HostFetch,
  path: string,
  parent?: AbortSignal,
  body?: unknown,
  scope?: CatalogScope,
) {
  const controller = new AbortController();
  let cancel: (() => void) | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const aborted = new Promise<never>((_, reject) => {
    cancel = () => {
      controller.abort();
      reject(new Error('unavailable'));
    };
    parent?.addEventListener('abort', cancel, { once: true });
    if (parent?.aborted) cancel();
    else timer = setTimeout(cancel, body === undefined ? 15000 : 90000);
  });
  try {
    return await Promise.race([
      aborted,
      (async () => {
        if (controller.signal.aborted) throw new Error('unavailable');
        const response = await fetch('catalog/' + path, {
          method: body === undefined ? 'GET' : 'POST',
          cache: 'no-store',
          redirect: 'error',
          signal: controller.signal,
          headers: {
            accept: 'application/json',
            ...(scope ? { 'x-auriga-catalog-owner': scope.ownerId } : {}),
            ...(scope?.generation
              ? { 'x-auriga-catalog-generation': scope.generation }
              : {}),
            ...(body === undefined
              ? {}
              : {
                  'content-type': 'application/json',
                  'x-auriga-catalog': '1',
                }),
          },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
        if (!response.ok)
          throw Object.assign(await resourceResponseError(response), {
            status: response.status,
          });
        if (
          !response.headers.get('content-type')?.startsWith('application/json')
        )
          throw new Error('invalid');
        return response.json();
      })(),
    ]);
  } finally {
    clearTimeout(timer);
    if (cancel) parent?.removeEventListener('abort', cancel);
  }
}

export function catalogStatus(error: unknown) {
  return error instanceof ResourceDiagnosticError
    ? (error as ResourceDiagnosticError & { status?: number }).status
    : undefined;
}

type Listing = {
  packages: SkillPackage[];
  cursor?: string;
  summaries: Map<string, SkillVersion>;
};
type VersionPage = { items: SkillVersion[]; nextCursor?: string };
type Snapshot = {
  market: Record<Kind, Listing>;
  installations: InstallationSnapshot;
};

export class DesktopCatalog {
  readonly listeners = new Set<() => void>();
  readonly versions = new Map<string, VersionPage>();
  snapshot?: Snapshot;
  error?: unknown;
  loading = false;
  mutating = false;
  private started = false;
  private lifetime = new AbortController();
  constructor(
    private fetch: HostFetch,
    private scope: CatalogScope,
  ) {}

  prefetch() {
    if (!this.started) void this.refresh();
  }

  dispose() {
    this.lifetime.abort();
    this.snapshot = undefined;
    this.versions.clear();
    this.emit();
    this.listeners.clear();
  }

  rejectAuth(error: unknown) {
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.snapshot = undefined;
    this.versions.clear();
    this.error = error;
    this.emit();
  }

  private emit() {
    for (const listener of this.listeners) listener();
  }

  private get(path: string, signal: AbortSignal = this.lifetime.signal) {
    return catalogRequest(this.fetch, path, signal, undefined, this.scope);
  }

  private async run(work: (signal: AbortSignal) => Promise<void>) {
    if (this.loading || this.mutating || this.lifetime.signal.aborted) return;
    this.loading = true;
    this.error = undefined;
    this.emit();
    const lifetime = this.lifetime.signal;
    const operation = new AbortController();
    const signal = AbortSignal.any([lifetime, operation.signal]);
    try {
      await work(signal);
    } catch (error) {
      if (lifetime.aborted) return;
      this.error = error;
      if ([401, 403].includes(catalogStatus(error) ?? 0)) {
        this.snapshot = undefined;
        this.versions.clear();
      }
    } finally {
      operation.abort();
      this.loading = false;
      this.emit();
    }
  }

  private async packages(kind: Kind, signal: AbortSignal, cursor?: string) {
    const { items, nextCursor } = await this.get(
      'packages?' +
        new URLSearchParams({
          limit: '50',
          kind,
          ...(cursor ? { cursor } : {}),
        }),
      signal,
    );
    if (
      !Array.isArray(items) ||
      items.length > 200 ||
      items.some((pack) => !pack || pack.kind !== kind) ||
      (nextCursor !== undefined && typeof nextCursor !== 'string') ||
      (kind === 'skill' && nextCursor && nextCursor === cursor)
    )
      throw new Error('invalid');
    return {
      packages: items as SkillPackage[],
      cursor: nextCursor || undefined,
    };
  }

  private async summaries(packages: SkillPackage[], signal: AbortSignal) {
    const pending = packages.filter(
      (pack) => pack.defaultVersionId && !pack.market,
    );
    const result = new Map<string, SkillVersion>();
    for (let index = 0; index < pending.length; index += 6) {
      await Promise.all(
        pending.slice(index, index + 6).map(async (pack) => {
          try {
            const version = await this.get(
              'packages/' + pack.id + '/versions/' + pack.defaultVersionId,
              signal,
            );
            if (
              version?.kind !== pack.kind ||
              version.packageId !== pack.id ||
              version.id !== pack.defaultVersionId
            )
              throw new Error('invalid');
            result.set(pack.id, version);
          } catch (error) {
            // The default can be withdrawn between the catalog and version reads.
            if (catalogStatus(error) !== 404) throw error;
          }
        }),
      );
    }
    return result;
  }

  async refresh(selected?: SkillPackage) {
    this.started = true;
    await this.run(async (signal) => {
      const [plugin, skill, installations] = await Promise.all([
        allPluginPackages((cursor) => this.packages('plugin', signal, cursor)),
        this.packages('skill', signal),
        this.get('installations', signal).then(validateInstallations),
      ]);
      if (installations.identity?.ownerId !== this.scope.ownerId)
        throw Object.assign(new ResourceDiagnosticError('unauthenticated'), {
          status: 401,
        });
      const packages = [...plugin.packages, ...skill.packages];
      if (new Set(packages.map((pack) => pack.id)).size !== packages.length)
        throw new Error('invalid');
      const current =
        selected && packages.find((pack) => pack.id === selected.id);
      const [summaries, details] = await Promise.all([
        this.summaries(packages, signal),
        current && this.readVersions(current, signal),
      ]);
      if (signal.aborted) return;
      this.versions.clear();
      if (current && details) this.versions.set(current.id, details);
      this.snapshot = {
        market: {
          plugin: { ...plugin, summaries },
          skill: { ...skill, summaries },
        },
        installations,
      };
    });
  }

  async more(kind: Kind) {
    const before = this.snapshot;
    const listing = before?.market[kind];
    if (!before || !listing?.cursor) return;
    await this.run(async (signal) => {
      const next = await this.packages(kind, signal, listing.cursor);
      const packages = [...listing.packages, ...next.packages];
      if (new Set(packages.map((pack) => pack.id)).size !== packages.length)
        throw new Error('invalid');
      const summaries = await this.summaries(next.packages, signal);
      if (signal.aborted || !this.snapshot) return;
      this.snapshot = {
        ...this.snapshot,
        market: {
          ...this.snapshot.market,
          [kind]: {
            packages,
            cursor: next.cursor,
            summaries: new Map([...listing.summaries, ...summaries]),
          },
        },
      };
    });
  }

  private async readVersions(pack: SkillPackage, signal: AbortSignal) {
    const page = await this.get(
      'packages/' + pack.id + '/versions?limit=200',
      signal,
    );
    if (
      !Array.isArray(page.items) ||
      page.items.some(
        (version: SkillVersion) =>
          version.kind !== pack.kind || version.packageId !== pack.id,
      )
    )
      throw new Error('invalid');
    return page as VersionPage;
  }

  async versionsFor(pack: SkillPackage, signal: AbortSignal) {
    const existing = this.versions.get(pack.id);
    if (existing) return existing;
    const current = this.snapshot?.market;
    const active = AbortSignal.any([signal, this.lifetime.signal]);
    const result = await this.readVersions(pack, active);
    if (!active.aborted && current === this.snapshot?.market)
      this.versions.set(pack.id, result);
    return result;
  }

  async star(pack: SkillPackage) {
    const market = pack.market;
    if (!market) return;
    await this.mutate(async (signal) => {
      const result = validateStar(
        await catalogRequest(
          this.fetch,
          'packages/' + encodeURIComponent(pack.id) + '/star',
          signal,
          { starred: !market.starred },
          this.scope,
        ),
      );
      const listing = this.snapshot?.market.plugin;
      if (signal.aborted || !this.snapshot || !listing) return;
      this.snapshot = {
        ...this.snapshot,
        market: {
          ...this.snapshot.market,
          plugin: {
            ...listing,
            packages: listing.packages.map((item) =>
              item.id === pack.id && item.market
                ? { ...item, market: { ...item.market, ...result } }
                : item,
            ),
          },
        },
      };
    });
  }

  async act(body: unknown) {
    return this.mutate(async (signal) => {
      const result = await catalogRequest(
        this.fetch,
        'actions',
        signal,
        body,
        this.scope,
      );
      if (!signal.aborted && result.installation)
        this.updateInstallation(result.installation);
      return result;
    });
  }

  private async mutate<T>(work: (signal: AbortSignal) => Promise<T>) {
    const signal = this.lifetime.signal;
    if (this.loading || this.mutating || signal.aborted)
      throw new Error('unavailable');
    this.mutating = true;
    this.error = undefined;
    this.emit();
    try {
      return await work(signal);
    } catch (error) {
      if (!signal.aborted) {
        if (catalogStatus(error) === 401) this.rejectAuth(error);
        else this.error = error;
      }
      throw error;
    } finally {
      this.mutating = false;
      this.emit();
    }
  }

  updateInstallation(row: SkillInstallation) {
    if (!this.snapshot || this.lifetime.signal.aborted) return;
    const { installations } = this.snapshot;
    this.snapshot = {
      ...this.snapshot,
      installations: {
        ...installations,
        items: [
          ...installations.items.filter(
            (item) =>
              item.resource.identity.packageId !==
              row.resource.identity.packageId,
          ),
          row,
        ],
      },
    };
    this.emit();
  }
}
