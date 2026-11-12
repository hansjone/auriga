/* global AbortSignal */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  readFileSync,
  writeFileSync,
  mkdirSync,
  renameSync,
  readdirSync,
} from 'node:fs';
import { join } from 'node:path';
import { catalogService } from '../../src/resources/catalog-service.js';
import { createPrivateSkills } from '../../src/resources/private-skills.js';
import { sha } from '../../src/resources/desktop-skills.js';
import { requireSchema } from '../../src/resources/schema.js';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createInterface } from 'node:readline';
import { catalogFixture } from './catalog-fixture.js';
const action = (f) => ({
  packageId: f.packageId,
  versionId: f.versionId,
  action: 'install',
  revision: 0,
  key: 'first-install',
});
const json = (p) => JSON.parse(readFileSync(p, 'utf8'));
function service(f, extra = {}) {
  return catalogService({
    ...f,
    privateSkills: createPrivateSkills({ home: f.home }),
    host: {
      observe: async () => ({
        state: 'observed',
        path: join(f.active, 'example/SKILL.md'),
        provider: 'auriga-shared',
        contentDigest: sha(Buffer.from('Verified body.')),
      }),
    },
    ...extra,
  });
}
test('Desktop writes frozen prepared files, waits outside the lock for E, then switches and only reports after actual source and bytes match', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  const accepted = await s.apply(action(f));
  assert.equal(accepted.accepted, true);
  assert.equal(existsSync(f.active), false);
  const ledger = requireSchema(
    'UnifiedLedgerFile',
    json(join(f.directory, 'ledger.json')),
  );
  assert.equal(ledger.installations[0].state, 'applying');
  assert.equal(existsSync(join(f.home, 'auriga/guardian/host.lock.d')), false);
  f.allow();
  const protectedFile = join(f.home, 'auriga/guardian/high-watermarks.json'),
    before = readFileSync(protectedFile, 'utf8');
  await s.list();
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
  const receipt = requireSchema(
    'UnifiedReceiptFile',
    json(join(f.directory, 'receipts', f.packageId, f.versionId + '.json')),
  );
  assert.equal(receipt.recordType, 'installation');
  assert.equal(receipt.selectionRevision, 7);
  assert.equal(f.getRow().state, 'pending_restart');
  assert.equal(
    f.requests.some(
      (r) =>
        r.path.endsWith('installation-reports') &&
        JSON.parse(r.options.body).state === 'applied',
    ),
    false,
  );
  assert.equal((await s.observe(f.packageId, 'standard')).state, 'applied');
  assert.equal(
    f.desktop.confirm(f.who, {
      ...f.getRow(),
      generation: f.getRow().generation + 1,
    }),
    undefined,
  );
  assert.equal(
    json(join(f.directory, 'ledger.json')).installations[0].state,
    'applied',
  );
  assert.equal(readFileSync(protectedFile, 'utf8'), before);
});
test('private/project override and stale body at the expected shared path never become applied', async (t) => {
  const f = catalogFixture(t);
  let observed = {
    state: 'observed',
    path: join(f.home, 'skills/example/SKILL.md'),
    provider: 'filesystem',
    contentDigest: sha(Buffer.from('private')),
  };
  const s = service(f, { host: { observe: async () => observed } });
  await s.apply(action(f));
  f.allow();
  await s.list();
  assert.equal((await s.observe(f.packageId, 'standard')).state, 'overridden');
  observed = {
    state: 'observed',
    path: join(f.active, 'example/SKILL.md'),
    provider: 'auriga-shared',
    contentDigest: sha(Buffer.from('stale loaded body')),
  };
  assert.equal(
    (await s.observe(f.packageId, 'standard')).state,
    'pending_restart',
  );
  writeFileSync(join(f.active, 'example/SKILL.md'), 'user edit');
  await assert.rejects(s.observe(f.packageId, 'standard'), {
    code: 'resource.state_conflict',
  });
});
test('tampered original manifest and logout during download never create shared cache or active view', async (t) => {
  const f = catalogFixture(t),
    fetchImpl = async (url, options) =>
      url.pathname.endsWith('/manifest')
        ? new Response(Buffer.concat([f.raw, Buffer.from(' ')]))
        : f.fetchImpl(url, options);
  await assert.rejects(
    service(f, { fetchImpl }).apply(action(f)),
    /manifest_binding/,
  );
  assert.equal(existsSync(f.directory), false);
  assert.equal(existsSync(f.active), false);
  const fetchLogout = async (url, options) => {
    const result = await f.fetchImpl(url, options);
    if (url.pathname.endsWith('/downloads')) f.setCurrent(undefined);
    return result;
  };
  await assert.rejects(
    service(f, { fetchImpl: fetchLogout }).apply(action(f)),
    { code: 'common.unauthenticated' },
  );
  assert.equal(existsSync(f.active), false);
});
test('cloud selection never calls roots, Desktop writer, manifest or artifact downloads', async (t) => {
  const f = catalogFixture(t);
  f.runtime.resourceIdentity = {
    tenantId: f.who.tenantId,
    ownerId: f.who.ownerId,
    instanceId: f.who.hostId,
  };
  const options = {
    ...f,
    roots: async () => {
      throw new Error('cloud roots forbidden');
    },
    privateSkills: {},
    host: {},
  };
  Object.defineProperty(options, 'desktop', {
    get() {
      throw new Error('cloud writer forbidden');
    },
  });
  const s = catalogService(options);
  assert.equal((await s.apply(action(f))).cloud, true);
  await s.list();
  assert.equal(
    f.requests.some((r) => /download|manifest/.test(r.path)),
    false,
  );
  assert.equal(existsSync(f.directory), false);
});
test('disable and remove clear the actual view before terminal reports and need no new downloads', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  await s.list();
  const downloads = f.requests.filter((r) => /download/.test(r.path)).length;
  const disabled = await s.apply({
    ...action(f),
    action: 'disable',
    revision: 1,
    key: 'disable',
  });
  assert.equal(disabled.installation.state, 'disabled');
  assert.deepEqual(readdirSync(f.active), []);
  const removed = await s.apply({
    ...action(f),
    action: 'remove',
    revision: 2,
    key: 'remove',
  });
  assert.equal(removed.installation.state, 'removed');
  assert.equal(
    f.requests.filter((r) => /download/.test(r.path)).length,
    downloads,
  );
});
test('recovers a crash after current rename and before the switched transaction was persisted', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  const transaction = readdirSync(join(f.directory, 'transactions'))
    .map((name) => json(join(f.directory, 'transactions', name)))
    .find((tx) => tx.phase === 'prepared');
  mkdirSync(join(f.active, '..'), { recursive: true });
  renameSync(join(f.directory, 'staging', transaction.transactionId), f.active);
  await service(f).list();
  assert.equal(
    json(join(f.directory, 'transactions', transaction.transactionId + '.json'))
      .phase,
    'committed',
  );
  assert.equal(json(join(f.directory, 'ledger.json')).revision, 2);
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
});
test(
  'Windows directory occupation retains old bytes and pending_restart, then explicit refresh completes removal',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const f = catalogFixture(t),
      s = service(f);
    await s.apply(action(f));
    f.allow();
    await s.list();
    const script = join(f.home, 'hold.ps1');
    writeFileSync(
      script,
      `Add-Type @"\nusing System; using System.Runtime.InteropServices; using Microsoft.Win32.SafeHandles;\npublic class HoldDirectory { [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern SafeFileHandle CreateFile(string p,uint a,uint s,IntPtr x,uint c,uint f,IntPtr y); }\n"@\n$h=[HoldDirectory]::CreateFile('${f.active.replace(/'/g, "''")}',2147483648,3,[IntPtr]::Zero,3,33554432,[IntPtr]::Zero)\nif($h.IsInvalid){throw 'directory handle failed'}\nWrite-Output 'READY'\n[Console]::OpenStandardInput().ReadByte() | Out-Null\n$h.Dispose()\n`,
    );
    const child = spawn(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-File', script],
      { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] },
    );
    const exited = once(child, 'exit');
    const lines = createInterface({ input: child.stdout });
    try {
      const [line] = await once(lines, 'line', {
        signal: AbortSignal.timeout(10000),
      });
      assert.equal(line, 'READY');
      const result = await s.apply({
        ...action(f),
        action: 'disable',
        revision: 1,
        key: 'occupied-disable',
      });
      assert.equal(result.installation.state, 'pending_restart');
      assert.match(
        readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
        /Verified body/,
      );
      child.stdin.end('\n');
      await exited;
      assert.equal((await s.list()).items[0].state, 'disabled');
      assert.deepEqual(readdirSync(f.active), []);
    } finally {
      lines.close();
      if (child.exitCode === null) child.kill();
      await exited;
    }
  },
);
test('identity switch clears an owned old view and an E disable receipt is retained and cleaned exactly once', async (t) => {
  const f = catalogFixture(t),
    s = service(f);
  await s.apply(action(f));
  f.allow();
  await s.list();
  f.desktop.switchIdentity({
    ...f.who,
    ownerId: '019a0000-0000-7000-8000-000000000099',
  });
  assert.equal(existsSync(f.active), false);
  assert.equal(
    json(join(f.directory, 'ledger.json')).installations[0].state,
    'disabled',
  );
  // Simulate E's stopped-process quarantine and receipt on the independent record branch.
  const backup = readdirSync(join(f.directory, 'staging')).find((n) =>
    existsSync(join(f.directory, 'staging', n, 'example')),
  );
  const transactionId = '019a0000-0000-7000-8000-000000000051',
    receiptId = '019a0000-0000-7000-8000-000000000052',
    q = join(f.home, 'auriga/skills-active/quarantine', transactionId);
  mkdirSync(join(q, '..'), { recursive: true });
  renameSync(join(f.directory, 'staging', backup), q);
  const value = json(join(f.directory, 'ledger.json'));
  value.installations[0].state = 'applied';
  writeFileSync(join(f.directory, 'ledger.json'), JSON.stringify(value));
  const receipts = join(f.home, 'auriga/guardian/disable-receipts');
  mkdirSync(receipts, { recursive: true });
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
    reason: 'lease_expired',
    processStopped: true,
    viewCleared: true,
    newSessionRequired: true,
    recordedAt: new Date().toISOString(),
  };
  writeFileSync(join(receipts, receiptId + '.json'), JSON.stringify(receipt));
  const first = f.desktop.read(f.who);
  assert.equal(existsSync(q), false);
  assert.ok(first.processedDisableReceiptIds.includes(receiptId));
  const revision = first.revision;
  assert.equal(f.desktop.read(f.who).revision, revision);
  assert.ok(existsSync(join(receipts, receiptId + '.json')));
});
