import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, boot, response } from './shell-host.mjs';

const button = (root, label) =>
  [...root.querySelectorAll('button')].find((el) => el.textContent === label);

test('sidebar label uses the native SlotLabel getter contract across host locale changes', () => {
  const host = surfaceHost(async () => response());
  try {
    const {
      meta: { label },
      Component,
    } = host.registrations.get('sidebar.panellist');
    assert.equal(typeof label, 'function');
    // Native resolveSlotLabel reads a string verbatim or invokes its getter;
    // the registration's locale namespace never translates a string label.
    const read = () => (typeof label === 'function' ? label() : label);
    assert.equal(read(), 'Auriga');
    host.locale('en');
    assert.equal(read(), 'Auriga');
    host.locale('zh-CN');
    assert.equal(read(), 'Auriga');
    for (const [size, active] of [
      [16, false],
      [18, true],
    ]) {
      const icon = Component({ size, active });
      assert.ok(icon, 'sidebar renderer supplies a decorative icon');
      assert.equal(
        icon.type,
        active
          ? host.primitives.IconUsersOutlineMedium
          : host.primitives.IconUsersOutlineRegular,
      );
      assert.equal(icon.props.size, size);
      assert.equal(icon.props['aria-hidden'], true);
      const glyph = icon.type(icon.props);
      assert.equal(glyph.type, 'svg');
      assert.equal(glyph.props.width, size);
      assert.equal(glyph.props.height, size);
      assert.equal(glyph.props['aria-hidden'], true);
    }
  } finally {
    host.dispose();
    host.close();
  }
});

test('review H1: registrations name the declared native slot rather than the locale namespace', () => {
  const host = surfaceHost(async () => response());
  try {
    for (const [slot, registration] of host.registrations)
      assert.equal(registration.meta.name, slot);
  } finally {
    host.dispose();
    host.close();
  }
});

