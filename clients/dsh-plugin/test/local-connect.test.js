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
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
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
import { createDeviceProvider } from '../src/network/device-provider.js';
import {
  publicationFixture,
  testRoots,
  signedKeyset,
  signedAnnouncement,
} from './network/trust/keyset-fixtures.js';

const config = {
  platformOrigin: 'https://portal.example.com',
  issuer: 'https://portal.example.com',
  clientId: 'auriga-dsh',
  providerId: 'team',
};

async function until(predicate, message) {
  const deadline = Date.now() + 3000;
  while (!predicate()) {
    assert.ok(Date.now() < deadline, message);
    await setImmediate();
  }
}

// Unit peer only: real native token validation, encrypted vault and default N1
// provider. Registration is denied deliberately; this never claims WSS/READY.
async function nativeNetworkPeer(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-native-network-'));
  const { publicKey, privateKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  const claims = {
    iss: config.issuer,
    aud: 'auriga',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    tid: randomUUID(),
    sub: randomUUID(),
    did: randomUUID(),
    iat: Math.floor(Date.now() / 1000),
    exp: Math.floor(Date.now() / 1000) + 3600,
  };
  const unsigned = [{ alg: 'ES256', kid: 'native-test', typ: 'at+jwt' }, claims]
    .map((value) => Buffer.from(JSON.stringify(value)).toString('base64url'))
    .join('.');
  const accessToken =
    unsigned +
    '.' +
    sign('sha256', Buffer.from(unsigned), {
      key: privateKey,
      dsaEncoding: 'ieee-p1363',
    }).toString('base64url');
  const tokens = {
    access_token: accessToken,
    refresh_token: 'invalid-native-network-test-proof',
    token_type: 'Bearer',
    scope: 'auriga.connect',
  };
  const requests = [];
  let runtime;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    const path = new URL(url).pathname;
    requests.push(path);
    if (path === '/oauth2/jwks')
      return Response.json({
        keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'native-test' }],
      });
    if (path === '/api/iam/v1/login/transactions')
      return Response.json({
        id: 'native-transaction',
        nativeDevice: {
          deviceCode: 'invalid-native-device-code',
          expiresAt: new Date(Date.now() + 60000).toISOString(),
          interval: 1,
        },
        step: { type: 'form', form: 'password' },
      });
    if (path.endsWith('/native-transaction/advance')) {
      const input = JSON.parse(init.body);
      return Response.json({
        id: 'native-transaction',
        status: input.values?.totp ? 'completed' : 'pending',
        step: input.values?.totp
          ? { type: 'complete' }
          : { type: 'form', form: 'totp' },
      });
    }
    if (path === '/oauth2/token') {
      assert.equal(
        new URLSearchParams(init.body).get('grant_type'),
        'urn:ietf:params:oauth:grant-type:device_code',
      );
      return options.exchange
        ? options.exchange(tokens)
        : Response.json(tokens);
    }
    if (path === '/ai/mcp')
      return options.teamRequest
        ? options.teamRequest(init)
        : Response.json({ code: 'common.unavailable' }, { status: 503 });
    if (path === '/api/iam/v1/network/register') {
      assert.equal(
        new Headers(init.headers).get('authorization'),
        'Bearer ' + accessToken,
      );
      assert.equal(init.credentials, 'omit');
      assert.equal(new Headers(init.headers).get('origin'), null);
      assert.equal(init.body, '{}');
      assert.equal(
        runtime.vault.snapshot().credential.accessToken,
        accessToken,
      );
      assert.equal(runtime.vault.snapshot().credential.claims.sub, claims.sub);
      assert.ok((await readFile(join(directory, 'connect.json'))).length > 0);
      return options.register
        ? options.register(init)
        : Response.json({ code: 'common.forbidden' }, { status: 403 });
    }
    if (
      path === '/oauth2/revoke' ||
      path === '/api/iam/v1/logout' ||
      (path.endsWith('/native-transaction') && init.method === 'DELETE')
    )
      return Response.json({});
    throw new Error('unexpected native network path ' + path);
  });
  runtime = await createConnectRuntime({
    directory,
    config: {
      ...config,
      network: {
        realm: '2',
        wssUrl: 'wss://network.example.com/',
        roots: testRoots,
      },
    },
    ctx: {
      webServer: { protocol: 'http:' },
      ...(options.noTeamCapabilities
        ? {}
        : {
            tools: { register: () => () => {} },
            sessionController: { create() {}, prompt() {} },
            sessions: { get() {}, flush() {} },
          }),
    },
  });
  t.after(async () => {
    await runtime.close();
    await rm(directory, { recursive: true, force: true });
  });
  const request = { headers: {} };
  request.headers.cookie = runtime.prepare(request).setCookie.split(';')[0];
  const advance = (values) =>
    runtime.login(request, {
      action: 'advance',
      input: { type: 'form', values },
    });
  async function complete() {
    await runtime.login(request, { action: 'begin' });
    await advance({ password: 'invalid-test-password' });
    return advance({ totp: 'invalid-test-totp' });
  }
  return { runtime, request, requests, tokens, claims, advance, complete };
}

