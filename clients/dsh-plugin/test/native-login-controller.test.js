import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createLocalLoginController } from '../src/login/local-controller.js';

test('desktop controller uses method-bound RPC and never sends plugin cookies or CSRF', async () => {
  let loggedIn = false;
  const calls = [];
  const controller = createLocalLoginController({
    native: true,
    fetch: async (path, init) => {
      const envelope = JSON.parse(init.body);
      assert.equal(path, '/auriga-native/' + envelope.method);
      assert.equal(init.method, 'POST');
      assert.equal(init.headers['X-Auriga-CSRF'], undefined);
      assert.equal(init.headers.cookie, undefined);
      calls.push(envelope.method);
      let value;
      if (envelope.method === 'aurigaAuth/status')
        value = {
          available: true,
          loggedIn,
          connected: false,
          resumeAvailable: false,
          remoteRevocation: 'complete',
        };
      else {
        loggedIn = true;
        value = { step: { type: 'complete', redirectTo: '/' } };
      }
      return Response.json({
        type: 'server-response',
        rpcId: envelope.rpcId,
        result: { ok: true, value },
      });
    },
  });
  const result = await controller.start({
    signal: new AbortController().signal,
  });
  assert.equal(result.type, 'complete');
  assert.deepEqual(calls, [
    'aurigaAuth/status',
    'aurigaAuth/login',
    'aurigaAuth/status',
  ]);
});

test('desktop controller refuses an RPC response for another request', async () => {
  const controller = createLocalLoginController({
    native: true,
    fetch: async () =>
      Response.json({
        type: 'server-response',
        rpcId: 'wrong',
        result: {
          ok: true,
          value: { available: true, loggedIn: true, connected: true },
        },
      }),
  });
  await assert.rejects(controller.status(new AbortController().signal));
});
