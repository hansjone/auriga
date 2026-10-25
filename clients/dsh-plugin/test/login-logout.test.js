import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { createLogoutView } from '../src/login/logout.js';
import { createLocalLoginController } from '../src/login/local-controller.js';
import { runtimePeer } from './local-runtime-peer.js';
import { EventEmitter } from 'node:events';
import { setImmediate } from 'node:timers';
const tick = async () => {
  for (let i = 0; i < 16; i++) await Promise.resolve();
};
function mount(t, options = {}) {
  const dom = new JSDOM('<main></main>');
  const root = dom.window.document.querySelector('main');
  let returned = 0,
    signedOut = 0;
  const view = createLogoutView(root, {
    onConfirm: async () => ({ loggedIn: false, remoteRevocation: 'complete' }),
    onReturn: () => returned++,
    onSignedOut: () => signedOut++,
    ...options,
  });
  t.after(() => {
    view.dispose();
    dom.window.close();
  });
  return {
    dom,
    root,
    view,
    returned: () => returned,
    signedOut: () => signedOut,
  };
}
test('GET/mount and cancel never sign out; confirmation is explicit', (t) => {
  let calls = 0;
  const { root, returned } = mount(t, {
    onConfirm: async () => {
      calls++;
    },
  });
  assert.match(root.textContent, /确认退出/);
  assert.match(root.textContent, /DSH.*任务/);
  assert.equal(calls, 0);
  root.querySelector('[data-action=return]').click();
  assert.equal(returned(), 1);
  assert.equal(calls, 0);
});
test('confirmation is single flight; pending remote revocation remains honest and can return to login', async (t) => {
  let resolve,
    calls = 0;
  const { root, signedOut } = mount(t, {
    onConfirm: async () => {
      calls++;
      return new Promise((done) => {
        resolve = done;
      });
    },
  });
  const confirm = root.querySelector('[data-action=confirm]');
  confirm.click();
  confirm.click();
  assert.equal(calls, 1);
  assert.equal(root.querySelector('.card').getAttribute('aria-busy'), 'true');
  resolve({ loggedIn: false, remoteRevocation: 'pending' });
  await tick();
  assert.match(root.textContent, /远端.*重试/);
  assert.equal(signedOut(), 0);
  root.querySelector('[data-action=login]').click();
  assert.equal(signedOut(), 1);
});
test('failed persistence/network and malformed success never claim sign-out; retry is explicit', async (t) => {
  let calls = 0;
  const { root, signedOut } = mount(t, {
    onConfirm: async () => {
      calls++;
      if (calls === 1) throw new Error('SECRET');
      return { loggedIn: true };
    },
  });
  root.querySelector('[data-action=confirm]').click();
  await tick();
  assert.match(root.textContent, /退出未完成/);
  assert.doesNotMatch(root.textContent, /SECRET/);
  root.querySelector('[data-action=confirm]').click();
  await tick();
  assert.match(root.textContent, /退出未完成/);
  assert.equal(signedOut(), 0);
});
test('page disposal aborts request and suppresses late success', async (t) => {
  let resolve, signal;
  const { root, view, signedOut } = mount(t, {
    onConfirm: async (context) => {
      signal = context.signal;
      return new Promise((done) => {
        resolve = done;
      });
    },
  });
  root.querySelector('[data-action=confirm]').click();
  view.dispose();
  assert.equal(signal.aborted, true);
  resolve({ loggedIn: false, remoteRevocation: 'complete' });
  await tick();
  assert.equal(root.childNodes.length, 0);
  assert.equal(signedOut(), 0);
});
test('confirmed logout closes identity HTTP/WS and rejects old admission while the runtime remains available', async (t) => {
  const { runtime, request } = await runtimePeer(t);
  let httpClosed = 0,
    wsClosed = 0;
  const http = new EventEmitter();
  http.destroy = () => {
    httpClosed++;
    http.emit('close');
  };
  const ws = new EventEmitter();
  ws.destroy = () => {
    wsClosed++;
    ws.emit('close');
  };
  runtime.bindHttpResponse(request, http);
  runtime.bindSocket({
    ...request,
    headers: { ...request.headers, upgrade: 'websocket' },
    socket: ws,
  });
  const controller = createLocalLoginController({
    fetch: async (url, options) => {
      if (url.endsWith('/status'))
        return Response.json(runtime.prepare(request).body);
      assert.ok(url.endsWith('/logout'));
      assert.equal(
        options.headers['X-Auriga-CSRF'],
        runtime.status(request).csrf,
      );
      return Response.json((await runtime.logout(request)).body);
    },
  });
  const { root } = mount(t, {
    onConfirm: ({ signal }) => controller.logout(signal),
  });
  assert.equal(httpClosed, 0);
  root.querySelector('[data-action=confirm]').click();
  // The runtime owns asynchronous filesystem persistence.
  await new Promise((resolve, reject) => {
    const deadline = Date.now() + 5000;
    const check = () => {
      if (/已退出本机/.test(root.textContent)) return resolve();
      if (Date.now() >= deadline)
        return reject(new Error('logout did not finish'));
      setImmediate(check);
    };
    check();
  });
  assert.equal(runtime.resolve(request), null);
  assert.equal(runtime.bindHttpResponse(request, new EventEmitter()), false);
  assert.equal(httpClosed, 1);
  assert.equal(wsClosed, 1);
  assert.equal(runtime.prepare({ headers: {} }).body.loggedIn, false);
});
