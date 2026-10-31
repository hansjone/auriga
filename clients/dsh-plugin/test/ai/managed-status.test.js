import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createManagedModelStatus } from '../../src/ai/managed-status.js';

const binding = {
  tenantId: '00000000-0000-4000-8000-000000000001',
  ownerId: '00000000-0000-4000-8000-000000000002',
  instanceId: '00000000-0000-4000-8000-000000000003',
};
async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-model-status-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = join(dir, 'model.json');
  const material = {
    ...binding,
    credentialVersion: 1,
    credential: `amc.${binding.tenantId}.${binding.ownerId}.${binding.instanceId}.00000000-0000-4000-8000-000000000004.${'a'.repeat(43)}`,
    baseUrl: 'https://portal.example.com/ai/v1',
    models: [{ id: 'pool-a' }],
  };
  const write = (value = material) =>
    writeFile(file, JSON.stringify(value), { mode: 0o600 });
  await write();
  const status = createManagedModelStatus({ ...binding, file });
  t.after(() => status.close());
  return { status, material, write, file };
}
const llm = {
  listModels: async (provider) => {
    assert.equal(provider, 'auriga-managed');
    return [{ id: 'pool-a' }];
  },
};

test('managed status: native provider plus online authorized catalog makes the relay ready', async (t) => {
  const { status, material } = await fixture(t);
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    assert.equal(url.toString(), material.baseUrl + '/models');
    assert.equal(
      init.headers.get('authorization'),
      'Bearer ' + material.credential,
    );
    assert.equal(init.credentials, 'omit');
    assert.equal(init.redirect, 'error');
    return Response.json({ object: 'list', data: [{ id: 'pool-a' }] });
  });
  await status.refresh(llm);
  assert.equal(status.status.available, true);
  assert.equal(status.status.state, 'model_ready');
  assert.doesNotMatch(JSON.stringify(status.status), /amc\.|pool-a/);
});
for (const failure of [
  'missing-provider',
  'denied',
  'empty',
  'wrong-model',
  'wrong-owner',
  'missing-file',
  'malformed',
  'timeout',
])
  test('managed status fails closed: ' + failure, async (t) => {
    const { status, material, write, file } = await fixture(t);
    let requests = 0;
    t.mock.method(globalThis, 'fetch', async () => {
      requests++;
      if (failure === 'timeout') throw new Error('private');
      if (failure === 'denied') return Response.json({}, { status: 403 });
      if (failure === 'malformed') return Response.json({ data: 'bad' });
      return Response.json({
        object: 'list',
        data:
          failure === 'empty'
            ? []
            : [{ id: failure === 'wrong-model' ? 'other' : 'pool-a' }],
      });
    });
    if (failure === 'wrong-owner')
      await write({ ...material, ownerId: binding.instanceId });
    if (failure === 'missing-file') await rm(file);
    await status.refresh(failure === 'missing-provider' ? {} : llm);
    assert.equal(status.status.available, false);
    if (['wrong-owner', 'missing-file', 'missing-provider'].includes(failure))
      assert.equal(requests, 0);
  });

test('managed status: shutdown prevents an in-flight probe from publishing ready', async (t) => {
  const { status } = await fixture(t);
  t.mock.method(globalThis, 'fetch', async () => {
    status.close();
    return Response.json({ object: 'list', data: [{ id: 'pool-a' }] });
  });
  await status.refresh(llm);
  assert.equal(status.status.available, false);
});

test('managed status: shutdown releases a host port that never resolves', async (t) => {
  const { status } = await fixture(t);
  let entered;
  const ready = new Promise((resolve) => {
    entered = resolve;
  });
  const pending = status.refresh({
    listModels() {
      entered();
      return new Promise(() => {});
    },
  });
  await ready;
  status.close();
  await pending;
  assert.equal(status.status.available, false);
});
