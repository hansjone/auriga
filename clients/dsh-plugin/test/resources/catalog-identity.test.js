import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdirSync,
  existsSync,
  readFileSync,
  writeFileSync,
  readdirSync,
} from 'node:fs';
import { join } from 'node:path';
import { createVault } from '../../src/local/vault.js';
import { createCatalogIdentityFence } from '../../src/resources/identity-fence.js';
import { catalogService } from '../../src/resources/catalog-service.js';
import { catalogFixture } from './catalog-fixture.js';
test('the verified proposed identity is not published until old shared files, including user edits, have been isolated', async (t) => {
  const f = catalogFixture(t),
    service = catalogService({ ...f, privateSkills: {}, host: {} });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'identity-install',
  });
  f.allow();
  await service.list();
  writeFileSync(join(f.active, 'example/SKILL.md'), 'preserve hand edit');
  const vault = await createVault(join(f.home, 'vault'));
  t.after(() => vault.close());
  const credential = {
    claims: { tid: f.who.tenantId, sub: f.who.ownerId, did: f.who.hostId },
    testOnly: 'no real token',
  };
  await vault.update((state) => {
    state.credential = credential;
  });
  const fence = createCatalogIdentityFence({
    home: f.home,
    desktop: f.desktop,
    timeout: 0,
  });
  t.after(() => fence.close());
  vault.subscribeResourceIdentityGate(async (next) => {
    assert.equal(vault.snapshot().credential.claims.sub, f.who.ownerId);
    assert.deepEqual(Object.keys(next).sort(), [
      'hostId',
      'ownerId',
      'tenantId',
    ]);
    await fence.setIdentity(next);
    assert.equal(existsSync(f.active), false);
  });
  const nextOwner = '019a0000-0000-7000-8000-000000000099';
  await vault.update((state) => {
    state.credential = {
      ...credential,
      claims: { ...credential.claims, sub: nextOwner },
    };
  });
  assert.equal(vault.snapshot().credential.claims.sub, nextOwner);
  const staged = join(f.directory, 'staging');
  assert.ok(
    readdirSync(staged).some((name) => {
      try {
        return (
          readFileSync(join(staged, name, 'example/SKILL.md'), 'utf8') ===
          'preserve hand edit'
        );
      } catch {
        return false;
      }
    }),
  );
});
test('a losing C lock aborts credential publication and preserves durable identity; same-identity refresh is unaffected', async (t) => {
  const f = catalogFixture(t),
    vault = await createVault(join(f.home, 'vault'));
  t.after(() => vault.close());
  const credential = {
    claims: { tid: f.who.tenantId, sub: f.who.ownerId, did: f.who.hostId },
    testOnly: 'original',
  };
  await vault.update((state) => {
    state.credential = credential;
  });
  const fence = createCatalogIdentityFence({
    home: f.home,
    desktop: f.desktop,
    timeout: 0,
  });
  vault.subscribeResourceIdentityGate(fence.setIdentity);
  t.after(() => fence.close());
  const file = join(f.home, 'vault/connect.json'),
    before = readFileSync(file, 'utf8'),
    lock = join(f.home, 'auriga/guardian/host.lock.d');
  mkdirSync(lock);
  await assert.rejects(
    vault.update((state) => {
      state.credential = {
        ...credential,
        claims: {
          ...credential.claims,
          sub: '019a0000-0000-7000-8000-000000000099',
        },
      };
    }),
    { code: 'EEXIST' },
  );
  assert.equal(readFileSync(file, 'utf8'), before);
  assert.equal(vault.snapshot().credential.claims.sub, f.who.ownerId);
  assert.deepEqual(readdirSync(lock), []);
  await vault.update((state) => {
    state.credential.testOnly = 'refreshed';
  });
  assert.equal(vault.snapshot().credential.testOnly, 'refreshed');
  assert.ok(existsSync(lock));
});