for (const noTeamCapabilities of [false, true])
  test(
    'committed native identity starts default N1 despite ' +
      (noTeamCapabilities
        ? 'missing legacy Team capabilities'
        : 'legacy MCP failure'),
    async (t) => {
      const peer = await nativeNetworkPeer(t, { noTeamCapabilities });
      const { runtime, request, requests, complete } = peer;
      const result = await complete();
      request.headers.cookie = result.setCookie[0].split(';')[0];
      await until(
        () => runtime.networkStatus.reason === 'forbidden',
        'default provider must reach the denied register endpoint independently of Team',
      );
      assert.equal(runtime.status(request).loggedIn, true);
      assert.equal(runtime.status(request).connected, false);
      assert.equal(runtime.status(request).communication, 'unavailable');
      assert.equal(runtime.networkStatus.connected, false);
      assert.equal(requests.includes('/api/iam/v1/network/register'), true);
      assert.equal(requests.includes('/ai/mcp'), !noTeamCapabilities);
      assert.equal(
        requests.some((path) => /network\/(tickets|keys)/.test(path)),
        false,
      );
    },
  );

test('password, TOTP, pending token exchange and uncommitted vault cannot start default N1', async (t) => {
  let exchangeStarted = false,
    releaseExchange,
    commitStarted = false,
    releaseCommit;
  const peer = await nativeNetworkPeer(t, {
    exchange: (tokens) => {
      exchangeStarted = true;
      return new Promise((resolve) => {
        releaseExchange = () => resolve(Response.json(tokens));
      });
    },
  });
  const { runtime, request, requests, advance } = peer;
  const update = runtime.vault.update;
  runtime.vault.update = async (write) => {
    commitStarted = true;
    await new Promise((resolve) => {
      releaseCommit = resolve;
    });
    return update(write);
  };
  const assertStopped = () => {
    assert.equal(runtime.networkStatus.state, 'idle');
    assert.equal(runtime.resolve(request), null);
    assert.equal(requests.includes('/api/iam/v1/network/register'), false);
    assert.equal(runtime.vault.snapshot().credential, null);
  };
  assertStopped();
  await runtime.login(request, { action: 'begin' });
  assertStopped();
  await advance({ password: 'invalid-test-password' });
  assertStopped();
  const completing = advance({ totp: 'invalid-test-totp' });
  await until(() => exchangeStarted, 'token exchange started');
  assertStopped();
  releaseExchange();
  await until(() => commitStarted, 'vault commit started');
  assertStopped();
  releaseCommit();
  await completing;
  await until(
    () => runtime.networkStatus.reason === 'forbidden',
    'committed device reaches default registration',
  );
});

