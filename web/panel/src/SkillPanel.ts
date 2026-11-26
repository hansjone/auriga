import {
  allPluginPackages,
  filterPlugins,
  marketAuthor,
  marketDate,
  marketDepartment,
  marketOptions,
  PluginPaginationError,
  validateStar,
  type MarketSort,
  type PluginMarket,
} from './plugin-market';
import { element, resourceHeading, fieldList } from './market-ui';
import {
  resourceErrorMessage,
  ResourceDiagnosticError,
} from './resource-diagnostics';
import {
  catalogRequest,
  catalogStatus,
  validateInstallations,
  type DesktopCatalog,
  type InstallationSnapshot,
} from './desktop-catalog';
import type {
  PanelContext,
  SkillPackage as Package,
  SkillVersion as Version,
  SkillInstallation as Installation,
} from './resources';
/** Native resource channel owns identity, downloads and all filesystem operations. */
export class SkillPanel {
  private epoch = 0;
  private controller?: AbortController;
  private packages: Package[] = [];
  private rows: Installation[] = [];
  private versions: Version[] = [];
  private selected?: Package;
  private cursor?: string;
  private versionCursor?: string;
  private busy = false;
  private sharedAvailable?: boolean;
  private summaries = new Map<string, Version>();
  private pendingRestart = false;
  private hostBusy = false;
  private policyCacheUnavailable = false;
  private search = '';
  private filters = {
    author: '',
    department: '',
    tag: '',
    sort: '' as MarketSort,
  };
  private blockedPersonalPlugins: string[] = [];
  private personalPluginPolicyPendingRestart = false;
  private allowPersonalPlugins = true;
  private sharedReason?: string;
  private confirmation?: {
    packageId: string;
    versionId: string;
    action: string;
    key: string;
    token: string;
  };
  private notice = '';
  private failed = false;
  private source = new Map<string, string>();
  constructor(
    private root: HTMLElement,
    private context: PanelContext,
    private mine: boolean,
    private kind: 'plugin' | 'skill' = 'skill',
    private desktop?: DesktopCatalog,
  ) {
    if (desktop) {
      this.controller = new AbortController();
      desktop.listeners.add(this.syncDesktop);
      desktop.prefetch();
      this.syncDesktop();
    } else void this.load();
  }
  dispose() {
    this.desktop?.listeners.delete(this.syncDesktop);
    this.epoch++;
    this.controller?.abort();
  }
  private desktopError?: unknown;
  private syncDesktop = () => {
    const desktop = this.desktop!;
    const snapshot = desktop.snapshot;
    const listing = snapshot?.market[this.kind];
    this.packages = listing?.packages ?? [];
    this.summaries = listing?.summaries ?? new Map();
    this.cursor = listing?.cursor;
    this.installationSnapshot(snapshot?.installations ?? { items: [] });
    if (this.selected) {
      this.selected = this.packages.find(
        (pack) => pack.id === this.selected?.id,
      );
      const details = this.selected && desktop.versions.get(this.selected.id);
      this.versions = details?.items ?? [];
      this.versionCursor = details?.nextCursor;
    }
    const needsAuth =
      desktop.error !== this.desktopError &&
      catalogStatus(desktop.error) === 401;
    this.desktopError = desktop.error;
    this.render();
    if (needsAuth) this.authenticationRequired();
  };
  private get pending() {
    return (
      this.busy ||
      this.desktop?.loading === true ||
      this.desktop?.mutating === true
    );
  }
  present(context: PanelContext) {
    this.context = context;
    this.render();
  }
  private get label() {
    return this.kind === 'plugin'
      ? this.text('插件', 'Plugin')
      : this.text('技能', 'Skill');
  }
  private installationSnapshot(value: InstallationSnapshot) {
    validateInstallations(value);
    const blocked = value.blockedPersonalPlugins ?? [];
    this.rows = value.items.filter(
      (r: Installation) => r.resource.identity.kind === this.kind,
    );
    this.sharedAvailable = value.sharedAvailable !== false;
    this.sharedReason = value.sharedUnavailableReason;
    this.pendingRestart = !!value.pendingRestart;
    this.hostBusy = value.hostBusy === true;
    this.policyCacheUnavailable = value.policyCacheUnavailable === true;
    this.blockedPersonalPlugins = blocked;
    this.personalPluginPolicyPendingRestart =
      value.personalPluginPolicyPendingRestart === true;
    this.allowPersonalPlugins = value.allowPersonalPlugins !== false;
  }
  private get zh() {
    return !this.context.locale?.startsWith('en');
  }
  private text(zh: string, en: string) {
    return this.zh ? zh : en;
  }
  private status(value: string) {
    const zh: Record<string, string> = {
      applying: '正在应用',
      applied: '已生效',
      failed: '失败',
      pending_restart: '待重启或确认',
      superseded: '已被新选择替代',
      overridden: '被同名技能覆盖',
      expired: '已过期',
      revoked: '已撤销',
      disabled: '已关闭',
      removed: '已卸载',
      draft: '草稿',
      pending_review: '待审核',
      approved: '审核通过',
      rejected: '审核驳回',
      published: '已发布',
      deprecated: '已下架',
      withdrawn: '已撤回',
    };
    return this.zh ? (zh[value] ?? '未知状态') : value.replaceAll('_', ' ');
  }
  private authenticationRequired() {
    this.root.dispatchEvent(
      new CustomEvent('auriga-auth-required', {
        bubbles: true,
        composed: true,
      }),
    );
  }
  private async request(path: string, body?: unknown) {
    if (!this.context.fetch) throw new Error('unavailable');
    try {
      return await catalogRequest(
        this.context.fetch,
        path,
        this.controller?.signal,
        body,
        this.context.catalogSession,
      );
    } catch (error) {
      if (!this.desktop && catalogStatus(error) === 401)
        this.authenticationRequired();
      throw error;
    }
  }
  private async run(work: () => Promise<void>) {
    if (this.pending) return;
    const epoch = this.epoch;
    this.busy = true;
    this.notice = '';
    this.failed = false;
    this.render();
    try {
      await work();
    } catch (error) {
      if (this.epoch === epoch) {
        if (
          this.desktop &&
          this.desktop.error !== error &&
          catalogStatus(error) === 401
        )
          this.desktop.rejectAuth(error);
        this.failed = true;
        if (!this.desktop) {
          this.packages = [];
          this.versions = [];
          this.selected = undefined;
          this.rows = [];
          this.summaries.clear();
        }
        this.notice = this.errorMessage(error);
      }
    } finally {
      if (this.epoch === epoch) {
        this.busy = false;
        this.render();
      }
    }
  }
  private async load(more = false) {
    if (this.desktop) {
      this.notice = '';
      this.failed = false;
      this.confirmation = undefined;
      if (more) await this.desktop.more(this.kind);
      else await this.desktop.refresh(this.selected);
      return;
    }
    this.controller?.abort();
    this.controller = new AbortController();
    const epoch = ++this.epoch;
    this.sharedAvailable = undefined;
    this.sharedReason = undefined;
    await this.run(async () => {
      if (this.mine) {
        const value = await this.request('installations');
        if (epoch !== this.epoch) return;
        this.installationSnapshot(value);
      } else {
        const read = async (cursor?: string) => {
          const value = await this.request(
            'packages?limit=50&kind=' +
              this.kind +
              (cursor ? '&cursor=' + encodeURIComponent(cursor) : ''),
          );
          if (
            !Array.isArray(value.items) ||
            value.items.length > 200 ||
            value.items.some((p: Package) => p?.kind !== this.kind) ||
            (value.nextCursor !== undefined &&
              typeof value.nextCursor !== 'string') ||
            (this.kind === 'skill' &&
              value.nextCursor &&
              value.nextCursor === cursor)
          )
            throw new Error('invalid');
          return {
            packages: value.items as Package[],
            cursor: value.nextCursor || undefined,
          };
        };
        const value =
          this.kind === 'plugin'
            ? await allPluginPackages(read)
            : await read(more ? this.cursor : undefined);
        if (epoch !== this.epoch) return;
        const merged = more
          ? [...this.packages, ...value.packages]
          : value.packages;
        if (new Set(merged.map((p) => p.id)).size !== merged.length)
          throw new Error('invalid');
        this.packages = merged;
        this.cursor = value.cursor;
        const installed = await this.request('installations');
        if (epoch !== this.epoch) return;
        this.installationSnapshot(installed);
        if (!more) this.summaries.clear();
        const displayed = this.packages.filter(
          (pack) =>
            pack.defaultVersionId &&
            !pack.market &&
            !this.summaries.has(pack.id),
        );
        for (let index = 0; index < displayed.length; index += 4) {
          await Promise.allSettled(
            displayed.slice(index, index + 4).map(async (pack) => {
              const version = await this.request(
                'packages/' + pack.id + '/versions/' + pack.defaultVersionId,
              );
              if (
                epoch === this.epoch &&
                version.kind === this.kind &&
                version.packageId === pack.id &&
                version.id === pack.defaultVersionId
              )
                this.summaries.set(pack.id, version);
            }),
          );
        }
        if (this.selected) {
          const detail = await this.request(
            'packages/' + this.selected.id + '/versions?limit=200',
          );
          if (
            !Array.isArray(detail.items) ||
            detail.items.some(
              (v: Version) =>
                v.kind !== this.kind || v.packageId !== this.selected!.id,
            )
          )
            throw new Error('invalid');
          if (epoch === this.epoch) this.versions = detail.items;
        }
      }
    });
  }
  private button(label: string, action: () => void, disabled = false) {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    b.disabled = this.pending || disabled;
    b.onclick = action;
    return b;
  }
  private async open(pack: Package) {
    await this.run(async () => {
      const value = this.desktop
        ? await this.desktop.versionsFor(pack, this.controller!.signal)
        : await this.request(
            'packages/' + encodeURIComponent(pack.id) + '/versions?limit=200',
          );
      if (
        !Array.isArray(value.items) ||
        value.items.some(
          (v: Version) => v.kind !== this.kind || v.packageId !== pack.id,
        )
      )
        throw new Error('invalid');
      this.selected = pack;
      this.versions = value.items;
      this.versionCursor = value.nextCursor;
    });
  }
  private action(
    packageId: string,
    versionId: string,
    action: string,
    confirmation?: string,
    key: string = crypto.randomUUID(),
  ) {
    void this.run(async () => {
      this.confirmation = undefined;
      const old = this.rows.find(
        (row) => row.resource.identity.packageId === packageId,
      );
      const input = {
        packageId,
        versionId,
        action,
        revision: old?.installationRevision ?? 0,
        key,
        ...(confirmation ? { confirmation } : {}),
      };
      const result = this.desktop
        ? await this.desktop.act(input)
        : await this.request('actions', input);
      if (result.confirmationRequired) {
        this.confirmation = {
          packageId,
          versionId,
          action,
          key,
          token: result.confirmation,
        };
        this.notice =
          this.text(
            '发现同名私有或宿主技能，安装后可能被覆盖。请确认后继续：',
            'A private or host skill has the same name and may take precedence. Confirm to continue:',
          ) +
          ' ' +
          result.conflicts
            .map(
              (item: { provider: string; path?: string }) =>
                item.provider + (item.path ? ' · ' + item.path : ''),
            )
            .join('；');
        return;
      }
      this.notice = this.text(
        '已接受请求，等待实际安装回报。关闭或卸载后请新建会话。',
        'Request accepted; waiting for observed installation. Start a new session after disabling or removing.',
      );
      if (!this.desktop && result.installation) {
        this.rows = this.rows.filter(
          (row) => row.resource.identity.packageId !== packageId,
        );
        this.rows.push(result.installation);
      }
    });
  }
  private moreVersions() {
    if (!this.selected || !this.versionCursor) return;
    const pack = this.selected,
      cursor = this.versionCursor;
    void this.run(async () => {
      const value = await this.request(
        'packages/' +
          pack.id +
          '/versions?limit=200&cursor=' +
          encodeURIComponent(cursor),
      );
      if (
        !Array.isArray(value.items) ||
        value.items.length > 200 ||
        value.items.some(
          (v: Version) => v.kind !== this.kind || v.packageId !== pack.id,
        ) ||
        (this.kind === 'skill' &&
          value.nextCursor &&
          value.nextCursor === cursor)
      )
        throw new Error('invalid');
      const merged = [...this.versions, ...value.items];
      if (new Set(merged.map((v) => v.id)).size !== merged.length)
        throw new Error('invalid');
      this.versions = merged;
      this.versionCursor = value.nextCursor || undefined;
      this.desktop?.versions.set(pack.id, {
        items: merged,
        nextCursor: this.versionCursor,
      });
    });
  }
  private observe(row: Installation) {
    void this.run(async () => {
      const value = await this.request('observe', {
        packageId: row.resource.identity.packageId,
        preset: 'standard',
      });
      this.source.set(
        row.resource.identity.packageId,
        `${this.status(value.state)} · ${value.source?.provider ?? ''} · ${value.source?.path ?? ''}`,
      );
      this.notice = this.text(
        '已在新建的 standard 会话中检查实际来源。',
        'Checked actual source in a new standard session.',
      );
      const current = validateInstallations(
        await this.request('installations'),
      );
      const installation = current.items.find(
        (item) =>
          item.resource.identity.packageId === row.resource.identity.packageId,
      );
      if (this.desktop) {
        if (installation) this.desktop.updateInstallation(installation);
      } else this.installationSnapshot(current);
    });
  }
  private badge(text: string) {
    return element('span', text, 'resource-badge');
  }
  private installation(packageId: string) {
    return this.rows.find(
      (row) => row.resource.identity.packageId === packageId,
    );
  }
  private permissions(version: Version) {
    const permissions = version.manifest.permissions;
    return fieldList([
      [
        this.text('工具', 'Tools'),
        permissions?.tools?.join('、') || this.text('无声明', 'None declared'),
      ],
      [
        this.text('外部访问', 'Outbound hosts'),
        permissions?.outboundHosts?.join('、') ||
          this.text('无声明', 'None declared'),
      ],
      [
        this.text('数据访问', 'Data access'),
        permissions?.dataAccess?.join('、') ||
          this.text('无声明', 'None declared'),
      ],
    ]);
  }
  private errorMessage(error: unknown) {
    return error instanceof PluginPaginationError
      ? this.text(
          '无法读取完整插件目录，请刷新后重试。',
          'The full plugin catalog could not be loaded. Refresh and try again.',
        )
      : resourceErrorMessage(error, !this.zh);
  }

