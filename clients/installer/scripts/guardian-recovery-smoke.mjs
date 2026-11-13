import assert from 'node:assert/strict';
import {
  mkdirSync,
  cpSync,
  readFileSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { setTimeout as delay } from 'node:timers/promises';
import { resourceFixture, id } from '../test/resource-fixture.mjs';
import { ProcessTree, assertNTFS } from '../runtime/process-tree.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
import { superviseDsh } from '../runtime/desktop-supervision.mjs';
import { acquireHostLock, durableJson } from '../runtime/host-lock.mjs';
import { mergeSkillPatch, yamlParser } from '../runtime/skill-config.mjs';
const home = resolve('clients/installer/target/r4-recovery-' + Date.now());
mkdirSync(home, { recursive: true });
assertNTFS(home);
const cli = resolve(
  'clients/installer/target/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js',
);
const require = createRequire(cli);
assert.equal(require('@deepseek-ai/dsh/package.json').version, '0.2.1-rc.3');
const f = resourceFixture(home),
  tree = new ProcessTree(home),
  s = new Supervisor(home, tree, f.trust);
const release = tree.singleton();
let child,
  starts = 0,
  notices = 0,
  output = '',
  privateReady = false,
  sharedReady = false;
try {
  await s.prepare();
  await s.guardian.disable('verification_failed');
  const state = s.guardian.read(),
    receipt = JSON.parse(
      readFileSync(
        join(
          s.guardian.directory,
          'disable-receipts',
          state.lastDisableReceiptId + '.json',
        ),
      ),
    );
  f.renew();
  f.ledger.processedDisableReceiptIds.push(state.lastDisableReceiptId);
  f.ledger.revision++;
  f.ledger.selectionRevision++;
  f.ledger.generation++;
  const installation = f.ledger.installations[0];
  Object.assign(installation, {
    state: 'applying',
    selectionRevision: 2,
    generation: 2,
  });
  const staged = join(f.directory, 'staging', id(7));
  mkdirSync(staged, { recursive: true });
  cpSync(
    join(home, receipt.quarantinePath, 'example-resource'),
    join(staged, 'example-resource'),
    { recursive: true },
  );
  const transaction = {
    schemaVersion: 1,
    tenantId: f.ledger.tenantId,
    ownerId: f.ledger.ownerId,
    hostId: f.ledger.hostId,
    transactionId: id(7),
    writer: 'plugin',
    operation: 'activate',
    phase: 'prepared',
    expectedLedgerRevision: f.ledger.revision,
    selectionRevision: 2,
    generation: 2,
    receiptId: id(6),
    recordedAt: new Date().toISOString(),
  };
  mkdirSync(join(f.directory, 'transactions'), { recursive: true });
  durableJson(join(f.directory, 'transactions', id(7) + '.json'), transaction);
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  const patch = join(home, 'shared.patch.yml');
  writeFileSync(
    patch,
    mergeSkillPatch('[]\n', yamlParser(require('yaml')), home),
  );
  const result = await superviseDsh({
    tree,
    supervisor: s,
    session: { child() {} },
    notify: () => {
      notices++;
    },
    start: () => {
      starts++;
      output = '';
      if (starts === 2) {
        assert.equal(s.guardian.read().guardian.state, 'active');
        const lock = acquireHostLock(home, 'C');
        try {
          mkdirSync(join(home, 'auriga/skills-active/current'), {
            recursive: true,
          });
          renameSync(
            join(staged, 'example-resource'),
            join(home, 'auriga/skills-active/current/example-resource'),
          );
          installation.state = 'applied';
          f.ledger.revision++;
          durableJson(join(f.directory, 'receipts', id(2), id(4) + '.json'), {
            schemaVersion: 1,
            tenantId: f.ledger.tenantId,
            ownerId: f.ledger.ownerId,
            hostId: f.ledger.hostId,
            recordType: 'installation',
            writer: 'plugin',
            receiptId: id(6),
            resource: f.ref,
            installationRevision: 1,
            selectionRevision: 2,
            generation: 2,
            recordedAt: new Date().toISOString(),
          });
          transaction.phase = 'committed';
          durableJson(
            join(f.directory, 'transactions', id(7) + '.json'),
            transaction,
          );
          durableJson(join(f.directory, 'ledger.json'), f.ledger);
        } finally {
          lock.release();
        }
      }
      child = spawn(
        process.execPath,
        [
          cli,
          '--profile',
          'web',
          '--patch',
          patch,
          '--no-open',
          '--host',
          '127.0.0.1',
          '--port',
          '0',
        ],
        {
          env: { ...process.env, DSH_HOME: home },
          stdio: ['ignore', 'pipe', 'pipe'],
          windowsHide: true,
        },
      );
      child.stdout.on('data', (b) => {
        output += b;
      });
      child.stderr.on('data', (b) => {
        output += b;
      });
      return child;
    },
    poll: async () => {
      for (
        let attempt = 0;
        !output.match(/http:\/\/127\.0\.0\.1:\d+/);
        attempt++
      ) {
        if (attempt > 180 || child.exitCode !== null)
          throw new Error(
            'isolated DSH readiness failed: ' +
              output.replace(/token=[^\s]+/g, 'token=[redacted]').slice(-1000),
          );
        await delay(100);
      }
      if (starts === 1) privateReady = true;
      else if (!sharedReady) sharedReady = true;
      else await tree.stopTree();
    },
  });
  assert.equal(starts, 2);
  assert.equal(notices, 1);
  assert(privateReady && sharedReady);
  assert.equal(s.guardian.read().guardian.generation, 2);
  assert.equal(s.guardian.read().guardian.state, 'active');
  writeFileSync(
    join(home, 'evidence.json'),
    JSON.stringify(
      {
        version: '0.2.1-rc.3',
        privateReady,
        sharedReady,
        starts,
        notices,
        guardianGeneration: 2,
        childExit: result,
        allStopped: await tree.isStopped(),
      },
      null,
      2,
    ),
  );
  console.log(home);
} finally {
  await tree.stopTree();
  release();
}
