import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdirSync,
  readFileSync,
  writeFileSync,
  readdirSync,
  existsSync,
} from 'node:fs';
import { join } from 'node:path';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from '../../src/resources/catalog-service.js';
import { createPrivateSkills } from '../../src/resources/private-skills.js';
test('a corrupt prepared cache and interrupted record are isolated and the exact selection can be safely prepared again', async (t) => {
  const f = catalogFixture(t),
    s = catalogService({
      ...f,
      host: {},
      privateSkills: createPrivateSkills({ home: f.home }),
    });
  await s.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'prepare',
  });
  const cache = join(
    f.directory,
    'cache',
    f.resource.identity.manifestDigest.slice(7),
  );
  writeFileSync(join(cache, 'bytes'), 'interrupted or tampered cache');
  const temp = join(
    f.directory,
    'ledger.json.019a0000-0000-7000-8000-000000000066.tmp',
  );
  writeFileSync(temp, 'uncommitted metadata');
  assert.equal(f.desktop.needsPreparation(f.who, [f.getRow()]), true);
  assert.equal(existsSync(temp), false);
  const downloads = f.requests.filter((r) =>
    r.path.endsWith('/manifest'),
  ).length;
  await s.list();
  assert.equal(
    f.requests.filter((r) => r.path.endsWith('/manifest')).length,
    downloads + 1,
  );
  assert.deepEqual(readFileSync(join(cache, 'bytes')), f.raw);
  assert.equal(existsSync(f.active), false);
  const stages = readdirSync(join(f.directory, 'staging'));
  assert.ok(
    stages.some((name) =>
      existsSync(join(f.directory, 'staging', name, 'interrupted-file')),
    ),
  );
  f.allow();
  await s.list();
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
  f.state.guardian.state = 'isolating';
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  assert.throws(
    () =>
      f.desktop.acknowledge(f.who, {
        identity: f.resource.identity,
        state: 'applied',
        installationRevision: 1,
        selectionRevision: 7,
        generation: 9,
      }),
    { code: 'resource.state_conflict' },
  );
});
test('an older disable receipt only cleans its matching historical view and never changes the newer generation', async (t) => {
  const f = catalogFixture(t),
    s = catalogService({
      ...f,
      host: {},
      privateSkills: createPrivateSkills({ home: f.home }),
    });
  await s.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'prepare',
  });
  f.allow();
  await s.list();
  const transactionId = '019a0000-0000-7000-8000-000000000077',
    receiptId = '019a0000-0000-7000-8000-000000000078';
  // E already stopped and isolated an older view; C retains history while a new selection is prepared.
  const q = join(
    f.home,
    'auriga/skills-active/quarantine',
    transactionId,
    'example',
  );
  mkdirSync(q, { recursive: true });
  writeFileSync(
    join(q, 'SKILL.md'),
    '---\nname: example\ndescription: "Public fixture"\n---\nVerified body.\n',
  );
  const row = { ...f.getRow(), selectionRevision: 8, generation: 10 };
  f.desktop.prepare(
    f.who,
    [row],
    [
      {
        manifest: f.raw,
        artifacts: [f.archive],
        revocations: Buffer.from(JSON.stringify(f.revoked)),
      },
    ],
  );
  const receipt = {
    schemaVersion: 1,
    ...f.who,
    recordType: 'disable',
    writer: 'guardian',
    receiptId,
    transactionId,
    selectionRevision: 7,
    generation: 9,
    quarantinePath: 'auriga/skills-active/quarantine/' + transactionId,
    reason: 'clock_rollback',
    processStopped: true,
    viewCleared: true,
    newSessionRequired: true,
    recordedAt: new Date().toISOString(),
  };
  const receipts = join(f.home, 'auriga/guardian/disable-receipts');
  mkdirSync(receipts, { recursive: true });
  writeFileSync(join(receipts, receiptId + '.json'), JSON.stringify(receipt));
  const ledger = f.desktop.read(f.who);
  assert.equal(existsSync(join(q, '..')), false);
  assert.equal(ledger.generation, 10);
  assert.equal(ledger.installations[0].state, 'applying');
  assert.ok(ledger.processedDisableReceiptIds.includes(receiptId));
  assert.ok(existsSync(join(receipts, receiptId + '.json')));
});
