import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdtemp, mkdir, rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import {
  persistNativeAttempt,
  abandonNativeAttempt,
} from '../src/local/runtime.js';
import { LoginJar } from '../src/local/platform.js';
import { createVault } from '../src/local/vault.js';
import { createTeam } from '../src/local/team.js';
import { peer } from './team-http-peer.js';
import { runtimePeer } from './local-runtime-peer.js';

// These tests enter at the already-verified device commit boundary. Random
// opaque records are not JWTs; no password/TOTP/JWKS/IAM success is simulated.
const credential = () => ({
  accessToken: randomBytes(24).toString('base64url'),
  refreshToken: randomBytes(24).toString('base64url'),
  claims: { tid: 'fixture-tenant', sub: 'alice', did: randomUUID() },
});
const attempt = () => ({
  version: 1,
  controller: new AbortController(),
  jar: new LoginJar(),
  verifier: 'ephemeral-proof',
  device: { code: 'ephemeral-device-code' },
});
async function state() {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-native-commit-')),
    vault = await createVault(directory);
  return {
    directory,
    vault,
    close: async () => {
      vault.close();
      await rm(directory, { recursive: true, force: true });
    },
  };
}

test('durable device survives transient Team start failure; fresh connection attempt resumes its same record', async () => {
  const local = await state(),
    remote = await peer(),
    row = attempt(),
    saved = credential(),
    tools = new Set();
  let backingActive = true,
    compensations = 0,
    first,
    retry;
  const options = {
    platform: remote.platform,
    vault: local.vault,
    getAccess: async () => {
      if (!backingActive || !local.vault.snapshot().credential)
        throw new Error('backing unavailable');
      return local.vault.snapshot().credential.accessToken;
    },
    assertActive() {},
    label: 'Handshake probe',
    ctx: {
      tools: {
        register(tool) {
          tools.add(tool.name);
          return () => tools.delete(tool.name);
        },
      },
    },
  };
  try {
    await persistNativeAttempt(row, () =>
      local.vault.update((s) => {
        s.credential = saved;
      }),
    );
    remote.state.available = false;
    first = createTeam(options);
    await assert.rejects(first.start(), (error) => error.status === 503);
    first.close();
    assert.equal(tools.size, 0);
    await abandonNativeAttempt(row, async () => {
      compensations++;
      backingActive = false;
    });
    assert.equal(compensations, 0);
    assert.equal(backingActive, true);
    assert.deepEqual(local.vault.snapshot().credential, saved);
    remote.state.available = true;
    retry = createTeam(options);
    await retry.start();
    assert.equal(tools.size, 4);
    assert.deepEqual(local.vault.snapshot().credential, saved);
  } finally {
    first?.close();
    retry?.close();
    await remote.close();
    await local.close();
  }
});

test('actual failed vault rename compensates once and never treats an uncommitted record as resumable', async () => {
  const local = await state(),
    row = attempt();
  let compensations = 0;
  try {
    await local.vault.update(() => {});
    await rename(
      join(local.directory, 'connect.json'),
      join(local.directory, 'preserved.json'),
    );
    await mkdir(join(local.directory, 'connect.json'));
    await assert.rejects(
      persistNativeAttempt(row, () =>
        local.vault.update((s) => {
          s.credential = credential();
        }),
      ),
    );
    const cleanup = () =>
      abandonNativeAttempt(row, async () => {
        compensations++;
      });
    await Promise.all([cleanup(), cleanup()]);
    assert.equal(compensations, 1);
    assert.equal(await row.commit, false);
    assert.equal(local.vault.snapshot().credential, null);
    assert.equal(row.verifier, undefined);
  } finally {
    await local.close();
  }
});

test('cancellation before commit admission compensates; the aborted proof cannot enter the vault', async () => {
  const local = await state(),
    row = attempt();
  let compensations = 0;
  try {
    const write = persistNativeAttempt(row, () =>
      local.vault.update((s) => {
        row.controller.signal.throwIfAborted();
        s.credential = credential();
      }),
    );
    const rejected = assert.rejects(write);
    await abandonNativeAttempt(row, async () => {
      compensations++;
    });
    await rejected;
    assert.equal(compensations, 1);
    assert.equal(await row.commit, false);
    assert.equal(local.vault.snapshot().credential, null);
  } finally {
    await local.close();
  }
});

