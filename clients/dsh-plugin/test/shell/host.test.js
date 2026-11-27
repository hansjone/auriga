import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  createShellUiHandler,
  installShellUi,
} from '../../src/shell/host.js';
import { createOverviewUI } from '../../src/shell/overview.js';

test('installShellUi serves overview and icons modules when admitted', async () => {
  const registrations = [];
  const ctx = {
    inject(_deps, fn) {
      return fn({
        profileContext: { dir: '/tmp/auriga-shell-ui-test' },
        connection: { requestRejection() {} },
        effect(setup) {
          return setup();
        },
        webServer: {
          register(entry) {
            registrations.push(entry);
            return () => {};
          },
        },
      });
    },
    webServer: { register() {} },
  };
  installShellUi(ctx);
  assert.equal(registrations.length, 1);
  assert.equal(registrations[0].path, '/auriga/shell-ui');
  const handler = registrations[0].handler;
  const capture = async (path, headers = {}) => {
    /** @type {{status?:number,headers?:Record<string,string>,body?:Buffer|string}} */
    const out = {};
    await handler(
      {
        method: 'GET',
        url: path,
        headers: { host: '127.0.0.1:18789', ...headers },
        rawHeaders: Object.entries({
          host: '127.0.0.1:18789',
          ...headers,
        }).flatMap(([k, v]) => [k, v]),
      },
      {
        writeHead(status, responseHeaders) {
          out.status = status;
          out.headers = responseHeaders;
        },
        end(body) {
          out.body = body;
        },
      },
    );
    return out;
  };
  const overview = await capture('/auriga/shell-ui/overview.js');
  assert.equal(overview.status, 200);
  assert.match(String(overview.headers['content-type']), /javascript/);
  assert.match(String(overview.body), /createOverviewUI/);
  const icons = await capture('/auriga/shell-ui/icons.js');
  assert.equal(icons.status, 200);
  assert.match(String(icons.body), /createAurigaIcons/);
  const missing = await capture('/auriga/shell-ui/missing.js');
  assert.equal(missing.status, 404);
});

test('shell-ui rejects unauthenticated and cross-origin requests', async () => {
  const denied = createShellUiHandler({
    auth: {
      requestRejection() {
        return 401;
      },
    },
  });
  /** @type {{status?:number,body?:string}} */
  const authOut = {};
  await denied(
    {
      method: 'GET',
      url: '/auriga/shell-ui/overview.js',
      headers: { host: '127.0.0.1:18789' },
      rawHeaders: ['host', '127.0.0.1:18789'],
    },
    {
      writeHead(status) {
        authOut.status = status;
      },
      end(body) {
        authOut.body = String(body);
      },
    },
  );
  assert.equal(authOut.status, 401);
  assert.match(authOut.body ?? '', /auth/);

  const gated = createShellUiHandler({
    auth: { requestRejection() {} },
    uiOrigin: 'https://ui.example',
  });
  /** @type {{status?:number,body?:string}} */
  const originOut = {};
  await gated(
    {
      method: 'GET',
      url: '/auriga/shell-ui/overview.js',
      headers: {
        host: '127.0.0.1:18789',
        origin: 'https://evil.example',
      },
      rawHeaders: [
        'host',
        '127.0.0.1:18789',
        'origin',
        'https://evil.example',
      ],
    },
    {
      writeHead(status) {
        originOut.status = status;
      },
      end(body) {
        originOut.body = String(body);
      },
    },
  );
  assert.equal(originOut.status, 403);
  assert.match(originOut.body ?? '', /forbidden/);
});

test('createOverviewUI renders welcome and capability routes', () => {
  const React = {
    createElement: (type, props, ...children) => {
      const flat = children.flat(Infinity);
      if (typeof type === 'function') {
        const merged = { ...(props ?? {}) };
        if (flat.length === 1) merged.children = flat[0];
        else if (flat.length > 1) merged.children = flat;
        return type(merged);
      }
      return { type, props: props ?? {}, children: flat };
    },
  };
  const ui = createOverviewUI(React);
  const tree = React.createElement(
    ui.OverviewPage,
    null,
    React.createElement(ui.OverviewWelcome, {
      signedIn: true,
      title: '欢迎回来，周同学',
      tagline: 'tag',
    }),
    React.createElement(ui.OverviewCapabilities, {
      items: [
        {
          id: 'market',
          route: 'market',
          icon: 'Store',
          title: '资源市场',
          description: 'desc',
        },
      ],
      onNavigate: () => {},
    }),
  );
  const text = JSON.stringify(tree);
  assert.match(text, /欢迎回来，周同学/);
  assert.match(text, /资源市场/);
  assert.match(text, /auriga-overview/);
});
