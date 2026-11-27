import assert from 'node:assert/strict';
import { test } from 'node:test';
import { installShellUi } from '../../src/shell/host.js';
import { createOverviewUI } from '../../src/shell/overview.js';

test('installShellUi serves overview and icons modules', async () => {
  const registrations = [];
  const ctx = {
    inject(_deps, fn) {
      return fn({
        profileContext: { dir: '/tmp/auriga-shell-ui-test' },
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
  const capture = async (path) => {
    /** @type {{status?:number,headers?:Record<string,string>,body?:Buffer}} */
    const out = {};
    await handler(
      { method: 'GET', url: path },
      {
        writeHead(status, headers) {
          out.status = status;
          out.headers = headers;
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