  private marketSelect(
    key: keyof SkillPanel['filters'],
    label: string,
    options: [string, string][],
  ) {
    const field = element('label', label);
    const select = element('select');
    select.dataset.marketFilter = key;
    select.disabled = this.pending;
    for (const [value, name] of options) {
      const option = element('option', name);
      option.value = value;
      select.append(option);
    }
    select.value = this.filters[key];
    select.onchange = () => {
      Object.assign(this.filters, { [key]: select.value });
      this.render();
      this.root
        .querySelector<HTMLSelectElement>(`[data-market-filter="${key}"]`)
        ?.focus();
    };
    field.append(select);
    return field;
  }

  private marketFilters() {
    const locale = this.zh ? 'zh-CN' : 'en';
    const options = marketOptions(this.packages, locale);
    const filters = element('div', undefined, 'resource-market-filters');
    const all = this.text('全部', 'All');
    const unknown = this.text('未知', 'Unknown');
    filters.append(
      this.marketSelect('author', this.text('作者', 'Author'), [
        ['', all],
        ...options.authors.map(([id, name]): [string, string] => [
          id,
          name ?? `${unknown} (${id})`,
        ]),
      ]),
      this.marketSelect('department', this.text('部门', 'Department'), [
        ['', all],
        ...options.departments.map((value): [string, string] => [value, value]),
      ]),
      this.marketSelect('tag', this.text('标签', 'Tag'), [
        ['', all],
        ...options.tags.map((value): [string, string] => [value, value]),
      ]),
      this.marketSelect('sort', this.text('排序', 'Sort by'), [
        ['', this.text('目录顺序', 'Catalog order')],
        ['publishedAt', this.text('发布时间', 'Publication date')],
        ['installationCount', this.text('安装数', 'Installations')],
        ['starCount', this.text('点赞数', 'Likes')],
      ]),
    );
    return filters;
  }

