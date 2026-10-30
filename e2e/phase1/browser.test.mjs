import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { runPhase1, totp } from './run.mjs';
import { BrowserSteps, desktopEnvironment } from './browser.mjs';
import { resolve, join } from 'node:path';

// This deliberately tiny deployed-HTTP fixture tests browser orchestration,
// cookies and form binding. It is not evidence of a real Auriga deployment.
const secret = () =>
  Array.from(
    randomBytes(32),
    (byte) => 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'[byte % 32],
  ).join('');
const html = (
  mode,
) => `<!doctype html><meta name="auriga-access-mode" content="${mode}"><main></main><script>
const main = document.querySelector('main');
const api = async (path, body) => { const res = await fetch('/api/iam/v1/' + path, body ? { method: 'POST', headers: {'content-type':'application/json'}, body:JSON.stringify(body) } : {}); return res.json(); };
let tx;
function render() {
  if (tx.status === 'completed') { location.href='/'; return; }
  main.innerHTML = '<form data-testid="form-step">' + tx.step.fields.filter(f=>f.kind!=='info').map(f=>'<input name="'+f.name+'">').join('')+'<button type="submit">继续</button></form>';
  main.querySelector('form').onsubmit = async e => { e.preventDefault(); tx = await api('login/transactions/'+tx.id+'/advance', { type:'form', formId:tx.step.formId, values:Object.fromEntries(new FormData(e.target)) }); render(); };
}
(async () => {
  if (location.pathname.startsWith('/invite/')) { main.innerHTML='<section class="invite-card"><button>接受邀请</button></section>'; main.querySelector('button').onclick=()=>location.href='/login?invitation='+location.pathname.split('/').pop(); }
  else if (location.pathname === '/login') {
    const data=await api('login/providers'); main.innerHTML=data.items.map(p=>'<button data-provider-id="'+p.id+'">登录</button>').join('');
    main.querySelectorAll('button').forEach(btn=>btn.onclick=async()=>{tx=await api('login/transactions',{providerId:btn.dataset.providerId,invitationCode:new URLSearchParams(location.search).get('invitation')});render();});
  } else main.textContent='已登录';
})();
</script>`;

for (const mode of ['ip', 'domain']) {
  test(`真实Playwright表单：${mode}模式邀请消费、TOTP登记、新上下文重新登录`, async (t) => {
    const adminSecret = secret();
    const adminPassword = randomBytes(20).toString('base64url');
    const sessions = new Map();
    const transactions = new Map();
    let registered;
    let used = false;
    let registeredCsrf;
    let loginCount = 0;
    let lastOtpCounter = -1;
    const handler = (admin) => async (req, res) => {
      const path = new URL(req.url, 'http://localhost').pathname;
      res.setHeader('traceparent', req.headers.traceparent || '');
      if (!path.startsWith('/api/')) {
        res.setHeader('content-type', 'text/html');
        res.end(html(mode));
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
              id: admin ? 'emergency-id' : 'team-id',
              connectorType: admin ? 'emergency' : 'team-account',
            },
          ],
        });
      if (path.endsWith('/login/transactions')) {
        const id = randomBytes(8).toString('hex');
        const formId = admin
          ? 'emergency'
          : body.invitationCode
            ? 'register'
            : 'password';
        const step = {
          type: 'form',
          formId,
          fields: ['username', 'password', ...(admin ? ['otp'] : [])].map(
            (name) => ({ name, kind: 'text', required: true }),
          ),
        };
        const tx = { id, status: 'pending', step, admin };
        transactions.set(id, tx);
        return json(tx, 201);
      }
      if (path.endsWith('/advance')) {
        const tx = transactions.get(path.split('/').at(-2));
        if (admin) {
          assert.equal(body.values.password, adminPassword);
          assert.equal(body.values.otp, totp(adminSecret));
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
          assert.equal(body.values.otp, totp(registered.secret));
          assert.ok(Math.floor(Date.now() / 30000) > lastOtpCounter);
          lastOtpCounter = Math.floor(Date.now() / 30000);
          used = true;
        } else {
          assert.equal(body.values.username, registered.username);
          assert.equal(body.values.password, registered.password);
          tx.step = {
            type: 'form',
            formId: 'totp',
            fields: [{ name: 'otp', kind: 'otp', required: true }],
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
    const baseUrl = await serve(handler(false));
    const options = { baseUrl, only: [2] };
    const config = {
      browser: {
        channel:
          process.env.PHASE1_BROWSER_CHANNEL ||
          (process.platform === 'win32' ? 'msedge' : undefined),
      },
      admin: {
        url: adminUrl,
        username: 'fixture-admin',
        password: adminPassword,
        totpSecret: adminSecret,
      },
    };
    const adapter = new BrowserSteps(options, config);
    const report = await runPhase1(options, config, { adapter });
    assert.equal(report.items[1].status, 'PASS', report.items[1].reason);
    assert.equal(used, true);
    assert.equal(JSON.stringify(report).includes(adminPassword), false);
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
