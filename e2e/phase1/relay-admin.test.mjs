import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { BrowserSteps } from './browser.mjs';
import { traceState } from './run.mjs';

test('中转管理使用平台浏览器登录、同源CSRF和幂等键，不向浏览器传服务口令', async (t) => {
  const seen = [];
  const server = http.createServer(async (req, res) => {
    if (req.url === '/favicon.ico') {
      res.writeHead(204);
      return res.end();
    }
    res.setHeader('content-type', 'text/html; charset=utf-8');
    if (req.url === '/api/iam/v1/login/providers')
      return res.end(
        '{"items":[{"id":"team","connectorType":"team-account"}]}',
      );
    if (req.url === '/api/iam/v1/login/transactions/team/advance') {
      seen.push('login');
      res.setHeader('content-type', 'application/json');
      res.setHeader('set-cookie', [
        'auriga_admin_session=fixture; HttpOnly; Path=/',
        'auriga_csrf=fixture-csrf; Path=/',
      ]);
      return res.end('{"status":"completed","step":{"type":"complete"}}');
    }
    if (req.url === '/api/iam/v1/me') {
      assert.match(req.headers.cookie ?? '', /auriga_admin_session=fixture/);
      return res.end('{"account":{"id":"admin-id"}}');
    }
    if (req.url.startsWith('/api/ai/v1/admin/relay/')) {
      assert.match(req.headers.cookie ?? '', /auriga_admin_session=fixture/);
      assert.equal(req.headers.authorization, undefined);
      seen.push(req.url);
      if (req.method === 'POST') {
        assert.equal(req.headers.origin, origin);
        assert.equal(req.headers['x-auriga-csrf'], 'fixture-csrf');
        assert.equal(req.headers['idempotency-key'], 'stable-id');
        res.setHeader('content-type', 'application/json');
        return res.end('{"key":{"id":"created"},"secret":"once"}');
      }
      res.writeHead(403, { 'content-type': 'application/json' });
      return res.end('{"detail":"private-server-error"}');
    }
    if (req.url === '/overview' || req.url === '/models/relay')
      return res.end('<h1>中转站</h1>');
    res.end(`<button data-provider-id="team">登录</button><script>
 document.querySelector('button').onclick=()=>{
 document.body.innerHTML='<form><input name="username"><input name="password" type="password"><button type="submit">继续</button></form>';
 document.querySelector('form').onsubmit=async(e)=>{e.preventDefault();await fetch('/api/iam/v1/login/transactions/team/advance',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(Object.fromEntries(new FormData(e.target)))});location.href='/overview'};
 };</script>`);
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  const origin = 'http://127.0.0.1:' + server.address().port;
  t.after(() => {
    server.closeAllConnections();
    return new Promise((done) => server.close(done));
  });
  const adapter = new BrowserSteps(
    { baseUrl: origin },
    {
      admin: {
        url: origin,
        accountId: 'admin-id',
        username: 'alice',
        password: 'test-password',
      },
      relay: { baseUrl: origin + '/api/ai/v1/admin/relay/' },
    },
  );
  t.after(() => adapter.close());
  const state = traceState();
  const call = (path, options) =>
    adapter.relayAdminRequest(
      new URL(path, origin + '/api/ai/v1/admin/relay/'),
      state,
      options,
    );
  assert.deepEqual(
    await call('virtual-keys', {
      method: 'POST',
      body: { name: 'test' },
      idempotencyKey: 'stable-id',
    }),
    { key: { id: 'created' }, secret: 'once' },
  );
  await assert.rejects(
    call('providers'),
    (error) => error.message === 'HTTP状态403，预期200',
  );
  await assert.rejects(call('../providers'), /管理请求必须留在/);
  await assert.rejects(
    adapter.relayAdminRequest(
      new URL('https://foreign.example.com/api/ai/v1/admin/relay/providers'),
      state,
    ),
    /管理请求必须留在/,
  );
  assert.equal(seen.filter((x) => x === 'login').length, 1);
  assert.equal(
    adapter.relayAdminEvidence.authentication,
    'platform-admin-session',
  );
});
