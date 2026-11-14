import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, response } from './shell-host.mjs';
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
      const input = root.querySelector('input[aria-label="平台地址"]');
      const save = [...root.querySelectorAll('button')].find(
        (button) => button.textContent === '保存',
      );
      if (
        input?.value === 'https://example.com' &&
        /alice.*Auriga DSH/.test(root.textContent) &&
        save &&
        !save.disabled
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
      settings = host.mount('settings.section');
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
    assert.match(settings.root.textContent, /本机端点.*已收消息3/);
    assert.match(settings.root.textContent, /alice.*Auriga DSH/);
    const input = settings.root.querySelector('input[aria-label="平台地址"]');
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
        /本机端点00000000-0000-4000-8000-000000000001/,
      );
      assert.match(settings.root.textContent, /已收消息3/);
      assert.doesNotMatch(settings.root.textContent, /尚未配置总部连接/);
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
          settings = host.mount('settings.section');
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
