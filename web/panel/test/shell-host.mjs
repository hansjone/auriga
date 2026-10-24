import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { JSDOM } from 'jsdom';

// Small host React hook/DOM adapter: no bundled React dependency in the plugin.
// This exercises the real ModuleLoader factory, effect lifecycle and native slots.
export function surfaceHost(
  fetchImpl,
  { defined = true, locale = 'zh-CN' } = {},
) {
  const dom = new JSDOM('<html><head></head><body></body></html>', {
    url: 'https://instance.example.com/i/example/',
  });
  const { window } = dom;
  const { document } = window;
  let current;
  const React = {
    createElement(type, props, ...children) {
      return {
        type,
        props: props ?? {},
        children: children
          .flat(Infinity)
          .filter((child) => child !== false && child != null),
      };
    },
    useState(initial) {
      const view = current,
        index = view.index++;
      if (!(index in view.hooks)) view.hooks[index] = initial;
      return [
        view.hooks[index],
        (next) => {
          if (!view.alive) return;
          view.hooks[index] =
            typeof next === 'function' ? next(view.hooks[index]) : next;
          view.queue();
        },
      ];
    },
    useReducer(reducer, initial) {
      const [value, set] = React.useState(initial);
      return [value, () => set(reducer)];
    },
    useRef(initial) {
      return React.useState({ current: initial })[0];
    },
    useEffect(fn, deps) {
      const view = current,
        index = view.index++;
      const previous = view.hooks[index];
      if (
        previous &&
        deps.every((value, i) => Object.is(value, previous.deps[i]))
      )
        return;
      view.effects.push(() => {
        previous?.cleanup?.();
        view.hooks[index] = { deps, cleanup: fn() };
      });
    },
  };
  function patch(parent, oldNode, value) {
    if (typeof value !== 'object') {
      if (oldNode?.nodeType === 3) {
        oldNode.data = String(value);
        return oldNode;
      }
      const node = document.createTextNode(String(value));
      if (oldNode) oldNode.replaceWith(node);
      else parent.append(node);
      return node;
    }
    let node = oldNode;
    if (node?.localName !== value.type) {
      node = document.createElement(value.type);
      if (oldNode) oldNode.replaceWith(node);
      else parent.append(node);
    }
    for (const [key, prop] of Object.entries(value.props)) {
      if (key === 'ref') prop.current = node;
      else if (key === 'style') Object.assign(node.style, prop);
      else if (key.startsWith('on')) node[key.toLowerCase()] = prop;
      else if (key === 'className') node.className = prop;
      else if (key !== 'key') node.setAttribute(key, String(prop));
    }
    value.children.forEach((child, i) =>
      patch(node, node.childNodes[i], child),
    );
    while (node.childNodes.length > value.children.length)
      node.lastChild.remove();
    return node;
  }
  const mount = (Component) => {
    const root = document.createElement('div');
    document.body.append(root);
    const view = {
      root,
      hooks: [],
      effects: [],
      index: 0,
      alive: true,
      queued: false,
      queue() {
        if (view.queued) return;
        view.queued = true;
        Promise.resolve().then(() => {
          view.queued = false;
          if (view.alive) view.render();
        });
      },
      render() {
        view.index = 0;
        current = view;
        const value = Component();
        current = undefined;
        patch(root, root.firstChild, value);
        view.effects.splice(0).forEach((fn) => fn());
      },
      close() {
        view.alive = false;
        view.hooks.forEach((hook) => hook?.cleanup?.());
        root.remove();
      },
    };
    view.render();
    return view;
  };
  const registrations = new Map(),
    disposals = [],
    subscriptions = new Map(),
    scheme = new Set();
  const counts = { registered: 0, unregistered: 0, timers: new Map() };
  let dictionary,
    plugin,
    activeLocale = locale;
  const dark = {
    matches: false,
    addEventListener(_event, fn) {
      void _event;
      scheme.add(fn);
    },
    removeEventListener(_event, fn) {
      void _event;
      scheme.delete(fn);
    },
  };
  window.matchMedia = () => dark;
  if (defined) {
    const append = document.head.append.bind(document.head);
    document.head.append = (...nodes) => {
      append(...nodes);
      for (const node of nodes)
        if (node.localName === 'script')
          Promise.resolve().then(() => {
            if (!window.customElements.get('auriga-team-panel'))
              window.customElements.define(
                'auriga-team-panel',
                class extends window.HTMLElement {},
              );
            node.onload?.();
          });
    };
  }
  window.__ModuleLoader__ = {
    load(spec) {
      plugin = spec.factory((name) => {
        if (name === 'react') return React;
        throw new Error(name);
      });
    },
  };
  let nextTimer = 0;
  vm.runInNewContext(
    readFileSync(
      new URL('../../../clients/dsh-plugin/src/client.js', import.meta.url),
      'utf8',
    ),
    {
      window,
      document,
      customElements: window.customElements,
      URL,
      AbortController: globalThis.AbortController,
      fetch: fetchImpl,
      setTimeout(fn, delay) {
        const id = ++nextTimer;
        counts.timers.set(id, { fn, delay });
        return id;
      },
      clearTimeout(id) {
        counts.timers.delete(id);
      },
    },
  );
  const ctx = {
    locale: {
      register(_ns, entries) {
        dictionary = entries;
        return () => {
          dictionary = undefined;
        };
      },
      getLocale() {
        return { active: activeLocale };
      },
      bind() {
        return (key) =>
          dictionary[activeLocale.startsWith('en') ? 'en' : 'zh'][key];
      },
    },
    effect(fn) {
      const cleanup = fn();
      if (cleanup) disposals.push(cleanup);
    },
    on(event, listener) {
      subscriptions.set(event, listener);
      return () => subscriptions.delete(event);
    },
    slots: {
      inject(slot, register) {
        const cleanup = register();
        return () => {
          cleanup?.();
          registrations.delete(slot);
        };
      },
      register(meta, Component) {
        const slot =
          meta.name === 'settings.section'
            ? 'settings.section'
            : meta.key
              ? 'main'
              : 'sidebar.panellist';
        registrations.set(slot, { meta, Component });
        counts.registered++;
        return () => {
          counts.unregistered++;
        };
      },
    },
  };
  const dispose = plugin.apply(ctx);
  return {
    document,
    window,
    registrations,
    counts,
    subscriptions,
    scheme,
    dark,
    mount(slot) {
      return mount(registrations.get(slot).Component);
    },
    locale(value) {
      activeLocale = value;
      subscriptions.get('locale/change')?.({ active: value });
    },
    theme(value) {
      dark.matches = value === 'dark';
      scheme.forEach((fn) => fn());
    },
    expire(delay) {
      for (const [id, timer] of [...counts.timers])
        if (timer.delay === delay) {
          counts.timers.delete(id);
          timer.fn();
        }
    },
    dispose() {
      dispose();
      disposals.reverse().forEach((fn) => fn());
    },
    close() {
      dom.window.close();
    },
  };
}
export const flush = async () => {
  for (let i = 0; i < 16; i++) await Promise.resolve();
};
export const boot = () => ({
  mode: 'managed',
  protocol: 1,
  pluginVersion: '0.2.3',
  manifest: {
    version: '2.4.6',
    entry: 'panel.js',
    integrity: { 'panel.js': 'sha384-YQ==' },
  },
  panelBase: '/panel/',
  fetchBase: '/',
});
export const response = (status = 200, body = boot()) => ({
  status,
  ok: status >= 200 && status < 300,
  json: async () => body,
});
