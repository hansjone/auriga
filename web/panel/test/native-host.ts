import { setImmediate } from 'node:timers';
import { registerPanel, AurigaTeamPanel } from '../src/index';

export async function flushPromises() {
  await new Promise<void>((resolve) => setImmediate(resolve));
}
export class NativeNode {
  constructor(readonly element?: Element) {}
  exists() {
    return !!this.element;
  }
  text() {
    return this.element?.textContent ?? '';
  }
  async trigger(type: string) {
    this.element?.dispatchEvent(new Event(type, { bubbles: true }));
    await Promise.resolve();
  }
  async setValue(value: string) {
    (this.element as HTMLInputElement).value = value;
    await this.trigger('input');
  }
}
export class NativeWrapper extends NativeNode {
  declare readonly element: AurigaTeamPanel;
  constructor(panel: AurigaTeamPanel) {
    super(panel);
  }
  override text() {
    return this.element.shadowRoot?.textContent ?? '';
  }
  find(selector: string) {
    return new NativeNode(
      this.element.shadowRoot?.querySelector(selector) ?? undefined,
    );
  }
  get(selector: string) {
    const node = this.find(selector);
    if (!node.exists()) throw new Error('Missing element: ' + selector);
    return node as NativeNode & { element: HTMLInputElement };
  }
  findAll(selector: string) {
    return [...this.element.shadowRoot!.querySelectorAll(selector)].map(
      (el) => new NativeNode(el),
    );
  }
  async setProps(props: { context?: AurigaTeamPanel['context'] }) {
    this.element.context = props.context;
    await Promise.resolve();
  }
  unmount() {
    this.element.remove();
  }
  attributes(name: string) {
    return this.element.shadowRoot!.querySelector('aside')?.getAttribute(name);
  }
}
export function mountPanel(context?: AurigaTeamPanel['context']) {
  registerPanel();
  const panel = document.createElement('auriga-team-panel') as AurigaTeamPanel;
  panel.context = context;
  document.body.append(panel);
  return new NativeWrapper(panel);
}