test('review M1: a foreign constructor claimed during loading never receives managed context', async () => {
  const host = surfaceHost(async () => response(), { defined: false });
  try {
    const main = host.mount('main');
    await flush();
    const script = host.document.querySelector('script');
    class Foreign extends host.window.HTMLElement {}
    host.window.customElements.define('auriga-team-panel', Foreign);
    host.completeScript(
      script,
      class Trusted extends host.window.HTMLElement {},
      { register: false },
    );
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), null);
    assert.ok(main.root.querySelector('[role="alert"]'));
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

for (const stop of ['timeout', 'unmount'])
  test(`review M1: A ${stop} then late A definition cannot be accepted as new B digest`, async () => {
    let current = boot();
    const host = surfaceHost(async () => response(200, current), {
      defined: false,
    });
    try {
      const first = host.mount('main');
      await flush();
      const a = host.document.querySelector('script');
      if (stop === 'timeout') {
        host.expire(10_000);
        await flush();
      } else first.close();
      current = boot();
      current.manifest.integrity['panel.js'] = 'sha384-Yg==';
      let second;
      if (stop === 'timeout') {
        button(first.root, '重试').click();
        second = first;
      } else second = host.mount('main');
      await flush();
      const b = host.document.querySelector('script');
      assert.notEqual(a, b);
      class Old extends host.window.HTMLElement {}
      host.completeScript(a, Old);
      host.completeScript(b, class New extends host.window.HTMLElement {}, {
        register: false,
      });
      await flush();
      assert.equal(second.root.querySelector('auriga-team-panel'), null);
      assert.ok(second.root.querySelector('[role="alert"]'));
      second.close();
      host.dispose();
      assert.equal(host.counts.timers.size, 0);
    } finally {
      host.close();
    }
  });

for (const variant of ['digest', 'source'])
  test(`review M1: simultaneous different ${variant} identities do not share a successful pending script`, async () => {
    let current = boot();
    const host = surfaceHost(async () => response(200, current), {
      defined: false,
    });
    try {
      const a = host.mount('main');
      await flush();
      const script = host.document.querySelector('script');
      current =
        variant === 'digest'
          ? boot()
          : {
              mode: 'standalone',
              protocol: 1,
              pluginVersion: '0.2.3',
              status: 'unconfigured',
              localPanel: {
                source: 'package',
                version: '2.4.6',
                entry: 'auriga/local-panel.js',
                integrity: 'sha384-YQ==',
              },
              fetchBase: null,
            };
      if (variant === 'digest')
        current.manifest.integrity['panel.js'] = 'sha384-Yg==';
      const b = host.mount('main');
      await flush();
      host.completeScript(script);
      await flush();
      assert.ok(a.root.querySelector('auriga-team-panel'));
      assert.equal(b.root.querySelector('auriga-team-panel'), null);
      assert.ok(b.root.querySelector('[role="alert"]'));
      a.close();
      b.close();
      host.dispose();
    } finally {
      host.close();
    }
  });

test('independent native slots: settings retains trusted bootstrap and versions beside editable preferences without leaking bootstrap credentials', async () => {
  const calls = [];
  const host = surfaceHost(async (url, init) => {
    calls.push({ url, init });
    return response(200, {
      ...boot(),
      platformUrl: 'https://private.example.com',
      token: 'private-credential',
    });
  });
  try {
    assert.deepEqual(
      [...host.registrations.keys()],
      ['sidebar.panellist', 'main', 'settings.section'],
    );
    assert.equal(
      host.registrations.get('main').meta.key,
      host.registrations.get('sidebar.panellist').meta.id,
    );
    assert.equal(host.registrations.get('settings.section').meta.order, 22);
    const settings = host.mount('settings.section');
    await flush();
    assert.match(settings.root.textContent, /系统托管.*协议版本1/);
    assert.match(settings.root.textContent, /插件版本0\.2\.3/);
    assert.match(settings.root.textContent, /账号/);
    assert.equal(
      settings.root.querySelector('form,nav,[role="tab"],[role="dialog"]'),
      null,
    );
    assert.equal(
      settings.root.querySelector('input[aria-label="平台地址"]'),
      null,
    );
    assert.equal(
      settings.root.querySelector('select[aria-label="默认部署位置"]'),
      null,
    );
    assert.equal(host.settingsImports.length, 1);
    assert.equal(host.document.querySelector('script'), null);
    assert.doesNotMatch(
      settings.root.textContent,
      /private|credential|连接成功|保存设置/,
    );
    assert.equal(
      calls[0].url.href,
      'https://instance.example.com/i/example/auriga/bootstrap',
    );
    assert.equal(calls[0].init.credentials, 'same-origin');
    assert.equal(calls[0].init.cache, 'no-store');
    assert.equal(calls[0].init.redirect, 'error');
    settings.close();
    host.dispose();
    assert.equal(host.counts.registered, 3);
    assert.equal(host.counts.unregistered, 3);
    assert.equal(host.registrations.size, 0);
    assert.equal(host.scheme.size, 0);
    assert.equal(host.subscriptions.size, 0);
    assert.equal(host.counts.timers.size, 0);
    assert.equal(host.document.querySelector('style'), null);
  } finally {
    host.close();
  }
});

test('opening, refreshing and closing host settings preserves main resource element and context', async () => {
  const host = surfaceHost(async () => response());
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    assert.ok(panel);
    const context = panel.context;
    panel.dataset.resourceTab = 'mine';
    panel.dataset.search = 'retained';
    const settings = host.mount('settings.section');
    await flush();
    button(settings.root, '刷新状态').click();
    await flush();
    settings.close();
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.context, context);
    assert.equal(panel.dataset.resourceTab, 'mine');
    assert.equal(panel.dataset.search, 'retained');
    assert.equal(main.root.querySelector('[role="tab"]'), null);
    main.close();
    assert.equal(panel.context, undefined);
    host.dispose();
  } finally {
    host.close();
  }
});

test('host language and dark/light changes update native settings and panel context', async () => {
  const host = surfaceHost(async () => response());
  try {
    const main = host.mount('main'),
      settings = host.mount('settings.section');
    await flush();
    host.locale('en');
    host.theme('dark');
    await flush();
    assert.match(
      settings.root.textContent,
      /System managed.*Plugin version0\.2\.3/,
    );
    const panel = main.root.querySelector('auriga-team-panel');
    assert.equal(panel.context.locale, 'en');
    assert.equal(panel.context.theme, 'dark');
    host.locale('zh-CN');
    host.theme('light');
    await flush();
    assert.match(settings.root.textContent, /系统托管/);
    assert.equal(panel.context.theme, 'light');
    assert.match(
      host.document.querySelector('style').textContent,
      /var\(--dsw-alias-bg-layer-1,transparent\)/,
    );
    main.close();
    settings.close();
    host.dispose();
  } finally {
    host.close();
  }
});

