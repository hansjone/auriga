import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, response } from './shell-host.mjs';
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
