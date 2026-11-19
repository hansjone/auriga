import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, rm, readFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, resolve, sep } from 'node:path';
import { createFileLedgerStore } from '../../src/network/host/store.js';
import {
  createMigratingMessageStore,
  sharedMessagePaths,
} from '../../src/shared/message-storage.js';
import { openSharedInboxLedger } from '../../src/shared/message-inbox-ledger.js';
import { inboxFixture } from './inbox-fixture.mjs';

async function paths(t) {
  const parent = fileURLToPath(
    new URL('../../../../.dev/shared-message-storage/', import.meta.url),
  );
  await mkdir(parent, { recursive: true });
  const directory = await mkdtemp(join(parent, 'case-'));
  t.after(async () => {
    assert.ok(resolve(directory).startsWith(resolve(parent) + sep));
    await rm(directory, { recursive: true, force: true });
  });
  return {
    dir: join(directory, '.host', 'message-preferences'),
    legacyDir: join(directory, 'message-preferences'),
  };
}
async function put(dir, value) {
  const handle = await createFileLedgerStore({ dir }).open();
  try {
    await handle.commit(value);
  } finally {
    await handle.close();
  }
}
const disk = async (dir) =>
  JSON.parse(await readFile(join(dir, 'ledger.json'), 'utf8'));

test('all writable message ledgers use existing hard quota roots without adding a quota allocation', () => {
  const pool = randomUUID(),
    context = randomUUID(),
    root = `/var/lib/auriga/pools/${pool}/host`;
  assert.deepEqual(sharedMessagePaths(pool, 'preferences'), {
    dir: root + '/.host/message-preferences',
    legacyDir: root + '/message-preferences',
  });
  assert.equal(
    sharedMessagePaths(pool, 'inboxes', context).dir,
    root + '/.host/message-inboxes/' + context,
  );
  const source = readFileSync(
    new URL(
      '../../../../infra/agent/internal/runtime/persistent_pool.go',
      import.meta.url,
    ),
    'utf8',
  );
  assert.match(source, /home := filepath.Join\(members, "\.host"\)/);
  assert.match(
    source,
    /layout.Quota.Ensure\(ctx, home, storage.MemberBytes, storage.MemberInodes\)/,
  );
  assert.match(
    readFileSync(
      new URL('../../src/shared/message-trust.js', import.meta.url),
      'utf8',
    ),
    /\/host\/message-trust/,
  );
  for (const args of [
    [pool, 'other'],
    [pool, 'inboxes', '../elsewhere'],
    ['../pool', 'preferences'],
  ])
    assert.throws(() => sharedMessagePaths(...args));
});

test('legacy intent is atomically copied inside quota; later writes retain old digest and never reset to the legacy snapshot', async (t) => {
  const p = await paths(t),
    original = { version: 1, entries: { one: { enabled: true } } };
  await put(p.legacyDir, original);
  const store = createMigratingMessageStore(p);
  let handle = await store.open();
  assert.deepEqual(await handle.load(), original);
  assert.deepEqual(await disk(p.legacyDir), original);
  const digest = (await disk(p.dir)).legacyDigest;
  assert.match(digest, /^sha256:/);
  const changed = { version: 1, entries: { one: { enabled: false } } };
  await handle.commit(changed);
  await handle.close();
  handle = await store.open();
  assert.deepEqual(await handle.load(), changed);
  await handle.close();
  assert.equal((await disk(p.dir)).legacyDigest, digest);
  assert.deepEqual(await disk(p.legacyDir), original);
});

