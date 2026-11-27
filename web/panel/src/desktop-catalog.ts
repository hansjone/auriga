import {
  allPluginPackages,
  validateStar,
  type MarketSort,
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
type Action = { packageId: string; versionId: string; revision: number };
type PluginOperation = {
  packageId: string;
  versionId: string;
  phase: string;
  attempt?: { index: number; total: number };
};

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
export type MarketListQuery = {
  page: number;
  pageSize: number;
  kind?: Kind;
  search: string;
  sort: MarketSort;
};
export type MarketPage = {
  packages: SkillPackage[];
  summaries: Map<string, SkillVersion>;
  total: number;
  query: MarketListQuery;
};
type Snapshot = {
  market: Record<Kind, Listing>;
  marketPage?: MarketPage;
  installations: InstallationSnapshot;
};

export class DesktopCatalog {
  readonly listeners = new Set<() => void>();
  readonly versions = new Map<string, VersionPage>();
  snapshot?: Snapshot;
  error?: unknown;
  loading = false;
  mutating = false;
  pluginOperation?: PluginOperation;
  private started = false;
  private lifetime = new AbortController();
  private progressTimer?: ReturnType<typeof setTimeout>;
  private installationPoll = false;
  private pollUntil = 0;
  private pendingPlugin?: Action;
  constructor(
    private fetch: HostFetch,
    private scope: CatalogScope,
  ) {}

  prefetch() {
    if (!this.started)
      void this.refresh(undefined, {
        page: 1,
        pageSize: 12,
        search: '',
        sort: '',
      });
  }

  dispose() {
    this.clear();
    this.emit();
    this.listeners.clear();
  }

  rejectAuth(error: unknown) {
    // Keep resident market rows; logout / identity switch clears via dispose.
    // Stop install-progress polling so a 401 blip does not hammer catalog.
    clearTimeout(this.progressTimer);
    this.progressTimer = undefined;
    this.pollUntil = 0;
    this.pluginOperation = undefined;
    this.pendingPlugin = undefined;
    this.error = error;
    this.emit();
  }

  private clear() {
    this.lifetime.abort();
    clearTimeout(this.progressTimer);
    this.progressTimer = undefined;
    this.pollUntil = 0;
    this.pluginOperation = undefined;
    this.pendingPlugin = undefined;
    this.snapshot = undefined;
    this.versions.clear();
  }

  private emit() {
    for (const listener of this.listeners) listener();
  }

  private get waitingForPlugin() {
    return this.snapshot?.installations.items.some(
      (row) =>
        row.resource.identity.kind === 'plugin' && row.state === 'applying',
    );
  }

  isTrackingPlugin(packageId: string) {
    return this.pendingPlugin?.packageId === packageId;
  }

  private get trackingPlugin() {
    return this.pendingPlugin || this.waitingForPlugin || this.pluginOperation;
  }

  private readPluginSelection(installations: InstallationSnapshot) {
    const selection = this.pendingPlugin;
    if (
      selection &&
      installations.items.some(
        (row) =>
          row.resource.identity.packageId === selection.packageId &&
          row.resource.identity.versionId === selection.versionId &&
          row.installationRevision > selection.revision,
      )
    )
      this.pendingPlugin = undefined;
  }

  private schedulePluginPoll() {
    const signal = this.lifetime.signal;
    if (
      this.progressTimer ||
      signal.aborted ||
      !this.pollUntil ||
      !this.trackingPlugin
    )
      return;
    this.progressTimer = setTimeout(async () => {
      await this.pollPlugins(signal);
      if (!signal.aborted) {
        this.progressTimer = undefined;
        this.schedulePluginPoll();
      }
    }, 1000);
  }

  private async pollPlugins(signal: AbortSignal) {
    if (signal.aborted || !this.trackingPlugin) return;
    if (Date.now() >= this.pollUntil) {
      this.pollUntil = 0;
      if (this.pendingPlugin)
        this.error ??= new ResourceDiagnosticError('platform_unreachable');
      this.pendingPlugin = undefined;
      this.pluginOperation = undefined;
      this.emit();
      return;
    }
    try {
      const { operation } = await this.get('plugin-progress', signal);
      if (signal.aborted) return;
      if (operation) this.pollUntil = Date.now() + 300000;
      if (
        JSON.stringify(this.pluginOperation ?? null) !==
        JSON.stringify(operation)
      ) {
        this.pluginOperation = operation ?? undefined;
        this.emit();
      }
      if (
        !operation &&
        !this.mutating &&
        (this.pendingPlugin || this.waitingForPlugin) &&
        !this.installationPoll
      ) {
        const before = this.snapshot;
        this.installationPoll = true;
        void this.readInstallations(signal)
          .then((installations) => {
            if (
              !signal.aborted &&
              before &&
              this.snapshot === before &&
              !this.loading &&
              !this.mutating
            ) {
              const wasChecking = !!this.pendingPlugin;
              this.readPluginSelection(installations);
              if (
                wasChecking !== !!this.pendingPlugin ||
                JSON.stringify(before.installations) !==
                  JSON.stringify(installations)
              ) {
                this.snapshot = { ...before, installations };
                this.emit();
              }
            }
          })
          .catch((error) => {
            if (!signal.aborted && catalogStatus(error) === 401)
              this.rejectAuth(error);
          })
          .finally(() => {
            this.installationPoll = false;
          });
      }
    } catch (error) {
      if (!signal.aborted && catalogStatus(error) === 401)
        this.rejectAuth(error);
    }
  }

  private get(path: string, signal: AbortSignal = this.lifetime.signal) {
    return catalogRequest(this.fetch, path, signal, undefined, this.scope);
  }

  private async readInstallations(signal: AbortSignal) {
    const installations = validateInstallations(
      await this.get('installations', signal),
    );
    if (installations.identity?.ownerId !== this.scope.ownerId)
      throw Object.assign(new ResourceDiagnosticError('unauthenticated'), {
        status: 401,
      });
    return installations;
  }

  private queuedWork?: (signal: AbortSignal) => Promise<void>;

  private async run(work: (signal: AbortSignal) => Promise<void>) {
    if (this.mutating || this.lifetime.signal.aborted) return;
    if (this.loading) {
      this.queuedWork = work;
      return;
    }
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
      // Keep the last loaded market snapshot on auth blips. Explicit sign-out /
      // scope clearCatalog owns wiping resident cache, not a transient 401.
    } finally {
      operation.abort();
      this.loading = false;
      this.schedulePluginPoll();
      const next = this.queuedWork;
      this.queuedWork = undefined;
      this.emit();
      if (next && !lifetime.aborted) void this.run(next);
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

  private async packagesPage(query: MarketListQuery, signal: AbortSignal) {
    const params = new URLSearchParams({
      limit: String(query.pageSize),
      page: String(query.page),
    });
    if (query.kind) params.set('kind', query.kind);
    if (query.search.trim()) params.set('search', query.search.trim());
    if (query.sort) params.set('sort', query.sort);
    const value = await this.get('packages?' + params, signal);
    if (
      !Array.isArray(value.items) ||
      value.items.length > query.pageSize ||
      value.items.some(
        (pack: SkillPackage) =>
          !pack ||
          !['plugin', 'skill'].includes(pack.kind) ||
          (query.kind && pack.kind !== query.kind),
      ) ||
      typeof value.total !== 'number' ||
      !Number.isSafeInteger(value.total) ||
      value.total < 0
    )
      throw new Error('invalid');
    const packages = value.items as SkillPackage[];
    const summaries = await this.summaries(packages, signal);
    return {
      packages,
      summaries,
      total: value.total as number,
      query,
    } satisfies MarketPage;
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

  async refresh(selected?: SkillPackage, query?: MarketListQuery) {
    this.started = true;
    await this.run(async (signal) => {
      if (query) {
        const [marketPage, installations] = await Promise.all([
          this.packagesPage(query, signal),
          this.readInstallations(signal),
        ]);
        const current =
          selected &&
          marketPage.packages.find((pack) => pack.id === selected.id);
        const details = current && (await this.readVersions(current, signal));
        if (signal.aborted) return;
        this.versions.clear();
        if (current && details) this.versions.set(current.id, details);
        const byKind = (kind: Kind): Listing => ({
          packages: marketPage.packages.filter((pack) => pack.kind === kind),
          summaries: new Map(
            [...marketPage.summaries].filter(([id]) =>
              marketPage.packages.some(
                (pack) => pack.id === id && pack.kind === kind,
              ),
            ),
          ),
        });
        this.snapshot = {
          market: { plugin: byKind('plugin'), skill: byKind('skill') },
          marketPage,
          installations,
        };
        this.readPluginSelection(installations);
        if (this.waitingForPlugin) this.pollUntil = Date.now() + 300000;
        return;
      }
      const [plugin, skill, installations] = await Promise.all([
        allPluginPackages((cursor) => this.packages('plugin', signal, cursor)),
        this.packages('skill', signal),
        this.readInstallations(signal),
      ]);
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
        marketPage: this.snapshot?.marketPage,
        installations,
      };
      this.readPluginSelection(installations);
      if (this.waitingForPlugin) this.pollUntil = Date.now() + 300000;
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
      if (signal.aborted || !this.snapshot) return;
      const patch = (item: SkillPackage) =>
        item.id === pack.id && item.market
          ? { ...item, market: { ...item.market, ...result } }
          : item;
      const listing = this.snapshot.market.plugin;
      const marketPage = this.snapshot.marketPage;
      this.snapshot = {
        ...this.snapshot,
        market: {
          ...this.snapshot.market,
          plugin: {
            ...listing,
            packages: listing.packages.map(patch),
          },
        },
        marketPage: marketPage
          ? {
              ...marketPage,
              packages: marketPage.packages.map(patch),
            }
          : marketPage,
      };
    });
  }

  async act<T extends Action>(body: T, kind: Kind) {
    if (kind === 'plugin' && this.pendingPlugin) throw new Error('unavailable');
    return this.mutate(async (signal) => {
      if (kind === 'plugin') {
        this.pendingPlugin = body;
        this.pollUntil = Date.now() + 300000;
        this.schedulePluginPoll();
      }
      try {
        const result = await catalogRequest(
          this.fetch,
          'actions',
          signal,
          body,
          this.scope,
        );
        if (!signal.aborted) {
          if (kind === 'plugin') this.pendingPlugin = undefined;
          if (result.installation) this.updateInstallation(result.installation);
        }
        return result;
      } catch (error) {
        if (kind === 'plugin' && !signal.aborted) {
          const status = catalogStatus(error);
          if (status === undefined || status >= 500) {
            // A lost response can still commit the selection. Keep polling by exact version.
            this.pollUntil =
              Date.now() + (this.pluginOperation ? 300000 : 30000);
            return { awaitingResult: true };
          }
          this.pendingPlugin = undefined;
        }
        throw error;
      }
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
      if (!this.pendingPlugin && !this.waitingForPlugin)
        this.pluginOperation = undefined;
      this.schedulePluginPoll();
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
