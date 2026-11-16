import { element, resourceHeading, fieldList } from './market-ui';
import {
  resourceResponseError,
  resourceErrorMessage,
} from './resource-diagnostics';
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
  private busy = false;
  private sharedAvailable = false;
  private summaries = new Map<string, Version>();
  private pendingRestart = false;
  private sharedReason?: string;
  private confirmation?: {
    packageId: string;
    versionId: string;
    action: string;
    key: string;
    token: string;
  };
  private notice = '';
  private source = new Map<string, string>();
  constructor(
    private root: HTMLElement,
    private context: PanelContext,
    private mine: boolean,
  ) {
    void this.load();
  }
  dispose() {
    this.epoch++;
    this.controller?.abort();
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
  private async request(path: string, body?: unknown) {
    if (!this.context.fetch) throw new Error('unavailable');
    const response = await this.context.fetch('catalog/' + path, {
      method: body === undefined ? 'GET' : 'POST',
      cache: 'no-store',
      redirect: 'error',
      signal: this.controller?.signal,
      headers: {
        accept: 'application/json',
        ...(body === undefined
          ? {}
          : { 'content-type': 'application/json', 'x-auriga-catalog': '1' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw await resourceResponseError(response);
    if (!response.headers.get('content-type')?.startsWith('application/json'))
      throw new Error('invalid');
    return response.json();
  }
  private async run(work: () => Promise<void>) {
    if (this.busy) return;
    const epoch = this.epoch;
    this.busy = true;
    this.notice = '';
    this.render();
    try {
      await work();
    } catch (error) {
      if (this.epoch === epoch)
        this.notice = resourceErrorMessage(error, !this.zh);
    } finally {
      if (this.epoch === epoch) {
        this.busy = false;
        this.render();
      }
    }
  }
  private async load(more = false) {
    this.controller?.abort();
    this.controller = new AbortController();
    const epoch = ++this.epoch;
    await this.run(async () => {
      if (this.mine) {
        const value = await this.request('installations');
        if (epoch !== this.epoch) return;
        if (!Array.isArray(value.items) || value.items.length > 10000)
          throw new Error('invalid');
        this.rows = value.items;
        this.sharedAvailable = value.sharedAvailable !== false;
        this.sharedReason = value.sharedUnavailableReason;
        this.pendingRestart = !!value.pendingRestart;
      } else {
        const value = await this.request(
          'packages?limit=50' +
            (more && this.cursor
              ? '&cursor=' + encodeURIComponent(this.cursor)
              : ''),
        );
        if (epoch !== this.epoch) return;
        if (
          !Array.isArray(value.items) ||
          value.items.length > 200 ||
          value.items.some((p: Package) => p.kind !== 'skill')
        )
          throw new Error('invalid');
        this.packages = more ? [...this.packages, ...value.items] : value.items;
        this.cursor = value.nextCursor;
        const installed = await this.request('installations');
        if (epoch !== this.epoch) return;
        this.rows = installed.items;
        this.sharedAvailable = installed.sharedAvailable !== false;
        this.sharedReason = installed.sharedUnavailableReason;
        this.pendingRestart = !!installed.pendingRestart;
        if (!more) this.summaries.clear();
        const displayed = this.packages.filter(
          (pack) => pack.defaultVersionId && !this.summaries.has(pack.id),
        );
        for (let index = 0; index < displayed.length; index += 4) {
          await Promise.allSettled(
            displayed.slice(index, index + 4).map(async (pack) => {
              const version = await this.request(
                'packages/' + pack.id + '/versions/' + pack.defaultVersionId,
              );
              if (
                epoch === this.epoch &&
                version.kind === 'skill' &&
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
                v.kind !== 'skill' || v.packageId !== this.selected!.id,
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
    b.disabled = this.busy || disabled;
    b.onclick = action;
    return b;
  }
  private async open(pack: Package) {
    await this.run(async () => {
      const value = await this.request(
        'packages/' + encodeURIComponent(pack.id) + '/versions?limit=200',
      );
      if (
        !Array.isArray(value.items) ||
        value.items.some(
          (v: Version) => v.kind !== 'skill' || v.packageId !== pack.id,
        )
      )
        throw new Error('invalid');
      this.selected = pack;
      this.versions = value.items;
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
      const result = await this.request('actions', {
        packageId,
        versionId,
        action,
        revision: old?.installationRevision ?? 0,
        key,
        ...(confirmation ? { confirmation } : {}),
      });
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
      if (result.installation) {
        this.rows = this.rows.filter(
          (row) => row.resource.identity.packageId !== packageId,
        );
        this.rows.push(result.installation);
      }
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
      const current = await this.request('installations');
      this.rows = current.items;
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
  render() {
    this.root.replaceChildren();
    this.root.setAttribute('aria-busy', String(this.busy));
    const toolbar = element('div', undefined, 'toolbar');
    const title = element('div', undefined, 'market-title');
    title.append(
      element(
        'h2',
        this.text(
          this.mine ? '我的共享技能' : '技能市场',
          this.mine ? 'My shared skills' : 'Skill market',
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
    if (this.notice) {
      const notice = element('p', this.notice, 'notice');
      notice.setAttribute('role', 'status');
      this.root.append(notice);
    }
    if (!this.sharedAvailable && !this.busy)
      this.root.append(
        element(
          'p',
          this.text(
            this.sharedReason === 'local_storage_path'
              ? '共享路径不可用：请使用本机文件系统的真实绝对路径；不支持短文件名、目录联接或符号链接。'
              : '当前宿主尚无可信 guardian，共享安装暂不可用。请通过 Auriga 桌面启动器打开 DSH 后刷新。',
            this.sharedReason === 'local_storage_path'
              ? 'Shared storage requires a real absolute local filesystem path; short names, junctions and symbolic links are not supported.'
              : 'Shared installation is unavailable until a trusted guardian is present. Open DSH through the Auriga desktop launcher, then refresh.',
          ),
          'notice',
        ),
      );
    if (this.pendingRestart)
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
    if (this.busy) {
      const loading = element(
        'p',
        this.text('正在读取资源…', 'Loading resources…'),
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
        resourceHeading(pack.name, 'skill', this.text('技能', 'Skill')),
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
        this.root.append(card);
      }
      if (!this.versions.length && !this.busy)
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
              'skill',
              this.text('共享技能', 'Shared skill'),
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
                !this.sharedAvailable,
              ),
            );
          actions.append(
            this.button(
              this.text('在新会话检查来源', 'Check source in a new session'),
              () => this.observe(row),
              (row.resource.desiredState ??
                (row.resource.enabled ? 'ENABLED' : 'DISABLED')) !== 'ENABLED',
            ),
          );
          card.append(actions);
          if (this.source.has(id.packageId))
            card.append(element('p', this.source.get(id.packageId), 'muted'));
          cards.append(card);
        }
      } else {
        for (const pack of this.packages) {
          const version = this.summaries.get(pack.id),
            row = this.installation(pack.id);
          const card = element('article', undefined, 'card resource-card');
          card.append(
            resourceHeading(pack.name, 'skill', this.text('技能', 'Skill')),
            element(
              'p',
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
            this.badge('L3'),
            this.badge(
              row
                ? this.status(row.state)
                : this.sharedAvailable
                  ? this.text('未安装', 'Not installed')
                  : this.text('安装待配置', 'Setup required'),
            ),
          );
          if (version)
            metadata.append(
              element('span', version.version, 'resource-version-label'),
            );
          card.append(metadata);
          if (version?.manifest.author?.displayName)
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
          card.append(actions);
          cards.append(card);
        }
      }
      if (
        !(this.mine ? this.rows : this.packages).length &&
        !this.busy &&
        !this.notice
      )
        this.root.append(
          element(
            'p',
            this.mine
              ? this.text(
                  '暂无共享技能，请前往资源市场选择技能；私有技能可在原生设置中管理。',
                  'No shared skills. Choose a skill from the market or manage private skills in native settings.',
                )
              : this.text(
                  '市场暂无共享技能，请联系管理员前往管理台发布后刷新。',
                  'No shared skills in the market. Ask an administrator to publish from the admin console, then refresh.',
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
