import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, response } from './shell-host.mjs';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { Buffer } from 'node:buffer';
import { Socket } from 'node:net';
import { createSettingsStore } from '../../../clients/dsh-plugin/src/settings/store.js';
import { createSettingsHandler } from '../../../clients/dsh-plugin/src/settings/host.js';
import { mountLocalLoginPage } from '../../../clients/dsh-plugin/src/login/page.js';
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

test('registered settings loads its real authenticated asset and saves through the real API without replacing main', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-shell-settings-'));
  const socket = new Socket();
  let host;
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
      async (url) =>
        response(
          200,
          String(url).endsWith('/status')
            ? {
                loggedIn: true,
                connected: true,
                account: { subject: 'alice' },
                csrf: 'native-csrf',
              }
            : localBoot(true),
        ),
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
      settings = host.mount('settings.section');
    // The asset route reads a file asynchronously; drain actual I/O, not a fake export.
    for (let i = 0; i < 100 && !settings.root.querySelector('input'); i++)
      await flush();
    const panel = main.root.querySelector('auriga-team-panel'),
      context = panel.context;
    assert.match(settings.root.textContent, /本机通信端点.*已接受的消息3/);
    assert.match(settings.root.textContent, /alice.*Auriga DSH/);
    const input = settings.root.querySelector('input[aria-label="平台地址"]');
    assert.ok(input, 'real settings asset evaluated in registered native slot');
    input.value = 'https://next.example.com';
    input.onchange({ target: input });
    const select = settings.root.querySelector(
      'select[aria-label="默认部署位置"]',
    );
    select.value = 'local';
    select.onchange({ target: select });
    await flush();
    await [...settings.root.querySelectorAll('button')]
      .find((b) => b.textContent === '保存')
      .onclick();
    await flush();
    assert.match(settings.root.textContent, /设置已保存/);
    assert.deepEqual((await createSettingsStore(directory)).snapshot(), {
      platformOrigin: 'https://next.example.com',
      deployment: 'local',
    });
    assert.equal(
      calls.find((c) => c.url.endsWith('/save')).init.credentials,
      'same-origin',
    );
    assert.ok(calls.some((c) => c.url.endsWith('/view.js')));
    // Both merged entry points coexist: the shell keeps LOGIN's explicit
    // confirmation page while SETTINGS retains its reviewed view and handlers.
    const logout = [...settings.root.querySelectorAll('button')].find(
      (b) => b.textContent === '退出本机登录',
    );
    assert.ok(logout);
    logout.click();
    assert.deepEqual(host.navigations, [
      'https://instance.example.com/i/example/auriga/local-login?logout=1',
    ]);
    const logoutRoot = host.document.createElement('main');
    host.document.body.append(logoutRoot);
    let logoutRequests = 0;
    const returned = [];
    const logoutPage = mountLocalLoginPage(logoutRoot, {
      logout: new URL(host.navigations[0]).searchParams.get('logout') === '1',
      fetch: async () => {
        logoutRequests++;
        throw new Error('unexpected logout request');
      },
      navigate: (path) => returned.push(path),
    });
    try {
      assert.match(logoutRoot.textContent, /确认退出本机登录/);
      assert.equal(logoutRequests, 0, 'navigation and mount never sign out');
      logoutRoot.querySelector('[data-action=return]').click();
      assert.deepEqual(returned, ['/']);
      assert.equal(logoutRequests, 0);
    } finally {
      logoutPage.dispose();
      logoutRoot.remove();
    }
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
    host?.close();
    socket.destroy();
    await rm(directory, { recursive: true, force: true });
  }
});
test('local connection state drives the real shell entry and endpoint/acceptance settings', async () => {
  for (const connected of [true, false]) {
    const host = surfaceHost(async () => response(200, localBoot(connected)));
    try {
      const main = host.mount('main'),
        settings = host.mount('settings.section');
      await flush();
      const start = [...main.root.querySelectorAll('button')].find(
        (b) => b.textContent === '在会话中使用 Team 工具',
      );
      assert.ok(start);
      assert.equal(start.disabled, !connected);
      assert.match(
        main.root.textContent,
        connected ? /平台已连接/ : /平台连接暂不可用/,
      );
      assert.match(
        settings.root.textContent,
        /本机通信端点00000000-0000-4000-8000-000000000001/,
      );
      assert.match(settings.root.textContent, /已接受的消息3/);
      assert.doesNotMatch(settings.root.textContent, /尚未配置总部连接/);
      main.close();
      settings.close();
      host.dispose();
    } finally {
      host.close();
    }
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
          ? { loggedIn: true, csrf: 'native-csrf' }
          : localBoot(true),
      ),
    {
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
      settings = host.mount('settings.section');
    await flush();
    const panel = main.root.querySelector('auriga-team-panel');
    const context = panel.context;
    const inbox = main.root.querySelector('.auriga-consent');
    assert.match(inbox.textContent, /<script>untrusted<\/script>/);
    assert.equal(inbox.querySelector('script'), null);
    assert.equal(
      settings.root.querySelectorAll('.auriga-consent select').length,
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