test('migration of a real committed inbox preserves its original signed receipt/cursor/producer high water', async (t) => {
  const f = inboxFixture();
  let scope, ledger;
  t.after(async () => {
    await ledger?.close();
    await scope?.close();
    await f.close();
  });
  const receiver = await f.openReceiver();
  await receiver.reconcile(true);
  const item = f.delivery();
  f.deliveries.push(item);
  await receiver.pull();
  const original = f.store.snapshot();
  await receiver.close();
  assert.equal(
    original.state.durable.inbound[item.recipients[0].recipient_id].stage,
    'committed',
  );
  const p = await paths(t);
  await put(p.legacyDir, original);
  scope = await f.ports.openMessageInbox(f.binding);
  ledger = await openSharedInboxLedger({
    context: scope.context,
    store: createMigratingMessageStore(p),
  });
  assert.deepEqual(ledger.peek().durable, original.state.durable);
  assert.equal(ledger.peek().durable.resumeCursor, '1');
  assert.ok(
    ledger.peek().durable.inbound[item.recipients[0].recipient_id].receipt
      .operation_id,
  );
  await ledger.fenceReceive();
  assert.deepEqual(
    (await disk(p.dir)).ledger.state.durable,
    original.state.durable,
  );
  assert.deepEqual(await disk(p.legacyDir), original);
});

test('quota failure aborts migration without deleting the old ledger; successful retry retains its exact state', async (t) => {
  const p = await paths(t),
    original = {
      version: 1,
      highWater: '99',
      receipts: { accepted: 'original' },
    };
  await put(p.legacyDir, original);
  const store = createFileLedgerStore({
    dir: p.dir,
    beforeStep(step) {
      if (step === 'write_tmp')
        throw Object.assign(new Error('quota'), { code: 'EDQUOT' });
    },
  });
  await assert.rejects(createMigratingMessageStore({ ...p, store }).open(), {
    code: 'EDQUOT',
  });
  assert.deepEqual(await disk(p.legacyDir), original);
  const handle = await createMigratingMessageStore(p).open();
  assert.deepEqual(await handle.load(), original);
  await handle.close();
});

test('active or later divergent legacy writers cannot race or roll back the quota ledger', async (t) => {
  const p = await paths(t),
    legacy = await createFileLedgerStore({ dir: p.legacyDir }).open();
  await legacy.commit({ cursor: '9' });
  const store = createMigratingMessageStore(p);
  await assert.rejects(store.open());
  await legacy.close();
  const handle = await store.open();
  await assert.rejects(createFileLedgerStore({ dir: p.legacyDir }).open());
  await handle.commit({ cursor: '10' });
  await handle.close();
  await put(p.legacyDir, { cursor: '11' });
  await assert.rejects(store.open());
  assert.deepEqual((await disk(p.dir)).ledger, { cursor: '10' });
  assert.deepEqual(await disk(p.legacyDir), { cursor: '11' });
});

test('fresh quota ledger cannot silently replace later-discovered history or a malformed migration wrapper', async (t) => {
  const p = await paths(t),
    store = createMigratingMessageStore(p);
  const handle = await store.open();
  assert.equal(await handle.load(), undefined);
  await handle.commit({ cursor: '0' });
  await handle.close();
  await put(p.legacyDir, { cursor: '8' });
  await assert.rejects(store.open());
  await put(p.dir, { version: 1, ledger: { cursor: '0' } });
  await assert.rejects(store.open());
  await put(p.dir, { version: 1, legacyDigest: null, ledger: null });
  await assert.rejects(store.open());
  assert.deepEqual(await disk(p.legacyDir), { cursor: '8' });
});

test('an uncertain subsequent quota write poisons its writer and preserves the previous committed evidence', async (t) => {
  const p = await paths(t);
  let fail = false;
  const target = createFileLedgerStore({
    dir: p.dir,
    beforeStep() {
      if (fail) throw new Error('uncertain fsync');
    },
  });
  const handle = await createMigratingMessageStore({
    ...p,
    store: target,
  }).open();
  await handle.commit({ cursor: '7', receipt: 'accepted' });
  fail = true;
  await assert.rejects(handle.commit({ cursor: '8' }));
  await assert.rejects(handle.commit({ cursor: '0' }));
  await assert.rejects(handle.load());
  await handle.close();
  assert.deepEqual((await disk(p.dir)).ledger, {
    cursor: '7',
    receipt: 'accepted',
  });
});
