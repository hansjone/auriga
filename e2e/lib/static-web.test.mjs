import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  entryAssets,
  assertStaticAsset,
  verifyStaticWeb,
} from './static-web.mjs';

const response = (text, mime = 'text/html; charset=utf-8', status = 200) => ({
  status,
  text,
  res: { headers: { get: () => mime } },
});
const html = (audience = 'portal', js = '/assets/index-Ab12.js') =>
  `<!doctype html><html><head><title>Auriga ${audience}</title>
  <script type="module" src="${js}"></script>
  <link rel="stylesheet" href="./assets/index-CD34.css"></head>
  <body><div id="app"></div></body></html>`;

test('portal/admin entry and actual local JS/CSS are checked', async () => {
  for (const audience of ['portal', 'admin']) {
    const requests = [];
    await verifyStaticWeb(async (path) => {
      requests.push(path);
      if (path === '/login') return response(html(audience));
      if (path.endsWith('.js'))
        return response('const app = {}; app.mount?.();', 'text/javascript');
      return response('body { color: red; }', 'text/css');
    }, audience);
    assert.deepEqual(requests, [
      '/login',
      '/assets/index-Ab12.js',
      '/assets/index-CD34.css',
    ]);
  }
});

test('404 static_unconfigured, API JSON, wrong audience and missing assets fail', () => {
  for (const bad of [
    response(
      '{"code":"edge.static_unconfigured"}',
      'application/problem+json',
      404,
    ),
    response('{"ok":true}', 'application/json'),
    response(html('admin')),
    response(html().replace(/<script[^>]*><\/script>/, '')),
    response(html().replace(/<link[^>]*>/, '')),
  ]) {
    assert.throws(() => entryAssets(bad, 'portal'));
  }
});

test('unsafe asset references fail before fetching any resource', async () => {
  for (const path of [
    'https://other.example/assets/app.js',
    '//other.example/assets/app.js',
    '/api/session.js',
    '/assets/../session.js',
    '/assets/%2e%2e.js',
    '/assets/app.js?token=secret',
    '/assets/app.js#fragment',
    'data:text/javascript,alert(1)',
  ]) {
    const requested = [];
    await assert.rejects(
      verifyStaticWeb(async (url) => {
        requested.push(url);
        return response(html('portal', path));
      }, 'portal'),
    );
    assert.deepEqual(requested, ['/login']);
  }
});

test('resource status and MIME fail closed, including HTML fallback and JSON', () => {
  for (const [bad, kind] of [
    [response('missing', 'text/javascript', 404), 'js'],
    [response(html()), 'js'],
    [response(html(), 'text/javascript'), 'js'],
    [response('{"ok":true}', 'text/javascript'), 'js'],
    [response('', 'text/javascript'), 'js'],
    [response('body{}', 'text/html'), 'css'],
    [response(html(), 'text/css'), 'css'],
    [response('{"ok":true}', 'text/css'), 'css'],
  ]) {
    assert.throws(() => assertStaticAsset(bad, kind));
  }
  assert.doesNotThrow(() =>
    assertStaticAsset(
      response('export const a = 1;', 'application/javascript; charset=utf-8'),
      'js',
    ),
  );
});