for (const [status, code, text] of [
  [401, undefined, '登录状态已失效'],
  [403, undefined, '没有读取托管状态的权限'],
  [404, undefined, '当前宿主尚未接入'],
  [426, undefined, '协议不受支持'],
  [502, 'common.unauthenticated', '界面清单验证未通过'],
  [502, 'common.validation_failed', '界面清单验证未通过'],
  [502, 'common.precondition_failed', '界面清单验证未通过'],
  [502, 'plugin.protocol_unsupported', '协议不受支持'],
  [503, 'secret-code', '暂时无法加载'],
])
  test(`settings classifies ${status}/${code} without exposing server details`, async () => {
    const host = surfaceHost(async () =>
      response(status, {
        code,
        detail: 'private-token private.example.com',
        error: { message: 'private-token' },
      }),
    );
    try {
      const settings = host.mount('settings.section');
      await flush();
      assert.ok(settings.root.querySelector('[role="alert"]'));
      assert.ok(settings.root.textContent.includes(text));
      assert.doesNotMatch(settings.root.textContent, /private|secret-code/);
      settings.close();
      host.dispose();
    } finally {
      host.close();
    }
  });

test('retry uses a new request; old completion after settings close cannot update the next opening', async () => {
  let calls = 0,
    late,
    oldSignal;
  const host = surfaceHost(async (_url, init) => {
    if (!new URL(_url).pathname.endsWith('/bootstrap'))
      return response(200, { loggedIn: false, connected: false });
    calls++;
    if (calls === 1)
      throw Object.assign(new Error('private failure'), {
        reason: 'private-token https://private.example.com',
      });
    if (calls === 2) {
      oldSignal = init.signal;
      return new Promise((resolve) => {
        late = resolve;
      });
    }
    return response();
  });
  try {
    const first = host.mount('settings.section');
    await flush();
    assert.doesNotMatch(
      first.root.textContent,
      /private-token|private\.example/,
    );
    button(first.root, '重试').click();
    await flush();
    assert.equal(calls, 2);
    first.close();
    assert.equal(oldSignal.aborted, true);
    const next = host.mount('settings.section');
    await flush();
    late(response(200, { ...boot(), pluginVersion: '9.9.9' }));
    await flush();
    assert.match(next.root.textContent, /0\.2\.3/);
    assert.doesNotMatch(next.root.textContent, /9\.9\.9/);
    next.close();
    host.dispose();
    assert.equal(host.counts.timers.size, 0);
  } finally {
    host.close();
  }
});

test('hung bootstrap times out and retry is available; plugin disposal aborts pending requests', async () => {
  let signal;
  const host = surfaceHost(async (_url, init) => {
    if (!new URL(_url).pathname.endsWith('/bootstrap'))
      return response(200, { loggedIn: false, connected: false });
    signal = init.signal;
    return new Promise(() => {});
  });
  try {
    const settings = host.mount('settings.section');
    await flush();
    host.expire(15_000);
    await flush();
    assert.equal(signal.aborted, true);
    assert.ok(button(settings.root, '重试'));
    button(settings.root, '重试').click();
    await flush();
    host.dispose();
    await flush();
    assert.equal(signal.aborted, true);
    assert.equal(host.counts.timers.size, 0);
    settings.close();
  } finally {
    host.close();
  }
});

test('SRI module failure can retry; late element definition and unload clean scripts and timers', async () => {
  const host = surfaceHost(async () => response(), { defined: false });
  try {
    const main = host.mount('main');
    await flush();
    const first = host.document.querySelector('script');
    assert.ok(first);
    assert.equal(first.integrity, 'sha384-YQ==');
    assert.equal(first.crossOrigin, 'anonymous');
    first.onerror();
    await flush();
    assert.equal(first.isConnected, false);
    assert.equal(host.moduleImports.length, 0);
    button(main.root, '重试').click();
    await flush();
    const second = host.document.querySelector('script');
    host.provideModule(second, new Promise(() => {}));
    second.onload();
    host.expire(10_000);
    await flush();
    assert.equal(second.isConnected, false);
    button(main.root, '重试').click();
    await flush();
    const third = host.document.querySelector('script');
    host.completeScript(third);
    await flush();
    assert.ok(main.root.querySelector('auriga-team-panel'));
    assert.equal(third.isConnected, false);
    main.close();
    host.dispose();
    assert.equal(host.counts.timers.size, 0);
  } finally {
    host.close();
  }
});

