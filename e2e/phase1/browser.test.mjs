import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { runPhase1, totp } from './run.mjs';
import { BrowserSteps, desktopEnvironment } from './browser.mjs';
import { resolve, join } from 'node:path';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';

// This deliberately tiny deployed-HTTP fixture tests browser orchestration,
// cookies and form binding. It is not evidence of a real Auriga deployment.
const secret = () =>
  Array.from(
    randomBytes(32),
    (byte) => 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'[byte % 32],
  ).join('');

test('browser login observation retains a safe fail code without private response fields', async (t) => {
  const path = '/api/iam/v1/login/transactions/failure/advance';
  const server = http.createServer((req, res) => {
    if (req.url === path) {
      res.setHeader('content-type', 'application/json');
      res.end(
        JSON.stringify({
          status: 'failed',
          token: 'private-token',
          step: {
            type: 'fail',
            code: 'iam.emergency_local_only',
            detail: 'private-detail',
          },
        }),
      );
    } else res.end('<!doctype html><title>login observer fixture</title>');
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  t.after(() => new Promise((done) => server.close(done)));
  const baseUrl = `http://127.0.0.1:${server.address().port}`;
  const adapter = new BrowserSteps({ baseUrl }, {});
  t.after(() => adapter.close());
  const context = await adapter.context();
  const page = await context.newPage();
  await page.goto(baseUrl);
  await page.evaluate(
    (path) => globalThis.fetch(path, { method: 'POST' }).then((r) => r.json()),
    path,
  );
  const retained = await adapter.retainedLogin(path);
  assert.equal(retained.step.code, 'iam.emergency_local_only');
  assert.equal(JSON.stringify(retained).includes('private-'), false);
});
const html = (
  mode,
  admin,
  portal,
) => `<!doctype html><meta name="auriga-access-mode" content="${mode}"><main></main><script>
const main = document.querySelector('main');
const admin = ${JSON.stringify(admin)}, portal = ${JSON.stringify(portal)}, mode = ${JSON.stringify(mode)};
const api = async (path, body) => { const res = await fetch('/api/iam/v1/' + path, body ? { method: 'POST', headers: {'content-type':'application/json'}, body:JSON.stringify(body) } : {}); return res.json(); };
let tx;
function render() {
  if (tx.status === 'completed') { location.href=admin?'/overview':'/'; return; }
  main.innerHTML = '<form data-testid="form-step">' + tx.step.fields.map(f=>f.kind==='info'?'<div class="info">'+f.label.en+'</div>':'<input name="'+f.name+'">').join('')+'<button type="submit">继续</button></form>';
  main.querySelector('form').onsubmit = async e => { e.preventDefault(); tx = await api('login/transactions/'+tx.id+'/advance', { type:'form', formId:tx.step.formId, values:Object.fromEntries(new FormData(e.target)) }); render(); };
}
(async () => {
  if (location.pathname.startsWith('/invite/')) { main.innerHTML='<section class="invite-card"><button>接受邀请</button></section>'; main.querySelector('button').onclick=()=>location.href='/login?invitation='+location.pathname.split('/').pop(); }
  else if (location.pathname === '/invitations') {
    main.innerHTML='<button id="create">创建普通成员邀请</button>';
    main.querySelector('#create').onclick=()=>{
      main.innerHTML='<section class="details"><label>显示名<input></label><button>创建普通成员邀请</button></section>';
      main.querySelector('button').onclick=()=>{
        const confirm=document.createElement('button'); confirm.dataset.testid='confirm-action'; confirm.textContent='确认'; main.append(confirm);
        confirm.onclick=async()=>{
          let token=document.cookie.split('; ').find(c=>c.startsWith('auriga_csrf=')).split('=')[1];
          if(mode==='ip') { token=crypto.randomUUID(); await fetch('/api/iam/v1/admin/session/csrf-tokens',{method:'POST',headers:{'content-type':'application/json','X-Auriga-CSRF-Bootstrap':'admin-session-v1'},body:JSON.stringify({token})}); }
          const res=await fetch('/api/org/v1/admin/invitations',{method:'POST',headers:{'content-type':'application/json','X-Auriga-CSRF':token},body:'{}'});
          const value=await res.json(); main.innerHTML='<div data-testid="invitation-url">'+portal+'/invite/'+value.code+'</div>';
        };
      };
    };
  } else if (location.pathname === '/login') {
    const data=await api('login/providers'); main.innerHTML=data.items.map(p=>'<button data-provider-id="'+p.id+'">登录</button>').join('');
    main.querySelectorAll('button').forEach(btn=>btn.onclick=async()=>{tx=await api('login/transactions',{providerId:btn.dataset.providerId,invitationCode:new URLSearchParams(location.search).get('invitation')});render();});
  } else main.textContent='已登录';
})();
</script>`;

for (const [mode, adminConnector] of [
  ['ip', 'emergency'],
  ['domain', 'emergency'],
  ['ip', 'team-account'],
]) {
  test(`真实Playwright表单：${mode}模式 ${adminConnector} 管理员邀请消费、TOTP登记、新上下文重新登录`, async (t) => {
    const adminSecret = secret();
    const adminPassword = randomBytes(20).toString('base64url');
    const sessions = new Map();
    const transactions = new Map();
    let registered;
    let used = false;
    let registeredCsrf;
    let loginCount = 0;
    let lastOtpCounter = -1;
    let baseUrl;
    const handler = (admin) => async (req, res) => {
      const path = new URL(req.url, 'http://localhost').pathname;
      res.setHeader('traceparent', req.headers.traceparent || '');
      if (!path.startsWith('/api/')) {
        res.setHeader('content-type', 'text/html; charset=utf-8');
        res.end(html(mode, admin, baseUrl));
        return;
      }
      const json = (value, status = 200) => {
        res.statusCode = status;
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify(value));
      };
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : {};
      const cookies = Object.fromEntries(
        (req.headers.cookie || '')
          .split('; ')
          .filter(Boolean)
          .map((pair) => pair.split('=')),
      );
      if (path.endsWith('/login/providers'))
        return json({
          items: [
            {
              id: admin
                ? '00000000-0000-0000-0000-000000000001'
                : '00000000-0000-0000-0000-000000000002',
              connectorType: admin ? adminConnector : 'team-account',
            },
          ],
        });
      if (path.endsWith('/login/transactions')) {
        const id = randomBytes(8).toString('hex');
        const formId = admin
          ? adminConnector === 'emergency'
            ? 'emergency'
            : 'password'
          : body.invitationCode
            ? 'register'
            : 'password';
        const step = {
          type: 'form',
          formId,
          fields: [
            'username',
            'password',
            ...(admin && adminConnector === 'emergency' ? ['otp'] : []),
            ...(formId === 'register' ? ['displayName'] : []),
          ].map((name) => ({ name, kind: 'text', required: true })),
        };
        const tx = { id, status: 'pending', step, admin };
        transactions.set(id, tx);
        return json(tx, 201);
      }
      if (path.endsWith('/advance')) {
        const tx = transactions.get(path.split('/').at(-2));
        if (admin) {
          if (adminConnector === 'team-account' && body.formId === 'password') {
            assert.equal(body.values.password, adminPassword);
            tx.step = {
              type: 'form',
              formId: 'totp',
              fields: [{ name: 'code', kind: 'otp', required: true }],
            };
            return json(tx);
          }
          if (adminConnector === 'emergency')
            assert.equal(body.values.password, adminPassword);
          assert.equal(
            body.values[adminConnector === 'emergency' ? 'otp' : 'code'],
            totp(adminSecret),
          );
        } else if (body.formId === 'register') {
          registered = { ...body.values, secret: secret() };
          tx.step = {
            type: 'form',
            formId: 'first_totp',
            fields: [
              {
                name: 'enrollment',
                kind: 'info',
                label: { en: 'Secret: ' + registered.secret },
              },
              { name: 'otp', kind: 'otp', required: true },
            ],
          };
          return json(tx);
        } else if (['first_totp', 'totp'].includes(body.formId)) {
          assert.equal(
            body.values[body.formId === 'totp' ? 'code' : 'otp'],
            totp(registered.secret),
          );
          assert.ok(Math.floor(Date.now() / 30000) > lastOtpCounter);
          lastOtpCounter = Math.floor(Date.now() / 30000);
          used = true;
        } else {
          assert.equal(body.values.username, registered.username);
          assert.equal(body.values.password, registered.password);
          tx.step = {
            type: 'form',
            formId: 'totp',
            fields: [{ name: 'code', kind: 'otp', required: true }],
          };
          return json(tx);
        }
        const sid = randomBytes(16).toString('hex');
        sessions.set(sid, admin ? 'admin-account' : 'member-account');
        res.setHeader('set-cookie', [
          `session=${sid}; Path=/; HttpOnly; SameSite=Lax`,
          'auriga_csrf=fixture-csrf; Path=/; SameSite=Lax',
        ]);
        return json({ ...tx, status: 'completed' });
      }
      if (path.endsWith('/me')) {
        assert.ok(sessions.has(cookies.session));
        return json({
          account: {
            id: sessions.get(cookies.session),
            lastLoginAt: String(loginCount++),
          },
        });
      }
      if (path.endsWith('/admin/session/csrf-tokens')) {
        assert.ok(admin && mode === 'ip');
        assert.equal(
          req.headers['x-auriga-csrf-bootstrap'],
          'admin-session-v1',
        );
        assert.equal(
          req.headers.origin,
          `http://127.0.0.1:${req.socket.localPort}`,
        );
        registeredCsrf = body.token;
        return json({ token: body.token });
      }
      if (path.endsWith('/admin/invitations')) {
        assert.equal(
          req.headers['x-auriga-csrf'],
          mode === 'ip' ? registeredCsrf : 'fixture-csrf',
        );
        assert.equal(
          req.headers.origin,
          `http://127.0.0.1:${req.socket.localPort}`,
        );
        return json({ id: 'invitation-id', code: 'fixture-invite' }, 201);
      }
      if (path.includes('/invitations/preview/'))
        return json({ valid: !used, reason: used ? 'used' : undefined });
      json({}, 404);
    };
    const serve = async (handler) => {
      const server = http.createServer((req, res) => {
        handler(req, res).catch((error) => {
          res.writeHead(500);
          res.end('{}');
          throw error;
        });
      });
      await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
      t.after(() => new Promise((resolve) => server.close(resolve)));
      return `http://127.0.0.1:${server.address().port}`;
    };
    const adminUrl = await serve(handler(true));
    baseUrl = await serve(handler(false));
    const parent = resolve('e2e/phase1/.local');
    await mkdir(parent, { recursive: true });
    const directory = await mkdtemp(join(parent, 'member-'));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const memberCredentialsFile = join(directory, 'member.json');
    const options = { baseUrl, only: [2] };
    const config = {
      memberCredentialsFile,
      access: {
        mode,
        portalOrigin: {
          scheme: 'http',
          host: '127.0.0.1',
          port: Number(new URL(baseUrl).port),
        },
        adminOrigin: {
          scheme: 'http',
          host: '127.0.0.1',
          port: Number(new URL(adminUrl).port),
        },
        instances:
          mode === 'ip'
            ? { mode: 'path' }
            : {
                mode: 'subdomain',
                scheme: 'http',
                parentDomain: 'i.example.com',
                port: Number(new URL(baseUrl).port),
              },
      },
      nodeEnrollment: false,
      browser: {
        channel: process.env.PHASE1_BROWSER_CHANNEL || 'chromium',
      },
      admin: {
        url: adminUrl,
        providerType: adminConnector,
        username: 'fixture-admin',
        password: adminPassword,
        totpSecret: adminSecret,
      },
    };
    const adapter = new BrowserSteps(options, config);
    const report = await runPhase1(options, config, { adapter });
    assert.equal(
      report.items[1].status,
      'PASS',
      JSON.stringify(report.cloudJourney),
    );
    assert.equal(used, true);
    assert.equal(JSON.stringify(report).includes(adminPassword), false);
    const saved = JSON.parse(await readFile(memberCredentialsFile, 'utf8'));
    assert.equal(saved.accountId, 'member-account');
    assert.equal(saved.password, registered.password);
    assert.equal(saved.totpSecret, registered.secret);
    assert.equal(JSON.stringify(report).includes(saved.password), false);
    assert.equal(JSON.stringify(report).includes(saved.totpSecret), false);
    assert.equal(adapter.loginResponses.size, 0);
    assert.equal(report.cleanupErrors.length, 0);
  });
}

test('桌面profile环境不继承现有DSH、模型Key或Node hook', () => {
  const home = resolve('e2e/phase1/.local/fixture-home');
  const env = desktopEnvironment(home, {
    PATH: 'fixture-path',
    OPENAI_API_KEY: randomBytes(20).toString('hex'),
    DSH_HOME: 'existing-user-profile',
    NODE_OPTIONS: '--require private-hook',
    AURIGA_MODE: 'managed',
    AURIGA_INSTANCE_TOKEN: 'fixture-token',
  });
  assert.equal(env.DSH_HOME, join(home, 'dsh'));
  assert.equal(env.AURIGA_MODE, 'connect');
  assert.equal(env.PATH, 'fixture-path');
  assert.equal(env.OPENAI_API_KEY, undefined);
  assert.equal(env.NODE_OPTIONS, undefined);
  assert.equal(env.AURIGA_INSTANCE_TOKEN, undefined);
});
