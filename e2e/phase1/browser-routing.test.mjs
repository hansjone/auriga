import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { launchBrowser } from './launch.mjs';

test('loopback browser navigation, fetch and an anonymous page reach the configured authority', async () => {
  // Infrastructure-only fixture on a random port; not an Auriga login/READY proof.
  const requests = [];
  const server = createServer((req, res) => {
    requests.push({ host: req.headers.host, path: req.url });
    res.writeHead(req.url === '/auriga/bootstrap' ? 401 : 200, {
      'content-type': 'text/html',
    });
    res.end('<!doctype html><title>Routing fixture</title>');
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const access = {
    mode: 'domain',
    portalOrigin: { scheme: 'http', host: 'portal.example.com', port },
    adminOrigin: { scheme: 'http', host: 'admin.example.com', port },
    instances: {
      mode: 'subdomain',
      scheme: 'http',
      parentDomain: 'i.example.com',
      port,
    },
  };
  let browser;
  try {
    browser = await launchBrowser(access, { loopback: true });
    const context = await browser.newContext();
    const page = await context.newPage();
    const response = await page.goto(`http://admin.example.com:${port}/login`);
    assert.equal(response.status(), 200);
    assert.equal(
      await page.evaluate(
        async () => (await globalThis.fetch('/probe')).status,
      ),
      200,
    );
    const anonymous = await browser.newContext();
    const denied = await (
      await anonymous.newPage()
    ).goto(`http://test.i.example.com:${port}/auriga/bootstrap`);
    assert.equal(denied.status(), 401);
    assert.ok(
      requests.some(
        (r) => r.host === `admin.example.com:${port}` && r.path === '/probe',
      ),
    );
    assert.ok(
      requests.some(
        (r) =>
          r.host === `test.i.example.com:${port}` &&
          r.path === '/auriga/bootstrap',
      ),
    );
  } finally {
    await browser?.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
});
