import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { runtimePeer } from '../local-runtime-peer.js';

const require = createRequire(
  createRequire(import.meta.url).resolve('@deepseek-ai/dsh-llm'),
);
const { Context } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/cordis'))
);

test('runtime: platform device revocation unregisters models, clears Team/browser and requires fresh authentication', async (t) => {
  const f = await runtimePeer(t);
  const ctx = new Context(),
    fiber = await ctx.plugin(hostApi.LlmRuntime);
  const detach = await f.runtime.attachModels(ctx.llm);
  const previous = globalThis.fetch;
  t.mock.method(globalThis, 'fetch', (url, init) =>
    new URL(url).pathname === '/ai/mcp'
      ? Promise.resolve(
          Response.json({ code: 'common.unauthenticated' }, { status: 401 }),
        )
      : previous(url, init),
  );
  try {
    const attempt = { headers: {} };
    attempt.headers.cookie = f.runtime.prepare(attempt).setCookie.split(';')[0];
    await assert.rejects(f.runtime.login(attempt, { action: 'resume' }));
    assert.equal(f.runtime.resolve(f.request), null);
    assert.equal(f.registered.size, 0);
    assert.equal(f.runtime.modelStatus.reauthenticationRequired, true);
    assert.deepEqual(ctx.llm.listProviders(), []);
  } finally {
    detach();
    f.runtime.close();
    await fiber.dispose();
  }
});

for (const scenario of ['same_owner', 'other_owner', 'other_platform'])
  test(`runtime: fresh native login after unknown refresh enforces owner and platform binding (${scenario})`, async (t) => {
    const sameOwner = scenario !== 'other_owner';
    t.mock.timers.enable({ apis: ['Date'], now: 2000000000000 });
    const f = await runtimePeer(t);
    const { privateKey, publicKey } = generateKeyPairSync('ec', {
      namedCurve: 'P-256',
    });
    const previous = globalThis.fetch;
    const ctx = new Context(),
      fiber = await ctx.plugin(hostApi.LlmRuntime);
    const detach = await f.runtime.attachModels(ctx.llm);
    t.mock.method(globalThis, 'fetch', async (url, init) => {
      const path = new URL(url).pathname;
      if (path === '/api/iam/v1/login/transactions')
        return Response.json({
          id: 'native-fixture',
          nativeDevice: {
            deviceCode: 'test-code',
            expiresAt: new Date(Date.now() + 300000).toISOString(),
            interval: 1,
          },
          step: { type: 'form' },
        });
      if (path === '/api/iam/v1/login/transactions/native-fixture/advance')
        return Response.json({
          id: 'native-fixture',
          status: 'completed',
          step: { type: 'complete' },
        });
      if (path === '/oauth2/jwks')
        return Response.json({
          keys: [
            { ...publicKey.export({ format: 'jwk' }), kid: 'fresh-login' },
          ],
        });
      if (path === '/oauth2/token') {
        const body = new URLSearchParams(init.body);
        if (body.get('resource')) throw Error('response lost');
        assert.equal(
          body.get('grant_type'),
          'urn:ietf:params:oauth:grant-type:device_code',
        );
        const claims = {
          ...f.credential.claims,
          did: 'new-device',
          sub: sameOwner ? f.credential.claims.sub : 'other-owner',
          exp: Math.floor(Date.now() / 1000) + 120,
        };
        const encoded = [{ alg: 'ES256', kid: 'fresh-login' }, claims]
          .map((value) =>
            Buffer.from(JSON.stringify(value)).toString('base64url'),
          )
          .join('.');
        return Response.json({
          access_token:
            encoded +
            '.' +
            sign('sha256', Buffer.from(encoded), {
              key: privateKey,
              dsaEncoding: 'ieee-p1363',
            }).toString('base64url'),
          token_type: 'Bearer',
          scope: 'auriga.connect',
          refresh_token: 'fresh-native-refresh',
        });
      }
      return previous(url, init);
    });
    try {
      await assert.rejects(ctx.llm.listModels('auriga'));
      if (scenario === 'other_platform')
        await f.runtime.vault.update((s) => {
          s.credential.platformOrigin = 'https://other.example.com';
        });
      const request = { headers: {} },
        prepared = f.runtime.prepare(request);
      assert.equal(prepared.body.resumeAvailable, false);
      request.headers.cookie = prepared.setCookie.split(';')[0];
      await f.runtime.login(request, { action: 'begin' });
      t.mock.timers.tick(1000);
      const complete = f.runtime.login(request, {
        action: 'advance',
        input: { type: 'form', values: {} },
      });
      if (!sameOwner || scenario === 'other_platform') {
        await assert.rejects(
          complete,
          scenario === 'other_platform'
            ? /platform binding changed/
            : /local owner conflict/,
        );
        assert.equal(
          f.runtime.vault.snapshot().credential.refreshPending,
          true,
        );
        assert.equal(
          f.runtime.vault.snapshot().credential.claims.sub,
          f.credential.claims.sub,
        );
        assert.deepEqual(ctx.llm.listProviders(), []);
      } else {
        const result = await complete;
        request.headers.cookie = result.setCookie[0].split(';')[0];
        const state = f.runtime.vault.snapshot();
        assert.equal(state.credential.refreshToken, 'fresh-native-refresh');
        assert.equal(state.credential.claims.did, 'new-device');
        assert.equal(state.revocations.length, 1);
        assert.equal(state.revocations[0].token, f.credential.refreshToken);
        assert.equal(f.runtime.status(request).connected, true);
        assert.equal(f.runtime.modelStatus.reauthenticationRequired, false);
        assert.equal(ctx.llm.listProviders().length, 1);
      }
    } finally {
      detach();
      f.runtime.close();
      await fiber.dispose();
    }
  });

