import { messages } from './messages';
import style from './panel.css?inline';
import { SkillPanel } from './SkillPanel';
import { DesktopCatalog } from './desktop-catalog';
import { element } from './market-ui';
import { canReadResources, type PanelContext } from './resources';

/** Both resource kinds use the same authenticated catalog and installation view. */
export class AurigaTeamPanel extends HTMLElement {
  private static desktopCatalogs = new WeakMap<
    NonNullable<PanelContext['catalogSession']>,
    DesktopCatalog
  >();
  private static desktopCatalog(context: PanelContext) {
    const scope = context.catalogSession;
    if (context.mode !== 'connect' || !scope || !canReadResources(context))
      return undefined;
    let value = this.desktopCatalogs.get(scope);
    if (!value) {
      value = new DesktopCatalog(context.fetch!, scope);
      this.desktopCatalogs.set(scope, value);
    }
    return value;
  }
  static prefetchCatalog(context: PanelContext) {
    this.desktopCatalog(context)?.prefetch();
  }
  static clearCatalog(scope: NonNullable<PanelContext['catalogSession']>) {
    this.desktopCatalogs.get(scope)?.dispose();
    this.desktopCatalogs.delete(scope);
  }
  private value?: PanelContext;
  private kind: 'plugin' | 'skill' = 'plugin';
  private tab: 'market' | 'mine' = 'market';
  private view?: SkillPanel;
  private viewKey?: string;
  private content = document.createElement('section');
  private root: HTMLElement;
  constructor() {
    super();
    const shadow = this.attachShadow({ mode: 'open' });
    const css = document.createElement('style');
    css.textContent = style;
    this.root = element('aside', undefined, 'resources');
    shadow.append(css, this.root);
  }
  get context() {
    return this.value;
  }
  set context(value: PanelContext | undefined) {
    const previous = this.value;
    const changed =
      !previous ||
      previous.mode !== value?.mode ||
      previous.protocol !== value?.protocol ||
      previous.catalogSession !== value?.catalogSession ||
      (!value?.catalogSession && previous.fetch !== value?.fetch) ||
      JSON.stringify(previous.resourceIdentity) !==
        JSON.stringify(value?.resourceIdentity);
    this.value = value;
    if (changed) {
      this.view?.dispose();
      this.view = undefined;
      this.viewKey = undefined;
    } else this.view?.present(value ?? {});
    if (this.isConnected) this.render();
  }
  connectedCallback() {
    this.render();
  }
  disconnectedCallback() {
    this.view?.dispose();
    this.view = undefined;
  }
  showTab(value: 'market' | 'mine') {
    this.tab = value;
    this.render();
  }
  private render() {
    const text =
      messages[this.value?.locale?.startsWith('en') ? 'en' : 'zh-CN'];
    if (this.value?.theme) this.root.dataset.theme = this.value.theme;
    else delete this.root.dataset.theme;
    this.root.setAttribute('aria-label', text.panel);
    this.root.replaceChildren(element('span', text.panel, 'sr-only'));
    const tabs = element('nav');
    tabs.setAttribute('role', 'tablist');
    tabs.setAttribute('aria-label', text.panel);
    for (const value of ['market', 'mine'] as const) {
      const button = element('button', text[value]);
      button.type = 'button';
      button.setAttribute('role', 'tab');
      button.setAttribute('aria-selected', String(value === this.tab));
      button.setAttribute('aria-pressed', String(value === this.tab));
      button.tabIndex = value === this.tab ? 0 : -1;
      button.onclick = () => this.showTab(value);
      button.onkeydown = (event) => {
        if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
          event.preventDefault();
          this.showTab(
            event.key === 'Home'
              ? 'market'
              : event.key === 'End'
                ? 'mine'
                : this.tab === 'market'
                  ? 'mine'
                  : 'market',
          );
          this.root
            .querySelector<HTMLButtonElement>('[aria-selected="true"]')
            ?.focus();
        }
      };
      tabs.append(button);
    }
    const kinds = element('nav');
    for (const kind of ['plugin', 'skill'] as const) {
      const button = element(
        'button',
        kind === 'plugin'
          ? this.value?.locale?.startsWith('en')
            ? 'Plugins'
            : '插件'
          : this.value?.locale?.startsWith('en')
            ? 'Skills'
            : '技能',
      );
      button.type = 'button';
      button.setAttribute('aria-pressed', String(kind === this.kind));
      button.onclick = () => {
        this.kind = kind;
        this.render();
      };
      kinds.append(button);
    }
    this.root.append(tabs, kinds);
    if (!canReadResources(this.value)) {
      this.view?.dispose();
      this.view = undefined;
      const notice = element(
        'p',
        this.value?.protocol !== undefined && this.value.protocol !== 1
          ? text.protocol
          : this.value?.mode === 'managed'
            ? text.managed
            : text.disconnected,
        'notice',
      );
      notice.setAttribute('role', 'status');
      this.root.append(notice);
      return;
    }
    this.root.append(this.content);
    const key = this.kind + ':' + this.tab;
    if (!this.view || this.viewKey !== key) {
      this.view?.dispose();
      this.viewKey = key;
      this.view = new SkillPanel(
        this.content,
        this.value!,
        this.tab === 'mine',
        this.kind,
        AurigaTeamPanel.desktopCatalog(this.value!),
      );
    }
  }
}
