import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { mkdirSync, renameSync, writeFileSync } from 'node:fs';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
test('F1: E clearing a prepared removal is acknowledged even while the prior installation receipt is still present', async (t) => {
  const f = catalogFixture(t),
    s = catalogService({ ...f, privateSkills: {}, host: {} });
  await s.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'before-removal',
  });
  f.allow();
  await s.list();
  const row = {
    ...f.getRow(),
    installationRevision: 2,
    selectionRevision: 8,
    generation: 10,
    resource: {
      ...f.resource,
      installationRevision: 2,
      enabled: false,
      desiredState: 'REMOVED',
    },
    state: 'applying',
  };
  f.desktop.prepare(f.who, [row], []);
  const transactionId = '019a0000-0000-7000-8000-0000000000d1',
    receiptId = '019a0000-0000-7000-8000-0000000000d2';
  const parent = join(f.home, 'auriga/skills-active/quarantine');
  mkdirSync(parent, { recursive: true });
  renameSync(f.active, join(parent, transactionId));
  const receipts = join(f.home, 'auriga/guardian/disable-receipts');
  mkdirSync(receipts, { recursive: true });
  writeFileSync(
    join(receipts, receiptId + '.json'),
    JSON.stringify({
      schemaVersion: 1,
      ...f.who,
      recordType: 'disable',
      writer: 'guardian',
      receiptId,
      transactionId,
      selectionRevision: 8,
      generation: 10,
      quarantinePath: 'auriga/skills-active/quarantine/' + transactionId,
      reason: 'lease_expired',
      processStopped: true,
      viewCleared: true,
      newSessionRequired: true,
      recordedAt: new Date().toISOString(),
    }),
  );
  assert.equal(f.desktop.read(f.who).installations[0].state, 'removed');
  assert.equal(f.desktop.confirmDisabled(f.who, row), true);
  assert.equal(
    f.desktop.confirmDisabled(f.who, { ...row, installationRevision: 3 }),
    false,
  );
});
