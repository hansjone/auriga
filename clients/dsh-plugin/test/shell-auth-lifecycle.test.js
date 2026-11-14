import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { randomUUID, randomBytes } from 'node:crypto';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { fixture, command } from '../../shell-auth/test/fixtures.mjs';
import { createLoopback } from '../../shell-auth/loopback.mjs';
import { valid } from '../../shell-auth/schema.mjs';
import { Vault } from '../../shell-auth/vault.mjs';
import { buildHelper } from '../../shell-auth/windows/build.mjs';
import { createPlatform } from '../../shell-auth/platform.mjs';
import { producerRuntime } from './shell-producer-peer.js';

const secret = () => randomBytes(32).toString('base64url');

test('cancelled shell browser attempt rejects stale revision and invalid CSRF; a late grant cannot sign D in', async (t) => {
  const entered = Promise.withResolvers(),
    grant = Promise.withResolvers();
  // Release blocked producer work before the peer cleanup waits for it.
  t.after(() => grant.resolve({}));
  const source = fixture({
    async poll() {
      entered.resolve();
      return grant.promise;
    },
  });
  const f = await producerRuntime(t, { source });
  const loop = await createLoopback({ broker: source.broker, validate: valid });
  t.after(() => loop.close());
  const nonce = secret();
  const headers = {
    origin: loop.origin,
    'content-type': 'application/json',
    'sec-fetch-site': 'same-origin',
    'sec-fetch-mode': 'cors',
    'sec-fetch-dest': 'empty',
  };
  const post = (path, body, extra = {}) =>
    fetch(loop.origin + path, {
      method: 'POST',
      headers: { ...headers, ...extra },
      body: JSON.stringify(body),
    });
  const ticket = new URL(loop.issue()).hash.slice(1);
  const exchange = await post('/loopback/exchange', {
    ticket,
    clientNonce: nonce,
  });
  assert.equal(exchange.status, 200);
  const page = await exchange.json();
  assert.equal(
    (await post('/loopback/exchange', { ticket, clientNonce: nonce })).status,
    403,
  );
  const proofs = {
    'x-auriga-shell-token': page.pageToken,
    'x-auriga-shell-csrf': page.csrf,
    'x-auriga-shell-nonce': nonce,
  };
  const begin = command(source.broker, 'open_login');
  assert.equal(
    (
      await post('/loopback/messages', begin, {
        ...proofs,
        'x-auriga-shell-csrf': secret(),
      })
    ).status,
    403,
  );
  assert.equal(source.opened.length, 0);
  assert.equal((await post('/loopback/messages', begin, proofs)).status, 200);
  await entered.promise;
  const oldAttempt = command(source.broker, 'open_login');
  assert.equal(
    (
      await post(
        '/loopback/messages',
        command(source.broker, 'cancel_login'),
        proofs,
      )
    ).status,
    200,
  );
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  assert.equal(
    (
      await post(
        '/loopback/messages',
        { ...oldAttempt, requestId: randomUUID() },
        proofs,
      )
    ).status,
    409,
  );
  grant.resolve(source.tokens);
  await Promise.allSettled([...source.broker.inflight]);
  assert.equal(source.broker.record.tokens, undefined);
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(source.revoked.includes(source.tokens.refresh_token), true);
  assert.equal(source.revoked.includes(source.tokens.access_token), true);
  // Page CSRF is port/epoch scoped in K2, rather than a retired plugin login cookie.
  assert.equal(f.runtime.login, undefined);
  assert.equal(f.runtime.cancelAttempt, undefined);
});

