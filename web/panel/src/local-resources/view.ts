import { localApi, type LocalPlugin, type LocalDetail } from './api';
import { ResourceError, type PanelContext } from '../resources';
import { messages } from '../messages';

/** Independent local inventory; a failed platform read cannot erase local state. */
export class LocalResourcesView {
  private context?: PanelContext;
  private root?: HTMLElement;
  private rows: LocalPlugin[] = [];
  private detail?: LocalDetail;
  private loaded = false;
  private controller?: AbortController;
  private epoch = 0;
  private busy = false;
  private notice = '';
  private search = '';
  private confirm?: () => void;
  private fields = new Map<string, HTMLInputElement>();
  private get text() {
    return messages[this.context?.locale?.startsWith('en') ? 'en' : 'zh-CN'];
  }
  stop() {
    this.epoch++;
    this.controller?.abort();
    this.busy = false;
    this.confirm = undefined;
    this.root = undefined;
    this.loaded = false;
    this.rows = [];
    this.detail = undefined;
    this.notice = '';
  }
  mount(root: HTMLElement, context?: PanelContext) {
    if (
      this.context?.localResources !== context?.localResources ||
      this.context?.mode !== context?.mode ||
      this.context?.protocol !== context?.protocol
    ) {
      this.stop();
      this.rows = [];
      this.detail = undefined;
      this.loaded = false;
      this.notice = '';
    }
    this.context = context;
    this.root = root;
    this.render();
    if (!this.loaded && !this.busy && context?.localResources) void this.load();
  }
  private async run<T>(
    operation: (signal: AbortSignal) => Promise<T>,
    apply: (result: T) => void,
  ) {
    this.controller?.abort();
    const generation = ++this.epoch;
    const controller = new AbortController();
    this.controller = controller;
    this.busy = true;
    this.notice = '';
    this.confirm = undefined;
    this.render();
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      const value = await Promise.race([
        operation(controller.signal),
        new Promise<never>((_, reject) =>
          controller.signal.addEventListener(
            'abort',
            () => reject(new ResourceError('unavailable')),
            { once: true },
          ),
        ),
      ]);
      if (generation === this.epoch) apply(value);
    } catch (error) {
      if (generation !== this.epoch) return;
      this.rows = [];
      this.detail = undefined;
      this.loaded = false;
      this.notice =
        this.text[
          error instanceof ResourceError ? error.reason : 'unavailable'
        ];
      if (error instanceof ResourceError && error.reason === 'auth')
        this.root?.dispatchEvent(
          new CustomEvent('auriga-auth-required', {
            bubbles: true,
            composed: true,
          }),
        );
    } finally {
      clearTimeout(timer);
      controller.abort();
      if (generation === this.epoch) {
        this.busy = false;
        this.render();
      }
    }
  }
  private load() {
    return this.run(
      (signal) => localApi(this.context!).list(signal),
      (result) => {
        this.rows = result.items;
        this.detail = undefined;
        this.loaded = true;
      },
    );
  }
  private open(id: string) {
    return this.run(
      (signal) => localApi(this.context!).detail(id, signal),
      (result) => {
        this.detail = result;
      },
    );
  }
  private change(enabled?: boolean) {
    const item = this.detail!;
    const changes = item.fields.map((field) => {
      const input = this.fields.get(field.key)!;
      return {
        key: field.key,
        value:
          field.type === 'boolean'
            ? input.checked
            : field.type === 'number'
              ? Number(input.value)
              : input.value,
      };
    });
    this.confirm = () => {
      void this.run(
        (signal) =>
          enabled === undefined
            ? localApi(this.context!).configure(item, changes, signal)
            : localApi(this.context!).enable(item, enabled, signal),
        (result) => {
          this.detail = result.item;
          this.notice = this.text[result.application];
          this.rows = this.rows.map((row) =>
            row.id === result.item.plugin.id ? result.item.plugin : row,
          );
        },
      );
    };
    this.notice =
      enabled === undefined ? this.text.confirmConfig : this.text.confirmToggle;
    this.render();
  }
  private el<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    text?: string,
    className?: string,
  ) {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (className) el.className = className;
    return el;
  }
  private button(text: string, action: () => void) {
    const el = this.el('button', text);
    el.type = 'button';
    el.disabled = this.busy;
    el.onclick = action;
    return el;
  }
  private render() {
    if (!this.root) return;
    const t = this.text,
      root = this.root;
    root.replaceChildren();
    root.setAttribute('aria-busy', String(this.busy));
    if (!this.context?.localResources) {
      root.append(this.el('p', t.mineHint, 'notice'));
      return;
    }
    const bar = this.el('div', undefined, 'toolbar');
    bar.append(
      this.el('span', t.localPlugins),
      this.button(t.refresh, () => {
        void (this.detail ? this.open(this.detail.plugin.id) : this.load());
      }),
    );
    if (this.detail)
      bar.append(
        this.button(t.localBack, () => {
          this.detail = undefined;
          this.confirm = undefined;
          this.notice = '';
          this.render();
        }),
      );
    root.append(bar);
    if (this.busy) root.append(this.el('p', t.loading));
    if (this.notice) {
      const notice = this.el('p', this.notice, 'notice');
      notice.setAttribute('role', 'status');
      root.append(notice);
    }
    if (this.confirm) {
      const action = this.confirm;
      root.append(
        this.button(t.confirm, action),
        this.button(t.cancel, () => {
          this.confirm = undefined;
          this.notice = '';
          this.render();
        }),
      );
      return;
    }
    if (!this.detail) {
      const input = this.el('input');
      input.type = 'search';
      input.placeholder = t.search;
      input.setAttribute('aria-label', t.search);
      input.value = this.search;
      input.oninput = () => {
        this.search = input.value;
        const cursor = input.selectionStart;
        this.render();
        const next = root.querySelector<HTMLInputElement>(
          'input[type="search"]',
        );
        next?.focus();
        if (cursor !== null) next?.setSelectionRange(cursor, cursor);
      };
      bar.append(input);
      const cards = this.el('div', undefined, 'cards');
      root.append(cards);
      for (const row of this.rows.filter((r) =>
        r.name.toLowerCase().includes(this.search.toLowerCase()),
      )) {
        const card = this.el('article', undefined, 'card');
        card.append(
          this.el('h3', row.name),
          this.el(
            'p',
            `${row.installation === 'present' ? t.installed : t.installUnknown} · ${row.version ?? t.unknown}`,
          ),
          this.el(
            'p',
            `${row.enabled ? t.localEnabled : t.localDisabled} · ${t[`phase_${row.phase}`]}`,
          ),
          this.button(t.details, () => {
            void this.open(row.id);
          }),
        );
        cards.append(card);
      }
      if (this.loaded && !this.rows.length)
        root.append(this.el('p', t.localEmpty, 'notice'));
      return;
    }
    const detail = this.detail,
      item = detail.plugin;
    root.append(
      this.el('h2', item.name),
      this.el(
        'p',
        `${item.installation === 'present' ? t.installed : t.installUnknown} · ${item.version ?? t.unknown}`,
      ),
      this.el(
        'p',
        `${item.enabled ? t.localEnabled : t.localDisabled} · ${t[`phase_${item.phase}`]}`,
      ),
    );
    if (item.readonly)
      root.append(
        this.el(
          'p',
          item.readonly === 'unverified' ? t.installUnknown : t.localReadonly,
          'notice',
        ),
      );
    if (item.canToggle)
      root.append(
        this.button(t.enablePlugin, () => this.change(true)),
        this.button(t.disablePlugin, () => this.change(false)),
      );
    this.fields.clear();
    root.append(this.el('p', t.configScope, 'notice'));
    if (!detail.fields.length) root.append(this.el('p', t.noConfig, 'notice'));
    for (const field of detail.fields) {
      const label = this.el('label'),
        input = this.el('input');
      input.type =
        field.type === 'boolean'
          ? 'checkbox'
          : field.type === 'number'
            ? 'number'
            : 'text';
      input.disabled = this.busy || !item.canConfigure;
      input.setAttribute('aria-label', field.key);
      if (field.type === 'boolean') input.checked = field.value === true;
      else input.value = String(field.value);
      label.append(this.el('span', field.key), input);
      root.append(label);
      this.fields.set(field.key, input);
    }
    if (item.canConfigure)
      root.append(this.button(t.saveConfig, () => this.change()));
  }
}
