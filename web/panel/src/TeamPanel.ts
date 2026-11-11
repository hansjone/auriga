import { messages } from './messages';
import style from './panel.css?inline';
import {
  readMembers,
  applyMember,
  memberActions,
  memberFailure,
  memberState,
  type MemberItem,
  type MemberIdentity,
  type MemberInput,
} from './members';
import {
  canReadResources,
  createResourceReader,
  ResourceError,
  readInstalledResources,
  type InstalledResource,
  type Failure,
  type PanelContext,
  type ResourcePackage,
  type ResourceVersion,
} from './resources';

export class AurigaTeamPanel extends HTMLElement {
  private value?: PanelContext;
  private tab: 'market' | 'mine' = 'market';
  private search = '';
  private packages: ResourcePackage[] = [];
  private nextCursor?: string;
  private loaded = false;
  private installed?: InstalledResource[];
  private selectedId?: string;
  private detail?: ResourcePackage;
  private versions: ResourceVersion[] = [];
  private versionCursor?: string;
  private busy = false;
  private failure?: Failure;
  private epoch = 0;
  private controller?: AbortController;
  private retryAction?: () => void;
  private memberRows?: MemberItem[];
  private memberIdentity?: MemberIdentity;
  private memberOffline = false;
  private memberError?: Failure;
  private memberRead = 0;
  private memberReadAbort?: AbortController;
  private memberAbort?: AbortController;
  private memberConfirm?: {
    packageId: string;
    versionId: string;
    action: MemberInput['action'];
    label: string;
  };
  private memberOperation?: {
    input: MemberInput;
    pending: boolean;
    row?: MemberItem;
    failure?: Failure;
  };
  private root: HTMLElement;
  constructor() {
    super();
    const shadow = this.attachShadow({ mode: 'open' });
    const css = document.createElement('style');
    css.textContent = style;
    this.root = document.createElement('aside');
    this.root.className = 'resources';
    shadow.append(css, this.root);
  }
  get context() {
    return this.value;
  }
  set context(value: PanelContext | undefined) {
    const previous = this.value;
    this.value = value;
    const changed =
      !previous ||
      previous.mode !== value?.mode ||
      previous.protocol !== value?.protocol ||
      previous.fetch !== value?.fetch ||
      previous.listInstalled !== value?.listInstalled ||
      previous.memberResources !== value?.memberResources ||
      previous.resourceDistributionAvailable !==
        value?.resourceDistributionAvailable ||
      JSON.stringify(previous.resourceIdentity) !==
        JSON.stringify(value?.resourceIdentity) ||
      previous.apiBase !== value?.apiBase;
    if (changed) {
      this.memberRead++;
      this.memberReadAbort?.abort();
      this.memberAbort?.abort();
      this.memberRows = undefined;
      this.memberIdentity = undefined;
      this.memberConfirm = undefined;
      this.memberOperation = undefined;
      this.memberError = undefined;
      this.cancel();
      this.clearData();
      this.installed = undefined;
      this.selectedId = undefined;
      this.failure = undefined;
      this.retryAction = undefined;
    }
    if (this.isConnected) {
      if (changed && value?.memberResources) void this.loadMembers();
      if (changed && this.tab === 'market') this.loadPackages();
      if (changed && this.tab === 'mine') this.loadInstalled();
      this.render();
    }
  }
  private get text() {
    return messages[this.context?.locale?.startsWith('en') ? 'en' : 'zh-CN'];
  }
  private get readable() {
    return canReadResources(this.context);
  }
  connectedCallback() {
    this.render();
    if (this.tab === 'market' && !this.loaded) this.loadPackages();
    if (this.tab === 'mine') this.loadInstalled();
    if (this.context?.memberResources) void this.loadMembers();
  }
  disconnectedCallback() {
    this.cancel();
    this.memberRead++;
    this.memberReadAbort?.abort();
    this.memberAbort?.abort();
  }
  cancel() {
    this.epoch++;
    this.controller?.abort();
    this.busy = false;
  }

