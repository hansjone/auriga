import { test } from 'node:test';
/* global structuredClone */
import assert from 'node:assert/strict';
import {
  mkdtemp,
  readFile,
  mkdir,
  rename,
  rm,
  readdir,
} from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { setImmediate } from 'node:timers/promises';
import { createSettingsStore } from '../src/settings/store.js';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createVault } from '../src/local/vault.js';
import { createServer } from 'node:http';
import { platformConfig } from '../src/local/config.js';
import { createPlatform } from '../../shell-auth/platform.mjs';
import { createTeam } from '../src/local/team.js';
import { shellNetworkPeer } from './shell-network-peer.js';
import { testRoots } from './network/trust/keyset-fixtures.js';

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

for (const noTeamCapabilities of [false, true])
  test(
    'committed native identity starts default N1 despite ' +
      (noTeamCapabilities
        ? 'missing legacy Team capabilities'
        : 'legacy MCP failure'),
    async (t) => {
      const f = await shellNetworkPeer(t, { noTeamCapabilities });
      await f.complete();
      await until(
        () => f.runtime.networkStatus.reason === 'forbidden',
        'default network register denial',
      );
      assert.equal(f.runtime.status(f.request).loggedIn, true);
      assert.equal(f.runtime.status(f.request).connected, false);
      assert.equal(
        f.runtime.status(f.request).communication,
        noTeamCapabilities ? 'idle' : 'unavailable',
      );
      assert.equal(f.runtime.networkStatus.connected, false);
      assert.equal(f.requests.includes('/api/iam/v1/network/register'), true);
      assert.equal(f.requests.includes('/ai/mcp'), !noTeamCapabilities);
      assert.equal(
        f.requests.some((path) => /network\/tickets|net\/v1/.test(path)),
        false,
      );
    },
  );

test('password, TOTP, pending token exchange and uncommitted vault cannot start default N1', async (t) => {
  const password = Promise.withResolvers(),
    totp = Promise.withResolvers(),
    exchange = Promise.withResolvers(),
    commit = Promise.withResolvers();
  let exchangeStarted = false,
    commitStarted = false;
  t.after(() => {
    password.resolve();
    totp.resolve();
    exchange.resolve();
    commit.resolve();
  });
  const f = await shellNetworkPeer(t, {
    configure(source) {
      source.platform.poll = async () => {
        await password.promise;
        await totp.promise;
        exchangeStarted = true;
        await exchange.promise;
        return source.tokens;
      };
      const save = source.broker.vault.save.bind(source.broker.vault);
      source.broker.vault.save = async (value) => {
        if (value.tokens) {
          commitStarted = true;
          await commit.promise;
        }
        return save(value);
      };
    },
  });
  const stopped = () => {
    assert.equal(f.runtime.networkStatus.state, 'idle');
    assert.equal(f.runtime.resolve(f.request), undefined);
    assert.equal(f.requests.includes('/api/iam/v1/network/register'), false);
    assert.equal(f.runtime.vault.snapshot().credential, undefined);
  };
  stopped();
  await f.runtime.command(f.request, 'open_login');
  stopped();
  password.resolve();
  await setImmediate();
  stopped();
  totp.resolve();
  await until(() => exchangeStarted, 'exchange reached');
  stopped();
  exchange.resolve();
  await until(() => commitStarted, 'durable shell commit reached');
  stopped();
  commit.resolve();
  await Promise.allSettled([...f.source.broker.inflight]);
  await until(
    () => f.runtime.networkStatus.reason === 'forbidden',
    'committed device starts network',
  );
});

