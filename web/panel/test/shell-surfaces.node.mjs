import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, boot, response } from './shell-host.mjs';

const button = (root, label) =>
  [...root.querySelectorAll('button')].find((el) => el.textContent === label);

test('independent native slots: settings uses trusted bootstrap, distinct versions and no editing or scripts', async () => {
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
    assert.match(settings.root.textContent, /插件版本0\.2\.3.*界面版本2\.4\.6/);
    assert.match(settings.root.textContent, /账号、认证与安全/);
    assert.equal(
      settings.root.querySelector(
        'input,select,form,nav,[role="tab"],[role="dialog"]',
      ),
      null,
    );
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
      /Headquarters connection.*System managed.*Plugin version0\.2\.3/,
    );
    const panel = main.root.querySelector('auriga-team-panel');
    assert.equal(panel.context.locale, 'en');
    assert.equal(panel.context.theme, 'dark');
    host.locale('zh-CN');
    host.theme('light');
    await flush();
    assert.match(settings.root.textContent, /总部连接/);
    assert.equal(panel.context.theme, 'light');
    assert.match(
      host.document.querySelector('style').textContent,
      /var\(--dsw-alias-bg-layer-1\)/,
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
    button(main.root, '重试').click();
    await flush();
    const second = host.document.querySelector('script');
    second.onload();
    host.expire(10_000);
    await flush();
    assert.equal(second.isConnected, false);
    button(main.root, '重试').click();
    await flush();
    const third = host.document.querySelector('script');
    third.onload();
    host.window.customElements.define(
      'auriga-team-panel',
      class extends host.window.HTMLElement {},
    );
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
    assert.match(settings.root.textContent, /尚未配置总部连接/);
    assert.match(settings.root.textContent, /界面来源插件安装包/);
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