  clearData() {
    this.packages = [];
    this.nextCursor = undefined;
    this.loaded = false;
    this.detail = undefined;
    this.versions = [];
    this.versionCursor = undefined;
  }

  async run<T>(
    operation: (signal: AbortSignal) => Promise<T>,
    apply: (result: T) => void,
    retry: () => void,
    local = false,
  ) {
    this.cancel();
    if (!local && !this.readable) return;
    const request = this.epoch;
    const active = new AbortController();
    this.controller = active;
    this.busy = true;
    this.failure = undefined;
    this.retryAction = retry;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let onAbort: (() => void) | undefined;
    this.render();
    try {
      const result = await Promise.race([
        operation(active.signal),
        new Promise<never>((_, reject) => {
          onAbort = () => reject(new ResourceError('unavailable'));
          active.signal.addEventListener('abort', onAbort, { once: true });
          timer = setTimeout(() => {
            active.abort();
          }, 15_000);
        }),
      ]);
      if (request === this.epoch) apply(result);
    } catch (error) {
      if (request !== this.epoch) return;
      this.failure =
        error instanceof ResourceError ? error.reason : 'unavailable';
      // A failed live read must not leave a cached market looking available.
      this.clearData();
      if (this.failure === 'auth')
        this?.dispatchEvent(
          new CustomEvent('auriga-auth-required', {
            bubbles: true,
            composed: true,
          }),
        );
    } finally {
      clearTimeout(timer);
      if (onAbort) active.signal.removeEventListener('abort', onAbort);
      active.abort();
      if (request === this.epoch) {
        this.busy = false;
        if (this.isConnected) this.render();
      }
    }
  }

  loadPackages(more = false) {
    if (!this.readable) return;
    const reader = createResourceReader(this.context!);
    const cursor = more ? this.nextCursor : undefined;
    if (more && !cursor) return;
    if (!more) {
      this.packages = [];
      this.nextCursor = undefined;
      this.loaded = false;
    }
    void this.run(
      (signal) => reader.list(signal, cursor),
      (page) => {
        // Repeated cursors must never turn into an unbounded pagination loop.
        if (cursor && page.nextCursor === cursor)
          throw new ResourceError('invalid');
        this.packages = [
          ...new Map(
            [...this.packages, ...page.items]
              .filter((item) => item.enabled && item.defaultVersionId !== null)
              .map((item) => [item.id, item]),
          ).values(),
        ];
        this.nextCursor = page.nextCursor;
        this.loaded = true;
      },
      () => this.loadPackages(more && !!this.nextCursor),
    );
  }

  openDetail(packageId: string) {
    if (!this.readable) return;
    this.selectedId = packageId;
    this.detail = undefined;
    this.versions = [];
    this.versionCursor = undefined;
    const reader = createResourceReader(this.context!);
    void this.run(
      (signal) =>
        Promise.all([
          reader.detail(packageId, signal),
          reader.versions(packageId, signal),
        ]),
      ([item, page]) => {
        this.detail = item;
        this.versions = page.items.filter((item) => item.state === 'published');
        this.versionCursor = page.nextCursor;
      },
      () => this.openDetail(packageId),
    );
  }

  moreVersions() {
    if (!this.readable || !this.selectedId || !this.versionCursor) return;
    const packageId = this.selectedId;
    const cursor = this.versionCursor;
    const reader = createResourceReader(this.context!);
    void this.run(
      (signal) => reader.versions(packageId, signal, cursor),
      (page) => {
        if (page.nextCursor === cursor) throw new ResourceError('invalid');
        this.versions = [
          ...new Map(
            [...this.versions, ...page.items]
              .filter((item) => item.state === 'published')
              .map((item) => [item.id, item]),
          ).values(),
        ];
        this.versionCursor = page.nextCursor;
      },
      () => (this.detail ? this.moreVersions() : this.openDetail(packageId)),
    );
  }