test(
  'shell logout fences D before failed revocation HTTP and DPAPI compensation survives restart',
  { skip: process.platform !== 'win32', timeout: 30000 },
  async (t) => {
    const root = await mkdtemp(join(tmpdir(), 'auriga-revoke-compensation-'));
    let active,
      unavailable = true;
    const seen = [];
    const server = createServer(async (req, res) => {
      let body = '';
      for await (const chunk of req) body += chunk;
      seen.push({ path: req.url, body, origin: req.headers.origin });
      assert.equal(active.runtime.resolve(active.request), undefined);
      assert.equal(active.source.broker.record.tokens, undefined);
      res.writeHead(unavailable ? 503 : 200, {
        'content-type': 'application/json',
      });
      res.end(
        JSON.stringify(unavailable ? { code: 'common.unavailable' } : {}),
      );
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const remote = createPlatform({
      platformOrigin: origin,
      issuer: origin,
      clientId: 'auriga-dsh',
    });
    const source = fixture({ revoke: remote.revoke });
    let resumed;
    try {
      const helper = buildHelper(join(root, 'helper.exe'));
      const protectedVault = new Vault(join(root, 'private'), helper);
      source.broker.vault = protectedVault;
      active = await producerRuntime(t, { source, loggedIn: true });
      await nextTurn();
      assert.equal(active.runtime.status(active.request).loggedIn, true);
      assert.equal(
        (await active.runtime.command(active.request, 'logout')).loggedIn,
        false,
      );
      await Promise.allSettled([...source.broker.inflight]);
      assert.equal(seen.length, 1, 'a failure stops the bounded drain');
      for (const request of seen) {
        assert.equal(request.path, '/oauth2/revoke');
        assert.equal(request.origin, undefined);
        const form = new URLSearchParams(request.body);
        assert.equal(form.get('client_id'), 'auriga-dsh');
        assert.ok(
          [source.tokens.refresh_token, source.tokens.access_token].includes(
            form.get('token'),
          ),
        );
      }
      assert.equal(source.broker.record.manualLoginRequired, true);
      assert.equal(source.broker.record.revocations.length, 2);
      const ciphertext = await readFile(protectedVault.file);
      assert.equal(
        ciphertext.includes(Buffer.from(source.tokens.refresh_token)),
        false,
      );
      assert.equal(
        ciphertext.includes(Buffer.from(source.tokens.access_token)),
        false,
      );
      await active.runtime.close();
      await source.broker.close();
      let refreshed = 0;
      resumed = fixture({
        ...source.platform,
        refresh: async () => {
          refreshed++;
          throw new Error('unexpected refresh');
        },
      });
      resumed.broker.vault = new Vault(join(root, 'private'), helper);
      const restored = await producerRuntime(t, { source: resumed });
      assert.equal(restored.runtime.status(restored.request).loggedIn, false);
      assert.equal(restored.runtime.resolve(active.request), undefined);
      assert.equal(resumed.broker.record.tokens, undefined);
      assert.equal(resumed.broker.record.revocations.length, 2);
      assert.equal(
        seen.length,
        2,
        'only the persisted revocation queue is retried',
      );
      unavailable = false;
      await resumed.broker.drainRevocations();
      assert.equal(seen.length, 4);
      assert.deepEqual(resumed.broker.record.revocations, []);
      assert.equal(restored.runtime.status(restored.request).loggedIn, false);
      assert.equal(refreshed, 0);
      await restored.runtime.close();
    } finally {
      await active?.runtime.close();
      await source.broker.close();
      await resumed?.broker.close();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await rm(root, { recursive: true, force: true });
    }
  },
);

test('status and port CSRF cannot mint identity or reuse an old process owner after shell logout', async (t) => {
  const first = await producerRuntime(t);
  const unadmitted = { headers: {} };
  assert.equal(first.runtime.prepare(unadmitted).body.csrf, undefined);
  assert.equal(first.runtime.resolve(unadmitted), undefined);
  const csrf = first.runtime.prepare(first.request).body.csrf;
  unadmitted.headers['x-auriga-csrf'] = csrf;
  assert.equal(first.runtime.checkCsrf(unadmitted), false);
  first.request.headers['x-auriga-csrf'] = csrf;
  assert.equal(first.runtime.checkCsrf(first.request), true);
  assert.equal(first.runtime.status(first.request).loggedIn, false);
  await first.runtime.command(first.request, 'open_login');
  await Promise.allSettled([...first.source.broker.inflight]);
  await nextTurn();
  assert.equal(first.runtime.status(first.request).loggedIn, true);
  await first.runtime.command(first.request, 'logout');
  assert.equal(first.runtime.resolve(first.request), undefined);
  await first.runtime.close();
  await first.source.broker.close();
  const resumed = fixture(first.source.platform);
  resumed.broker.vault = first.source.vault;
  const next = await producerRuntime(t, { source: resumed });
  assert.equal(next.runtime.prepare(next.request).body.loggedIn, false);
  assert.equal(next.runtime.resolve(first.request), undefined);
  assert.equal(next.runtime.checkCsrf(first.request), false);
  next.request.headers['x-auriga-csrf'] = csrf;
  assert.equal(next.runtime.checkCsrf(next.request), false);
  assert.notEqual(next.runtime.prepare(next.request).body.csrf, csrf);
  assert.equal(next.runtime.getAccess, undefined);
});
