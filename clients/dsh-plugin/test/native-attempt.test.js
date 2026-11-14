import test from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes, createHash } from 'node:crypto';
import { readFile, rename, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { teamRuntimePeer } from './team-runtime-peer.js';
import { createVault } from '../src/local/vault.js';
import { createConnectRuntime } from '../src/local/runtime.js';

async function legacy(directory) {
  const credential = {
    accessToken: randomBytes(32).toString('base64url'),
    refreshToken: randomBytes(32).toString('base64url'),
  };
  const vault = await createVault(directory);
  await vault.update((s) => {
    s.credential = credential;
    s.endpoint = 'old-endpoint';
    s.threads.private = { local: true };
  });
  await vault.close();
  return credential;
}
test('legacy platform records are removed before Team startup while private local state survives', async (t) => {
  const f = await teamRuntimePeer(t, {
    async setup(directory) {
      await legacy(directory);
    },
    before() {
      throw new Error('Team unavailable');
    },
  });
  await f.settled();
  // The old device commit/compensation implementation belongs solely to C.
  for (const name of ['login', 'cancelAttempt', 'getAccess'])
    assert.equal(f.runtime[name], undefined);
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(f.runtime.status(f.request).communication, 'unavailable');
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
});
test('an actual filesystem read failure aborts legacy migration before any broker authority is used', async (t) => {
  const f = await shellRuntimePeer(t);
  await f.runtime.close();
  const credential = await legacy(f.directory);
  await rename(
    join(f.directory, 'connect.json'),
    join(f.directory, 'preserved.json'),
  );
  await mkdir(join(f.directory, 'connect.json'));
  const before = f.connection.calls.length;
  await assert.rejects(
    createConnectRuntime({
      directory: f.directory,
      config: {},
      ctx: {},
      connection: f.connection,
    }),
    /state unavailable/,
  );
  assert.equal(f.connection.calls.length, before);
  const ciphertext = await readFile(
    join(f.directory, 'preserved.json'),
    'utf8',
  );
  assert.equal(ciphertext.includes(credential.accessToken), false);
  assert.equal(ciphertext.includes(credential.refreshToken), false);
});
test('saved platform secrets never resume an identity when the shell is signed out', async (t) => {
  let credential;
  const f = await shellRuntimePeer(t, {
    configure(c) {
      c.transition('signed_out');
    },
    async setup(directory) {
      credential = await legacy(directory);
      await writeFile(join(directory, 'private.txt'), 'private-content');
    },
  });
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  assert.equal(f.runtime.status(f.request).migrationRequired, true);
  assert.equal(f.runtime.getAccess, undefined);
  const vault = await createVault(f.directory);
  assert.equal(vault.snapshot().credential, null);
  assert.equal(vault.snapshot().endpoint, null);
  assert.deepEqual(vault.snapshot().revocations, []);
  assert.equal(vault.snapshot().threads.private.local, true);
  await vault.close();
  assert.equal(
    await readFile(join(f.directory, 'private.txt'), 'utf8'),
    'private-content',
  );
  assert.equal(
    JSON.stringify(f.connection.calls).includes(credential.accessToken),
    false,
  );
});
test('logout fences a pending resource identity gate before its late successful completion', async (t) => {
  const f = await shellRuntimePeer(t),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  const detach = f.runtime.vault.subscribeResourceIdentityGate(async (who) => {
    if (who) {
      entered.resolve();
      await release.promise;
    }
  });
  t.after(detach);
  f.connection.transition('authenticated');
  await entered.promise;
  assert.equal(f.runtime.resolve(f.request), undefined);
  await f.runtime.command(f.request, 'logout');
  assert.equal(f.runtime.resolve(f.request), undefined);
  release.resolve();
  await nextTurn();
  assert.equal(f.runtime.resolve(f.request), undefined);
});
test('a failed isolation gate never publishes the candidate and a later clean generation can recover', async (t) => {
  const f = await shellRuntimePeer(t);
  const detach = f.runtime.vault.subscribeResourceIdentityGate(async () => {
    throw new Error('isolation unavailable');
  });
  f.connection.transition('authenticated');
  await nextTurn();
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(!!f.runtime.vault.snapshot().resourceIsolationPending, true);
  detach();
  f.connection.transition('authenticated');
  await nextTurn();
  assert.equal(f.runtime.status(f.request).loggedIn, true);
});
test('closing during Team initialization drains local storage and late IPC cannot restart it', async (t) => {
  const entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  const f = await teamRuntimePeer(t, {
    before() {
      entered.resolve();
      return release.promise;
    },
  });
  await entered.promise;
  await f.runtime.close();
  assert.equal(f.tools.size, 0);
  release.resolve();
  await nextTurn();
  assert.equal(f.tools.size, 0);
  assert.equal(f.runtime.resolve(f.request), undefined);
});

test('corrupt private Team state cannot crash the runtime or acquire a platform identity of its own', async (t) => {
  const f = await teamRuntimePeer(t, {
    async setup(directory, connection) {
      const who = connection.identity;
      const partition = createHash('sha256')
        .update(
          JSON.stringify([
            who.tenantId,
            who.memberId,
            who.deviceId,
            who.providerId,
          ]),
        )
        .digest('hex');
      const path = join(directory, 'team', partition),
        vault = await createVault(path);
      await vault.update(() => {});
      await vault.close();
      await writeFile(join(path, 'connect.json'), '"invalid"');
    },
  });
  await f.settled();
  assert.equal(f.runtime.status(f.request).communication, 'unavailable');
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(f.tools.size, 0);
  assert.equal(
    f.connection.calls.some((c) => c.call?.operation === 'team_initialize'),
    false,
  );
});