  showTab(value: 'market' | 'mine') {
    this.cancel();
    this.tab = value;
    this.selectedId = undefined;
    this.detail = undefined;
    this.versions = [];
    this.failure = undefined;
    this.retryAction = undefined;
    this.memberConfirm = undefined;
    if (value === 'market' && !this.loaded) this.loadPackages();
    if (value === 'mine') this.loadInstalled();
    this.render();
  }

  private loadInstalled() {
    if (this.context?.memberResources) {
      void this.loadMembers();
      return;
    }
    this.installed = undefined;
    void this.run(
      () => readInstalledResources(this.context),
      (items) => {
        this.installed = items;
      },
      () => this.loadInstalled(),
      true,
    );
  }
  private async loadMembers() {
    const context = this.context;
    if (!context?.memberResources) return;
    this.memberReadAbort?.abort();
    const signal = new AbortController();
    this.memberReadAbort = signal;
    const request = ++this.memberRead;
    try {
      const data = await readMembers(context, signal.signal);
      if (request !== this.memberRead) return;
      this.memberRows = data.items;
      this.memberIdentity = data.identity;
      this.memberOffline = data.offline;
      this.memberError = undefined;
    } catch (error) {
      if (request !== this.memberRead) return;
      this.memberError = memberFailure(error);
      this.memberRows = undefined;
      if (this.memberError === 'auth') {
        this.cancel();
        this.clearData();
        this.dispatchEvent(
          new CustomEvent('auriga-auth-required', {
            bubbles: true,
            composed: true,
          }),
        );
      }
    } finally {
      if (request === this.memberRead && this.isConnected) this.render();
    }
  }
  private memberBlocked() {
    const op = this.memberOperation;
    return (
      !!op && (op.pending || !op.row || memberState(op.row) === 'applying')
    );
  }
  private async runMember(retry = false) {
    const context = this.context,
      identity = this.memberIdentity;
    if (!context?.memberResources || !identity || this.memberOperation?.pending)
      return;
    if (!retry) {
      if (!this.memberConfirm || this.memberBlocked()) return;
      const bytes = new Uint8Array(16);
      globalThis.crypto.getRandomValues(bytes);
      const key = [...bytes]
        .map((b) => b.toString(16).padStart(2, '0'))
        .join('');
      const { packageId, versionId, action } = this.memberConfirm;
      this.memberOperation = {
        input: { packageId, versionId, action, key },
        pending: false,
      };
    }
    const own = this.memberOperation;
    if (!own) return;
    this.memberConfirm = undefined;
    own.pending = true;
    own.failure = undefined;
    const controller = new AbortController();
    this.memberAbort = controller;
    let timer: ReturnType<typeof setTimeout> | undefined;
    this.render();
    try {
      const row = await Promise.race([
        applyMember(context, own.input, identity, controller.signal),
        new Promise<never>((_, reject) => {
          controller.signal.addEventListener(
            'abort',
            () => reject(new ResourceError('unavailable')),
            { once: true },
          );
          timer = setTimeout(() => controller.abort(), 30000);
        }),
      ]);
      if (own !== this.memberOperation) return;
      own.row = row;
      await this.loadMembers();
    } catch (error) {
      if (own !== this.memberOperation) return;
      own.failure = memberFailure(error);
      if (own.failure === 'auth')
        this.dispatchEvent(
          new CustomEvent('auriga-auth-required', {
            bubbles: true,
            composed: true,
          }),
        );
    } finally {
      clearTimeout(timer);
      controller.abort();
      own.pending = false;
      if (own === this.memberOperation && this.isConnected) this.render();
    }
  }
  private memberNotice(section: HTMLElement) {
    const text = this.text;
    if (this.memberConfirm) {
      const confirm = this.element('div', undefined, 'notice');
      confirm.setAttribute('role', 'group');
      confirm.append(
        this.element('p', text.memberConfirm + ' ' + this.memberConfirm.label),
        this.button(text.confirm, () => void this.runMember()),
        this.button(text.cancel, () => {
          this.memberConfirm = undefined;
          this.render();
        }),
      );
      section.append(confirm);
    }
    const op = this.memberOperation;
    if (op) {
      const notice = this.element('div', undefined, 'notice');
      notice.setAttribute('role', op.failure ? 'alert' : 'status');
      notice.append(
        this.element(
          'p',
          op.failure
            ? text[op.failure]
            : op.pending
              ? text.memberWorking
              : op.row?.restartRequired
                ? text.memberRestart
                : op.row
                  ? text[`member_${memberState(op.row)}`]
                  : text.distribution_unknown,
        ),
      );
      if (op.failure && op.failure === 'unavailable')
        notice.append(this.element('p', text.distribution_unknown));
      if (!op.pending)
        notice.append(this.button(text.retry, () => void this.runMember(true)));
      section.append(notice);
    }
  }
  private memberCards(section: HTMLElement) {
    const text = this.text;
    section.append(this.button(text.refresh, () => void this.loadMembers()));
    if (this.memberError) {
      const error = this.element('p', text[this.memberError], 'notice');
      error.setAttribute('role', 'alert');
      section.append(error);
    }
    if (!this.memberRows && !this.memberError)
      section.append(this.element('p', text.loading));
    if (this.memberOffline)
      section.append(this.element('p', text.memberOffline, 'notice'));
    if (!this.memberRows?.length && !this.memberError)
      section.append(this.element('p', text.localEmpty));
    const cards = this.element('div', undefined, 'cards');
    for (const item of this.memberRows ?? []) {
      const card = this.element('article', undefined, 'card');
      card.append(
        this.element('h3', item.name),
        this.element(
          'p',
          text.memberLocalVersion + ' · ' + (item.localVersion ?? text.unknown),
        ),
        this.element('p', text.versions + ' · ' + item.version),
        this.element('p', text.level + ' · ' + item.level.toUpperCase()),
        this.element(
          'p',
          text.memberReport + ' · ' + text[`member_${memberState(item)}`],
        ),
        this.element(
          'p',
          text.localEnabled +
            ' · ' +
            (item.localEnabled ? text.enabled : text.disabled),
        ),
      );
      if (item.level === 'l4') card.append(this.element('p', text.unreviewed));
      if (item.restartRequired)
        card.append(this.element('p', text.memberRestart, 'notice'));
      const actions: MemberInput['action'][] =
        item.level === 'l1'
          ? ['enable']
          : item.level === 'l2'
            ? ['enable', 'disable']
            : ['enable', 'disable', 'remove'];
      for (const action of actions)
        card.append(
          this.button(
            text[`action_${action}`],
            () => {
              this.memberConfirm = {
                packageId: item.packageId,
                versionId: item.versionId,
                action,
                label: item.name + ' ' + item.version,
              };
              this.render();
            },
            this.memberBlocked() ||
              item.state === 'applying' ||
              (this.memberOffline && action === 'enable'),
          ),
        );
      cards.append(card);
    }
    section.append(cards);
  }

