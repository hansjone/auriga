import { test } from 'node:test';
/* global Headers, structuredClone */
import assert from 'node:assert/strict';
import {
  mkdtemp,
  readFile,
  mkdir,
  rename,
  rm,
  readdir,
} from 'node:fs/promises';
import { generateKeyPairSync } from 'node:crypto';
import { setImmediate } from 'node:timers/promises';
import { createSettingsStore } from '../src/settings/store.js';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createVault } from '../src/local/vault.js';
import { createServer } from 'node:http';
import {
  createPlatform,
  platformConfig,
  LoginJar,
} from '../src/local/platform.js';
import { createConnectRuntime, cookie } from '../src/local/runtime.js';
import { createTeam } from '../src/local/team.js';
import { runtimePeer } from './local-runtime-peer.js';

const config = {
  platformOrigin: 'https://portal.example.com',
  issuer: 'https://portal.example.com',
  clientId: 'auriga-dsh',
  providerId: 'team',
};
test('loader network installation selects official device default after login and never leaks to settings/status', async (t) => {
  const network = {
    realm: '1',
    wssUrl: 'wss://network.example.com:9443/custom/ws',
    roots: [
      {
        kid: 'root',
        public_key_spki: generateKeyPairSync('ed25519')
          .publicKey.export({ format: 'der', type: 'spki' })
          .toString('base64url'),
      },
    ],
  };
  const expected = structuredClone(network);
  const { runtime, request, directory, requests, registered } =
    await runtimePeer(t, { config: { network } });
  network.realm = '2';
  network.roots[0].kid = 'injected';
  assert.equal(runtime.networkStatus.available, true);
  assert.equal(runtime.networkStatus.connected, false);
  assert.equal(
    requests.some((path) => /tickets|keys|net\/v1/.test(path)),
    false,
  );
  assert.equal(
    [...registered].some((name) => /send/i.test(name)),
    false,
  );
  // Official default may already own its writer lock while register is pending.
  assert.ok(Array.isArray(await readdir(directory)));
  const settings = await createSettingsStore(directory, config.platformOrigin);
  await assert.rejects(
    settings.save({ ...settings.snapshot(), network: expected }),
    /invalidSettings/,
  );
  for (const value of [
    runtime.status(request),
    runtime.networkStatus,
    settings.snapshot(),
  ]) {
    assert.equal(JSON.stringify(value).includes('public_key_spki'), false);
    assert.equal(JSON.stringify(value).includes(expected.wssUrl), false);
  }
  // Even an internal adapter bypass cannot promote request data to installation authority.
  const attempt = { headers: {} };
  attempt.headers.cookie = runtime.prepare(attempt).setCookie.split(';')[0];
  await runtime.login(attempt, { action: 'resume', network });
  let prepared;
  const detach = runtime.attachNetwork({
    ports: {},
    prepare(input) {
      prepared = input;
      return new Promise(() => {});
    },
  });
  const deadline = Date.now() + 2000;
  while (!prepared) {
    assert.ok(Date.now() < deadline, 'trusted prepare timeout');
    await setImmediate();
  }
  assert.deepEqual(prepared.installation, expected);
  assert.ok(Object.isFrozen(prepared.installation.roots[0]));
  detach();
  await runtime.close();
});
test('cancelled browser attempt loses its CSRF and cannot be advanced or restarted under the old proof', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-attempt-cancel-'));
  try {
    const runtime = await createConnectRuntime({
      config,
      directory,
      ctx: { webServer: { protocol: 'http:' } },
    });
    const request = { headers: {} };
    const prepared = runtime.prepare(request);
    request.headers.cookie = prepared.setCookie.split(';')[0];
    request.headers['x-auriga-csrf'] = prepared.body.csrf;
    assert.equal(runtime.checkCsrf(request), true);
    await runtime.cancelAttempt(request);
    assert.equal(runtime.checkCsrf(request), false);
    await assert.rejects(
      runtime.login(request, { action: 'begin' }),
      /login attempt expired/,
    );
    assert.equal(runtime.resolve(request), null);
    runtime.close();
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
test('logout tombstone precedes real failed revocation HTTP; encrypted compensation survives restart without a browser session', async (t) => {
  let runtime;
  const seen = [];
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const part of req) body += part;
    seen.push({
      path: req.url,
      body,
      origin: req.headers.origin,
      credential: runtime.vault.snapshot().credential,
    });
    res.writeHead(503, { 'content-type': 'text/plain' });
    res.end('common.unavailable');
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const options = {
    config: {
      ...config,
      platformOrigin: origin,
      issuer: origin,
      allowLocalHttp: true,
    },
    ctx: { webServer: { protocol: 'http:' } },
  };
  try {
    const fixture = await runtimePeer(t, {
      config: options.config,
      revokeHttp: true,
    });
    runtime = fixture.runtime;
    const { request, directory } = fixture;
    options.directory = directory;
    const result = await runtime.logout(request);
    assert.equal(result.body.loggedIn, false);
    assert.equal(result.body.remoteRevocation, 'pending');
    assert.equal(seen.length, 1);
    assert.equal(seen[0].path, '/oauth2/revoke');
    assert.equal(seen[0].origin, undefined);
    assert.equal(seen[0].credential, null);
    assert.equal(
      new URLSearchParams(seen[0].body).get('client_id'),
      config.clientId,
    );
    assert.equal(
      new URLSearchParams(seen[0].body).get('token'),
      'invalid-revocation-test-proof',
    );
    assert.doesNotMatch(
      await readFile(join(directory, 'connect.json'), 'utf8'),
      /invalid-revocation-test-proof/,
    );
    runtime.close();
    runtime = await createConnectRuntime(options);
    assert.equal(runtime.resolve(request), null);
    assert.equal(runtime.vault.snapshot().credential, null);
    assert.equal(runtime.status(request).remoteRevocation, 'pending');
  } finally {
    runtime?.close();
    await new Promise((resolve) => server.close(resolve));
  }
});
test('real HTTP negatives preserve 401 and Node requests omit browser Origin; redirects are refused', async () => {
  const seen = [];
  const server = createServer((req, res) => {
    seen.push({ path: req.url, origin: req.headers.origin });
    if (req.url === '/redirect') {
      res.writeHead(302, { location: '/should-not-follow' });
      res.end();
    } else {
      res.writeHead(401, { 'content-type': 'text/plain' });
      res.end('common.unauthenticated');
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  try {
    const origin = 'http://127.0.0.1:' + server.address().port;
    const platform = createPlatform(
      platformConfig({
        ...config,
        platformOrigin: origin,
        issuer: origin,
        allowLocalHttp: true,
      }),
    );
    await assert.rejects(
      platform.request('/ai/mcp', {
        method: 'POST',
        body: { jsonrpc: '2.0', id: 1, method: 'ping' },
        token: 'invalid-test-proof',
      }),
      (error) => error.status === 401,
    );
    assert.equal(seen[0].origin, undefined);
    await assert.rejects(platform.request('/redirect'));
    assert.equal(seen.length, 2);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});
test('model input cannot select actor/session and missing trusted execution fails before network', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-context-'));
  try {
    const vault = await createVault(directory),
      platform = createPlatform(platformConfig(config));
    let credentialsRead = 0;
    const team = createTeam({
      platform,
      vault,
      getAccess: async () => {
        credentialsRead++;
        throw new Error('not signed in');
      },
      assertActive() {},
      ctx: {},
      label: 'Test',
    });
    await assert.rejects(
      team.toolCall('auriga_open_thread', { sessionId: 'invented' }, {}),
      /trusted DSH execution required/,
    );
    const exec = {
      agent: { session: { id: 'context-only' } },
      callId: 'call-1',
      rootCallId: 'root-1',
      signal: new AbortController().signal,
    };
    for (const key of ['actor', 'tenantId', 'accountId', 'sessionId', '_meta'])
      await assert.rejects(
        team.toolCall('auriga_open_thread', { [key]: 'invented' }, exec),
        /untrusted Team arguments/,
      );
    assert.equal(credentialsRead, 0);
    team.close();
    vault.close();
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
test('installation config refuses redirects, non-HTTPS, foreign issuer and non-loopback test flag', () => {
  assert.equal(platformConfig(config).platformOrigin, config.platformOrigin);
  for (const patch of [
    { platformOrigin: 'http://example.com' },
    { platformOrigin: 'https://portal.example.com/path' },
    { platformOrigin: 'https://alice@portal.example.com' },
    { issuer: 'https://other.example.com' },
    { allowLocalHttp: true },
    { clientId: '' },
  ])
    assert.throws(() => platformConfig({ ...config, ...patch }));
  assert.equal(
    platformConfig({
      ...config,
      platformOrigin: 'http://127.0.0.1:8080',
      issuer: 'http://127.0.0.1:8080',
      allowLocalHttp: true,
    }).platformOrigin,
    'http://127.0.0.1:8080',
  );
});
test('JWT role/issuer/audience/expiry/algorithm rejected before any network call', async () => {
  const platform = createPlatform(platformConfig(config));
  for (const header of [
    { alg: 'none', kid: 'x' },
    { alg: 'HS256', kid: 'x' },
    { alg: 'ES256', kid: 'x', jku: 'https://other.example.com' },
  ]) {
    const token = [
      header,
      {
        iss: config.issuer,
        aud: 'auriga',
        ptyp: 'user',
        ctx: 'portal',
        exp: Math.floor(Date.now() / 1000) + 60,
        iat: Math.floor(Date.now() / 1000),
      },
      'signature',
    ]
      .map((x, i) =>
        i < 2 ? Buffer.from(JSON.stringify(x)).toString('base64url') : x,
      )
      .join('.');
    await assert.rejects(
      platform.verifyDevice(token),
      /invalid device credential/,
    );
  }
  await assert.rejects(
    platform.request('//other.example.com/oauth2/token'),
    /untrusted platform path/,
  );
});
test('login cookie jar is isolated, host-only and clears sensitive material', () => {
  const first = new LoginJar(),
    second = new LoginJar();
  first.accept(
    new Headers({
      'set-cookie': 'auriga_session=opaque; Path=/; HttpOnly; Secure',
    }),
  );
  assert.equal(first.header('/oauth2/token'), 'auriga_session=opaque');
  assert.equal(second.header('/oauth2/token'), '');
  assert.throws(
    () =>
      second.accept(
        new Headers({ 'set-cookie': 'session=x; Domain=example.com; Path=/' }),
      ),
    /domain cookie refused/,
  );
  first.clear();
  assert.equal(first.header('/'), '');
  assert.equal(
    cookie(
      { headers: { cookie: 'auriga_local_session=a; auriga_local_session=b' } },
      'auriga_local_session',
    ),
    undefined,
  );
});
test('vault encrypts state, serializes mutations and refuses corrupt ciphertext', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-vault-'));
  try {
    const vault = await createVault(directory);
    await Promise.all([
      vault.update((s) => {
        s.credential = {
          accessToken: 'private-access',
          refreshToken: 'private-refresh',
        };
      }),
      vault.update((s) => {
        s.endpoint = 'endpoint';
      }),
    ]);
    const raw = await readFile(join(directory, 'connect.json'), 'utf8');
    assert.doesNotMatch(raw, /private-access|private-refresh|endpoint/);
    const next = await createVault(directory);
    assert.equal(next.snapshot().credential.refreshToken, 'private-refresh');
    assert.equal(next.snapshot().endpoint, 'endpoint');
    next.close();
    vault.close();
    const { writeFile } = await import('node:fs/promises');
    await writeFile(join(directory, 'connect.json'), '"invalid"');
    await assert.rejects(createVault(directory));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
test('real filesystem write failure cannot publish credential/accepted delivery or deletion', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-vault-failure-'));
  try {
    const vault = await createVault(directory);
    await vault.update((s) => {
      s.credential = { accessToken: 'old' };
    });
    await rename(
      join(directory, 'connect.json'),
      join(directory, 'preserved.json'),
    );
    await mkdir(join(directory, 'connect.json'));
    await assert.rejects(
      vault.update((s) => {
        s.credential = null;
        s.deliveries.delivery = { accepted: true };
      }),
    );
    assert.equal(vault.snapshot().credential.accessToken, 'old');
    assert.equal(vault.snapshot().deliveries.delivery, undefined);
    vault.close();
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
test('runtime status/CSRF cannot mint a session and logout cannot restore a previous process bearer', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-local-state-'));
  try {
    const runtime = await createConnectRuntime({
      config,
      directory,
      ctx: { webServer: { protocol: 'http:' } },
    });
    const req = { headers: {} };
    const prepared = runtime.prepare(req);
    req.headers.cookie = prepared.setCookie.split(';')[0];
    assert.equal(prepared.body.loggedIn, false);
    assert.equal(runtime.resolve(req), null);
    assert.equal(runtime.checkCsrf(req), false);
    req.headers['x-auriga-csrf'] = prepared.body.csrf;
    assert.equal(runtime.checkCsrf(req), true);
    assert.equal(runtime.isReady(), false);
    await runtime.logout(req);
    assert.equal(runtime.checkCsrf(req), false);
    runtime.close();
    const restarted = await createConnectRuntime({
      config,
      directory,
      ctx: { webServer: { protocol: 'http:' } },
    });
    assert.equal(restarted.resolve(req), null);
    assert.equal(restarted.isReady(), false);
    restarted.close();
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
