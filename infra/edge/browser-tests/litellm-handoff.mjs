import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import http from 'node:http';
import { chromium } from 'playwright';

const fixture = JSON.parse(readFileSync(0, 'utf8'));
function dataProbe(origin, key, method) {
  const target = new URL(origin + (method === 'POST' ? '/ai/v1/chat/completions' : '/ai/v1/models'));
  const authority = target.host;
  target.hostname = '127.0.0.1';
  return new Promise((resolve,reject) => {
    const body = method === 'POST' ? JSON.stringify({model:'test-pool',messages:[{role:'user',content:'fixture'}]}) : '';
    const req = http.request(target, {method, headers:{Host:authority, Authorization:'Bearer '+key,'Content-Type':'application/json','Content-Length':Buffer.byteLength(body)}},(response)=>{
      response.resume();
      response.on('end',()=>resolve(response.statusCode));
    });
    req.setTimeout(10000,()=>req.destroy(new Error('data probe timeout')));
    req.on('error',()=>reject(new Error('data probe failed')));
    req.end(body);
  });
}
let browser;
let observedOrigin;
let handoffStatus;
let stage = 'launch';
let page;
try {
  browser = await chromium.launch({
    channel: 'chromium',
    headless: true,
    args: [
      '--no-proxy-server',
      '--host-resolver-rules=MAP *.example.com 127.0.0.1',
    ],
  });
  stage = 'context';
  const context = await browser.newContext();
  await context.addCookies([
    {
      url: fixture.admin,
      name: 'auriga_admin_session',
      value: 'admin',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  page = await context.newPage();
  page.setDefaultTimeout(30000);
  const origin = new Promise((resolve) => {
    page.on('request', async (request) => {
      if (request.url() === fixture.ui + '/ai/session') {
        assert.equal(request.method(), 'POST');
        // Observe the browser-generated header; never override it.
        observedOrigin = (await request.allHeaders()).origin;
        resolve(observedOrigin);
      }
    });
  });
  page.on('response', (response) => {
    if (response.url() === fixture.ui + '/ai/session') {
      handoffStatus = response.status();
    }
  });
  stage = 'admin-navigation';
  await page.goto(fixture.admin + '/');
  const handoff = page.waitForResponse(fixture.admin + '/ai/ui/');
  const final = page.waitForResponse(
    (response) =>
      response.url() === fixture.ui + '/ai/ui/' &&
      response.request().isNavigationRequest(),
  );
  stage = 'model-link';
  await page.getByRole('link', { name: '模型', exact: true }).click();
  stage = 'automatic-handoff';
  const response = await final;
  await page.waitForLoadState('domcontentloaded');
  assert.equal(await origin, fixture.admin);
  assert.equal(
    (await (await handoff).allHeaders())['referrer-policy'],
    'strict-origin',
  );
  assert.equal(handoffStatus, 303);
  assert.equal(response.status(), 200);
  const html = await response.text();
  assert.match(html, /LiteLLM/i);
  assert.equal(await page.evaluate(() => location.origin), fixture.ui);
  if (fixture.live) {
    stage = 'live-ui-ready';
    if (fixture.native) {
      assert.equal(await page.getByRole('button', { name: 'Login', exact: true }).count(), 0);
      for (const [label, file, route] of [['Models + Endpoints', 'models', 'models-and-endpoints'], ['Virtual Keys', 'keys', 'api-keys'], ['Usage', 'usage', 'usage']]) {
        stage = 'native-' + file;
        const menu = page.getByText(label, {exact:true}).first();
        await menu.waitFor();
        await menu.click();
        await page.waitForURL((url) => url.pathname.includes('/' + route));
        await page.getByRole('heading', {name: file === 'models' ? 'Model Management' : file === 'keys' ? /Virtual Keys/ : /Usage/}).first().waitFor();
        await page.waitForLoadState('networkidle');
        assert.equal(await page.getByLabel('Username', {exact:true}).count(), 0);
        if (fixture.evidence) {
          await mkdir(fixture.evidence, {recursive:true});
          await page.screenshot({path:path.join(fixture.evidence, fixture.mode + '-' + file + '.png'), fullPage:true});
        }
      }
    } else {
    // Receiving the SSR Loading shell and its first script is insufficient.
    // A fresh browser has no LiteLLM token and must reach its actual login form.
    await page.getByRole('button', { name: 'Login', exact: true }).waitFor();
    await page.getByLabel('Username', { exact: true }).waitFor();
    await page.getByLabel('Password', { exact: true }).waitFor();
    }
    assert.equal(new URL(page.url()).origin, fixture.ui);
  }
  const cookies = await context.cookies(fixture.ui + '/ai/ui/');
  assert.ok(
    cookies.some(
      (cookie) => cookie.name === 'auriga_litellm_session' && cookie.httpOnly,
    ),
  );
  if (fixture.native) {
    stage = 'ui-key-data-route-rejected';
    const token = cookies.find((cookie) => cookie.name === 'token').value;
    const key = JSON.parse(Buffer.from(token.split('.')[1], 'base64url')).key;
    // Chromium's process-only DNS mapping does not apply to Playwright's
    // Node API client. Connect to the fixture socket and retain its Host.
    const status = await dataProbe(fixture.portal,key,'POST');
    assert.ok([401,403].includes(status));
    stage = 'external-key-data-route-allowed';
    const external = await page.evaluate(async (key) => {
      const reply = await fetch('/ai/key/generate',{method:'POST',headers:{Authorization:'Bearer '+key,'Content-Type':'application/json'},body:JSON.stringify({models:['test-pool'],duration:'1m'})});
      const result = await reply.json();
      return {status:reply.status,key:result.key};
    },key);
    assert.equal(external.status,200);
    assert.equal(await dataProbe(fixture.portal,external.key,'GET'),200);
    stage = 'platform-revocation';
    assert.equal((await fetch(fixture.revoke, {method:'POST'})).status, 204);
    const denied = await page.goto(fixture.ui + '/ai/ui/');
    assert.equal(denied.status(), 401);
    if (fixture.evidence) await page.screenshot({path:path.join(fixture.evidence, fixture.mode + '-revoked.png')});
  }
  console.log(
    'PASS Chromium ' +
      browser.version() +
      ' automatically posts the admin Origin and reaches LiteLLM UI',
  );
} catch (error) {
  if (fixture.evidence && page) {
    await mkdir(fixture.evidence, {recursive:true});
    await page.screenshot({path:path.join(fixture.evidence, fixture.mode + '-failure.png')});
    console.error('headings=' + JSON.stringify(await page.locator('h1,h2,h3').allTextContents()));
    console.error('path=' + new URL(page.url()).pathname);
  }
  // Do not print the one-time form code, cookies or browser network dumps.
  const origin =
    observedOrigin === 'null'
      ? 'null'
      : observedOrigin === fixture.admin
        ? 'admin'
        : 'missing';
  console.error(
    'FAIL Chromium LiteLLM handoff: Origin=' +
      origin +
      ', status=' +
      (handoffStatus ?? 'missing'),
  );
  console.error('stage=' + stage + ': ' + String(error.message).split('\n')[0]);
  process.exitCode = 1;
} finally {
  await browser?.close();
}