test('cancellation while asynchronous vault commit is pending waits; a later successful commit preserves its backing', async () => {
  const local = await state(),
    row = attempt(),
    saved = credential(),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  let compensations = 0,
    cleaned = false;
  try {
    const write = persistNativeAttempt(row, () =>
      local.vault.update(async (s) => {
        row.controller.signal.throwIfAborted();
        s.credential = saved;
        entered.resolve();
        await release.promise;
      }),
    );
    await entered.promise;
    const cleanup = abandonNativeAttempt(row, async () => {
      compensations++;
    }).then(() => {
      cleaned = true;
    });
    await Promise.resolve();
    assert.equal(cleaned, false);
    assert.equal(compensations, 0);
    release.resolve();
    await Promise.all([write, cleanup]);
    assert.equal(await row.commit, true);
    assert.equal(compensations, 0);
    assert.deepEqual(local.vault.snapshot().credential, saved);
    assert.equal(row.controller.signal.aborted, true);
    const reopened = await createVault(local.directory);
    assert.deepEqual(reopened.snapshot().credential, saved);
    reopened.close();
  } finally {
    release.resolve();
    await local.close();
  }
});

test('an in-flight commit that eventually fails is compensated after settlement, not before', async () => {
  const local = await state(),
    row = attempt(),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  let compensations = 0;
  try {
    const write = persistNativeAttempt(row, () =>
      local.vault.update(async () => {
        entered.resolve();
        await release.promise;
        throw new Error('commit rejected');
      }),
    );
    const rejected = assert.rejects(write);
    await entered.promise;
    const cleanup = abandonNativeAttempt(row, async () => {
      compensations++;
    });
    await Promise.resolve();
    assert.equal(compensations, 0);
    release.resolve();
    await Promise.all([rejected, cleanup]);
    assert.equal(compensations, 1);
    assert.equal(await row.commit, false);
    assert.equal(local.vault.snapshot().credential, null);
  } finally {
    release.resolve();
    await local.close();
  }
});

test('explicit runtime logout queues behind an in-flight real vault write and tombstones it before remote retry', async (t) => {
  const entered = Promise.withResolvers(),
    release = Promise.withResolvers(),
    row = attempt();
  let runtime,
    compensations = 0;
  const server = createServer((req, res) => {
    assert.equal(req.url, '/oauth2/revoke');
    res.writeHead(503);
    res.end();
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  try {
    const fixture = await runtimePeer(t, {
      config: {
        platformOrigin: origin,
        issuer: origin,
        clientId: 'native-test',
        providerId: 'team',
        allowLocalHttp: true,
      },
      revokeHttp: true,
    });
    runtime = fixture.runtime;
    const { request, credential: saved } = fixture;
    const write = persistNativeAttempt(row, () =>
      runtime.vault.update(async (s) => {
        s.credential = saved;
        entered.resolve();
        await release.promise;
      }),
    );
    await entered.promise;
    const logout = runtime.logout(request),
      cleanup = abandonNativeAttempt(row, async () => {
        compensations++;
      });
    release.resolve();
    await Promise.all([write, cleanup]);
    const out = await logout;
    assert.equal(compensations, 0);
    assert.equal(out.body.loggedIn, false);
    assert.equal(out.body.remoteRevocation, 'pending');
    assert.equal(runtime.resolve(request), null);
    assert.equal(runtime.vault.snapshot().credential, null);
    assert.equal(runtime.vault.snapshot().revocations.length, 1);
    await assert.rejects(runtime.getAccess(), /platform sign-in required/);
    const next = { headers: {} };
    const prepared = runtime.prepare(next);
    next.headers.cookie = prepared.setCookie.split(';')[0];
    assert.equal(prepared.body.resumeAvailable, false);
    await assert.rejects(
      runtime.login(next, { action: 'resume' }),
      /no device credential/,
    );
  } finally {
    release.resolve();
    runtime?.close();
    await new Promise((resolve) => server.close(resolve));
  }
});