for (const failure of [
  'unknown-exchange',
  'invalid-token',
  'failed-commit',
  'owner-conflict',
])
  test('native ' + failure + ' never starts default N1', async (t) => {
    const { runtime, request, requests, complete, claims } =
      await nativeNetworkPeer(t, {
        exchange: (tokens) =>
          Response.json(
            failure === 'unknown-exchange'
              ? { code: 'common.unavailable' }
              : {
                  ...tokens,
                  access_token:
                    failure === 'invalid-token'
                      ? 'invalid-jwt'
                      : tokens.access_token,
                },
            { status: failure === 'unknown-exchange' ? 503 : 200 },
          ),
      });
    let owner;
    if (failure === 'owner-conflict') {
      owner = {
        accessToken: 'invalid-existing-owner-proof',
        refreshToken: 'invalid-existing-refresh-proof',
        claims: { ...claims, sub: randomUUID() },
      };
      await runtime.vault.update((state) => {
        state.credential = owner;
      });
    }
    if (failure === 'failed-commit')
      runtime.vault.update = async () => {
        throw new Error('simulated vault write failure');
      };
    await assert.rejects(
      complete(),
      failure === 'unknown-exchange'
        ? (error) => error.status === 503 && error.code === 'common.unavailable'
        : failure === 'owner-conflict'
          ? /local owner conflict/
          : failure === 'failed-commit'
            ? /simulated vault write failure/
            : /invalid device credential/,
    );
    assert.equal(runtime.resolve(request), null);
    assert.equal(runtime.networkStatus.state, 'idle');
    assert.equal(requests.includes('/api/iam/v1/network/register'), false);
    assert.deepEqual(runtime.vault.snapshot().credential, owner ?? null);
  });

for (const stop of ['logout', 'reset', 'close'])
  test(
    stop +
      ' retires default N1 and late legacy MCP/register responses cannot revive it',
    async (t) => {
      let releaseTeam, releaseRegister, registerSignal;
      const peer = await nativeNetworkPeer(t, {
        teamRequest: (init) =>
          new Promise((resolve) => {
            const body = JSON.parse(init.body);
            releaseTeam = () =>
              resolve(
                Response.json({
                  jsonrpc: '2.0',
                  id: body.id,
                  result: { protocolVersion: '2025-06-18' },
                }),
              );
          }),
        register: (init) =>
          new Promise((resolve) => {
            registerSignal = init.signal;
            releaseRegister = () =>
              resolve(
                Response.json({
                  tenant: peer.claims.tid,
                  device: peer.claims.did,
                  realm: '2',
                  node: '64',
                  systemAddress: 'aur1:2.40.0',
                  created: true,
                }),
              );
          }),
      });
      const { runtime, request, requests, complete } = peer;
      const result = await complete();
      request.headers.cookie = result.setCookie[0].split(';')[0];
      await until(
        () => releaseRegister && releaseTeam,
        'default register and legacy MCP both in flight',
      );
      const closing = stop === 'close' ? runtime.close() : undefined;
      if (stop === 'logout') await runtime.logout(request);
      else if (stop === 'reset') await runtime.resetAuthentication();
      assert.equal(registerSignal.aborted, true);
      const count = requests.length;
      releaseTeam();
      releaseRegister();
      await closing;
      // Drain both resolved HTTP response streams and their continuation microtasks.
      await setImmediate();
      assert.equal(runtime.resolve(request), null);
      if (stop !== 'close')
        assert.equal(runtime.vault.snapshot().credential, null);
      assert.equal(
        runtime.networkStatus.state,
        stop === 'close' ? 'stopped' : 'idle',
      );
      assert.equal(runtime.networkStatus.connected, false);
      assert.deepEqual(requests.slice(count), []);
    },
  );

