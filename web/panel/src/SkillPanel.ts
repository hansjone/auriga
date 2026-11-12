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
  private sharedAvailable = true;
  private pendingRestart = false;
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
    if (!response.ok) throw new Error(String(response.status));
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
        this.notice =
          this.text(
            '操作未完成，请刷新确认；冲突后请重新选择版本。',
            'Operation incomplete. Refresh and reselect after a conflict.',
          ) +
          ' ' +
          (error instanceof Error ? error.message : '');
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
        this.pendingRestart = !!installed.pendingRestart;
      }
    });
  }
  private element(tag: string, text?: string) {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    return el;
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
  private action(packageId: string, versionId: string, action: string) {
    void this.run(async () => {
      const old = this.rows.find(
        (row) => row.resource.identity.packageId === packageId,
      );
      const result = await this.request('actions', {
        packageId,
        versionId,
        action,
        revision: old?.installationRevision ?? 0,
        key: crypto.randomUUID(),
      });
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
  render() {
    this.root.replaceChildren();
    this.root.append(
      this.element(
        'h2',
        this.text(
          this.mine ? '我的共享技能' : '技能市场',
          this.mine ? 'My shared skills' : 'Skill market',
        ),
      ),
    );
    this.root.append(
      this.element(
        'p',
        this.text(
          '共享技能仅在具备可信 guardian 的云端或 Auriga Desktop 生效。私有技能在原生设置中管理。',
          'Shared skills require a trusted cloud or Auriga Desktop guardian. Manage private skills in native settings.',
        ),
      ),
    );
    this.root.append(
      this.button(this.text('刷新', 'Refresh'), () => {
        void this.load();
      }),
    );
    if (this.selected && !this.mine) {
      this.root.append(
        this.button(this.text('返回列表', 'Back'), () => {
          this.selected = undefined;
          this.render();
        }),
        this.element('h3', this.selected.name),
      );
      for (const version of this.versions) {
        const card = this.element('article');
        card.className = 'card';
        card.append(
          this.element('h4', version.version),
          this.element('p', this.status(version.state)),
          this.element('p', version.manifest.description),
        );
        card.append(
          this.button(
            this.text('安装此精确版本', 'Install this exact version'),
            () => this.action(this.selected!.id, version.id, 'install'),
            version.state !== 'published' || !this.sharedAvailable,
          ),
        );
        this.root.append(card);
      }
    } else if (this.mine) {
      for (const row of this.rows) {
        const id = row.resource.identity,
          card = this.element('article');
        card.className = 'card';
        card.append(
          this.element('h3', row.resource.envelope.statement.name),
          this.element('p', id.version + ' · ' + this.status(row.state)),
        );
        for (const [action, zh, en] of [
          ['enable', '启用', 'Enable'],
          ['disable', '关闭', 'Disable'],
          ['remove', '卸载', 'Remove'],
        ])
          card.append(
            this.button(
              this.text(zh, en),
              () => this.action(id.packageId, id.versionId, action),
              !this.sharedAvailable,
            ),
          );
        card.append(
          this.button(
            this.text('在新会话检查来源', 'Check source in a new session'),
            () => this.observe(row),
            (row.resource.desiredState ??
              (row.resource.enabled ? 'ENABLED' : 'DISABLED')) !== 'ENABLED',
          ),
        );
        if (this.source.has(id.packageId))
          card.append(this.element('p', this.source.get(id.packageId)));
        this.root.append(card);
      }
      if (!this.rows.length)
        this.root.append(
          this.element('p', this.text('暂无共享技能', 'No shared skills')),
        );
    } else {
      for (const pack of this.packages) {
        const card = this.element('article');
        card.className = 'card';
        card.append(
          this.element('h3', pack.name),
          this.button(
            this.text('选择版本／更新', 'Select version / update'),
            () => {
              void this.open(pack);
            },
          ),
        );
        this.root.append(card);
      }
      if (this.cursor)
        this.root.append(
          this.button(this.text('加载更多', 'Load more'), () => {
            void this.load(true);
          }),
        );
    }
    if (!this.sharedAvailable)
      this.root.append(
        this.element(
          'p',
          this.text(
            '???????? guardian??????????',
            'Shared installation is unavailable until a trusted guardian is present.',
          ),
        ),
      );
    if (this.pendingRestart)
      this.root.append(
        this.element(
          'p',
          this.text(
            '???????????????????????',
            'Shared files are in use. Close affected sessions and refresh.',
          ),
        ),
      );
    if (this.busy)
      this.root.append(this.element('p', this.text('正在处理…', 'Working…')));
    if (this.notice) {
      const notice = this.element('p', this.notice);
      notice.setAttribute('role', 'status');
      this.root.append(notice);
    }
  }
}
