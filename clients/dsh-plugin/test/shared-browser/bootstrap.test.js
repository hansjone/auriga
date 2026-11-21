import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { TextDecoder } from 'node:util';
import { ReadableStream } from 'node:stream/web';
import { JSDOM } from 'jsdom';
import { browserBootstrap } from '../../src/shared-browser/bootstrap.js';

function page(fetcher) {
  const dom = new JSDOM(
    '<html><head><base href="./"></head><body><div id="root"></div></body></html>',
    {
      url: 'https://example.invalid/instances/member-dsh/',
      runScripts: 'outside-only',
    },
  );
  dom.window.fetch = fetcher;
  dom.window.TextDecoder = TextDecoder;
  dom.window.eval(browserBootstrap);
  return dom;
}

test('native composition uses same-origin trusted POST, decodes split stream frames and cancels reader', async () => {
  const seen = [];
  let cancelled = false;
  const dom = page(async (url, options) => {
    seen.push({ url: url.href, options });
    if (url.pathname.endsWith('/rpc'))
      return Response.json({ ok: true, value: 'native' });
    const encoder = new TextEncoder();
    return new Response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode('{"type":"item","val'));
          controller.enqueue(
            encoder.encode('ue":{"native":true}}\n{"type":"end"}\n'),
          );
        },
        cancel() {
          cancelled = true;
        },
      }),
    );
  });
  const rpc = dom.window.__DSH_TRANSPORT__.rpc;
  assert.equal(
    (await rpc.call('/api', 'session/list', { args: { request: {} } })).value,
    'native',
  );
  const stream = [];
  for await (const value of rpc.open('/api', 'session/follow', { args: {} }))
    stream.push(value);
  assert.deepEqual(JSON.parse(JSON.stringify(stream)), [{ native: true }]);
  assert.equal(cancelled, true);
  assert.equal(
    seen[0].url,
    'https://example.invalid/instances/member-dsh/auriga/shared/browser/rpc',
  );
  assert.equal(seen[0].options.credentials, 'same-origin');
  assert.equal(
    Object.hasOwn(seen[0].options.headers, 'auriga-identity'),
    false,
  );
  assert.equal(Object.hasOwn(seen[0].options.headers, 'authorization'), false);
  await assert.rejects(rpc.call('/credentials', 'read', {}));
  dom.window.close();
});

test('remote stream failures and aborted late frames do not become assistant messages', async () => {
  const encoder = new TextEncoder();
  const dom = page(
    async () =>
      new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(
              encoder.encode(
                '{"type":"error","error":{"code":"shared.revoked","message":"revoked","details":{}}}\n',
              ),
            );
            controller.close();
          },
        }),
      ),
  );
  const iterator = dom.window.__DSH_TRANSPORT__.rpc.open(
    '/api',
    'session/follow',
    { args: {} },
  );
  await assert.rejects(
    iterator.next(),
    (error) => error.dshRemoteStreamFailure?.code === 'shared.revoked',
  );
  const abort = new AbortController();
  abort.abort();
  await assert.rejects(
    dom.window.__DSH_TRANSPORT__.rpc.call(
      '/api',
      'session/list',
      { args: {} },
      abort.signal,
    ),
  );
  dom.window.close();
});

const root = process.env.AURIGA_REACT_ROOT;
test(
  'native slot messaging stays off by default, can explicitly enable, and can disable during provider outage',
  {
    skip: root
      ? false
      : 'AURIGA_REACT_ROOT required for the installed React 18 DOM fixture',
  },
  async (t) => {
    const require = createRequire(join(root, 'package.json'));
    const React = require('react');
    const dom = page(async (_url, options) => {
      const envelope = JSON.parse(options.body);
      if (envelope.endpoint === 'aurigaShared/messagingSessions')
        return Response.json({
          ok: true,
          value: [{ sessionId: 'own', label: '本人会话' }],
        });
      const request = envelope.payload.args.request;
      calls.push(request);
      if (Object.hasOwn(request, 'enabled')) enabled = request.enabled;
      return Response.json({
        ok: true,
        value: {
          sessionId: 'own',
          address: 'dsh:2/3/4',
          receiveEnabled: enabled,
          desiredReceiveEnabled: enabled,
          pending: false,
          available: !enabled,
        },
      });
    });
    const calls = [];
    let enabled = false;
    const before = {
      window: globalThis.window,
      document: globalThis.document,
      navigator: Object.getOwnPropertyDescriptor(globalThis, 'navigator'),
    };
    globalThis.window = dom.window;
    globalThis.document = dom.window.document;
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      value: dom.window.navigator,
    });
    t.after(() => {
      globalThis.window = before.window;
      globalThis.document = before.document;
      if (before.navigator)
        Object.defineProperty(globalThis, 'navigator', before.navigator);
      else delete globalThis.navigator;
      dom.window.close();
    });
    const { createRoot } = require('react-dom/client');
    const { act } = React;
    globalThis.IS_REACT_ACT_ENVIRONMENT = true;
    t.after(() => delete globalThis.IS_REACT_ACT_ENVIRONMENT);
    const registrations = [];
    const cleanup = [];
    const ctx = {
      slots: {
        inject: (_name, callback) => callback(),
        register: (descriptor, component) => {
          registrations.push({ descriptor, component });
          return () => {};
        },
      },
      effect: (factory) => cleanup.push(factory()),
    };
    dom.window.__AURIGA_SHARED_BROWSER__.installMessaging(ctx, React, {});
    assert.ok(
      registrations.some(
        (entry) => entry.descriptor.name === 'sidebar.panellist',
      ),
    );
    assert.ok(
      registrations.some(
        (entry) =>
          entry.descriptor.name === 'conversation.hero.workspace' &&
          entry.descriptor.priority === -1,
      ),
      'shared mode must shadow workspace picker',
    );
    assert.ok(
      registrations.some(
        (entry) =>
          entry.descriptor.name === 'conversation.input.model' &&
          entry.descriptor.priority === -1,
      ),
      'shared mode must hide model selector',
    );
    assert.ok(
      registrations.some(
        (entry) =>
          entry.descriptor.name === 'conversation.input.permission' &&
          entry.descriptor.priority === -1,
      ),
      'shared mode must hide permission seat (shell banned)',
    );
    const Main = registrations.find(
      (entry) => entry.descriptor.name === 'main',
    ).component;
    const render = createRoot(dom.window.document.getElementById('root'));
    await act(async () => {
      render.render(React.createElement(Main));
    });
    const button = () => dom.window.document.querySelector('button');
    assert.equal(button().textContent, '开启接收');
    assert.equal(
      calls.some((call) => Object.hasOwn(call, 'enabled')),
      false,
    );
    await act(async () => {
      button().dispatchEvent(
        new dom.window.MouseEvent('click', { bubbles: true }),
      );
    });
    assert.equal(calls.at(-1).enabled, true);
    assert.equal(button().textContent, '关闭接收');
    assert.equal(button().disabled, false);
    await act(async () => {
      button().dispatchEvent(
        new dom.window.MouseEvent('click', { bubbles: true }),
      );
    });
    assert.equal(calls.at(-1).enabled, false);
    assert.ok(dom.window.document.body.textContent.includes('dsh:2/3/4'));
    await act(async () => {
      render.unmount();
    });
    cleanup.forEach((detach) => detach?.());
  },
);
