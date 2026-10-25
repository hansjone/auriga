import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { generateKeyPairSync, sign } from 'node:crypto';
import { mkdtemp, rm, mkdir, rename, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { createPlatform, platformConfig } from '../../src/local/platform.js';
import { createVault } from '../../src/local/vault.js';
import { createRefreshBroker } from '../../src/ai/refresh-broker.js';
import { registerAurigaAdapter } from '../../src/ai/index.js';

const require = createRequire(
  createRequire(import.meta.url).resolve('@deepseek-ai/dsh-llm'),
);
const { Context } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/cordis'))
);

async function wire(t) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-broker-wire-'));
  const { privateKey, publicKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  let origin,
    refresh = 'test-refresh-0',
    lost = false,
    permission = true;
  const exchanges = [],
    modelRequests = [];
  const identity = { tid: 'tenant', sub: 'owner', did: 'device' };
  function jwt(ai, patch = {}) {
    const now = Math.floor(Date.now() / 1000);
    const claims = {
      iss: origin,
      ...identity,
      aud: ai ? 'auriga.ai-gateway' : 'auriga',
      ptyp: 'device',
      ctx: 'device',
      scope: 'auriga.connect',
      iat: now,
      exp: now + 120,
      ...(ai ? { perms: ['auriga.connect.models.invoke'] } : {}),
      ...patch,
    };
    const encoded = [{ alg: 'ES256', kid: 'ephemeral', typ: 'at+jwt' }, claims]
      .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
      .join('.');
    return {
      claims,
      token:
        encoded +
        '.' +
        sign('sha256', Buffer.from(encoded), {
          key: privateKey,
          dsaEncoding: 'ieee-p1363',
        }).toString('base64url'),
    };
  }
  const server = createServer(async (req, res) => {
    assert.equal(req.headers.cookie, undefined);
    assert.equal(req.headers.origin, undefined);
    const json = (status, data) => {
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(data));
    };
    if (req.url === '/oauth2/jwks')
      return json(200, {
        keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'ephemeral' }],
      });
    let text = '';
    for await (const part of req) text += part;
    if (req.url === '/oauth2/token') {
      const form = new URLSearchParams(text);
      exchanges.push(Object.fromEntries(form));
      assert.equal(
        form.get('refresh_token'),
        refresh,
        'no replay/stale queued refresh',
      );
      const ai = form.get('resource') === 'urn:auriga:ai-gateway';
      if (ai && !permission) return json(400, { error: 'invalid_grant' });
      refresh = `test-refresh-${exchanges.length}`;
      if (lost) {
        req.socket.destroy();
        return;
      }
      return json(200, {
        token_type: 'Bearer',
        scope: 'auriga.connect',
        expires_in: 120,
        refresh_token: refresh,
        access_token: jwt(ai).token,
      });
    }
    assert.equal(req.url, '/ai/v1/chat/completions');
    assert.equal(req.method, 'POST');
    const claims = JSON.parse(
      Buffer.from(req.headers.authorization.split('.')[1], 'base64url'),
    );
    assert.equal(claims.aud, 'auriga.ai-gateway');
    modelRequests.push(JSON.parse(text));
    // Synthetic upstream rule: each complete text message is EXACTLY one
    // input token. This rule is only a fixture; no real tokenizer is inferred.
    assert.equal(modelRequests.at(-1).messages.length, 1);
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    res.write(
      'data: ' +
        JSON.stringify({
          id: 'fake',
          object: 'chat.completion.chunk',
          created: 1,
          model: 'synthetic',
          choices: [
            { index: 0, delta: { content: 'test' }, finish_reason: null },
          ],
        }) +
        '\n\n',
    );
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  origin = 'http://127.0.0.1:' + server.address().port;
  const config = platformConfig({
    platformOrigin: origin,
    issuer: origin,
    clientId: 'auriga-dsh',
    providerId: 'team',
    allowLocalHttp: true,
  });
  const vault = await createVault(directory);
  const saved = jwt(false, { exp: Math.floor(Date.now() / 1000) + 1 });
  await vault.update((s) => {
    s.credential = {
      accessToken: saved.token,
      claims: saved.claims,
      refreshToken: refresh,
      platformOrigin: origin,
      issuer: origin,
      clientId: config.clientId,
    };
  });
  const broker = createRefreshBroker({
    vault,
    platform: createPlatform(config),
    config,
  });
  t.after(async () => {
    broker.invalidate('closed');
    vault.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(directory, { recursive: true, force: true });
  });
  return {
    broker,
    vault,
    directory,
    origin,
    config,
    exchanges,
    modelRequests,
    lose: () => {
      lost = true;
    },
    deny: () => {
      permission = false;
    },
  };
}