test('unmount cancels an in-flight panel script and does not mount from a late load', async () => {
  const host = surfaceHost(async () => response(), { defined: false });
  try {
    const main = host.mount('main');
    await flush();
    const script = host.document.querySelector('script');
    const late = script.onload;
    main.close();
    await flush();
    assert.equal(script.isConnected, false);
    late();
    host.window.customElements.define(
      'auriga-team-panel',
      class extends host.window.HTMLElement {},
    );
    await flush();
    assert.equal(host.document.querySelector('auriga-team-panel'), null);
    host.dispose();
    assert.equal(host.counts.timers.size, 0);
  } finally {
    host.close();
  }
});

test('bootstrap rejects external panel/API origins and does not expose their values', async () => {
  for (const extra of [
    { panelBase: 'https://private.example.com/' },
    { fetchBase: 'https://private.example.com/' },
    { mode: 'connect' },
    { protocol: 2 },
  ]) {
    const host = surfaceHost(async () =>
      response(200, { ...boot(), ...extra }),
    );
    try {
      const settings = host.mount('settings.section');
      await flush();
      assert.ok(settings.root.querySelector('[role="alert"]'));
      assert.doesNotMatch(settings.root.textContent, /private.example/);
      settings.close();
      host.dispose();
    } finally {
      host.close();
    }
  }
});

