import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  surfaceHost,
  flush,
  response,
  openSettings,
  ensureOverlay,
} from './shell-host.mjs';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { Buffer } from 'node:buffer';
import { Socket } from 'node:net';
import { setTimeout, clearTimeout } from 'node:timers';
import { createSettingsStore } from '../../../clients/dsh-plugin/src/settings/store.js';
import { createSettingsHandler } from '../../../clients/dsh-plugin/src/settings/host.js';

const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

// One test-only deadline covers asset I/O, status consumption and DOM commit.
// A generic input exists before Promise.all(state, status) has settled.
function waitForSettings(host, root, statusBody, timeout = 5000) {
  return new Promise((resolve, reject) => {
    let statusComplete = false;
    let settled = false;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      observer.disconnect();
      clearTimeout(timer);
      if (error) reject(error);
      else resolve();
    };
    const check = () => {
      if (settled || !statusComplete) return;
      const input =
        root.querySelector('#auriga-platform-address') ||
        root.querySelector('input[aria-label="云平台地址"]') ||
        root.querySelector('input[aria-label="平台地址"]');
      const save = [...root.querySelectorAll('button')].find(
        (button) => button.textContent === '保存',
      );
      // Save stays disabled while the loaded address already matches state.
      if (
        input?.value === 'https://example.com' &&
        /alice/.test(root.textContent) &&
        save
      )
        finish();
    };
    const observer = new host.window.MutationObserver(check);
    observer.observe(root, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
    });
    const timer = setTimeout(
      () =>
        finish(new Error('settings readiness timed out: ' + root.textContent)),
      timeout,
    );
    statusBody.then(({ ok, data }) => {
      try {
        assert.equal(ok, true, 'settings status response must succeed');
        assert.equal(data.loggedIn, true);
        assert.equal(data.connected, true);
        assert.equal(data.account?.subject, 'alice');
        statusComplete = true;
        check();
      } catch (error) {
        finish(error);
      }
    }, finish);
  });
}

const localBoot = (connected) => ({
  mode: 'connect',
  protocol: 1,
  pluginVersion: '0.0.0',
  status: connected ? 'connected' : 'disconnected',
  connection: {
    connected,
    endpointId: '00000000-0000-4000-8000-000000000001',
    received: 3,
  },
  localPanel: {
    source: 'package',
    version: '0.0.0',
    entry: 'auriga/local-panel.js',
    integrity: 'sha384-YQ==',
  },
  fetchBase: null,
});