  private element<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    text?: string,
    className?: string,
  ): HTMLElementTagNameMap[K] {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (className) el.className = className;
    return el;
  }
  private button(text: string, action: () => void, disabled = false) {
    const button = this.element('button', text);
    button.type = 'button';
    button.disabled = disabled;
    button.onclick = action;
    return button;
  }
  private render() {
    const text = this.text;
    const active = this.shadowRoot?.activeElement as HTMLInputElement | null;
    const focusSearch = active?.type === 'search';
    const caret = active?.selectionStart;
    if (this.context?.theme) this.root.dataset.theme = this.context.theme;
    else delete this.root.dataset.theme;
    this.root.setAttribute('aria-label', text.panel);
    this.root.replaceChildren(this.element('span', text.panel, 'sr-only'));
    const tabs = this.element('nav');
    tabs.setAttribute('aria-label', text.panel);
    tabs.setAttribute('role', 'tablist');
    for (const value of ['market', 'mine'] as const) {
      const button = this.button(text[value], () => this.showTab(value));
      button.setAttribute('aria-pressed', String(this.tab === value));
      button.setAttribute('role', 'tab');
      button.setAttribute('aria-selected', String(this.tab === value));
      button.tabIndex = this.tab === value ? 0 : -1;
      button.onkeydown = (event) => {
        if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
          event.preventDefault();
          const next =
            event.key === 'Home'
              ? 'market'
              : event.key === 'End'
                ? 'mine'
                : this.tab === 'market'
                  ? 'mine'
                  : 'market';
          this.showTab(next);
          this.root
            .querySelector<HTMLButtonElement>('[aria-selected="true"]')
            ?.focus();
        }
      };
      tabs.append(button);
    }
    this.root.append(tabs);
    const section = this.element('section');
    this.root.append(section);
    if (this.context?.memberResources) this.memberNotice(section);
    if (this.tab === 'mine') {
      if (this.context?.memberResources) {
        this.memberCards(section);
        return;
      }
      section.setAttribute('aria-busy', String(this.busy));
      section.append(
        this.button(text.refresh, () => this.loadInstalled(), this.busy),
      );
      if (this.busy) section.append(this.element('p', text.loading));
      if (this.failure || (!this.busy && !this.installed)) {
        const notice = this.element('p', text.mineHint, 'notice');
        notice.setAttribute('role', 'alert');
        section.append(notice);
      }
      if (this.installed) {
        section.append(this.element('p', text.inventoryHint, 'muted'));
        if (!this.installed.length)
          section.append(this.element('p', text.localEmpty));
        const cards = this.element('div', undefined, 'cards');
        for (const item of this.installed) {
          if (item.unavailable) {
            const notice = this.element('article', undefined, 'notice error');
            notice.setAttribute('role', 'status');
            notice.append(
              this.element('h3', text.preset + ' · ' + item.name),
              this.element('p', text.presetUnavailable),
            );
            cards.append(notice);
            continue;
          }
          const card = this.element('article', undefined, 'card');
          card.append(
            this.element('h3', item.name),
            this.element(
              'p',
              text.localEnabled +
                ' · ' +
                (item.enabled === 'conditional'
                  ? text.unknown
                  : item.enabled
                    ? text.enabled
                    : text.disabled),
            ),
            this.element(
              'p',
              text.runtimeState +
                ' · ' +
                (item.phase
                  ? text[
                      item.phase as
                        | 'active'
                        | 'pending'
                        | 'loading'
                        | 'failed'
                        | 'unloading'
                    ]
                  : text.unknown),
            ),
            this.element(
              'p',
              text.versions + ' · ' + (item.version ?? text.unknown),
            ),
          );
          if (item.preset)
            card.append(this.element('p', text.preset + ' · ' + item.preset));
          cards.append(card);
        }
        section.append(cards);
      }
      section.append(this.element('p', text.installHint, 'notice'));
      return;
    }
    if (!this.readable) {
      section.className = 'notice';
      section.setAttribute('role', 'status');
      section.textContent =
        this.context?.protocol !== undefined && this.context.protocol !== 1
          ? text.protocol
          : this.context?.mode === 'managed'
            ? text.managed
            : text.disconnected;
      return;
    }
    section.setAttribute('aria-busy', String(this.busy));
    const toolbar = this.element('div', undefined, 'toolbar');
    section.append(toolbar);
    if (!this.selectedId) {
      const input = this.element('input');
      input.type = 'search';
      input.value = this.search;
      input.setAttribute('aria-label', text.search);
      input.placeholder = text.search;
      input.oninput = () => {
        this.search = input.value;
        this.render();
      };
      toolbar.append(
        this.element('span', text.plugins, 'kind'),
        input,
        this.button(text.refresh, () => this.loadPackages(), this.busy),
      );
    } else
      toolbar.append(
        this.button('← ' + text.back, () => this.showTab('market')),
        this.button(
          text.refresh,
          () => this.openDetail(this.selectedId!),
          this.busy,
        ),
      );
    if (this.failure) {
      const notice = this.element('div', undefined, 'notice error');
      notice.setAttribute('role', 'alert');
      notice.append(
        this.element('p', text[this.failure]),
        this.button(text.retry, () => this.retryAction?.(), this.busy),
      );
      section.append(notice);
    }
    if (this.busy) {
      const status = this.element('p', text.loading);
      status.setAttribute('role', 'status');
      section.append(status);
    }
    if (!this.selectedId) {
      if (this.loaded)
        section.append(
          this.element('p', text.loaded + ' ' + this.packages.length, 'muted'),
        );
      const cards = this.element('div', undefined, 'cards');
      section.append(cards);
      const filtered = this.packages.filter((item) =>
        item.name
          .toLocaleLowerCase()
          .includes(this.search.trim().toLocaleLowerCase()),
      );
      for (const item of filtered) {
        const card = this.element('article', undefined, 'card');
        card.append(
          this.element('h3', item.name),
          this.element('p', text.level + ' · ' + item.level.toUpperCase()),
          this.element('p', text.author + ' · ' + item.ownerId, 'muted'),
          this.element(
            'p',
            text.availability +
              ' · ' +
              (item.enabled ? text.enabled : text.disabled),
          ),
          this.element('p', text.installState + ' · ' + text.unknown, 'muted'),
          this.button(text.details, () => this.openDetail(item.id)),
        );
        if (item.level === 'l4')
          card.append(this.element('p', text.unreviewed, 'notice'));
        cards.append(card);
      }
      if (this.loaded && !filtered.length && !this.busy && !this.failure)
        section.append(
          this.element(
            'p',
            this.packages.length ? text.noMatch : text.empty,
            'notice',
          ),
        );
      if (this.nextCursor)
        section.append(
          this.button(text.more, () => this.loadPackages(true), this.busy),
        );
    } else if (this.detail) {
      const item = this.detail,
        detail = this.element('article', undefined, 'detail'),
        list = this.element('dl');
      for (const [label, value] of [
        [text.author, item.ownerId],
        [text.level, item.level.toUpperCase()],
        [text.availability, item.enabled ? text.enabled : text.disabled],
        [text.defaultVersion, item.defaultVersionId ?? text.noDefault],
        [text.installState, text.unknown],
        [text.review, item.level === 'l4' ? text.unreviewed : text.unknown],
        [text.permissions, text.permissionsUnavailable],
      ])
        list.append(this.element('dt', label), this.element('dd', value));
      if (item.level === 'l4') list.append(this.element('dt', text.unreviewed));
      const versions = this.element('ul', undefined, 'versions');
      for (const version of this.versions) {
        const row = this.element('li');
        row.append(
          this.element('strong', version.version),
          this.element('span', text[version.state]),
          this.element('p', text.digest + ' · ' + version.digest, 'muted'),
        );
        if (
          this.context?.memberResources &&
          this.memberIdentity &&
          memberActions(item, this.memberIdentity).includes('install')
        ) {
          row.append(
            this.button(
              text.installOrUpdate,
              () => {
                this.memberConfirm = {
                  packageId: item.id,
                  versionId: version.id,
                  action: 'install',
                  label: item.name + ' ' + version.version,
                };
                this.render();
              },
              this.memberBlocked() || this.memberOffline,
            ),
          );
        }
        versions.append(row);
      }
      detail.append(
        this.element('h2', item.name),
        list,
        this.element(
          'p',
          this.context?.memberResources
            ? text.memberInstallHint
            : this.context?.mode === 'managed'
              ? text.managedDistributionUnavailable
              : text.installHint,
          'notice',
        ),
        this.element('h3', text.versions),
        versions,
      );
      if (!this.versions.length && !this.busy)
        detail.append(this.element('p', text.versionEmpty));
      if (this.versionCursor)
        detail.append(
          this.button(text.more, () => this.moreVersions(), this.busy),
        );
      section.append(detail);
    }
    if (focusSearch) {
      const input = this.root.querySelector('input');
      input?.focus();
      if (caret != null) input?.setSelectionRange(caret, caret);
    }
  }
}
