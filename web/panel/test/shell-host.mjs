import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { setImmediate } from 'node:timers';
import { JSDOM } from 'jsdom';

// Small host React hook/DOM adapter: no bundled React dependency in the plugin.
// This exercises the real ModuleLoader factory, effect lifecycle and native slots.
export function surfaceHost(
  fetchImpl,
  {
    defined = true,
    locale = 'zh-CN',
    slotCore,
    hostTheme = 'light',
    osTheme = 'light',
    title = '',
    consentFetch,
    settingsFetch,
    remote,
    hash = '',
    url = 'https://instance.example.com/i/example/',
    accountLauncher = false,
  } = {},
) {
  const dom = new JSDOM('<html><head></head><body></body></html>', {
    url,
  });
  const { window } = dom;
  const { document } = window;
  document.title = title;
  const modules = new Map(),
    moduleImports = [];
  const navigations = [],
    selections = [];
  const completeScript = (
    script,
    Constructor = class extends window.HTMLElement {},
    { register = true } = {},
  ) => {
    modules.set(script.src, Constructor);
    if (register && !window.customElements.get('auriga-team-panel'))
      window.customElements.define('auriga-team-panel', Constructor);
    script.onload?.();
    return Constructor;
  };
  let current;
  const FRAGMENT = Symbol.for('react.fragment');
  const React = {
    Fragment: FRAGMENT,
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
      // Match React lazy initializers: useState(fn) calls fn once.
      if (!(index in view.hooks))
        view.hooks[index] =
          typeof initial === 'function' ? initial() : initial;
      return [
        view.hooks[index],
        (next) => {
          if (!view.alive) return;
          const value =
            typeof next === 'function' ? next(view.hooks[index]) : next;
          // React bails out when Object.is says the state is unchanged. Without
          // this, ConsentSection's mount effect (setView(null)/setStatus
          // ('checking')) re-queues forever when the instance is remounted.
          if (Object.is(view.hooks[index], value)) return;
          view.hooks[index] = value;
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
  // Function components that return another function component share one DOM
  // node. Keep a stack per node so the outer instance is not overwritten/closed
  // when the inner root re-renders.
  const components = new WeakMap();
  const stackOf = (node) => {
    const stack = components.get(node);
    return Array.isArray(stack) ? stack : stack ? [stack] : [];
  };
  const findInstance = (node, type, owner) => {
    const stack = stackOf(node);
    return (
      stack.find(
        (item) => item.Component === type && item.owner === owner && item.alive,
      ) ||
      // Same component type on this node: reuse across owner re-parenting so a
      // parent remount does not wipe descendant hook state / DOM bindings.
      stack.find((item) => item.Component === type && item.alive) ||
      stack.find((item) => item.owner === owner && item.alive) ||
      undefined
    );
  };
  const bindInstance = (node, child) => {
    // Keep outer function-component wrappers that share this host node
    // (SettingsView → SettingsLayout → div). Only replace same Component.
    const stack = stackOf(node).filter(
      (item) =>
        item.alive && item !== child && item.Component !== child.Component,
    );
    stack.push(child);
    components.set(node, stack);
    child.node = node;
  };
  const releaseNode = (node, owner) => {
    const stack = stackOf(node);
    if (stack.length) {
      for (const item of [...stack].reverse()) {
        if (item !== owner && item.alive) item.close();
      }
      components.delete(node);
    }
    for (const nested of node.childNodes) releaseNode(nested, owner);
  };
  function patch(parent, oldNode, value, owner) {
    if (value == null || typeof value === 'boolean') {
      // Keep the placeholder comment so the owning function component can
      // reattach without releaseNode wiping its instance (ConsentSection
      // returns null while status === 'checking').
      if (oldNode?.nodeType === 8) return oldNode;
      const node = document.createComment('');
      if (oldNode) {
        // Move component bindings to the comment before releasing descendants.
        const stack = stackOf(oldNode).filter((item) => item.alive);
        for (const nested of [...oldNode.childNodes])
          releaseNode(nested, owner);
        if (stack.length) components.set(node, stack);
        for (const item of stack) item.node = node;
        components.delete(oldNode);
        oldNode.replaceWith(node);
      } else parent.append(node);
      return node;
    }
    // Fragment has no DOM node: expand children into the parent.
    if (typeof value === 'object' && value.type === FRAGMENT) {
      const fragChildren = Array.isArray(value.children)
        ? value.children
        : value.children != null
          ? [value.children]
          : [];
      // When used as a sole child of a host element, the parent reconciler
      // flattens Fragments. Direct returns get a comment anchor + siblings.
      if (!oldNode) {
        const anchor = document.createComment('fragment');
        parent.append(anchor);
        let cursor = anchor;
        for (const child of fragChildren) {
          cursor = patch(parent, null, child, owner);
        }
        return anchor;
      }
      // Replacing an existing node with a Fragment: patch first child onto
      // oldNode and append the rest after it.
      let node = oldNode;
      if (fragChildren.length === 0) {
        return patch(parent, oldNode, null, owner);
      }
      node = patch(parent, oldNode, fragChildren[0], owner);
      let insertAfter = node;
      for (let i = 1; i < fragChildren.length; i++) {
        const sibling = patch(parent, null, fragChildren[i], owner);
        insertAfter.after(sibling);
        insertAfter = sibling;
      }
      return node;
    }
    if (typeof value === 'object' && typeof value.type === 'function') {
      // Index fallback may point at a sibling's node; never take over a DOM
      // node that already hosts a different function component.
      if (
        oldNode &&
        !stackOf(oldNode).some(
          (item) => item.Component === value.type && item.alive,
        ) &&
        stackOf(oldNode).some((item) => item.alive)
      )
        oldNode = null;
      let child = oldNode && findInstance(oldNode, value.type, owner);
      if (child && child.Component === value.type) {
        // Re-parent instead of remounting when only the owner identity changed.
        if (child.owner !== owner) {
          child.owner?.children?.delete?.(child);
          child.owner = owner;
          owner.children.add(child);
        }
      } else {
        if (oldNode && !child) releaseNode(oldNode, owner);
        child = {
          Component: value.type,
          owner,
          hooks: [],
          effects: [],
          children: new Set(),
          index: 0,
          alive: true,
          queued: false,
          lastProps: null,
          name: value.type?.name || value.type?.displayName || 'anon',
          // Re-render only this instance (React semantics). Owner bubbling
          // remounted ConsentSection while status === 'checking' (null output).
          queue() {
            if (!child.alive || child.queued) return;
            child.queued = true;
            Promise.resolve().then(() => {
              child.queued = false;
              if (!child.alive || !child.node || !child.lastProps) return;
              const mountParent = child.node.parentNode;
              if (!mountParent) return;
              patch(
                mountParent,
                child.node,
                {
                  type: child.Component,
                  props: child.lastProps,
                  children: [],
                },
                child.owner,
              );
            });
          },
          close() {
            child.alive = false;
            for (const nested of child.children) nested.close();
            child.hooks.forEach((hook) => hook?.cleanup?.());
            owner.children.delete(child);
            if (child.node) {
              const next = stackOf(child.node).filter(
                (item) => item !== child && item.alive,
              );
              if (next.length) components.set(child.node, next);
              else components.delete(child.node);
            }
          },
        };
        owner.children.add(child);
      }
      // Prefer React key; fall back to id/title so repeated siblings of the same
      // component type (SettingsPanel / SettingsCard) keep stable identity.
      child.propsId =
        value.props?.key ?? value.props?.id ?? value.props?.title;
      const previous = current;
      current = child;
      child.index = 0;
      let rendered;
      try {
        // Preserve props.children when callers pass children only via props
        // (React.createElement(type, { children }) ), matching React.
        const props = { ...value.props };
        if (value.children.length === 1) props.children = value.children[0];
        else if (value.children.length > 1) props.children = value.children;
        // Self-queue re-enters with children: [] — keep prior props.children.
        if (
          value.children.length === 0 &&
          props.children == null &&
          child.lastProps?.children != null
        )
          props.children = child.lastProps.children;
        child.lastProps = props;
        rendered = value.type(props);
      } finally {
        current = previous;
      }
      const node = patch(parent, oldNode, rendered, child);
      if (oldNode && oldNode !== node) {
        const moved = stackOf(oldNode).filter((item) => item !== child);
        if (moved.length) components.set(oldNode, moved);
        else components.delete(oldNode);
      }
      bindInstance(node, child);
      child.effects.splice(0).forEach((fn) => fn());
      return node;
    }
    // Do not releaseNode here: nested function components share one host node
    // (SettingsView → SettingsLayout → div). Closing "foreign" stack entries
    // would kill the outer instance and append duplicate trees on the next paint.
    if (typeof value !== 'object' || typeof value.type !== 'string') {
      const text =
        value == null || typeof value === 'object' ? '' : String(value);
      if (oldNode?.nodeType === 3) {
        oldNode.data = text;
        return oldNode;
      }
      const node = document.createTextNode(text);
      if (oldNode) releaseNode(oldNode, owner);
      if (oldNode) oldNode.replaceWith(node);
      else parent.append(node);
      return node;
    }
    let node = oldNode;
    if (node?.localName !== value.type) {
      if (oldNode) releaseNode(oldNode, owner);
      node = document.createElement(value.type);
      if (oldNode) oldNode.replaceWith(node);
      else parent.append(node);
    }
    const props = value.props ?? {};
    if (value.type.includes('-') && !Object.hasOwn(props, 'hidden'))
      node.removeAttribute('hidden');
    for (const [key, prop] of Object.entries(props)) {
      if (key === 'ref') prop.current = node;
      else if (key === 'style') Object.assign(node.style, prop);
      else if (key.startsWith('on')) node[key.toLowerCase()] = prop;
      else if (key === 'className') node.className = prop;
      // React applies boolean DOM properties; disabled={false} is not an HTML
      // disabled="false" attribute (which would still disable the element).
      else if (key === 'hidden' && value.type.includes('-')) {
        if (prop == null) node.removeAttribute(key);
        else node.setAttribute(key, String(prop));
      } else if (key === 'disabled' || key === 'hidden')
        node[key] = Boolean(prop);
      else if (prop == null) node.removeAttribute(key);
      else if (key !== 'key') node.setAttribute(key, String(prop));
    }
    // Reconcile by index, but reuse function-component instances when a
    // preceding sibling is inserted/removed (index-only patch would remount).
    const previous = [...node.childNodes];
    const claimed = new Set();
    const takeMatch = (child, preferred) => {
      if (!child || typeof child !== 'object' || typeof child.type !== 'function')
        return preferred && !claimed.has(preferred) ? preferred : undefined;
      const id =
        child.props?.key ?? child.props?.id ?? child.props?.title;
      const matches = (candidate) => {
        if (!candidate || claimed.has(candidate)) return false;
        const inst = findInstance(candidate, child.type, owner);
        if (!inst || inst.Component !== child.type) return false;
        // Disambiguate repeated siblings of the same component type.
        if (id != null && inst.propsId != null && inst.propsId !== id)
          return false;
        return true;
      };
      if (matches(preferred)) return preferred;
      return previous.find(matches);
    };
    const rawChildren = Array.isArray(value.children)
      ? value.children
      : value.children != null
        ? [value.children]
        : [];
    // Expand React.Fragment children so ConsentSection settings cards land
    // as real siblings under the host element (no wrapper node).
    const childList = [];
    for (const child of rawChildren) {
      if (child && typeof child === 'object' && child.type === FRAGMENT) {
        const nested = Array.isArray(child.children)
          ? child.children
          : child.children != null
            ? [child.children]
            : [];
        childList.push(...nested);
      } else {
        childList.push(child);
      }
    }
    const rendered = childList.map((child, i) => {
      const preferred = previous[i];
      const matched = takeMatch(child, preferred);
      if (matched) claimed.add(matched);
      if (matched && matched !== node.childNodes[i]) {
        const anchor = node.childNodes[i] ?? null;
        node.insertBefore(matched, anchor);
      }
      return patch(node, matched ?? node.childNodes[i], child, owner);
    });
    for (const old of previous) {
      if (!rendered.includes(old) && old.parentNode === node) {
        releaseNode(old, owner);
        old.remove();
      }
    }
    if ('value' in props) node.value = props.value;
    return node;
  }
  const mount = (Component) => {
    const root = document.createElement('div');
    document.body.append(root);
    const view = {
      root,
      hooks: [],
      effects: [],
      children: new Set(),
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
        patch(root, root.firstChild, value, view);
        view.effects.splice(0).forEach((fn) => fn());
      },
      close() {
        view.alive = false;
        for (const child of view.children) child.close();
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
  const declarations = new Map([
    ['main', 'keyed'],
    ['sidebar.panellist', 'list'],
    ['settings.section', 'list'],
    ['settings.models.provider-card', 'keyed'],
    ['sidebar.brand.mark', 'single'],
    ['sidebar.brand.name', 'single'],
  ]);
  if (accountLauncher) declarations.set('settings.launcher', 'single');
  declarations.set('shell.overlay', 'list');
  const counts = { registered: 0, unregistered: 0, timers: new Map() };
  let dictionary,
    plugin,
    activeLocale = locale,
    activeTheme = hostTheme;
  const dark = {
    matches: osTheme === 'dark',
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
  // PlatformLogin acquires an exclusive lock before starting inline login.
  Object.defineProperty(window.navigator, 'locks', {
    configurable: true,
    value: {
      request(_name, options, callback) {
        const run =
          typeof options === 'function' ? options : callback;
        return Promise.resolve().then(() => run?.());
      },
    },
  });
  if (defined) {
    const append = document.head.append.bind(document.head);
    document.head.append = (...nodes) => {
      append(...nodes);
      for (const node of nodes)
        if (node.localName === 'script')
          Promise.resolve().then(() => {
            completeScript(node);
          });
    };
  }
  // Routing doubles for the two exports verified in the shipped rc.3 static module table.
  const primitives = {
    IconUsersOutlineRegular: ({ size }) =>
      React.createElement('svg', {
        width: size,
        height: size,
        'aria-hidden': true,
      }),
    IconUsersOutlineMedium: ({ size }) =>
      React.createElement('svg', {
        width: size,
        height: size,
        'aria-hidden': true,
      }),
  };
  window.__ModuleLoader__ = {
    load(spec) {
      plugin = spec.factory((name) => {
        if (name === 'react') return React;
        if (name === '@deepseek-ai/dsh-client-ui-primitives') return primitives;
        throw new Error(name);
      });
    },
  };
  let nextTimer = 0;
  const settingsImports = [];
  let preferences = {
    platformOrigin: '',
    activeOrigin: '',
    deployment: 'cloud',
  };
  const routeFetch = (url, init = {}) => {
    const path = new URL(url).pathname;
    if (path.includes('/auriga/settings/')) {
      if (settingsFetch) return settingsFetch(url, init);
      if (path.endsWith('/save'))
        preferences = { ...preferences, ...JSON.parse(init.body) };
      return Promise.resolve(
        response(
          200,
          path.endsWith('/test') ? { reachable: true } : preferences,
        ),
      );
    }
    if (path.includes('/auriga/shell/consent/'))
      return consentFetch
        ? consentFetch(url, init)
        : Promise.resolve(response(503, { code: 'common.unavailable' }));
    if (path.endsWith('/auriga/shell/status'))
      return Promise.resolve(fetchImpl(url, init)).then(async (result) => {
        if (result.ok === false) return result;
        const data = await result.json();
        if (typeof data.loggedIn === 'boolean')
          return response(result.status ?? 200, data);
        // Bootstrap fixtures are not status responses. Supply the independent
        // authenticated runtime projection used by the settings integration.
        const loggedIn = ['managed', 'connect'].includes(data.mode);
        return response(200, {
          loggedIn,
          connected: data.connection?.connected === true,
          configured: data.mode === 'connect' || data.mode === 'managed',
          brokerAvailable: true,
          csrf: 'fixture-csrf',
          platform: loggedIn ? 'connected' : 'signed_out',
          account: loggedIn ? { subject: 'fixture-member' } : null,
          network: {
            connected: data.connection?.connected === true,
            state: 'unavailable',
          },
          connection: data.connection,
        });
      });
    return fetchImpl(url, init);
  };
  vm.runInNewContext(
    readFileSync(
      new URL('../../../clients/dsh-plugin/src/client.js', import.meta.url),
      'utf8',
    ),
    {
      window: new Proxy(window, {
        get(target, key) {
          if (key === 'location')
            return {
              hash,
              hostname: window.location.hostname,
              assign: (url) => navigations.push(String(url)),
            };
          return Reflect.get(target, key);
        },
      }),
      document,
      customElements: window.customElements,
      URL,
      AbortController: globalThis.AbortController,
      fetch: routeFetch,
      setTimeout(fn, delay) {
        const id = ++nextTimer;
        counts.timers.set(id, { fn, delay });
        return id;
      },
      clearTimeout(id) {
        counts.timers.delete(id);
      },
      setInterval(fn, delay) {
        const id = ++nextTimer;
        counts.timers.set(id, { fn, delay, repeat: true });
        return id;
      },
      clearInterval(id) {
        counts.timers.delete(id);
      },
    },
    {
      importModuleDynamically: async (specifier) => {
        const pathname = new URL(specifier).pathname;
        if (pathname.endsWith('/auriga/settings/view.js')) {
          settingsImports.push(specifier);
          const settingsRoot = new URL(
            '../../../clients/dsh-plugin/src/settings/',
            import.meta.url,
          );
          const settingsContext = vm.createContext({
            AbortSignal: globalThis.AbortSignal,
            fetch: routeFetch,
            document,
            setInterval: window.setInterval.bind(window),
            clearInterval: window.clearInterval.bind(window),
            setTimeout: window.setTimeout.bind(window),
            clearTimeout: window.clearTimeout.bind(window),
          });
          const loadSettingsModule = async (href, sourceText) => {
            const source =
              sourceText ?? readFileSync(new URL(href), 'utf8');
            const module = new vm.SourceTextModule(source, {
              identifier: String(href),
              context: settingsContext,
            });
            await module.link(async (dep, referencing) =>
              loadSettingsModule(new URL(dep, referencing.identifier).href),
            );
            if (module.status !== 'evaluated') await module.evaluate();
            return module;
          };
          // Evaluate the real browser module graph (view → ui → icons).
          const viewHref = new URL('view.js', settingsRoot).href;
          if (settingsFetch) {
            const asset = await settingsFetch(specifier, {
              credentials: 'same-origin',
              redirect: 'error',
              cache: 'no-store',
            });
            return loadSettingsModule(viewHref, await asset.text());
          }
          return loadSettingsModule(viewHref);
        }
        const shellUi = pathname.match(/\/auriga\/shell-ui\/([^/]+\.js)$/);
        if (shellUi) {
          const diskRoot = new URL(
            '../../../clients/dsh-plugin/src/shell/',
            import.meta.url,
          );
          const shellContext = vm.createContext({});
          const loadShellModule = async (href) => {
            const source = readFileSync(new URL(href), 'utf8');
            const module = new vm.SourceTextModule(source, {
              identifier: String(href),
              context: shellContext,
            });
            await module.link(async (dep, referencing) =>
              loadShellModule(new URL(dep, referencing.identifier).href),
            );
            if (module.status !== 'evaluated') await module.evaluate();
            return module;
          };
          return loadShellModule(new URL(shellUi[1], diskRoot).href);
        }
        moduleImports.push(specifier);
        const Constructor = await modules.get(specifier);
        if (!Constructor) throw new Error('Module was not evaluated');
        const module = new vm.SyntheticModule(['AurigaTeamPanel'], function () {
          this.setExport('AurigaTeamPanel', Constructor);
        });
        await module.link(() => {});
        await module.evaluate();
        return module;
      },
    },
  );
  const ctx = {
    inject(names, apply) {
      if (names.length === 1 && names[0] === 'layout')
        apply({
          layout: {
            selectPanel(id) {
              if (registrations.get('main')?.meta.key !== id)
                throw new Error(
                  'The panel must be registered before selection',
                );
              selections.push(id);
            },
          },
        });
    },
    get(name) {
      return name === 'remote' ? remote : undefined;
    },
    theme: {
      getTheme() {
        return {
          active: { id: 'host-custom-theme', colorScheme: activeTheme },
        };
      },
    },
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
        if (!declarations.has(slot))
          throw new Error(`slot "${slot}" is not declared`);
        const cleanup = register();
        return () => {
          cleanup?.();
          registrations.delete(slot);
        };
      },
      register(meta, Component) {
        const slot = meta.name;
        const kind = declarations.get(slot);
        if (!kind) throw new Error(`slot "${slot}" is not declared`);
        if (kind === 'keyed' && typeof meta.key !== 'string')
          throw new Error(`keyed slot "${slot}" requires options.key`);
        if (kind === 'list' && typeof meta.id !== 'string')
          throw new Error(`list slot "${slot}" requires options.id`);
        const release = slotCore?.register(meta, Component);
        registrations.set(slot, { meta, Component });
        counts.registered++;
        return () => {
          release?.();
          counts.unregistered++;
        };
      },
    },
  };
  const dispose = plugin.apply(ctx);
  const host = {
    document,
    window,
    navigations,
    selections,
    registrations,
    primitives,
    counts,
    subscriptions,
    scheme,
    dark,
    completeScript,
    moduleImports,
    settingsImports,
    provideModule(script, Constructor) {
      modules.set(script.src, Constructor);
    },
    mount(slot) {
      const view = mount(registrations.get(slot).Component);
      view.host = host;
      return view;
    },
    locale(value) {
      activeLocale = value;
      subscriptions.get('locale/change')?.({ active: value });
    },
    theme(value) {
      activeTheme = value;
      subscriptions.get('theme/change')?.(ctx.theme.getTheme());
    },
    osTheme(value) {
      dark.matches = value === 'dark';
      scheme.forEach((fn) => fn());
    },
    expire(delay) {
      for (const [id, timer] of [...counts.timers])
        if (timer.delay === delay) {
          if (!timer.repeat) counts.timers.delete(id);
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
  return host;
}
export const flush = async () => {
  await new Promise((resolve) => setImmediate(resolve));
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

// Auriga settings live in the activity-bar route (no native settings.section).
// Pass the host to mount a dedicated main surface, or an already mounted main.
export async function openSettings(target) {
  const owned = typeof target.mount === 'function';
  const main = owned ? target.mount('main') : target;
  // Only auto-mount the login overlay when the caller passes the host. Tests
  // that already mounted main can call ensureOverlay(host) when they need it.
  if (owned) ensureOverlay(target);
  await flush();
  const tab =
    main.root.querySelector('#auriga-tab-settings') ||
    [...main.root.querySelectorAll('.auriga-nav-item,[role="tab"]')].find(
      (element) => ['设置', 'Settings'].includes(element.textContent?.trim()),
    );
  tab.click();
  await flush();
  return {
    root: main.root.querySelector('#auriga-panel-settings'),
    close() {
      if (owned && main.alive !== false) main.close();
    },
  };
}

/** Keep PlatformLogin registered so overview "登录平台" can open the overlay. */
export function ensureOverlay(host) {
  if (!host?.registrations?.has('shell.overlay')) return null;
  if (host._overlayView?.alive) return host._overlayView;
  host._overlayView = host.mount('shell.overlay');
  return host._overlayView;
}