test('desktop login starts verified market prefetch before the Auriga main panel is opened', async () => {
  let loggedIn = false;
  let generation = 'a'.repeat(64);
  const ownerId = '01900000-0000-7000-8000-000000000001';
  const prefetched = [];
  const cleared = [];
  const host = surfaceHost(
    async (url) => {
      const path = new URL(url).pathname;
      if (path.endsWith('/auriga/shell/status'))
        return response(200, {
          loggedIn,
          connected: loggedIn,
          account: loggedIn
            ? { subject: ownerId, catalogGeneration: generation }
            : null,
        });
      if (path.endsWith('/auriga/uac/status'))
        return response(200, { available: false });
      assert.ok(path.endsWith('/auriga/bootstrap'), path);
      return response(200, localBoot(true));
    },
    { defined: false, accountLauncher: true, url: 'http://127.0.0.1:12345/' },
  );
  try {
    const launcher = host.mount('settings.launcher');
    await flush();
    assert.equal(
      host.document.querySelector('script[data-auriga-panel]'),
      null,
    );
    loggedIn = true;
    host.expire(15000);
    await flush();
    const script = host.document.querySelector('script[data-auriga-panel]');
    assert.ok(script);
    assert.equal(prefetched.length, 0);
    host.completeScript(
      script,
      class extends host.window.HTMLElement {
        static prefetchCatalog(context) {
          prefetched.push(context.catalogSession);
        }
        static clearCatalog(scope) {
          cleared.push(scope);
        }
      },
    );
    await flush();
    assert.equal(prefetched.length, 1);
    assert.equal(prefetched[0].ownerId, ownerId);
    assert.equal(prefetched[0].generation, generation);
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    assert.equal(panel.context.catalogSession, prefetched[0]);
    host.expire(15000);
    await flush();
    assert.equal(prefetched.length, 1);
    generation = 'b'.repeat(64);
    host.expire(4000);
    await flush();
    assert.equal(cleared[0], prefetched[0]);
    assert.equal(prefetched.length, 2);
    assert.equal(prefetched[1].generation, generation);
    assert.equal(panel.context.catalogSession, prefetched[1]);
    loggedIn = false;
    host.expire(4000);
    await flush();
    assert.equal(cleared[1], prefetched[1]);
    assert.equal(panel.context, undefined);
    main.close();
    launcher.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('a desktop login prefetch survives the Auriga main panel closing during verified bundle loading', async () => {
  let loggedIn = false;
  const prefetched = [];
  const host = surfaceHost(
    async (url) => {
      const path = new URL(url).pathname;
      if (path.endsWith('/auriga/shell/status'))
        return response(200, {
          loggedIn,
          account: loggedIn ? { subject: 'fixture-member' } : null,
        });
      if (path.endsWith('/auriga/uac/status'))
        return response(200, { available: false });
      return response(200, localBoot(true));
    },
    { defined: false, accountLauncher: true, url: 'http://127.0.0.1:12345/' },
  );
  try {
    const main = host.mount('main');
    const launcher = host.mount('settings.launcher');
    await flush();
    const first = host.document.querySelector('script[data-auriga-panel]');
    assert.ok(first);
    loggedIn = true;
    host.expire(15000);
    await flush();
    assert.equal(
      host.document.querySelector('script[data-auriga-panel]'),
      first,
    );
    main.close();
    await flush();
    const retry = host.document.querySelector('script[data-auriga-panel]');
    assert.ok(retry && retry !== first);
    host.completeScript(
      retry,
      class extends host.window.HTMLElement {
        static prefetchCatalog(context) {
          prefetched.push(context.catalogSession.ownerId);
        }
      },
    );
    await flush();
    assert.deepEqual(prefetched, ['fixture-member']);
    launcher.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('connect resource elements remain hidden until platform sign-in', async () => {
  const host = surfaceHost(async (url) =>
    response(
      200,
      String(url).endsWith('/auriga/shell/status')
        ? {
            configured: true,
            loggedIn: false,
            connected: false,
            platform: 'signed_out',
            brokerAvailable: true,
          }
        : localBoot(false),
    ),
  );
  let main;
  try {
    main = host.mount('main');
    await flush();
    for (const name of ['market', 'mine']) {
      main.root.querySelector('#auriga-tab-' + name).click();
      await flush();
      const element = main.root.querySelector('auriga-team-panel');
      assert.ok(element);
      assert.equal(element.hidden, true);
      assert.ok(element.hasAttribute('hidden'));
    }
  } finally {
    main?.close();
    host.dispose();
    host.close();
  }
});

async function exerciseRealSettings(delayed) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-shell-settings-'));
  const socket = new Socket();
  let host;
  const shellCommands = [];
  const gate = deferred(),
    entered = deferred(),
    statusBody = deferred();
  try {
    const store = await createSettingsStore(directory, 'https://example.com');
    const handler = createSettingsHandler({
      uiOrigin: 'https://instance.example.com',
      store,
      auth: { requestRejection: () => undefined },
      activeOrigin: 'https://example.com',
      deviceName: 'Auriga DSH',
    });
    const calls = [];
    host = surfaceHost(
      async (url, init = {}) => {
        if (new URL(url).pathname.endsWith('/shell/command'))
          shellCommands.push(init);
        const data = String(url).endsWith('/status')
          ? {
              loggedIn: true,
              connected: true,
              account: { subject: 'alice', displayName: 'alice' },
              network: { connected: true, state: 'ready' },
              connection: localBoot(true).connection,
              csrf: 'native-csrf',
            }
          : localBoot(true);
        const result = response(200, data);
        if (new URL(url).pathname.endsWith('/shell/status')) {
          result.json = async () => {
            if (delayed === 'status') {
              entered.resolve();
              await gate.promise;
            }
            statusBody.resolve({ ok: result.ok, data });
            return data;
          };
        }
        return result;
      },
      {
        async settingsFetch(url, init = {}) {
          calls.push({ url: String(url), init });
          const req = Readable.from(init.body ? [Buffer.from(init.body)] : []);
          Object.assign(req, {
            url: new URL(url).pathname.replace('/i/example', ''),
            method: init.method ?? 'GET',
            socket,
            headers: {
              host: 'instance.example.com',
              origin: 'https://instance.example.com',
              ...Object.fromEntries(
                Object.entries(init.headers ?? {}).map(([k, v]) => [
                  k.toLowerCase(),
                  v,
                ]),
              ),
            },
          });
          req.rawHeaders = Object.entries(req.headers).flat();
          let status, body;
          if (
            delayed === 'asset' &&
            new URL(url).pathname.endsWith('/view.js')
          ) {
            entered.resolve();
            await gate.promise;
          }
          await handler(req, {
            writeHead(code) {
              status = code;
            },
            end(value) {
              body = value;
            },
          });
          return {
            status,
            ok: status >= 200 && status < 300,
            json: async () => JSON.parse(body),
            text: async () => String(body),
          };
        },
      },
    );
    const main = host.mount('main'),
      settings = await openSettings(main);
    const ready = waitForSettings(host, settings.root, statusBody.promise);
    if (delayed !== 'none') {
      // Hold the real route/body beyond every turn of the old guard.
      // Release by an explicit event, never by guessed I/O latency.
      await Promise.race([entered.promise, ready]);
      let completed = false;
      void ready.then(
        () => {
          completed = true;
        },
        () => {},
      );
      for (let i = 0; i < 100; i++) await flush();
      assert.equal(
        completed,
        false,
        'pending real resource/status is not ready',
      );
      gate.resolve();
    }
    await ready;
    const panel = main.root.querySelector('auriga-team-panel'),
      context = panel.context;
    const openSettingsNav = async (label) => {
      const item = [...settings.root.querySelectorAll('button')].find(
        (button) => button.textContent === label,
      );
      assert.ok(item, label);
      item.click();
      await flush();
    };
    await openSettingsNav('消息网络');
    assert.match(settings.root.textContent, /本机端点.*已收消息3/);
    await openSettingsNav('账户');
    assert.match(settings.root.textContent, /alice/);
    await openSettingsNav('平台连接');
    const input =
      settings.root.querySelector('#auriga-platform-address') ||
      settings.root.querySelector('input[aria-label="云平台地址"]') ||
      settings.root.querySelector('input[aria-label="平台地址"]');
    assert.ok(input, 'real settings asset evaluated in registered native slot');
    input.value = 'https://next.example.com';
    input.onchange({ target: input });
    await flush();
    await [...settings.root.querySelectorAll('button')]
      .find((b) => b.textContent === '保存')
      .onclick();
    await flush();
    assert.match(settings.root.textContent, /已保存/);
    assert.deepEqual((await createSettingsStore(directory)).snapshot(), {
      platformOrigin: 'https://next.example.com',
      deployment: 'cloud',
    });
    assert.equal(
      calls.find((c) => c.url.endsWith('/save')).init.credentials,
      'same-origin',
    );
    assert.ok(calls.some((c) => c.url.endsWith('/view.js')));
    // The explicit native settings action delegates to the shell; no plugin
    // login page or browser-selected navigation participates.
    await openSettingsNav('账户');
    const logout = [...settings.root.querySelectorAll('button')].find(
      (button) => button.textContent === '退出登录',
    );
    assert.ok(logout);
    logout.click();
    await flush();
    assert.deepEqual(host.navigations, []);
    assert.equal(shellCommands.length, 1);
    assert.deepEqual(JSON.parse(shellCommands[0].body), { action: 'logout' });
    assert.equal(shellCommands[0].headers['x-auriga-csrf'], 'native-csrf');
    assert.equal(shellCommands[0].credentials, 'same-origin');
    assert.equal(shellCommands[0].redirect, 'error');
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.context, context);
    settings.close();
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.context, context);
    main.close();
    host.dispose();
    assert.equal(host.counts.timers.size, 0);
    assert.equal(host.subscriptions.size, 0);
  } finally {
    gate.resolve();
    host?.close();
    socket.destroy();
    await rm(directory, { recursive: true, force: true });
  }
}

for (const delayed of ['none', 'asset', 'status']) {
  test(
    'registered settings loads its real authenticated asset and saves through the real API without replacing main: ' +
      delayed,
    () => exerciseRealSettings(delayed),
  );
}

test('local connection state drives the real shell entry and endpoint/acceptance settings', async () => {
  for (const connected of [true, false]) {
    const host = surfaceHost(async (url) =>
      String(url).endsWith('/auriga/shell/status')
        ? response(200, {
            ...localBoot(connected),
            configured: true,
            loggedIn: true,
            connected,
            platform: 'authenticated',
            brokerAvailable: true,
            csrf: 'local-csrf',
            account: { subject: 'alice' },
          })
        : response(200, localBoot(connected)),
    );
    try {
      const main = host.mount('main'),
        settings = await openSettings(main);
      await flush();
      await flush();
      const network = [...settings.root.querySelectorAll('button')].find(
        (button) => button.textContent === '消息网络',
      );
      assert.ok(network);
      network.click();
      await flush();
      assert.match(
        settings.root.textContent,
        /本机端点00000000-0000-4000-8000-000000000001/,
      );
      assert.match(settings.root.textContent, /已收消息3/);
      assert.doesNotMatch(settings.root.textContent, /尚未配置总部连接/);
      const tabs = [...main.root.querySelectorAll('.auriga-nav-item')];
      assert.deepEqual(
        tabs.map((tab) => tab.getAttribute('aria-label')),
        ['概览', '资源市场', '我的资源', 'Team 工具', '收件箱', '设置'],
      );
      tabs[3].click();
      await flush();
      await flush();
      const start = [...main.root.querySelectorAll('button')].find(
        (b) => b.textContent === '在会话中使用 Team 工具',
      );
      // Team tools render only with a connected platform; otherwise the tab
      // shows the platform state and the next step instead of a dead button.
      assert.equal(!!start, connected, main.root.textContent);
      assert.match(
        main.root.textContent,
        connected ? /平台已连接/ : /已登录，通信未就绪.*前往概览/,
      );
      main.close();
      settings.close();
      host.dispose();
    } finally {
      host.close();
    }
  }
});
test('desktop resource port uses current local CSRF and keeps member credentials out of browser requests', async () => {
  const calls = [];
  let loggedIn = true;
  const host = surfaceHost(async (url, init) => {
    const path = new URL(url).pathname;
    calls.push({ path, init });
    if (path.endsWith('/shell/status'))
      return response(200, { loggedIn, csrf: 'test-csrf' });
    if (path.includes('/resources/local/')) return response(200, { ok: true });
    return response(200, {
      ...localBoot(true),
      memberResourcesAvailable: true,
    });
  });
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    const port = panel.context.memberResources;
    assert(port);
    await port.list();
    const input = {
      packageId: 'package',
      versionId: 'version',
      action: 'install',
      key: 'intent',
    };
    await port.apply(input);
    const post = calls.find(({ path }) =>
      path.endsWith('/resources/local/actions'),
    );
    assert.equal(post.init.method, 'POST');
    assert.equal(post.init.headers['X-Auriga-CSRF'], 'test-csrf');
    assert.deepEqual(JSON.parse(post.init.body), input);
    assert.equal(post.init.redirect, 'error');
    assert.equal(post.init.credentials, 'same-origin');
    assert.equal(post.init.headers.authorization, undefined);
    loggedIn = false;
    await assert.rejects(port.apply(input), { reason: 'auth' });
    assert.equal(
      calls.filter(({ path }) => path.endsWith('/resources/local/actions'))
        .length,
      1,
    );
    main.close();
    host.dispose();
    assert.equal(host.counts.timers.size, 0);
  } finally {
    host.close();
  }
});
test('connection bootstrap without an explicit validated status cannot claim connected', async () => {
  const value = localBoot(true);
  delete value.connection;
  const host = surfaceHost(async () => response(200, value));
  try {
    const main = host.mount('main');
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), null);
    assert.match(main.root.textContent, /验证未通过/);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

for (const consent of [false, undefined])
  test(
    'R2 shell omits unsupported first-contact UI and never reads a snapshot: ' +
      consent,
    async () => {
      let snapshots = 0;
      const host = surfaceHost(
        async (url) =>
          response(
            200,
            new URL(url).pathname.endsWith('/status')
              ? {
                  loggedIn: true,
                  connected: true,
                  account: { subject: 'alice', displayName: 'Alice' },
                  capabilities: { consent },
                }
              : localBoot(true),
          ),
        {
          consentFetch() {
            snapshots++;
            throw Error('unsupported consent');
          },
        },
      );
      try {
        const main = host.mount('main'),
          settings = await openSettings(main);
        await flush();
        assert.equal(main.root.querySelector('.auriga-consent'), null);
        assert.equal(settings.root.querySelector('.auriga-consent'), null);
        assert.doesNotMatch(settings.root.textContent, /首联授权|暂时无法加载/);
        assert.equal(snapshots, 0);
        main.close();
        settings.close();
        host.dispose();
        assert.equal(host.subscriptions.size, 0);
        assert.equal(host.counts.timers.size, 0);
      } finally {
        host.close();
      }
    },
  );

test('consent children use independent hooks and the authenticated HTTP bridge without replacing the main panel', async () => {
  let snapshot = {
    policyRevision: 1,
    defaults: { conversation: 'ask_each', execution: 'ask_each' },
    organization: {
      available: true,
      revision: 'org-1',
      conversation: 'allow',
      execution: 'allow',
    },
    rules: [],
    pending: [
      {
        request: { layer: 'conversation', requestId: 'contact-1' },
        source: { realm: '2', node: '63', userId: 'alice' },
        preview: '<script>untrusted</script>',
      },
    ],
  };
  const writes = [];
  const host = surfaceHost(
    async (url) =>
      response(
        200,
        new URL(url).pathname.endsWith('/status')
          ? {
              loggedIn: true,
              connected: true,
              account: { subject: 'alice' },
              capabilities: { consent: true },
              csrf: 'native-csrf',
            }
          : localBoot(true),
      ),
    {
      url: 'http://127.0.0.1:12345/',
      async consentFetch(_url, init) {
        if (init.method === 'POST') {
          writes.push(init);
          snapshot = { ...snapshot, pending: [] };
          return response(200, { ok: true });
        }
        return response(200, snapshot);
      },
    },
  );
  try {
    const main = host.mount('main'),
      settings = await openSettings(main);
    await flush();
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    const context = panel.context;
    main.root.querySelector('#auriga-tab-inbox')?.click();
    await flush();
    const inbox = main.root.querySelector('.auriga-consent');
    assert.match(inbox.textContent, /<script>untrusted<\/script>/);
    assert.equal(inbox.querySelector('script'), null);
    // Settings unmounts when the activity tab leaves; reopen before nav asserts.
    main.root.querySelector('#auriga-tab-settings')?.click();
    await flush();
    await flush();
    const consentNav = [...settings.root.querySelectorAll('button')].find(
      (button) => button.textContent === '同意策略',
    );
    assert.ok(consentNav);
    consentNav.click();
    await flush();
    await flush();
    assert.equal(
      settings.root.querySelectorAll('.auriga-consent .auriga-setting-select')
        .length,
      2,
    );
    const allow = [...inbox.querySelectorAll('button')].find(
      (element) => element.textContent === '允许本次',
    );
    await allow.onclick();
    await flush();
    assert.equal(writes.length, 1);
    assert.equal(writes[0].headers['X-Auriga-CSRF'], 'native-csrf');
    assert.equal(writes[0].credentials, 'same-origin');
    assert.deepEqual(JSON.parse(writes[0].body), {
      layer: 'conversation',
      requestId: 'contact-1',
      choice: 'allow_once',
      expectedRevision: 1,
    });
    assert.match(
      main.root.querySelector('.auriga-consent').textContent,
      /暂无待处理请求/,
    );
    settings.close();
    assert.equal(main.root.querySelector('auriga-team-panel'), panel);
    assert.equal(panel.context, context);
    main.close();
    host.dispose();
    assert.equal(host.subscriptions.size, 0);
    assert.equal(host.counts.timers.size, 0);
  } finally {
    host.close();
  }
});

test('connect resource context mounts the authenticated Node catalogue proxy', async () => {
  const calls = [];
  const host = surfaceHost(async (url, init) => {
    calls.push({ url: String(url), init });
    return response(200, localBoot(true));
  });
  try {
    const main = host.mount('main');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    await panel.context.fetch('/api/resource/v1/packages?limit=50', {
      method: 'GET',
    });
    assert.equal(
      calls.at(-1).url,
      'https://instance.example.com/i/example/auriga/resources/api/resource/v1/packages?limit=50',
    );
    assert.equal(calls.at(-1).init.credentials, 'same-origin');
    assert.equal(calls.at(-1).init.headers, undefined);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});

test('local UAC scan works without a platform: waiting → scanned → signed in, hidden when not configured', async () => {
  for (const configured of [true, false]) {
    const writes = [];
    const challenge = (state) => ({
      id: '00000000-0000-4000-8000-0000000000aa',
      state,
      expiresAt: new Date(Date.now() + 60000).toISOString(),
      qr: { size: 29, path: 'M4 4h1v1h-1z' },
      correlationId: '00000000-0000-4000-8000-0000000000bb',
    });
    let uac = {
      available: configured,
      state: 'signed_out',
      ownerBound: false,
      csrf: 'uac-csrf',
      platformLink: 'none',
    };
    const replies = [
      { ...uac, challenge: challenge('waiting') },
      { ...uac, challenge: challenge('scanned') },
      {
        ...uac,
        state: 'authenticated',
        ownerBound: true,
        empNo: 'A1001',
        loginAt: new Date(0).toISOString(),
        verifiedAt: new Date(0).toISOString(),
        revalidation: 'ok',
        challenge: { ...challenge('authenticated'), qr: undefined },
      },
    ];
    const host = surfaceHost(
      async (url, init = {}) => {
        const path = new URL(String(url)).pathname;
        if (path.endsWith('/auriga/uac/status')) return response(200, uac);
        if (path.includes('/auriga/uac/')) {
          writes.push({ path, init });
          uac = replies.shift();
          return response(200, uac);
        }
        if (path.endsWith('/auriga/shell/status'))
          return response(200, {
            available: true,
            configured: false,
            loggedIn: false,
          });
        return response(200, {
          ...localBoot(false),
          mode: 'standalone',
          status: 'unconfigured',
          connection: undefined,
        });
      },
      { url: 'http://127.0.0.1:12345/' },
    );
    try {
      const main = host.mount('main');
      const overlay = ensureOverlay(host);
      await flush();
      await flush();
      const text = () =>
        (overlay?.root?.textContent || '') + (main.root.textContent || '');
      assert.match(main.root.textContent, /未配置平台/);
      if (!configured) {
        assert.doesNotMatch(text(), /本机身份|获取二维码/);
        main.close();
        overlay?.close();
        host.dispose();
        continue;
      }
      const login = [...main.root.querySelectorAll('button')].find((button) =>
        /登录平台|Sign in to platform/.test(button.textContent),
      );
      assert.ok(login, main.root.textContent);
      login.click();
      await flush();
      await flush();
      assert.match(text(), /本机身份|UAC 扫码|公司扫码登录|请扫码登录/);
      assert.ok(
        overlay.root.querySelector('svg[role="img"] path') ||
          main.root.querySelector('svg[role="img"] path'),
      );
      assert.match(text(), /请扫码登录/);
      assert.equal(writes[0].init.headers['X-Auriga-CSRF'], 'uac-csrf');
      host.expire(2000);
      await flush();
      assert.match(text(), /已扫码，请在手机上确认/);
      host.expire(2000);
      await flush();
      assert.match(
        text(),
        /已登录本机.*工号A1001|本地登录已登录[\s\S]*A1001|欢迎回来，A1001/,
      );
      assert.equal(
        overlay.root.querySelector('svg[role="img"]') ||
          main.root.querySelector('svg[role="img"]'),
        null,
      );
      assert.deepEqual(
        writes.map((w) => w.path.split('/').pop()),
        ['start', 'poll', 'poll'],
      );
      assert.equal(JSON.parse(writes[1].init.body).id, challenge('x').id);
      const authenticated = uac;
      for (const lastOutcome of [
        'revalidation_rejected',
        'revalidation_unreachable',
        'user_logout',
      ]) {
        uac = {
          ...authenticated,
          state: 'signed_out',
          lastOutcome,
          // An older response can retain the previous successful challenge.
          challenge: challenge('authenticated'),
        };
        host.expire(15000);
        await flush();
        await flush();
        // Successful local sign-in closes the overlay when the platform is
        // unconfigured; the overview returns to a signed-out local state.
        assert.doesNotMatch(main.root.textContent, /登录成功|已登录本机/);
        assert.match(main.root.textContent, /本地登录未登录|未登录/);
        assert.ok(
          [...main.root.querySelectorAll('button')].some((button) =>
            /登录平台|Sign in to platform/.test(button.textContent),
          ),
          main.root.textContent,
        );
        assert.equal(overlay.root.querySelector('svg[role="img"]'), null);
      }
      main.close();
      overlay?.close();
      host.dispose();
    } finally {
      host.close();
    }
  }
});

test('Team availability follows the live authenticated shell without reloading bootstrap', async () => {
  const status = {
    available: true,
    configured: true,
    brokerAvailable: true,
    loggedIn: false,
    connected: false,
    platform: 'signed_out',
  };
  let shell = response(200, status);
  let bootstraps = 0;
  const host = surfaceHost(async (url) => {
    const path = new URL(url).pathname;
    if (path.endsWith('/auriga/shell/status')) return shell;
    if (path.endsWith('/auriga/uac/status'))
      return response(200, { available: false });
    if (path.endsWith('/auriga/bootstrap')) bootstraps++;
    return response(200, localBoot(false));
  });
  const main = host.mount('main');
  try {
    await flush();
    await flush();
    main.root.querySelector('#auriga-tab-team').click();
    await flush();
    const team = main.root.querySelector('#auriga-panel-team');
    const overview = main.root.querySelector('#auriga-panel-overview');
    const assertState = (connected, state, overviewState = state) => {
      assert.equal(team.hidden, false);
      assert.equal(
        [...team.querySelectorAll('button')].some(
          (button) =>
            button.textContent === '在会话中使用 Team 工具' && !button.disabled,
        ),
        connected,
      );
      // Overview uses status chips; Team keeps the platformBlocked copy.
      assert.match(overview.textContent, overviewState);
      assert.match(team.textContent, state);
    };
    const update = async (next) => {
      shell = next;
      host.expire(4000);
      await flush();
    };
    const signedIn = { ...status, loggedIn: true, platform: 'authenticated' };
    const connected = { ...signedIn, connected: true };
    assertState(false, /未登录平台/, /云平台.*未连接/);
    await update(response(200, signedIn));
    assertState(false, /已登录，通信未就绪/, /已认证，未连接/);
    await update(response(200, connected));
    assertState(true, /平台已连接/, /云平台.*已连接/);
    await update(response(200, { ...signedIn, communication: 'unavailable' }));
    assertState(false, /已登录，通信未就绪/, /已认证，未连接/);
    await update(response(200, connected));
    assertState(true, /平台已连接/, /云平台.*已连接/);
    await update(
      response(200, { ...status, platform: 'revoked', connected: true }),
    );
    assertState(false, /未登录平台/, /云平台.*未连接/);
    await update(response(200, connected));
    assertState(true, /平台已连接/, /云平台.*已连接/);
    await update(response(503, {}));
    // Overview status chips stay on the last known shell projection; Team
    // falls back to the unreachable platformBlocked copy.
    assertState(false, /平台不可达/, /云平台/);
    assert.equal(bootstraps, 1);
  } finally {
    main.close();
    host.dispose();
    host.close();
  }
});
