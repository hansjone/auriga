import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  renameSync,
  readdirSync,
  rmSync,
} from 'node:fs';
import { join } from 'node:path';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { createVault } from '../../src/local/vault.js';
import { createCatalogIdentityFence } from '../../src/resources/identity-fence.js';
import { clockBootId } from '../../src/resources/host-lock.js';
const action = (f) => ({
  packageId: f.packageId,
  versionId: f.versionId,
  action: 'install',
  revision: 0,
  key: 'review-install',
});
const service = (f) => catalogService({ ...f, host: {}, privateSkills: {} });
function disableReceipt(
  f,
  reason = 'lease_expired',
  selectionRevision = 7,
  generation = 9,
) {
  const transactionId = '019a0000-0000-7000-8000-0000000000a7',
    receiptId = '019a0000-0000-7000-8000-0000000000a8';
  const path = join(f.home, 'auriga/skills-active/quarantine', transactionId),
    directory = join(f.home, 'auriga/guardian/disable-receipts');
  mkdirSync(directory, { recursive: true });
  mkdirSync(join(path, '..'), { recursive: true });
  if (existsSync(f.active)) renameSync(f.active, path);
  writeFileSync(
    join(directory, receiptId + '.json'),
    JSON.stringify({
      schemaVersion: 1,
      ...f.who,
      recordType: 'disable',
      writer: 'guardian',
      receiptId,
      transactionId,
      selectionRevision,
      generation,
      quarantinePath: 'auriga/skills-active/quarantine/' + transactionId,
      reason,
      processStopped: true,
      viewCleared: true,
      newSessionRequired: true,
      recordedAt: new Date().toISOString(),
    }),
  );
  return { path, receiptId, transactionId };
}
test('F1: a mixed E disable receipt expires enabled rows while DISABLED/REMOVED keep their success semantics', async (t) => {
  const f = catalogFixture(t),
    g = catalogFixture(t, undefined, {
      packageId: '019a0000-0000-7000-8000-0000000000b1',
    }),
    h = catalogFixture(t, undefined, {
      packageId: '019a0000-0000-7000-8000-0000000000b2',
    });
  const rows = [
    f.getRow(),
    {
      ...g.getRow(),
      resource: { ...g.resource, enabled: false, desiredState: 'DISABLED' },
    },
    {
      ...h.getRow(),
      resource: { ...h.resource, enabled: false, desiredState: 'REMOVED' },
    },
  ];
  f.desktop.prepare(f.who, rows, [
    {
      manifest: f.raw,
      artifacts: [f.archive],
      revocations: Buffer.from(JSON.stringify(f.revoked)),
    },
  ]);
  f.allow();
  f.desktop.finish(f.who, () => true);
  disableReceipt(f);
  const ledger = f.desktop.read(f.who);
  assert.deepEqual(
    ledger.installations.map((r) => r.state),
    ['expired', 'disabled', 'removed'],
  );
  assert.equal(f.desktop.confirmDisabled(f.who, rows[2]), true);
  const reports = [];
  const s = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    fetchImpl: async (url, options) => {
      if (url.pathname.endsWith('/member/installations'))
        return Response.json({ items: rows });
      if (url.pathname.endsWith('/installation-reports')) {
        const report = JSON.parse(options.body);
        reports.push(report);
        return Response.json({});
      }
      return f.fetchImpl(url, options);
    },
  });
  await s.list();
  assert.equal(
    reports.find((r) => r.identity.packageId === h.packageId).state,
    'removed',
  );
  assert.equal(
    reports.find((r) => r.identity.packageId === g.packageId).state,
    'disabled',
  );
});
test('F2: hand-edited quarantine is retained as an owned conflict copy and does not block later shared use', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  await s.list();
  const receipt = disableReceipt(f, 'revoked');
  writeFileSync(join(receipt.path, 'example/SKILL.md'), 'USER HAND EDIT');
  assert.equal(f.desktop.read(f.who).installations[0].state, 'revoked');
  assert.equal(existsSync(receipt.path), false);
  const preserved = join(
    f.directory,
    'staging',
    receipt.transactionId,
    'conflict-view/example/SKILL.md',
  );
  assert.equal(readFileSync(preserved, 'utf8'), 'USER HAND EDIT');
  await s.list();
  await s.apply({
    ...action(f),
    action: 'enable',
    revision: 1,
    key: 'after-conflict',
  });
  f.allow();
  await s.list();
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
  assert.equal(readFileSync(preserved, 'utf8'), 'USER HAND EDIT');
});
test('F3: failed isolation cannot retain logout credentials; revocation and pending isolation survive restart', async (t) => {
  const f = catalogFixture(t),
    directory = join(f.home, 'vault');
  let vault = await createVault(directory);
  t.after(() => vault.close());
  const credential = {
    claims: { tid: f.who.tenantId, sub: f.who.ownerId, did: f.who.hostId },
    refreshToken: 'synthetic-refresh',
  };
  await vault.update((s) => {
    s.credential = credential;
  });
  f.runtime.vault = vault;
  const boundService = service(f);
  const fence = createCatalogIdentityFence({
    home: f.home,
    desktop: f.desktop,
    timeout: 0,
  });
  t.after(() => fence.close());
  await fence.setIdentity(f.who);
  mkdirSync(join(f.active, 'stray'), { recursive: true });
  writeFileSync(join(f.active, 'stray/SKILL.md'), 'preserve unknown content');
  vault.subscribeResourceIdentityGate(async (next) => {
    if (!next) {
      assert.equal(vault.snapshot().credential, null);
      assert.equal(vault.snapshot().revocations[0].token, 'synthetic-refresh');
    }
    await fence.setIdentity(next);
  });
  await vault.update((s) => {
    s.revocations.push({ token: s.credential.refreshToken });
    s.credential = null;
  });
  assert.equal(vault.snapshot().credential, null);
  assert.ok(vault.snapshot().resourceIsolationPending);
  await assert.rejects(boundService.list(), {
    code: 'resource.storage_unavailable',
  });
  vault.close();
  vault = await createVault(directory);
  assert.equal(vault.snapshot().credential, null);
  assert.ok(vault.snapshot().resourceIsolationPending);
  assert.equal(vault.snapshot().revocations[0].token, 'synthetic-refresh');
  vault.subscribeResourceIdentityGate(fence.setIdentity);
  await assert.rejects(
    vault.update((s) => {
      s.credential = credential;
    }),
  );
  assert.equal(vault.snapshot().credential, null);
  rmSync(f.active, { recursive: true });
  await vault.update((s) => {
    s.credential = credential;
  });
  assert.equal(vault.snapshot().resourceIsolationPending, undefined);
});
test(
  'F3: a slow resource gate never delays durable credential removal or logout completion',
  { timeout: 5000 },
  async (t) => {
    const f = catalogFixture(t),
      vault = await createVault(join(f.home, 'slow-vault'));
    t.after(() => vault.close());
    await vault.update((s) => {
      s.credential = {
        claims: { tid: f.who.tenantId, sub: f.who.ownerId, did: f.who.hostId },
        refreshToken: 'synthetic-slow',
      };
    });
    let release;
    const waiting = new Promise((resolve) => {
      release = resolve;
    });
    t.after(() => release());
    vault.subscribeResourceIdentityGate(() => waiting);
    await vault.update((s) => {
      s.revocations.push({ token: s.credential.refreshToken });
      s.credential = null;
    });
    assert.equal(vault.snapshot().credential, null);
    assert.equal(vault.snapshot().revocations[0].token, 'synthetic-slow');
    assert.ok(vault.snapshot().resourceIsolationPending);
    release();
  },
);
test('M1: boot mismatch denies activation even when monotonic and wall clocks would otherwise permit it', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  f.state.clock.bootId = clockBootId('2026-11-13T00:00:00.000Z');
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  await s.list();
  assert.equal(existsSync(f.active), false);
});
test('M2: the highest observed revocation revision and equal-revision digest remain durable', (t) => {
  const f = catalogFixture(t),
    list = { ...f.revoked, revision: 2 };
  f.desktop.acceptRevocations(f.who, [Buffer.from(JSON.stringify(list))]);
  assert.throws(
    () =>
      f.desktop.acceptRevocations(f.who, [
        Buffer.from(JSON.stringify(f.revoked)),
      ]),
    { code: 'resource.signature_invalid' },
  );
  assert.throws(
    () =>
      f.desktop.acceptRevocations(f.who, [
        Buffer.from(JSON.stringify({ ...list, revokedKids: ['different'] })),
      ]),
    { code: 'resource.signature_invalid' },
  );
});
test('E changing identity before isolating old bytes cannot publish a new C identity or activate old shared content', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  await s.list();
  const next = { ...f.who, ownerId: '019a0000-0000-7000-8000-000000000099' };
  f.state.guardian.ownerId = next.ownerId;
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  const fence = createCatalogIdentityFence({
    home: f.home,
    desktop: f.desktop,
    timeout: 0,
  });
  t.after(() => fence.close());
  await assert.rejects(fence.setIdentity(next), {
    code: 'resource.storage_unavailable',
  });
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
  renameSync(f.active, join(f.home, 'e-isolated-view'));
  await fence.setIdentity(next);
  assert.equal(existsSync(f.active), false);
});
test('M3: committed verified old backups are collected and user conflict copies are retained', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  await s.list();
  await s.apply({
    ...action(f),
    action: 'disable',
    revision: 1,
    key: 'gc-disable',
  });
  const backup = readdirSync(join(f.directory, 'staging')).find((n) =>
    existsSync(join(f.directory, 'staging', n, 'example/SKILL.md')),
  );
  assert.ok(backup);
  await s.apply({
    ...action(f),
    action: 'enable',
    revision: 2,
    key: 'gc-enable',
  });
  f.allow();
  await s.list();
  assert.equal(existsSync(join(f.directory, 'staging', backup)), false);
});
test('M5: private-name collisions require a bound explicit confirmation before any installation selection', async (t) => {
  const f = catalogFixture(t),
    s = catalogService({
      ...f,
      host: {
        inspect: async () => [
          { provider: 'filesystem', path: '/project/example/SKILL.md' },
        ],
      },
      privateSkills: { list: async () => [{ name: 'example', enabled: true }] },
    });
  const first = await s.apply(action(f));
  assert.equal(first.confirmationRequired, true);
  assert.equal(first.conflicts.length, 2);
  assert.equal(
    f.requests.some(
      (r) =>
        r.path.endsWith('/member/installations') && r.options.method === 'POST',
    ),
    false,
  );
  const wrong = await s.apply({ ...action(f), confirmation: 'invalid' });
  assert.equal(wrong.confirmationRequired, true);
  assert.equal(
    (await s.apply({ ...action(f), confirmation: first.confirmation }))
      .accepted,
    true,
  );
});
