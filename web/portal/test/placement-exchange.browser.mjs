// Run with node web/portal/test/placement-exchange.browser.mjs.
// Uses the repository's locked e2e Playwright dependency and installed Chromium.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { chromium } from '../../../e2e/node_modules/playwright/index.mjs';

function compile(name) {
  const source = readFileSync(
    new URL('../src/views/' + name, import.meta.url),
    'utf8',
  );
  return ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ES2022,
    },
  }).outputText;
}
const moduleUrl = (source) =>
  'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const config = moduleUrl(compile('placement-config.ts'));
const api = moduleUrl(
  compile('placement-api.ts').replace(
    /(['"])\.\/placement-config\1/,
    JSON.stringify(config),
  ),
);
const portal = 'https://portal.example.test';
const id = '11111111-1111-4111-8111-111111111111';
const pool = 'https://' + id + '.instances.example.test';
const requests = [];
const responses = [];
const browser = await chromium.launch({
  headless: true,
  ...(process.env.AURIGA_TEST_CHROMIUM
    ? { executablePath: process.env.AURIGA_TEST_CHROMIUM }
    : { channel: 'chromium' }),
});
try {
  const page = await browser.newPage();
  await page.exposeFunction('recordExchange', (value) => responses.push(value));
  await page.route(portal + '/**', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: '<meta name="auriga-instance-parent-domain" content="instances.example.test"><p>Portal</p>',
    }),
  );
  await page.route(pool + '/**', async (route) => {
    const request = route.request();
    requests.push({
      method: request.method(),
      url: request.url(),
      navigation: request.isNavigationRequest(),
    });
    if (request.method() === 'OPTIONS') {
      await route.fulfill({
        status: 204,
        headers: {
          'Access-Control-Allow-Origin': portal,
          'Access-Control-Allow-Credentials': 'true',
          'Access-Control-Allow-Methods': 'POST',
          'Access-Control-Allow-Headers': 'Content-Type',
        },
      });
    } else if (request.method() === 'POST') {
      assert.equal(request.url(), pool + '/_auriga/placement-ticket');
      assert.equal(typeof request.postDataJSON().ticket, 'string');
      await route.fulfill({
        status: 302,
        headers: {
          Location: '/',
          'Access-Control-Allow-Origin': portal,
          'Access-Control-Allow-Credentials': 'true',
        },
      });
    } else {
      // The native root intentionally has no CORS headers.
      await route.fulfill({
        contentType: 'text/html',
        body: '<p>New instance session</p>',
      });
    }
  });
  await page.goto(portal);
  await Promise.all([
    page.waitForURL(pool + '/'),
    page.evaluate(
      async ({ api, pool, id }) => {
        const fetch = globalThis.fetch.bind(globalThis);
        globalThis.fetch = async (...args) => {
          const response = await fetch(...args);
          await globalThis.recordExchange({
            type: response.type,
            status: response.status,
            redirect: args[1].redirect,
          });
          return response;
        };
        const { exchangePlacement } = await import(api);
        await exchangePlacement(
          {
            placement: {
              memberId: id,
              placementId: id,
              mode: 'shared',
              state: 'ready',
              epoch: 1,
              revision: 1,
            },
            ticket: globalThis.crypto.randomUUID(),
            expiresAt: Date.now() / 1000 + 60,
            exchangeUrl: pool + '/_auriga/placement-ticket',
          },
          new globalThis.AbortController().signal,
        );
      },
      { api, pool, id },
    ),
  ]);
  assert.deepEqual(responses, [
    { type: 'opaqueredirect', status: 0, redirect: 'manual' },
  ]);
  const rootReads = requests.filter((request) => request.method === 'GET');
  assert.equal(rootReads.length, 1);
  assert.equal(rootReads[0].navigation, true);
  assert.equal(rootReads[0].url, pool + '/');
  console.log(
    'Chromium cross-origin exchange passed: opaque manual redirect and one root document navigation.',
  );
} finally {
  await browser.close();
}