  private marketFacts(market: PluginMarket) {
    const unknown = this.text('未知', 'Unknown');
    const timestamp = marketDate(market);
    return fieldList([
      [this.text('作者', 'Author'), marketAuthor(market) ?? unknown],
      [this.text('部门', 'Department'), marketDepartment(market) ?? unknown],
      [
        this.text('发布时间', 'Published'),
        timestamp === undefined
          ? unknown
          : new Intl.DateTimeFormat(this.zh ? 'zh-CN' : 'en', {
              dateStyle: 'medium',
              timeStyle: 'short',
            }).format(timestamp),
      ],
      [this.text('版本', 'Version'), market.version],
      [this.text('DSH 兼容范围', 'DSH compatibility'), market.dshVersionRange],
      [this.text('标签', 'Tags'), market.tags.join(' · ') || '—'],
      [this.text('安装数', 'Installations'), String(market.installationCount)],
      [this.text('点赞数', 'Likes'), String(market.starCount)],
    ]);
  }

  private starButton(pack: Package) {
    const market = pack.market!;
    const button = this.button(
      market.starred
        ? this.text('取消点赞', 'Unlike')
        : this.text('点赞', 'Like'),
      () => {
        if (this.desktop) {
          void this.desktop.star(pack).catch(() => undefined);
          return;
        }
        void this.run(async () => {
          const epoch = this.epoch;
          const result = validateStar(
            await this.request(
              'packages/' + encodeURIComponent(pack.id) + '/star',
              { starred: !market.starred },
            ),
          );
          if (epoch !== this.epoch) return;
          const updated = { ...pack, market: { ...market, ...result } };
          this.packages = this.packages.map((item) =>
            item.id === pack.id ? updated : item,
          );
          if (this.selected?.id === pack.id) this.selected = updated;
        });
      },
    );
    button.setAttribute('aria-pressed', String(market.starred));
    return button;
  }

