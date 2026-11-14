import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fixture, login, OWNER } from '../../shell-auth/test/fixtures.mjs';
import { createConnectRuntime } from '../src/local/runtime.js';
import { validate } from './broker-fixture.js';

// Actual C control producer and D consumer. Only IAM and private IPC peer
// authentication are replaced; no process, browser or external network starts.
async function peer(t, options = {}) {
  const source = fixture(options.platform);
  await source.broker.initialize();
  if (options.loggedIn) await login(source);
  const directory = await mkdtemp(join(tmpdir(), 'auriga-producer-consumer-'));
  let unsubscribe;
  const runtime = await createConnectRuntime({
    config: { platformOrigin: '' },
    directory,
    ctx: {},
    connection: {
      validate,
      ipc: {
        call(command) {
          if (command.path || command.call)
            throw new Error('business operation not configured');
          return source.broker.command(command, OWNER);
        },
        subscribe(command, receive) {
          unsubscribe = source.broker.subscribe(command, OWNER, receive);
        },
        close() {
          unsubscribe?.();
        },
      },
    },
  });
  t.after(async () => {
    await runtime.close();
    await source.broker.close();
    await rm(directory, { recursive: true, force: true });
  });
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  return { source, runtime, request };
}

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
