import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { chromium } from 'playwright';

const fixture = JSON.parse(readFileSync(0, 'utf8'));
let browser;
let observedOrigin;
let handoffStatus;
let stage = 'launch';
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
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
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
  const cookies = await context.cookies(fixture.ui);
  assert.ok(
    cookies.some(
      (cookie) => cookie.name === 'auriga_litellm_session' && cookie.httpOnly,
    ),
  );
  console.log(
    'PASS Chromium ' +
      browser.version() +
      ' automatically posts the admin Origin and reaches LiteLLM UI',
  );
} catch (error) {
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