test('wire: fake HTTP platform, real ES256/vault and public LlmRuntime; refresh never extends old stream and logout cancels new stream', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: 2000000000000 });
  const f = await wire(t);
  await Promise.all([
    f.broker.getAiAccess(),
    f.broker.getPlatformAccess(),
    f.broker.getAiAccess(),
  ]);
  assert.equal(f.exchanges.length, 2);
  const ctx = new Context(),
    fiber = await ctx.plugin(hostApi.LlmRuntime);
  const registration = registerAurigaAdapter(ctx.llm, {
    hostApi,
    apiBase: f.origin,
    fetch,
    getAccessToken: () => f.broker.getAiAccess(),
    modelLimits: { synthetic: { maxOutputTokens: 32, contextWindow: 100 } },
    countInputTokens(body) {
      assert.equal(body.messages.length, 1);
      return body.messages.length;
    },
  });
  const options = {
    provider: 'auriga',
    model: 'synthetic',
    maxTokens: 8,
    messages: [
      hostApi.createUserMessage({
        source: { kind: 'user' },
        content: [{ type: 'text', text: 'synthetic-only' }],
      }),
    ],
  };
  const old = ctx.llm.stream(options)[Symbol.asyncIterator]();
  const next = ctx.llm.stream(options)[Symbol.asyncIterator]();
  try {
    await old.next();
    t.mock.timers.tick(100000);
    const fresh = await f.broker.getAiAccess();
    await next.next();
    t.mock.timers.tick(21000);
    const terminal = (await old.next()).value;
    assert.equal(terminal.reason.failure.code, 'AUTH');
    assert.ok(fresh.expiresAt > Date.now());
    assert.equal(fresh.signal.aborted, false);
    f.broker.invalidate('signed_out');
    assert.equal((await next.next()).value.reason.kind, 'error');
    assert.equal(f.modelRequests.length, 2);
    assert.equal(f.exchanges.length, 3);
    assert.doesNotMatch(
      JSON.stringify(f.vault.snapshot()),
      /auriga.ai-gateway/,
    );
    assert.doesNotMatch(
      await readFile(join(f.directory, 'connect.json'), 'utf8'),
      /test-refresh|synthetic-only/,
    );
  } finally {
    await old.return();
    await next.return();
    registration.dispose();
    await fiber.dispose();
  }
});

test('wire: consumed response loss persists fence across real vault restart; no automatic replay', async (t) => {
  const f = await wire(t);
  f.lose();
  await assert.rejects(f.broker.getAiAccess(), { code: 'REAUTH_REQUIRED' });
  const reopened = await createVault(f.directory);
  try {
    const restarted = createRefreshBroker({
      vault: reopened,
      platform: createPlatform(f.config),
      config: f.config,
    });
    await assert.rejects(restarted.getPlatformAccess(), {
      code: 'REAUTH_REQUIRED',
    });
    assert.equal(f.exchanges.length, 1);
  } finally {
    reopened.close();
  }
});

test('wire: filesystem save failure after a successful rotation exposes no AI token and retains pending fence', async (t) => {
  const f = await wire(t);
  const update = f.vault.update.bind(f.vault);
  t.mock.method(f.vault, 'update', async (change) => {
    // First write is the pending fence. Sabotage ONLY the final atomic save.
    if (f.exchanges.length) {
      await rename(
        join(f.directory, 'connect.json'),
        join(f.directory, 'old.json'),
      );
      await mkdir(join(f.directory, 'connect.json'));
    }
    return update(change);
  });
  await assert.rejects(f.broker.getAiAccess(), { code: 'REAUTH_REQUIRED' });
  assert.equal(f.vault.snapshot().credential.refreshPending, true);
  await assert.rejects(f.broker.getAiAccess(), { code: 'REAUTH_REQUIRED' });
  assert.equal(f.exchanges.length, 1);
});

test('wire: explicit AI permission denial leaves platform access usable and refresh unchanged', async (t) => {
  const f = await wire(t);
  const platform = await f.broker.getPlatformAccess();
  const refresh = f.vault.snapshot().credential.refreshToken;
  f.deny();
  await assert.rejects(f.broker.getAiAccess(), { code: 'AI_NOT_AUTHORIZED' });
  assert.equal(await f.broker.getPlatformAccess(), platform);
  assert.equal(f.vault.snapshot().credential.refreshToken, refresh);
  assert.equal(f.vault.snapshot().credential.refreshPending, false);
});