for (const status of [403, 401])
  test(
    'official network HTTP ' +
      status +
      ' has the correct native login and model scope',
    async (t) => {
      const { runtime, request, credential } = await runtimePeer(t);
      const before = runtime.modelStatus;
      let denied = false;
      runtime.attachNetwork({
        kind: 'device-connection',
        async prepare() {
          denied = true;
          throw Object.assign(new Error('network denied'), { status });
        },
      });
      const deadline = Date.now() + 2000;
      while (!denied || runtime.networkStatus.state === 'connecting') {
        assert.ok(Date.now() < deadline, 'network denial handled');
        await setImmediate();
      }
      if (status === 403) {
        assert.ok(
          runtime.resolve(request),
          'native session survives a network-only denial',
        );
        assert.deepEqual(runtime.modelStatus, before);
        assert.equal(
          runtime.vault.snapshot().credential.accessToken,
          credential.accessToken,
        );
        assert.equal(runtime.networkStatus.reason, 'forbidden');
      } else {
        assert.equal(runtime.resolve(request), null);
        assert.equal(runtime.modelStatus.reauthenticationRequired, true);
      }
    },
  );
for (const point of ['register', 'tickets'])
  test(
    'real official provider HTTP 403 at ' +
      point +
      ' preserves native session and model login',
    async (t) => {
      const { runtime, request, credential } = await runtimePeer(t);
      const { payload } = publicationFixture();
      const originalFetch = globalThis.fetch;
      t.after(() => {
        globalThis.fetch = originalFetch;
      });
      let denied = false;
      globalThis.fetch = async (url, init) => {
        const path = new URL(url).pathname;
        if (path.endsWith('/' + point)) {
          denied = true;
          return Response.json({ code: 'common.forbidden' }, { status: 403 });
        }
        if (path.endsWith('/network/register'))
          return Response.json({
            tenant: credential.claims.tid,
            device: credential.claims.did,
            realm: '2',
            node: '64',
            systemAddress: 'aur1:2.40.0',
            created: true,
          });
        if (path.endsWith('/keys/announcement'))
          return Response.json(signedAnnouncement(payload));
        if (path.endsWith('/network/keys'))
          return Response.json(signedKeyset(payload));
        return originalFetch(url, init);
      };
      const before = runtime.modelStatus;
      runtime.attachNetwork(
        createDeviceProvider({
          config: {
            ...config,
            network: {
              realm: '2',
              roots: testRoots,
              wssUrl: 'wss://network.example.com/',
            },
          },
          getDevice: async () => ({
            accessToken: credential.accessToken,
            claims: credential.claims,
          }),
        }),
      );
      const deadline = Date.now() + 3000;
      while (!denied || runtime.networkStatus.reason !== 'forbidden') {
        assert.ok(Date.now() < deadline, 'official network denial diagnosed');
        await setImmediate();
      }
      assert.ok(runtime.resolve(request));
      assert.deepEqual(runtime.modelStatus, before);
      assert.equal(
        runtime.vault.snapshot().credential.accessToken,
        credential.accessToken,
      );
    },
  );

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
  let registerStarted = false,
    releaseRegister;
  const registerResponse = new Promise((resolve) => {
    releaseRegister = resolve;
  });
  const denyRegister = () =>
    releaseRegister(
      Response.json({ code: 'common.forbidden' }, { status: 403 }),
    );
  t.after(denyRegister);
  const { runtime, request, directory, requests, registered } =
    await runtimePeer(t, {
      config: { network },
      networkRegister() {
        registerStarted = true;
        return registerResponse;
      },
    });
  network.realm = '2';
  network.roots[0].kid = 'injected';
  // Team readiness is independent of network startup. Hold the real default
  // provider at register so this assertion cannot sample a closing candidate.
  await until(() => registerStarted, 'official default reached register');
  assert.equal(runtime.networkStatus.available, true);
  assert.equal(runtime.networkStatus.state, 'connecting');
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
  denyRegister();
  await until(
    () => runtime.networkStatus.reason === 'forbidden',
    'official default completed denied register cleanup',
  );
  assert.equal(runtime.networkStatus.available, true);
  assert.equal(runtime.networkStatus.connected, false);
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
