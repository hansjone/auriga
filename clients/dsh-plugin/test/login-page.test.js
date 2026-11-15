import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';

// The removed plugin login surface must stay absent for reads and writes.
for (const method of ['GET', 'POST'])
  for (const path of [
    '',
    '?logout=1',
    '/status',
    '/login',
    '/logout',
    '/cancel',
    '/assets/adapter.js',
    '/assets/index.js',
    '/assets/styles.js',
    '/assets/errors.js',
    '/assets/local-controller.js',
    '/assets/logout.js',
    '/assets/page.js',
    '/assets/../runtime.js',
  ])
    test(`removed local login route ${method} ${path} never serves a page or writes`, async (t) => {
      const f = await shellRuntimePeer(t);
      const before = f.connection.calls.length;
      const result = await f.http('/auriga/local-login' + path, {
        method,
        body: method === 'POST' ? { action: 'begin' } : undefined,
      });
      assert.equal(result.status, 404);
      assert.equal(result.headers['cache-control'], 'no-store');
      assert.equal(result.headers['x-content-type-options'], 'nosniff');
      assert.equal(f.connection.calls.length, before);
      assert.ok(f.runtime.resolve(f.request));
      assert.equal(f.runtime.login, undefined);
    });
