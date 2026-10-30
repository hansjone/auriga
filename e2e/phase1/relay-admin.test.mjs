import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { BrowserSteps } from './browser.mjs';
import { traceState } from './run.mjs';
import { setTimeout as delay } from 'node:timers/promises';

test('relay admin uses a real login and browser handoff, confines credentials, and retains API errors', async (t) => {
  let uiOrigin, adminOrigin;
  const seen = [];
  const ui = http.createServer(async (req, res) => {
    if (req.url === '/favicon.ico') {
      res.writeHead(204);
      return res.end();
    }
    res.setHeader('content-type', 'text/html; charset=utf-8');
    if (req.url === '/ai/session') {
      assert.equal(req.method, 'POST');
      assert.equal(req.headers.origin, adminOrigin);
      seen.push('handoff');
      res.writeHead(303, {
        location: '/ai/ui/',
        'set-cookie':
          'auriga_litellm_session=fixture; Path=/ai; HttpOnly; SameSite=Strict',
      });
      return res.end();
    }
    if (req.url === '/ai/ui/')
      return res.end(
        '<title>LiteLLM</title><script>setTimeout(()=>location.assign("/ai/ui/login"),100)</script>',
      );
    if (req.url === '/ai/ui/login')
      return res.end('<title>LiteLLM sign in</title>');
    if (req.url === '/ai/health/readiness')
      return res.end('{"status":"healthy"}');
    assert.match(
      req.headers.cookie ?? '',
      /auriga_litellm_session=fixture/,
      req.url,
    );
    assert.equal(req.headers.authorization, 'Bearer test-master');
    if (req.method === 'POST') assert.equal(req.headers.origin, uiOrigin);
    seen.push(req.url);
    res.setHeader('content-type', 'application/json');
    if (req.url === '/ai/key/generate') {
      await delay(300);
      return res.end('{"key":"test-key"}');
    }
    res.writeHead(403);
    res.end('{"error":"test-private-error"}');
  });
  await new Promise((done) => ui.listen(0, '127.0.0.1', done));
  uiOrigin = `http://127.0.0.1:${ui.address().port}`;
  t.after(() => {
    ui.closeAllConnections();
    return new Promise((done) => ui.close(done));
  });
  const admin = http.createServer(async (req, res) => {
    if (req.url === '/favicon.ico') {
      res.writeHead(204);
      return res.end();
    }
    res.setHeader('content-type', 'text/html; charset=utf-8');
    if (req.url === '/api/iam/v1/login/providers') {
      res.setHeader('content-type', 'application/json');
      return res.end(
        '{"items":[{"id":"team","connectorType":"team-account"}]}',
      );
    }
    if (req.url === '/api/iam/v1/login/transactions/team/advance') {
      let raw = '';
      for await (const part of req) raw += part;
      assert.deepEqual(JSON.parse(raw), {
        username: 'alice',
        password: 'test-password',
      });
      seen.push('login');
      res.setHeader('content-type', 'application/json');
      res.setHeader(
        'set-cookie',
        'auriga_admin_session=fixture; HttpOnly; Path=/',
      );
      return res.end('{"status":"completed","step":{"type":"complete"}}');
    }
    if (req.url === '/api/iam/v1/me') {
      assert.match(req.headers.cookie ?? '', /auriga_admin_session=fixture/);
      res.setHeader('content-type', 'application/json');
      return res.end('{"account":{"id":"test-admin"}}');
    }
    if (req.url === '/ai/ui/') {
      assert.match(req.headers.cookie ?? '', /auriga_admin_session=fixture/);
      return res.end(
        `<form method="POST" action="${uiOrigin}/ai/session"><input name="code" value="fixture-code"></form><script>document.forms[0].submit()</script>`,
      );
    }
    if (req.url === '/overview')
      return res.end('<a href="/ai/ui/">LiteLLM</a>');
    res.end(`<button data-provider-id="team">登录</button><script>
document.querySelector('button').onclick=()=>{
 document.body.innerHTML='<form><input name="username"><input name="password" type="password"><button type="submit">继续</button></form>';
 document.querySelector('form').onsubmit=async(e)=>{e.preventDefault();await fetch('/api/iam/v1/login/transactions/team/advance',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(Object.fromEntries(new FormData(e.target)))});location.href='/overview'};
};</script>`);
  });
  await new Promise((done) => admin.listen(0, '127.0.0.1', done));
  adminOrigin = `http://127.0.0.1:${admin.address().port}`;
  t.after(() => {
    admin.closeAllConnections();
    return new Promise((done) => admin.close(done));
  });
  const adapter = new BrowserSteps(
    { baseUrl: adminOrigin },
    {
      admin: {
        url: adminOrigin,
        accountId: 'test-admin',
        username: 'alice',
        password: 'test-password',
      },
      relay: { baseUrl: uiOrigin + '/ai/', browserAdmin: true },
    },
  );
  t.after(() => adapter.close());
  const state = traceState();
  const call = (path, options) =>
    adapter.relayAdminRequest(new URL(path, uiOrigin + '/ai/'), state, {
      key: 'test-master',
      ...options,
    });
  assert.deepEqual(await call('key/generate', { method: 'POST', body: {} }), {
    key: 'test-key',
  });
  assert.deepEqual(seen, ['login', 'handoff', '/ai/key/generate']);
  await assert.rejects(
    call('key/info'),
    (error) => error.message === 'HTTP状态403，预期200',
  );
  assert.equal(seen.filter((value) => value === 'handoff').length, 1);
  await assert.rejects(
    adapter.relayAdminRequest(
      new URL('https://foreign.example.com/ai/key/info'),
      state,
      { key: 'test-master' },
    ),
    /管理请求必须留在/,
  );
  await assert.rejects(call('../key/info'), /管理请求必须留在/);
  assert.equal(
    state.requests.some((row) => row.status === 403),
    true,
  );
});
