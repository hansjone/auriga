import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { setTimeout as delay } from 'node:timers/promises';
import { launchBrowser } from './launch.mjs';
import { loginInstalledDesktop } from './installed-login.mjs';

// Real local-login page/controller against an HTTP orchestration fixture. No
// identity, host compatibility or NAS acceptance is established by this test.
async function fixture(t, { resume = false, subject = 'test-member' } = {}) {
  const member = {
    accountId: 'test-member',
    username: 'alice',
    password: 'test-password',
    totpSecret: 'JBSWY3DPEHPK3PXP',
  };
  const seen = [];
  let loggedIn = false,
    busy = false,
    entered;
  const started = new Promise((done) => (entered = done));
  const server = http.createServer(async (req, res) => {
    const send = (value, status = 200) => {
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(value));
    };
    if (req.url === '/auriga/local-login/status')
      return send({
        available: true,
        loggedIn,
        connected: loggedIn,
        csrf: 'test-csrf',
        resumeAvailable: resume,
        account: loggedIn ? { subject } : null,
      });
    if (req.url === '/auriga/local-login/login') {
      let raw = '';
      for await (const part of req) raw += part;
      const body = JSON.parse(raw);
      seen.push(body.action);
      assert.equal(req.headers['x-auriga-csrf'], 'test-csrf');
      if (busy) return send({ code: 'common.unavailable' }, 503);
      if (body.action === 'begin' || body.action === 'resume') {
        busy = true;
        entered();
        await delay(250);
        busy = false;
        if (body.action === 'resume') {
          loggedIn = true;
          return send({ step: { type: 'complete' } });
        }
        return send({
          step: {
            type: 'form',
            formId: 'password',
            fields: [
              { name: 'username', kind: 'text', required: true },
              { name: 'password', kind: 'password', required: true },
            ],
          },
        });
      }
      if (body.input.formId === 'password') {
        assert.deepEqual(body.input.values, {
          username: member.username,
          password: member.password,
        });
        return send({
          step: {
            type: 'form',
            formId: 'totp',
            fields: [{ name: 'otp', kind: 'otp', required: true }],
          },
        });
      }
      assert.match(body.input.values.otp, /^\d{6}$/);
      loggedIn = true;
      return send({ step: { type: 'complete' } });
    }
    const prefix = '/auriga/local-login/assets/';
    if (req.url.startsWith(prefix)) {
      const name = req.url.slice(prefix.length);
      assert.match(name, /^[a-z-]+\.js$/);
      res.setHeader('content-type', 'text/javascript');
      return res.end(
        await readFile(
          new URL(
            '../../clients/dsh-plugin/src/login/' + name,
            import.meta.url,
          ),
        ),
      );
    }
    if (req.url === '/auriga/local-login')
      return res.end(
        '<!doctype html><html lang="zh-CN"><main id="auriga-login"></main><script type="module" src="/auriga/local-login/assets/local-adapter.js"></script></html>',
      );
    res.end('<!doctype html><title>native UI fixture</title>');
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  t.after(() => new Promise((done) => server.close(done)));
  const browser = await launchBrowser();
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.goto(
    `http://127.0.0.1:${server.address().port}/auriga/local-login`,
  );
  await started;
  const request = (path, body, csrf) =>
    page.evaluate(
      async ({ path, body, csrf }) => {
        const r = await globalThis.fetch(path, {
          method: body ? 'POST' : 'GET',
          headers: body
            ? { 'content-type': 'application/json', 'x-auriga-csrf': csrf }
            : {},
          body: body ? JSON.stringify(body) : undefined,
        });
        if (!r.ok) throw new Error('native HTTP ' + r.status);
        return r.json();
      },
      { path, body, csrf },
    );
  return { page, member, request, seen };
}

test('installed login follows the auto-started real page without a concurrent begin', async (t) => {
  const { page, member, request, seen } = await fixture(t);
  const result = await loginInstalledDesktop(page, member, request);
  assert.equal(result.loggedIn, true);
  assert.equal(result.accountId, member.accountId);
  assert.equal(result.resumed, false);
  assert.deepEqual(seen, ['begin', 'advance', 'advance']);
});

test('installed login observes the page resume and rejects another saved member', async (t) => {
  const { page, member, request, seen } = await fixture(t, {
    resume: true,
    subject: 'other-member',
  });
  await assert.rejects(
    loginInstalledDesktop(page, member, request),
    /same authenticated test member/,
  );
  assert.deepEqual(seen, ['resume']);
});