test('packaged UI shows unconfigured native main/settings without a resource fetch capability', async () => {
  const local = {
    mode: 'standalone',
    protocol: 1,
    pluginVersion: '0.2.3',
    status: 'unconfigured',
    localPanel: {
      source: 'package',
      version: '2.4.6',
      entry: 'auriga/local-panel.js',
      integrity: 'sha384-YQ==',
    },
    fetchBase: null,
  };
  const host = surfaceHost(async () => response(200, local));
  try {
    const main = host.mount('main'),
      settings = host.mount('settings.section');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    assert.ok(panel);
    assert.equal(panel.context.mode, 'standalone');
    assert.equal(panel.context.fetch, undefined);
    assert.match(settings.root.textContent, /未配置平台/);
    assert.match(settings.root.textContent, /界面来源package/);
    assert.doesNotMatch(settings.root.textContent, /已读取托管配置/);
    main.close();
    settings.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('managed failures never load or fall back to the packaged local panel', async () => {
  const calls = [];
  const host = surfaceHost(
    async (url) => {
      calls.push(String(url));
      return response(503, {});
    },
    { defined: false },
  );
  try {
    const main = host.mount('main');
    await flush();
    assert.ok(main.root.querySelector('[role="alert"]'));
    assert.equal(host.document.querySelector('script'), null);
    assert.equal(calls.length, 1);
    assert.doesNotMatch(calls.join(' '), /local-panel/);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('host owns the settings dialog and Esc; opening/closing cannot navigate main', async () => {
  const host = surfaceHost(async () => response());
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    panel.dataset.search = 'keep';
    const settings = host.mount('settings.section');
    await flush();
    const close = (event) => {
      if (event.key === 'Escape') settings.close();
    };
    host.document.addEventListener('keydown', close);
    host.document.dispatchEvent(
      new host.window.KeyboardEvent('keydown', { key: 'Escape' }),
    );
    await flush();
    assert.equal(settings.root.isConnected, false);
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.dataset.search, 'keep');
    host.document.removeEventListener('keydown', close);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('a different manifest or source cannot reuse an already defined panel constructor', async () => {
  let active = {
    mode: 'standalone',
    protocol: 1,
    pluginVersion: '0.2.3',
    status: 'unconfigured',
    localPanel: {
      source: 'package',
      version: '2.4.6',
      entry: 'auriga/local-panel.js',
      integrity: 'sha384-YQ==',
    },
    fetchBase: null,
  };
  const host = surfaceHost(async () => response(200, active));
  try {
    const local = host.mount('main');
    await flush();
    assert.ok(local.root.querySelector('auriga-team-panel'));
    local.close();
    active = boot();
    const managed = host.mount('main');
    await flush();
    assert.match(managed.root.textContent, /界面清单验证未通过/);
    assert.equal(managed.root.querySelector('auriga-team-panel'), null);
    managed.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('review M1: the same trusted module can share pending work and reuse its exact constructor', async () => {
  const host = surfaceHost(async () => response(), { defined: false });
  try {
    const a = host.mount('main'),
      b = host.mount('main');
    await flush();
    const scripts = host.document.querySelectorAll('script');
    assert.equal(scripts.length, 1);
    const Constructor = host.completeScript(scripts[0]);
    await flush();
    assert.equal(
      a.root.querySelector('auriga-team-panel').constructor,
      Constructor,
    );
    assert.equal(
      b.root.querySelector('auriga-team-panel').constructor,
      Constructor,
    );
    assert.equal(host.moduleImports.length, 1);
    assert.equal(host.moduleImports[0], scripts[0].src);
    a.close();
    b.close();
    const reused = host.mount('main');
    await flush();
    assert.equal(
      reused.root.querySelector('auriga-team-panel').constructor,
      Constructor,
    );
    assert.equal(host.moduleImports.length, 1);
    assert.equal(host.document.querySelector('script'), null);
    reused.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('review M1: an import that finishes after timeout cannot claim its constructor during B retry', async () => {
  let current = boot(),
    resolveA;
  const host = surfaceHost(async () => response(200, current), {
    defined: false,
  });
  try {
    const main = host.mount('main');
    await flush();
    const a = host.document.querySelector('script');
    host.provideModule(
      a,
      new Promise((resolve) => {
        resolveA = resolve;
      }),
    );
    a.onload();
    await flush();
    host.expire(10_000);
    await flush();
    current = boot();
    current.manifest.integrity['panel.js'] = 'sha384-Yg==';
    button(main.root, '重试').click();
    await flush();
    const b = host.document.querySelector('script');
    assert.notEqual(a.src, b.src);
    class Old extends host.window.HTMLElement {}
    host.window.customElements.define('auriga-team-panel', Old);
    resolveA(Old);
    await flush();
    host.completeScript(b, class New extends host.window.HTMLElement {}, {
      register: false,
    });
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), null);
    assert.ok(main.root.querySelector('[role="alert"]'));
    assert.equal(host.moduleImports.length, 2);
    assert.equal(host.counts.timers.size, 0);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('review M1: a same-identity waiter arriving after definition still waits for positive module validation', async () => {
  let resolveModule;
  const host = surfaceHost(async () => response(), { defined: false });
  try {
    const a = host.mount('main');
    await flush();
    const script = host.document.querySelector('script');
    class Trusted extends host.window.HTMLElement {}
    host.provideModule(
      script,
      new Promise((resolve) => {
        resolveModule = resolve;
      }),
    );
    host.window.customElements.define('auriga-team-panel', Trusted);
    script.onload();
    await flush();
    const b = host.mount('main');
    await flush();
    resolveModule(Trusted);
    await flush();
    assert.equal(
      a.root.querySelector('auriga-team-panel').constructor,
      Trusted,
    );
    assert.equal(
      b.root.querySelector('auriga-team-panel').constructor,
      Trusted,
    );
    assert.equal(host.moduleImports.length, 1);
    a.close();
    b.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('host theme overrides opposite OS preference and never installs an independent theme listener or switcher', async () => {
  const host = surfaceHost(async () => response(), {
    hostTheme: 'light',
    osTheme: 'dark',
  });
  try {
    const main = host.mount('main'),
      settings = host.mount('settings.section');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    assert.equal(panel.context.theme, 'light');
    assert.equal(host.scheme.size, 0);
    host.osTheme('light');
    await flush();
    assert.equal(panel.context.theme, 'light');
    host.theme('dark');
    await flush();
    assert.equal(panel.context.theme, 'dark');
    host.osTheme('light');
    await flush();
    assert.equal(panel.context.theme, 'dark');
    assert.equal(
      settings.root.querySelector(
        'select:not([aria-label="默认部署位置"]),input[type=radio],[data-theme-toggle]',
      ),
      null,
    );
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    main.close();
    settings.close();
    host.dispose();
    assert.equal(host.subscriptions.size, 0);
  } finally {
    host.close();
  }
});