for (const failure of [
  'unknown-exchange',
  'invalid-token',
  'failed-commit',
  'owner-conflict',
])
  test('native ' + failure + ' never starts default N1', async (t) => {
    const f = await shellNetworkPeer(t, {
      expectedIdentity:
        failure === 'owner-conflict'
          ? { tenantId: randomUUID(), ownerId: randomUUID() }
          : undefined,
      configure(source) {
        if (failure === 'unknown-exchange')
          source.platform.poll = async () => {
            throw Object.assign(new Error('common.unavailable'), {
              status: 503,
              code: 'common.unavailable',
            });
          };
        if (failure === 'invalid-token')
          source.platform.verifyDevice = async () => {
            throw new Error('invalid device credential');
          };
        if (failure === 'failed-commit')
          source.broker.vault.save = async () => {
            throw new Error('simulated vault write failure');
          };
      },
    });
    await f.complete().catch((error) => {
      assert.equal(error.code, 'common.conflict');
      assert.notEqual(f.source.broker.snapshot().state, 'authenticated');
    });
    assert.equal(f.runtime.resolve(f.request), undefined);
    assert.equal(f.runtime.networkStatus.state, 'idle');
    assert.equal(f.requests.includes('/api/iam/v1/network/register'), false);
    assert.equal(f.runtime.vault.snapshot().credential, undefined);
    assert.equal(f.runtime.status(f.request).loggedIn, false);
    assert.equal(
      f.source.broker.snapshot().state === 'authenticated',
      failure === 'owner-conflict',
    );
  });

for (const stop of ['logout', 'reset', 'close'])
  test(
    stop +
      ' retires default N1 and late legacy MCP/register responses cannot revive it',
    async (t) => {
      let releaseTeam, releaseRegister;
      const f = await shellNetworkPeer(t, {
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
        register: () =>
          new Promise((resolve) => {
            releaseRegister = () =>
              resolve(
                Response.json({
                  tenant: f.source.identity.tenantId,
                  device: f.source.identity.deviceId,
                  realm: '2',
                  node: '64',
                  systemAddress: 'aur1:2.40.0',
                  created: true,
                }),
              );
          }),
      });
      await f.complete();
      await until(
        () => releaseTeam && releaseRegister,
        'register and Team both pending',
      );
      if (stop === 'logout') await f.runtime.command(f.request, 'logout');
      if (stop === 'reset') await f.runtime.resetAuthentication();
      if (stop === 'close') await f.runtime.close();
      assert.equal(f.runtime.resolve(f.request), undefined);
      const count = f.requests.length;
      releaseTeam();
      releaseRegister();
      await setImmediate();
      await setImmediate();
      assert.equal(f.runtime.resolve(f.request), undefined);
      if (stop !== 'close')
        assert.equal(f.runtime.vault.snapshot().credential, undefined);
      assert.equal(
        f.runtime.networkStatus.state,
        stop === 'close' ? 'stopped' : 'idle',
      );
      assert.equal(f.runtime.networkStatus.connected, false);
      assert.deepEqual(f.requests.slice(count), []);
      assert.equal(
        f.runtime.broker.state.current()?.state === 'authenticated',
        false,
      );
    },
  );

for (const status of [403, 401])
  test(
    'official network HTTP ' +
      status +
      ' has the correct native login and model scope',
    async (t) => {
      const f = await shellNetworkPeer(t, { loggedIn: true });
      await until(
        () => f.runtime.status(f.request).communication === 'unavailable',
        'Team settled',
      );
      const before = f.runtime.modelStatus;
      const tokens = structuredClone(f.source.broker.record.tokens);
      let denied = false;
      f.runtime.attachNetwork({
        kind: 'device-connection',
        async prepare() {
          denied = true;
          throw Object.assign(new Error('network denied'), { status });
        },
      });
      await until(
        () => denied && f.runtime.networkStatus.state !== 'connecting',
        'network-only denial settled',
      );
      assert.ok(
        f.runtime.resolve(f.request),
        'native identity survives a network-only denial',
      );
      assert.deepEqual(f.runtime.modelStatus, before);
      assert.deepEqual(f.source.broker.record.tokens, tokens);
      assert.equal(
        f.runtime.networkStatus.reason,
        status === 403 ? 'forbidden' : 'unavailable',
      );
    },
  );

