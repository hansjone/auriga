import { messages } from './messages';
import style from './panel.css?inline';
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
      previous.apiBase !== value?.apiBase;
    if (changed) {
      this.cancel();
      this.clearData();
      this.installed = undefined;
      this.selectedId = undefined;
      this.failure = undefined;
      this.retryAction = undefined;
    }
    if (this.isConnected) {
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
  }
  disconnectedCallback() {
    this.cancel();
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
            [...this.packages, ...page.items].map((item) => [item.id, item]),
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
        this.versions = page.items;
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
            [...this.versions, ...page.items].map((item) => [item.id, item]),
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
    if (value === 'market' && !this.loaded) this.loadPackages();
    if (value === 'mine') this.loadInstalled();
    this.render();
  }

  private loadInstalled() {
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
    if (this.tab === 'mine') {
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
            this.element('p', text.versions + ' · ' + text.unknown),
          );
          if (item.preset)
            card.append(this.element('p', text.preset + ' · ' + item.preset));
          for (const action of ['install', 'update', 'uninstall'] as const) {
            const button = this.button(text[action], () => {}, true);
            button.title = text.installHint;
            card.append(button);
          }
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
      ])
        list.append(this.element('dt', label), this.element('dd', value));
      const versions = this.element('ul', undefined, 'versions');
      for (const version of this.versions) {
        const row = this.element('li');
        row.append(
          this.element('strong', version.version),
          this.element('span', text[version.state]),
          this.element('p', text.digest + ' · ' + version.digest, 'muted'),
        );
        versions.append(row);
      }
      detail.append(
        this.element('h2', item.name),
        list,
        this.element('p', text.installHint, 'notice'),
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
