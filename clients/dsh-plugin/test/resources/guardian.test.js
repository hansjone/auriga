import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, rename } from 'node:fs/promises';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { fixture, id } from './fixtures.js';
import { createMemberRoutes } from '../../src/resources/member-routes.js';
import { createVault } from '../../src/local/vault.js';
import { createMemberApi } from '../../src/resources/member-api.js';
import { verifyResourceArchive } from '../../src/resources/verify.js';
const input = {
  packageId: id(4),
  versionId: id(5),
  action: 'install',
  key: 'first',
};
async function routesFor(t, f, current = () => f.runtime) {
  await f.engine.close();
  const scope = createHash('sha256')
    .update(f.runtime.config.platformOrigin)
    .digest('hex');
  const target = join(f.directory, 'auriga-resources', scope);
  await mkdir(target, { recursive: true });
  await rename(join(f.directory, id(1)), join(target, id(1)));
  const trustFile = await f.writeRoots();
  const result = (name, outcome) => ({
    target: name,
    application: outcome.restartRequired ? 'restart-required' : 'applied',
  });
  const manager = {
    listBundles: async () => {
      const value = await f.host.observe(f.pack.name);
      return value ? [value] : [];
    },
    installBundle: async (spec) =>
      result(f.pack.name, await f.host.install(spec.slice(5), f.pack.name)),
    setBundleEnabled: async (name, enabled) =>
      result(name, await f.host.enable(name, enabled)),
    removeBundle: async (name) => result(name, await f.host.remove(name)),
  };
  const routes = createMemberRoutes({
    directory: f.directory,
    ctx: { get: () => manager },
    sameOrigin: () => true,
    trustFile,
    current,
    now: f.engineOptions.now,
    fetchImpl: f.fetchImpl,
  });
  t.after(() => routes.close());
  await routes.ready;
  return routes;
}
test('profile discovery loads signed-out persisted L4 and stops it before any market request', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l4';
  f.pack.visibility = 'private';
  await f.engine.apply(input);
  await f.logout();
  f.hostCalls.length = 0;
  await routesFor(t, f);
  assert(f.hostCalls.includes('disable'));
  assert(!f.hostCalls.includes('stop'));
});
test('restoring the same persisted member keeps its installed private plugin running while offline', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l4';
  f.pack.visibility = 'private';
  await f.engine.apply(input);
  let current;
  const routes = await routesFor(t, f, () => current);
  f.offline();
  current = f.runtime;
  await routes.attach(current);
  assert.equal((await f.host.observe(f.pack.name)).enabled, true);
  assert(!f.hostCalls.includes('disable'));
  await f.logout();
  assert.equal((await f.host.observe(f.pack.name)).enabled, false);
});
test('an absent runtime cannot indefinitely defer private ownership enforcement', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l4';
  f.pack.visibility = 'private';
  await f.engine.apply(input);
  t.mock.timers.enable({ apis: ['setInterval'] });
  const routes = await routesFor(t, f, () => undefined);
  t.mock.timers.tick(1000);
  await routes.reconcile();
  assert.equal((await f.host.observe(f.pack.name)).enabled, false);
});
test('durable credential changes stop the old private plugin and switched members cannot read its manifest', async (t) => {
  const f = await fixture(t);
  f.pack.level = 'l4';
  f.pack.visibility = 'private';
  await f.engine.apply(input);
  const routes = await routesFor(t, f);
  await routes.attach(f.runtime);
  await f.switchMember();
  assert.equal((await f.host.observe(f.pack.name)).enabled, false);
  const owner = {};
  f.runtime.resolve = () => owner;
  f.runtime.checkCsrf = () => true;
  const response = {
    status: 0,
    body: '',
    writeHead(status) {
      this.status = status;
    },
    end(body) {
      this.body = body;
    },
  };
  await routes.handle(
    f.runtime,
    {
      url: '/auriga/resources/local/installations',
      method: 'GET',
      headers: {},
    },
    response,
  );
  assert(!response.body.includes(f.pack.name));
  assert(!response.body.includes(id(3)));
});
test('clearing a credential immediately enforces cached revocations without a subsequent UI read', async (t) => {
  const f = await fixture(t);
  await f.engine.apply(input);
  f.revocations.revision = 2;
  f.revocations.revokedKids = ['release'];
  await f.engine.monitor();
  const routes = await routesFor(t, f);
  await routes.attach(f.runtime);
  await f.host.enable(f.pack.name, true);
  f.hostCalls.length = 0;
  await f.logout();
  assert(f.hostCalls.includes('disable'));
});
test('the vault publishes committed identity changes to resource guards before update completes', async (t) => {
  const f = await fixture(t);
  const vault = await createVault(join(f.directory, 'vault'));
  t.after(() => vault.close());
  const observed = [];
  const unsubscribe = vault.subscribeResourceIdentity(async () => {
    await Promise.resolve();
    observed.push(vault.snapshot().credential?.claims.sub ?? null);
  });
  await vault.update((state) => {
    state.credential = { claims: { tid: id(1), sub: id(2), did: id(3) } };
  });
  await vault.update((state) => {
    state.credential = { claims: { tid: id(1), sub: id(8), did: id(9) } };
  });
  await vault.update((state) => {
    state.credential = null;
  });
  assert.deepEqual(observed, [id(2), id(8), null]);
  unsubscribe();
});
test('ticket admission preserves original envelope number spelling through strict parsing', async (t) => {
  const f = await fixture(t);
  const api = createMemberApi({
    runtime: f.runtime,
    now: f.engineOptions.now,
    fetchImpl: async (url, init) => {
      const response = await f.fetchImpl(url, init);
      if (!url.pathname.endsWith('/download-tickets')) return response;
      return new Response(
        (await response.text()).replace(
          '"releaseSequence":1',
          '"releaseSequence":1e0',
        ),
        { status: 201, headers: { 'content-type': 'application/json' } },
      );
    },
  });
  const ref = await api.reference(id(4), id(5));
  const download = await api.download(ref);
  assert(
    Buffer.from(download.wire).toString().includes('"releaseSequence":1e0'),
  );
  verifyResourceArchive(download.wire, download.archive, {
    expected: { ...download.envelope, sizeBytes: download.archive.length },
    releaseKeys: (await f.engineOptions.roots()).releaseKeys,
    now: f.engineOptions.now(),
  });
});