for (const point of ['register', 'tickets'])
  test(
    'real official provider HTTP 403 at ' +
      point +
      ' preserves native session and model login',
    async (t) => {
      let denied = false;
      const f = await shellNetworkPeer(t, {
        register: (_init, source) =>
          point === 'register'
            ? ((denied = true),
              Response.json({ code: 'common.forbidden' }, { status: 403 }))
            : Response.json({
                tenant: source.identity.tenantId,
                device: source.identity.deviceId,
                realm: '2',
                node: '64',
                systemAddress: 'aur1:2.40.0',
                created: true,
              }),
        ticketDenied: () => {
          denied = true;
        },
      });
      await f.complete();
      await until(
        () => f.runtime.status(f.request).communication === 'unavailable',
        'Team settled',
      );
      const before = f.runtime.modelStatus;
      const tokens = structuredClone(f.source.broker.record.tokens);
      await until(
        () => denied && f.runtime.networkStatus.reason === 'forbidden',
        'official C provider denial settled',
      );
      assert.ok(f.runtime.resolve(f.request));
      assert.deepEqual(f.runtime.modelStatus, before);
      assert.deepEqual(f.source.broker.record.tokens, tokens);
      assert.equal(f.runtime.vault.snapshot().credential, undefined);
    },
  );

test('loader network installation selects official device default after login and never leaks to settings/status', async (t) => {
  const network = {
    realm: '2',
    roots: testRoots.map((r) => ({ ...r })),
    wssUrl: 'wss://network.example.com:9443/custom/ws',
  };
  const expected = structuredClone(network);
  let registerStarted = false,
    finish;
  const response = new Promise((resolve) => {
    finish = resolve;
  });
  const deny = () =>
    finish(Response.json({ code: 'common.forbidden' }, { status: 403 }));
  t.after(deny);
  const f = await shellNetworkPeer(t, {
    config: { network },
    register: () => {
      registerStarted = true;
      return response;
    },
  });
  await f.complete();
  network.realm = '3';
  network.roots[0].kid = 'injected';
  await until(() => registerStarted, 'trusted installation reached register');
  assert.equal(f.runtime.networkStatus.available, true);
  assert.equal(f.runtime.networkStatus.state, 'connecting');
  assert.equal(f.runtime.networkStatus.connected, false);
  assert.equal(
    f.requests.some((path) => /tickets|net\/v1/.test(path)),
    false,
  );
  assert.equal(
    [...f.registered].some((name) => /send/i.test(name)),
    false,
  );
  assert.ok(Array.isArray(await readdir(f.directory)));
  const settings = await createSettingsStore(
    f.directory,
    config.platformOrigin,
  );
  await assert.rejects(
    settings.save({ ...settings.snapshot(), network: expected }),
    /invalidSettings/,
  );
  for (const value of [
    f.runtime.status(f.request),
    f.runtime.networkStatus,
    settings.snapshot(),
  ]) {
    assert.equal(JSON.stringify(value).includes('public_key_spki'), false);
    assert.equal(JSON.stringify(value).includes(expected.wssUrl), false);
  }
  deny();
  await until(
    () => f.runtime.networkStatus.reason === 'forbidden',
    'denied register retired',
  );
  assert.equal(f.runtime.networkStatus.available, true);
  assert.equal(f.runtime.networkStatus.connected, false);
  const request = f.runtime.nativeRequest({ principal: { kind: 'owner' } });
  assert.equal(f.runtime.prepare(request).body.loggedIn, true);
  let prepared;
  const detach = f.runtime.attachNetwork({
    ports: {},
    prepare(input) {
      prepared = input;
      return new Promise(() => {});
    },
  });
  await until(() => prepared, 'trusted native provider attached');
  assert.deepEqual(prepared.installation, expected);
  assert.ok(Object.isFrozen(prepared.installation.roots[0]));
  detach();
  await f.runtime.close();
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
    const vault = await createVault(directory);
    let credentialsRead = 0;
    const team = createTeam({
      vault,
      broker: {
        call: async () => {
          credentialsRead++;
          throw new Error('not signed in');
        },
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
