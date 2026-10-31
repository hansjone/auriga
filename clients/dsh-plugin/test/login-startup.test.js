import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';

for (const state of ['unconfigured', 'signed-out', 'signed-in'])
  test(`native index bootstrap sends only ${state} to the local login page`, async () => {
    const script = await readFile(
      new URL('../src/login/startup.js', import.meta.url),
      'utf8',
    );
    const calls = [],
      navigations = [];
    await runInNewContext(script, {
      location: { replace: (path) => navigations.push(path) },
      fetch: async (path, options) => {
        calls.push(path);
        assert.equal(options.credentials, 'same-origin');
        return {
          ok: true,
          status: 200,
          json: async () =>
            path.endsWith('/state')
              ? {
                  platformOrigin:
                    state === 'unconfigured'
                      ? ''
                      : 'https://portal.example.com',
                }
              : { loggedIn: state === 'signed-in' },
        };
      },
      Date,
      setTimeout,
    });
    assert.deepEqual(
      navigations,
      state === 'signed-out' ? ['/auriga/local-login'] : [],
    );
    assert.equal(calls[0], '/auriga/settings/state');
  });

test('native login bootstrap waits for dependencies without requesting business RPCs', async () => {
  const script = await readFile(
    new URL('../src/login/startup.js', import.meta.url),
    'utf8',
  );
  let attempts = 0;
  const navigations = [];
  await runInNewContext(script, {
    location: { replace: (path) => navigations.push(path) },
    fetch: async (path) => {
      assert.ok(
        ['/auriga/settings/state', '/auriga/local-login/status'].includes(path),
      );
      if (path.endsWith('/state'))
        return {
          ok: true,
          json: async () => ({ platformOrigin: 'https://portal.example.com' }),
        };
      if (++attempts === 1) return { ok: false, status: 503 };
      return { ok: true, json: async () => ({ loggedIn: false }) };
    },
    Date,
    setTimeout: (fn) => fn(),
  });
  assert.equal(attempts, 2);
  assert.deepEqual(navigations, ['/auriga/local-login']);
});

test('Electron boot restores its admitted owner through RPC then reloads with business admission ready', async () => {
  const script = await readFile(
    new URL('../src/login/startup.js', import.meta.url),
    'utf8',
  );
  const navigations = [];
  let loggedIn = false;
  await runInNewContext(script, {
    location: {
      protocol: 'dsh-app:',
      replace: (path) => navigations.push(path),
    },
    fetch: async (path, init) => {
      if (path === '/auriga/settings/state')
        return {
          ok: true,
          json: async () => ({ platformOrigin: 'https://portal.example.com' }),
        };
      const envelope = JSON.parse(init.body);
      assert.equal(path, '/auriga-native/' + envelope.method);
      if (envelope.method === 'aurigaAuth/login') loggedIn = true;
      return {
        ok: true,
        json: async () => ({
          type: 'server-response',
          rpcId: envelope.rpcId,
          result: {
            ok: true,
            value: envelope.method.endsWith('/status')
              ? { loggedIn, resumeAvailable: true }
              : { step: { type: 'complete' } },
          },
        }),
      };
    },
    Date,
    setTimeout,
  });
  assert.equal(loggedIn, true);
  assert.deepEqual(navigations, ['/']);
});