  render() {
    this.root.replaceChildren();
    this.root.setAttribute('aria-busy', String(this.pending));
    const toolbar = element('div', undefined, 'toolbar');
    const title = element('div', undefined, 'market-title');
    title.append(
      element(
        'h2',
        this.text(
          this.mine ? '我的共享' + this.label : this.label + '市场',
          this.mine
            ? 'My shared ' + this.label.toLowerCase() + 's'
            : this.label + ' market',
        ),
      ),
      element(
        'p',
        this.text(
          '为新会话添加经过签名验证的能力。',
          'Add signed capabilities to new sessions.',
        ),
        'muted',
      ),
    );
    toolbar.append(
      title,
      this.button(this.text('刷新', 'Refresh'), () => {
        void this.load();
      }),
    );
    this.root.append(toolbar);
    if (!this.mine && !this.selected) {
      const search = element('input');
      search.type = 'search';
      search.value = this.search;
      const placeholder =
        this.kind === 'plugin'
          ? this.text(
              '搜索名称、简介或标签',
              'Search names, descriptions or tags',
            )
          : this.text('搜索资源', 'Search resources');
      search.setAttribute('aria-label', placeholder);
      search.placeholder = placeholder;
      const searchChanged = () => {
        if (this.search === search.value) return;
        this.search = search.value;
        const { selectionStart, selectionEnd } = search;
        this.render();
        const updated =
          this.root.querySelector<HTMLInputElement>('input[type=search]');
        updated?.focus();
        updated?.setSelectionRange(selectionStart, selectionEnd);
      };
      search.oninput = (event) => {
        if (!(event as InputEvent).isComposing) searchChanged();
      };
      search.addEventListener('compositionend', searchChanged);
      toolbar.append(search);
      if (this.kind === 'plugin' && this.packages.some((pack) => pack.market))
        this.root.append(this.marketFilters());
    }
    if (this.policyCacheUnavailable)
      this.root.append(
        element(
          'p',
          this.text(
            '本机无法保存个人插件策略。请检查客户端数据目录权限后刷新。',
            'Could not save the personal plugin policy. Check permissions on the client data directory, then refresh.',
          ),
          'notice',
        ),
      );
    if (
      !this.allowPersonalPlugins ||
      this.blockedPersonalPlugins.length ||
      this.personalPluginPolicyPendingRestart
    ) {
      const text = this.personalPluginPolicyPendingRestart
        ? this.text(
            '个人插件已设置为停用，请重启 DSH 以完成组织策略限制。',
            'Personal plugins are set to disabled. Restart DSH to finish applying organization policy.',
          )
        : this.blockedPersonalPlugins.length
          ? this.text(
              '以下个人插件已按组织策略停用：',
              'Personal plugins disabled by organization policy: ',
            )
          : this.policyCacheUnavailable
            ? this.text(
                '当前保留个人插件安装限制。',
                'Personal plugin installation remains restricted.',
              )
            : this.text(
                '组织策略不允许自行安装个人插件。',
                'Organization policy does not allow personal plugin installation.',
              );
      const notice = element(
        'p',
        text +
          (this.blockedPersonalPlugins.length
            ? ' ' + this.blockedPersonalPlugins.join('、')
            : ''),
        'notice',
      );
      notice.setAttribute('role', 'status');
      this.root.append(notice);
    }
    const failed = this.failed || this.desktopError !== undefined;
    const message = this.desktopError
      ? this.errorMessage(this.desktopError)
      : this.notice;
    if (message) {
      const notice = element('p', message, 'notice');
      notice.setAttribute('role', failed ? 'alert' : 'status');
      this.root.append(notice);
    }
    if (this.sharedAvailable === false && !this.pending)
      this.root.append(
        element(
          'p',
          this.text(
            this.sharedReason === 'platform_offline'
              ? '平台离线，已应用最近一次已认证的个人插件策略。共享资源状态待联网确认。'
              : this.sharedReason === 'local_storage_path'
                ? '共享路径不可用：请使用本机文件系统的真实绝对路径；不支持短文件名、目录联接或符号链接。'
                : '当前宿主尚无可信 guardian，共享安装暂不可用。请通过 Auriga 桌面启动器打开 DSH 后刷新。',
            this.sharedReason === 'platform_offline'
              ? 'The platform is offline. The last authenticated personal plugin policy is applied; shared resource status awaits an online refresh.'
              : this.sharedReason === 'local_storage_path'
                ? 'Shared storage requires a real absolute local filesystem path; short names, junctions and symbolic links are not supported.'
                : 'Shared installation is unavailable until a trusted guardian is present. Open DSH through the Auriga desktop launcher, then refresh.',
          ),
          'notice',
        ),
      );
    if (this.hostBusy)
      this.root.append(
        element(
          'p',
          resourceErrorMessage(
            new ResourceDiagnosticError('host_busy'),
            !this.zh,
          ),
          'notice',
        ),
      );
    else if (this.pendingRestart)
      this.root.append(
        element(
          'p',
          this.text(
            '共享目录正在被占用，请关闭占用后刷新；仍无法完成时请重启 DSH。',
            'Shared directory is occupied. Release it and refresh, or restart DSH.',
          ),
          'notice',
        ),
      );
    if (this.pending) {
      const loading = element(
        'p',
        this.desktop?.snapshot && this.desktop.loading
          ? this.text('正在刷新资源…', 'Refreshing resources…')
          : this.text('正在读取资源…', 'Loading resources…'),
        'muted',
      );
      loading.setAttribute('role', 'status');
      this.root.append(loading);
    }
    if (this.selected && !this.mine) {
      const pack = this.selected;
      this.root.append(
        this.button(this.text('返回列表', 'Back'), () => {
          this.selected = undefined;
          this.render();
        }),
        resourceHeading(pack.name, this.kind, this.label),
      );
      if (pack.market)
        this.root.append(
          element('p', pack.market.description, 'resource-description'),
          this.marketFacts(pack.market),
          this.starButton(pack),
        );
      const identity = element('details', undefined, 'resource-origin');
      identity.append(
        element(
          'summary',
          this.text('来源与资源标识', 'Source and resource identity'),
        ),
        fieldList([
          [
            this.text('资源所有者 ID', 'Resource owner ID'),
            pack.ownerId ?? '—',
          ],
          [this.text('资源 ID', 'Resource ID'), pack.id],
          ...(pack.market
            ? [
                [this.text('作者 ID', 'Author ID'), pack.market.authorId] as [
                  string,
                  string,
                ],
              ]
            : []),
        ]),
      );
      this.root.append(
        identity,
        element(
          'h3',
          this.text('可用版本', 'Available versions'),
          'section-title',
        ),
      );
      for (const version of this.versions) {
        const card = element('article', undefined, 'resource-version');
        const header = element('header', undefined, 'resource-version-header');
        const label = element('div');
        label.append(
          element('h4', version.version),
          this.badge(this.status(version.state)),
        );
        const installed = this.installation(pack.id);
        const action = this.button(
          this.text('安装此精确版本', 'Install this exact version'),
          () => this.action(pack.id, version.id, 'install'),
          version.state !== 'published' || !this.sharedAvailable,
        );
        action.className = 'primary';
        header.append(label, action);
        card.append(
          header,
          element('p', version.manifest.description, 'resource-description'),
        );
        if (installed?.resource.identity.versionId === version.id)
          card.append(this.badge(this.status(installed.state)));
        card.append(
          fieldList([
            [
              this.text('作者', 'Author'),
              version.manifest.author?.displayName ??
                this.text('未提供', 'Not provided'),
            ],
            [this.text('许可证', 'License'), version.manifest.license ?? '—'],
            [
              this.text('签名', 'Signature'),
              version.resourceEnvelope?.signature?.kid ??
                this.text('安装时核验签名', 'Verified during installation'),
            ],
          ]),
        );
        const details = element('details', undefined, 'resource-origin');
        details.append(
          element(
            'summary',
            this.text('权限与内容摘要', 'Permissions and content digest'),
          ),
          this.permissions(version),
          fieldList([
            [this.text('内容摘要', 'Content digest'), version.digest ?? '—'],
          ]),
        );
        card.append(details);
        if (version.manifest.kind === 'plugin')
          card.append(
            fieldList([
              [
                this.text('npm 包名', 'npm package'),
                version.manifest.plugin?.packageName ?? '—',
              ],
              [
                this.text('DSH 兼容范围', 'DSH compatibility'),
                version.manifest.plugin?.dshVersionRange ?? '—',
              ],
            ]),
          );
        this.root.append(card);
      }
      if (!this.versions.length && !this.pending)
        this.root.append(
          element(
            'p',
            this.text(
              '当前没有可见版本，请刷新或联系管理员。',
              'No visible versions. Refresh or contact an administrator.',
            ),
            'notice',
          ),
        );
      if (this.versionCursor)
        this.root.append(
          this.button(this.text('更多版本', 'More versions'), () =>
            this.moreVersions(),
          ),
        );
    } else {
      const cards = element('div', undefined, 'cards');
      this.root.append(cards);
      if (this.mine) {
        for (const row of this.rows) {
          const id = row.resource.identity;
          const card = element('article', undefined, 'card resource-card');
          card.append(
            resourceHeading(
              row.resource.envelope.statement.name,
              this.kind,
              this.label,
            ),
            element('p', id.version, 'resource-version-label'),
            this.badge(this.status(row.state)),
          );
          const actions = element('div', undefined, 'resource-actions');
          for (const [action, zh, en] of [
            ['enable', '启用', 'Enable'],
            ['disable', '关闭', 'Disable'],
            ['remove', '卸载', 'Remove'],
          ])
            actions.append(
              this.button(
                this.text(zh, en),
                () => this.action(id.packageId, id.versionId, action),
                !this.sharedAvailable ||
                  (row.resource.installationMode === 'required' &&
                    ['disable', 'remove'].includes(action)),
              ),
            );
          if (this.kind === 'skill')
            actions.append(
              this.button(
                this.text('在新会话检查来源', 'Check source in a new session'),
                () => this.observe(row),
                (row.resource.desiredState ??
                  (row.resource.enabled ? 'ENABLED' : 'DISABLED')) !==
                  'ENABLED',
              ),
            );
          card.append(actions);
          if (this.source.has(id.packageId))
            card.append(element('p', this.source.get(id.packageId), 'muted'));
          cards.append(card);
        }
      } else {
        const displayed =
          this.kind === 'plugin'
            ? filterPlugins(
                this.packages,
                { ...this.filters, search: this.search },
                (pack) => this.summaries.get(pack.id)?.manifest.description,
              )
            : this.packages.filter((pack) =>
                pack.name.toLowerCase().includes(this.search.toLowerCase()),
              );
        if (this.packages.length && !displayed.length && !this.pending) {
          const notice = element(
            'p',
            this.text('没有符合条件的资源。', 'No matching resources.'),
            'notice',
          );
          notice.setAttribute('role', 'status');
          cards.append(notice);
        }
        for (const pack of displayed) {
          const version = this.summaries.get(pack.id),
            row = this.installation(pack.id);
          const card = element('article', undefined, 'card resource-card');
          card.append(
            resourceHeading(pack.name, this.kind, this.label),
            element(
              'p',
              pack.market?.description ??
                version?.manifest.description ??
                this.text(
                  '查看版本、权限与来源，选择适合的技能。',
                  'Review versions, permissions and source to choose a skill.',
                ),
              'resource-description',
            ),
          );
          const metadata = element('div', undefined, 'resource-meta');
          metadata.append(
            this.badge(
              this.text(
                { required: '必装', default: '默认安装', optional: '自选' }[
                  row?.resource.installationMode ?? 'optional'
                ],
                row?.resource.installationMode ?? 'optional',
              ),
            ),
            this.badge(
              row
                ? this.status(row.state)
                : this.sharedAvailable
                  ? this.text('未安装', 'Not installed')
                  : this.sharedAvailable === false
                    ? this.text('安装待配置', 'Setup required')
                    : this.text(
                        '安装状态未知',
                        'Installation status unavailable',
                      ),
            ),
          );
          if (version)
            metadata.append(
              element('span', version.version, 'resource-version-label'),
            );
          card.append(metadata);
          if (pack.market) card.append(this.marketFacts(pack.market));
          else if (version?.manifest.author?.displayName)
            card.append(
              element('p', version.manifest.author.displayName, 'muted'),
            );
          const actions = element('div', undefined, 'resource-actions');
          if (version) {
            const install = this.button(
              this.text('安装', 'Install') + ' ' + version.version,
              () => this.action(pack.id, version.id, 'install'),
              !this.sharedAvailable || version.state !== 'published',
            );
            install.className = 'primary';
            actions.append(install);
          }
          actions.append(
            this.button(
              this.text('选择版本／更新', 'Select version / update'),
              () => {
                void this.open(pack);
              },
            ),
          );
          if (pack.market) actions.append(this.starButton(pack));
          card.append(actions);
          cards.append(card);
        }
      }
      if (
        !(this.mine ? this.rows : this.packages).length &&
        !this.pending &&
        !message
      )
        this.root.append(
          element(
            'p',
            this.mine
              ? this.text(
                  '暂无共享' + this.label + '，请前往资源市场选择。',
                  'No shared ' +
                    this.label.toLowerCase() +
                    's. Choose a resource from the market.',
                )
              : this.text(
                  '市场暂无共享' +
                    this.label +
                    '，请联系管理员前往管理台发布后刷新。',
                  'No shared ' +
                    this.label.toLowerCase() +
                    's in the market. Ask an administrator to publish from the admin console, then refresh.',
                ),
            'notice',
          ),
        );
      if (!this.mine && this.cursor)
        this.root.append(
          this.button(this.text('加载更多', 'Load more'), () => {
            void this.load(true);
          }),
        );
    }
    if (this.confirmation) {
      const pending = this.confirmation;
      this.root.append(
        this.button(
          this.text('仍然安装此精确版本', 'Install this exact version anyway'),
          () =>
            this.action(
              pending.packageId,
              pending.versionId,
              pending.action,
              pending.token,
              pending.key,
            ),
        ),
      );
    }
  }
}
