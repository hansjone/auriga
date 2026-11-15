import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  surfaceHost,
  flush,
  boot,
  response,
  openSettings,
} from './shell-host.mjs';

const button = (root, label) =>
  [...root.querySelectorAll('button')].find((el) => el.textContent === label);

test('the local sign-in launch fragment selects the existing Auriga overview', async () => {
  for (const hash of ['', '#auriga=login-other', '#auriga=login']) {
    const host = surfaceHost(async () => response(), { hash });
    try {
      assert.deepEqual(
        host.selections,
        hash === '#auriga=login' ? ['auriga-team'] : [],
      );
      if (host.selections.length) {
        const main = host.mount('main');
        await flush();
        assert.equal(
          main.root.querySelector('#auriga-panel-overview').hidden,
          false,
        );
        main.close();
      }
    } finally {
      host.dispose();
      host.close();
    }
  }
});

test('desktop update polling refreshes the native banner and releases its interval on unmount', async () => {
  let desktop = null;
  let reads = 0;
  let pollingSignal;
  const actions = [];
  const host = surfaceHost(async () => response(), {
    settingsFetch: async (url, init) => {
      if (new URL(url).pathname.endsWith('/state')) {
        reads++;
        pollingSignal = init.signal;
        return response(200, { desktop });
      }
      actions.push(JSON.parse(init.body).action);
      return response(200, desktop);
    },
  });
  let main;
  try {
    main = host.mount('main');
    await flush();
    assert.equal(reads, 1);
    desktop = { available: { version: '2.0.0' }, required: true };
    host.expire(5000);
    await flush();
    assert.equal(reads, 2);
    assert.match(main.root.textContent, /当前版本已停用.*2\.0\.0/);
    assert.equal(main.root.querySelector('auriga-team-panel').hidden, true);
    await button(main.root, '更新').onclick();
    assert.deepEqual(actions, ['update']);
    desktop = {
      available: { version: '2.0.0' },
      downloading: true,
      totalBytes: 100,
      downloadedBytes: 40,
    };
    host.expire(5000);
    await flush();
    assert.equal(reads, 3);
    assert.equal(main.root.querySelector('progress').value, 40);
    await button(main.root, '取消下载').onclick();
    assert.deepEqual(actions, ['update', 'cancel']);
    main.close();
    assert.equal(pollingSignal.aborted, true);
    assert.equal(host.counts.timers.size, 0);
    host.expire(5000);
    await flush();
    assert.equal(reads, 3);
  } finally {
    if (main?.alive) main.close();
    host.dispose();
    host.close();
  }
});

for (const locale of ['zh-CN', 'en'])
  test(`settings sign-in opens the inline login in the Overview tab: ${locale}`, async () => {
    const commands = [];
    let authenticating = false;
    const localBoot = {
      mode: 'connect',
      protocol: 1,
      pluginVersion: '0.0.0',
      status: 'disconnected',
      connection: { connected: false },
      localPanel: {
        source: 'package',
        version: '0.0.0',
        entry: 'auriga/local-panel.js',
        integrity: 'sha384-YQ==',
      },
      fetchBase: null,
    };
    const host = surfaceHost(
      async (url, init = {}) => {
        const path = new URL(url).pathname;
        if (path.endsWith('/shell/command')) {
          commands.push(init);
          authenticating = true;
          return response(200, {});
        }
        if (path.endsWith('/shell/status'))
          return response(200, {
            loggedIn: false,
            connected: false,
            configured: true,
            platform: authenticating ? 'authenticating' : 'signed_out',
            brokerAvailable: true,
            csrf: 'settings-login-csrf',
            ...(authenticating
              ? {
                  inlineLogin: {
                    providerId: '11111111-2222-4333-8444-555555555555',
                    providers: [
                      {
                        id: '11111111-2222-4333-8444-555555555555',
                        connectorType: 'team-account',
                        displayName: { zh: '团队账号', en: 'Team account' },
                      },
                    ],
                    step: {
                      type: 'form',
                      formId: 'password',
                      fields: [
                        {
                          name: 'password',
                          kind: 'password',
                          label: { zh: '口令', en: 'Password' },
                        },
                      ],
                    },
                  },
                }
              : {}),
          });
        return response(200, localBoot);
      },
      {
        locale,
        settingsFetch: async (url) => {
          if (String(url).endsWith('/view.js')) {
            return new Response(
              readFileSync(
                new URL(
                  '../../../clients/dsh-plugin/src/settings/view.js',
                  import.meta.url,
                ),
                'utf8',
              ),
            );
          }
          return response(200, {
            mode: 'connect',
            platformOrigin: 'https://example.com',
            activeOrigin: 'https://example.com',
          });
        },
      },
    );
    let main;
    try {
      main = host.mount('main');
      const settings = await openSettings(main);
      await flush();
      assert.equal(settings.root.hidden, false);
      button(settings.root, locale === 'en' ? 'Sign in' : '登录').click();
      await flush();
      assert.equal(commands.length, 1);
      assert.deepEqual(JSON.parse(commands[0].body), {
        action: 'inline_login',
      });
      assert.equal(commands[0].headers['x-auriga-csrf'], 'settings-login-csrf');
      assert.equal(
        main.root.querySelector('#auriga-panel-settings').hidden,
        true,
      );
      const overview = main.root.querySelector('#auriga-panel-overview');
      assert.equal(overview.hidden, false);
      assert.ok(overview.querySelector('input[type="password"]'));
      assert.deepEqual(host.navigations, []);
    } finally {
      main?.close();
      host.dispose();
      host.close();
    }
  });