for (const failure of ['permission', 'unknown', 'none'])
  test(`runtime public model registration: ${failure}, Team separation and logout/unload`, async (t) => {
    const f = await runtimePeer(t);
    const { runtime, request } = f;
    const priorFetch = globalThis.fetch;
    const { privateKey, publicKey } = generateKeyPairSync('ec', {
      namedCurve: 'P-256',
    });
    let exchanges = 0;
    t.mock.method(globalThis, 'fetch', async (url, init) => {
      const path = new URL(url).pathname;
      if (path === '/oauth2/token') {
        exchanges++;
        assert.equal(
          new URLSearchParams(init.body).get('resource'),
          'urn:auriga:ai-gateway',
        );
        if (failure === 'permission')
          return Response.json({ error: 'invalid_grant' }, { status: 400 });
        if (failure === 'unknown') throw Error('test response lost');
        const now = Math.floor(Date.now() / 1000);
        const claims = {
          ...f.credential.claims,
          aud: 'auriga.ai-gateway',
          iat: now,
          exp: now + 120,
          perms: ['auriga.connect.models.invoke'],
        };
        const body = [
          { alg: 'ES256', kid: 'model-fixture', typ: 'at+jwt' },
          claims,
        ]
          .map((value) =>
            Buffer.from(JSON.stringify(value)).toString('base64url'),
          )
          .join('.');
        const token =
          body +
          '.' +
          sign('sha256', Buffer.from(body), {
            key: privateKey,
            dsaEncoding: 'ieee-p1363',
          }).toString('base64url');
        return Response.json({
          access_token: token,
          token_type: 'Bearer',
          scope: 'auriga.connect',
          expires_in: 120,
          refresh_token: 'test-rotated-refresh',
        });
      }
      if (path === '/oauth2/jwks')
        return Response.json({
          keys: [
            { ...publicKey.export({ format: 'jwk' }), kid: 'model-fixture' },
          ],
        });
      if (path === '/ai/v1/models')
        return Response.json({
          object: 'list',
          data: [
            {
              id: 'synthetic',
              object: 'model',
              created: 1,
              owned_by: 'auriga',
            },
          ],
        });
      return priorFetch(url, init);
    });
    const ctx = new Context(),
      fiber = await ctx.plugin(hostApi.LlmRuntime);
    const detach = await runtime.attachModels(ctx.llm);
    try {
      assert.equal(ctx.llm.listProviders().length, 1);
      assert.equal(runtime.status(request).connected, true);
      if (failure === 'none') {
        assert.equal((await ctx.llm.listModels('auriga'))[0].id, 'synthetic');
        assert.equal(
          runtime.vault.snapshot().credential.accessToken,
          f.credential.accessToken,
        );
        assert.equal(
          runtime.vault.snapshot().credential.refreshToken,
          'test-rotated-refresh',
        );
      } else {
        await assert.rejects(ctx.llm.listModels('auriga'));
        assert.equal(
          runtime.status(request).connected,
          failure === 'permission',
        );
        assert.equal(
          runtime.status(request).models.reauthenticationRequired,
          failure === 'unknown',
        );
        assert.equal(f.registered.size > 0, failure === 'permission');
        await assert.rejects(ctx.llm.listModels('auriga'));
        assert.equal(exchanges, 1);
      }
      assert.doesNotMatch(
        JSON.stringify(runtime.status(request)),
        /access_token|test-rotated-refresh|eyJ/,
      );
      if (failure !== 'unknown') {
        assert.equal(runtime.status(request).models.available, false);
        await runtime.logout(request);
        assert.equal(runtime.vault.snapshot().credential, null);
        assert.equal(f.registered.size, 0);
      }
      assert.deepEqual(ctx.llm.listProviders(), []);
      runtime.close();
      assert.deepEqual(ctx.llm.listProviders(), []);
    } finally {
      detach();
      runtime.close();
      await fiber.dispose();
    }
  });
