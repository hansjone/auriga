import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { producerRuntime as peer } from './shell-producer-peer.js';

test('real C login and cancel pushes before command replies are accepted by D', async (t) => {
  const started = Promise.withResolvers();
  const release = Promise.withResolvers();
  t.after(() => release.resolve({}));
  const f = await peer(t, {
    platform: {
      async begin() {
        started.resolve();
        return release.promise;
      },
    },
  });
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  await f.runtime.command(f.request, 'open_login');
  await started.promise;
  assert.equal(f.runtime.status(f.request).platform, 'authenticating');
  await f.runtime.command(f.request, 'cancel_login');
  assert.equal(f.runtime.status(f.request).platform, 'signed_out');
  release.resolve({});
  await Promise.allSettled([...f.source.broker.inflight]);
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.source.opened.length, 0);
});

test('real C logout push before its command reply succeeds and fences the owner', async (t) => {
  const f = await peer(t, { loggedIn: true });
  await nextTurn();
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  const response = await f.runtime.command(f.request, 'logout');
  assert.equal(response.loggedIn, false);
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.source.broker.record.tokens, undefined);
});