test('plugin loading and panel mounting preserve the native host title', async () => {
  const title = 'DSH · Existing session';
  const host = surfaceHost(async () => response(), { title });
  try {
    assert.equal(host.document.title, title);
    const main = host.mount('main');
    const settings = await openSettings(main);
    await flush();
    assert.equal(host.document.title, title);
    main.close();
    settings.close();
    assert.equal(host.document.title, title);
  } finally {
    host.dispose();
    host.close();
  }
});

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
    // The public sidebar brand slots carry the Auriga DSH mark and name.
    const mark = host.registrations.get('sidebar.brand.mark');
    const name = host.registrations.get('sidebar.brand.name');
    assert.equal(mark.meta.name, 'sidebar.brand.mark');
    assert.equal(mark.meta.priority, -1);
    assert.equal(name.meta.name, 'sidebar.brand.name');
    assert.equal(name.meta.priority, -1);
    const glyph = mark.Component({ size: 24 });
    assert.equal(glyph.type, 'svg');
    assert.equal(glyph.props.width, 24);
    assert.equal(glyph.props['aria-hidden'], true);
    const text = (node) =>
      typeof node === 'string'
        ? node
        : (node?.children ?? []).map(text).join(' ');
    assert.equal(text(name.Component({})), 'Auriga DSH');
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
      ['sidebar.panellist', 'sidebar.brand.mark', 'sidebar.brand.name', 'main'],
    );
    assert.equal(
      host.registrations.get('main').meta.key,
      host.registrations.get('sidebar.panellist').meta.id,
    );
    const settings = await openSettings(host);
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
    assert.equal(host.counts.registered, 4);
    assert.equal(host.counts.unregistered, 4);
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
    const settings = await openSettings(main);
    await flush();
    button(settings.root, '刷新状态').click();
    await flush();
    settings.close();
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.context, context);
    assert.equal(panel.dataset.resourceTab, 'mine');
    assert.equal(panel.dataset.search, 'retained');
    // Main owns one native tab list; resource tabs live in it, not in the element.
    assert.equal(main.root.querySelectorAll('[role="tablist"]').length, 1);
    assert.equal(panel.getAttribute('tabs'), 'host');
    for (const tab of main.root.querySelectorAll('[role="tab"]')) {
      tab.click();
      await flush();
      const pane = main.root.querySelector(
        '#' + tab.getAttribute('aria-controls'),
      );
      assert.equal(pane.getAttribute('aria-labelledby'), tab.id);
      assert.equal(pane.hidden, false);
      assert.equal(
        main.root.querySelectorAll('[role="tabpanel"]:not([hidden])').length,
        1,
      );
      assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    }
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
      settings = await openSettings(main);
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
      const settings = await openSettings(host);
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
    // Call 1 is the main surface; settings in its tab bootstraps next.
    calls++;
    if (calls === 2)
      throw Object.assign(new Error('private failure'), {
        reason: 'private-token https://private.example.com',
      });
    if (calls === 3) {
      oldSignal = init.signal;
      return new Promise((resolve) => {
        late = resolve;
      });
    }
    return response();
  });
  try {
    const first = await openSettings(host);
    await flush();
    assert.doesNotMatch(
      first.root.textContent,
      /private-token|private\.example/,
    );
    button(first.root, '重试').click();
    await flush();
    assert.equal(calls, 3);
    first.close();
    assert.equal(oldSignal.aborted, true);
    const next = await openSettings(host);
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
    const settings = await openSettings(host);
    await flush();
    host.expire(15_000);
    await flush();
    assert.equal(signal.aborted, true);
    assert.ok(button(settings.root, '重试'));
    button(settings.root, '重试').click();
    await flush();
    settings.close();
    host.dispose();
    await flush();
    assert.equal(signal.aborted, true);
    assert.equal(host.counts.timers.size, 0);
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
      const settings = await openSettings(host);
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
      settings = await openSettings(main);
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
    assert.equal(
      calls.filter((url) => !url.endsWith('/auriga/shell/status')).length,
      1,
    );
    assert.equal(
      calls.filter((url) => url.endsWith('/auriga/shell/status')).length,
      1,
    );
    assert.doesNotMatch(calls.join(' '), /local-panel/);
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
    assert.equal(host.counts.timers.size, 1);
    const [poll] = host.counts.timers.values();
    assert.equal(poll.delay, 5000);
    assert.equal(poll.repeat, true);
    main.close();
    assert.equal(host.counts.timers.size, 0);
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
      settings = await openSettings(main);
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

test('managed resource context uses the existing channel without browser cookies and exposes only the public inventory reader', async () => {
  const calls = [];
  let reads = 0;
  const inventory = {
    entries: [
      {
        entryId: 'example',
        moduleName: 'example-plugin',
        enabled: true,
        fiberPhase: 'active',
      },
    ],
  };
  const host = surfaceHost(
    async (url, init) => {
      calls.push({ url: String(url), init });
      return response(200, boot());
    },
    {
      remote: {
        pluginInventory: {
          async list() {
            reads++;
            return { ok: true, value: inventory };
          },
        },
      },
    },
  );
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    await panel.context.fetch('/api/resource/v1/packages?limit=50', {
      method: 'GET',
    });
    assert.equal(
      calls.at(-1).url,
      'https://instance.example.com/api/resource/v1/packages?limit=50',
    );
    assert.equal(calls.at(-1).init.credentials, 'omit');
    assert.deepEqual(await panel.context.listInstalled(), inventory);
    assert.equal(reads, 1);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('public inventory Remote failures are rejected without exposing host errors', async () => {
  for (const result of [
    { ok: false, error: { code: 'private-code', message: 'private-message' } },
    { entries: [] },
    undefined,
    new Error('private-transport-error'),
  ]) {
    const host = surfaceHost(async () => response(), {
      remote: {
        pluginInventory: {
          async list() {
            if (result instanceof Error) throw result;
            return result;
          },
        },
      },
    });
    try {
      const main = host.mount('main');
      await flush();
      const panel = main.root.querySelector('auriga-team-panel');
      await assert.rejects(
        () => panel.context.listInstalled(),
        (error) =>
          error.message === 'Auriga DSH surface unavailable' &&
          error.reason === 'unavailable',
      );
      main.close();
      host.dispose();
    } finally {
      host.close();
    }
  }
});
test('managed renderer resource identity and local server channel survive the real browser adapter', async () => {
  const resourceIdentity = {
    instanceId: '01900000-0000-7000-8000-000000000001',
    tenantId: '01900000-0000-7000-8000-000000000002',
    ownerId: '01900000-0000-7000-8000-000000000003',
  };
  const calls = [];
  const host = surfaceHost(async (url, init) => {
    calls.push({ url: String(url), init });
    return response(200, {
      ...boot(),
      fetchBase: '/auriga/resources/',
      resourceIdentity,
    });
  });
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    assert.deepEqual(
      JSON.parse(JSON.stringify(panel.context.resourceIdentity)),
      resourceIdentity,
    );
    await panel.context.fetch('/api/resource/v1/distributions', {
      method: 'POST',
      body: '{}',
    });
    assert.equal(
      calls.at(-1).url,
      'https://instance.example.com/auriga/resources/api/resource/v1/distributions',
    );
    assert.equal(calls.at(-1).init.credentials, 'omit');
    assert.equal(calls.at(-1).init.headers, undefined);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

// React 18 serializes false on custom elements; hidden="false" still hides them.
test('resource tabs expose the trusted element with React 18 attribute semantics', async () => {
  const host = surfaceHost(async () => response());
  let main;
  try {
    main = host.mount('main');
    await flush();
    for (const label of ['market', 'mine', 'overview', 'market']) {
      main.root.querySelector('#auriga-tab-' + label).click();
      await flush();
      const el = main.root.querySelector('auriga-team-panel');
      assert.equal(el.hidden, false);
      if (label !== 'overview')
        assert.equal(el.closest('[role="tabpanel"]').hidden, false);
    }
  } finally {
    main?.close();
    host.dispose();
    host.close();
  }
});
