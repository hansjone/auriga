import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { mountLocalLoginPage } from '../src/login/page.js';

function fixture(t, replies, options = {}) {
  const dom = new JSDOM('<main id="auriga-login"></main>', {
    url: 'http://localhost/auriga/local-login',
  });
  const root = dom.window.document.querySelector('main');
  const requests = [],
    navigations = [];
  const page = mountLocalLoginPage(root, {
    fetch: async (url, init) => {
      requests.push({ url, ...init });
      const next = replies.shift();
      if (next instanceof Error) throw next;
      assert.ok(next, 'unexpected request');
      return next;
    },
    navigate: (path) => navigations.push(path),
    ...options,
  });
  t.after(() => {
    page.dispose();
    dom.window.close();
  });
  return { root, requests, navigations, page };
}
const status = (extra = {}) =>
  Response.json({
    available: true,
    loggedIn: false,
    connected: false,
    csrf: 'proof',
    remoteRevocation: 'complete',
    ...extra,
  });
const flush = async () => {
  for (let i = 0; i < 60; i++) await Promise.resolve();
};

test('actual login page mounts transport and exposes a recoverable network failure', async (t) => {
  const { root, requests } = fixture(t, [
    new TypeError('private transport error'),
  ]);
  assert.match(root.textContent, /正在处理/);
  await flush();
  assert.match(root.textContent, /总部不可达/);
  assert.ok(root.querySelector('[data-action=skip]'));
  assert.ok(root.querySelector('[data-action=restart]'));
  assert.equal(requests[0].url, '/auriga/local-login/status');
});

for (const locale of ['zh-CN', 'en'])
  test(`R2 ${locale}: skipping unreachable platform login returns immediately without logging out`, async (t) => {
    const { root, requests, navigations } = fixture(
      t,
      [new TypeError('offline')],
      { locale },
    );
    await flush();
    assert.match(
      root.textContent,
      locale === 'en' ? /Platform unreachable/ : /总部不可达/,
    );
    const skip = root.querySelector('[data-action=skip]');
    assert.equal(skip.textContent, locale === 'en' ? 'Skip' : '跳过');
    skip.click();
    assert.deepEqual(navigations, ['/']);
    assert.equal(requests.length, 1);
  });
test('complete returns to current DSH only after verified ready status; ignores remote redirectTo', async (t) => {
  const { navigations } = fixture(t, [
    status(),
    Response.json({
      step: { type: 'complete', redirectTo: 'https://evil.example.com' },
    }),
    status({ loggedIn: true, connected: true }),
  ]);
  await flush();
  assert.deepEqual(navigations, ['/']);
});
test('pending revocation remains visible on the next login page', async (t) => {
  const { root } = fixture(t, [
    status({ remoteRevocation: 'pending' }),
    Response.json({
      step: {
        type: 'form',
        formId: 'password',
        fields: [{ name: 'password', kind: 'password' }],
      },
    }),
  ]);
  await flush();
  assert.match(root.textContent, /远端设备撤销正在重试/);
  assert.ok(root.querySelector('form'));
});
test('logout entry is read only until explicit confirmation, and cancel returns to DSH', (t) => {
  const { root, requests, navigations } = fixture(t, [], { logout: true });
  assert.match(root.textContent, /确认退出/);
  assert.equal(requests.length, 0);
  root.querySelector('[data-action=return]').click();
  assert.deepEqual(navigations, ['/']);
});
test('disposed login ignores a response that arrives after pagehide', async (t) => {
  let resolve;
  const { root, page, navigations } = fixture(t, [], {
    fetch: () =>
      new Promise((done) => {
        resolve = done;
      }),
  });
  page.dispose();
  resolve(status({ loggedIn: true, connected: true }));
  await flush();
  assert.equal(root.textContent, '');
  assert.deepEqual(navigations, []);
});
