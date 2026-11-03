import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loadManagedModelMaterial } from '../../src/ai/managed-status.js';
const binding = {
  tenantId: '00000000-0000-4000-8000-000000000001',
  ownerId: '00000000-0000-4000-8000-000000000002',
  instanceId: '00000000-0000-4000-8000-000000000003',
};
async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'manual-model-material-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const value = {
    ...binding,
    credentialVersion: 1,
    credential: `amc.${binding.tenantId}.${binding.ownerId}.${binding.instanceId}.00000000-0000-4000-8000-000000000004.${'a'.repeat(43)}`,
    baseUrl: 'https://portal.example.com/ai/v1',
    models: [{ id: 'auto', contextWindow: 32768, maxOutputTokens: 4096 }],
  };
  const file = join(dir, 'model.json');
  const write = () => writeFile(file, JSON.stringify(value), { mode: 0o600 });
  await write();
  return { value, file, write };
}
test('manual model binding loads the sole private file with exact identity and capacity', async (t) => {
  const f = await fixture(t);
  const material = await loadManagedModelMaterial({ ...binding, file: f.file });
  assert.equal(material.origin, 'https://portal.example.com');
  assert.equal(JSON.stringify(material.value), JSON.stringify(f.value));
});
for (const [name, change] of [
  [
    'tenant',
    (v) => {
      v.tenantId = binding.ownerId;
    },
  ],
  [
    'owner',
    (v) => {
      v.ownerId = binding.instanceId;
    },
  ],
  [
    'instance',
    (v) => {
      v.instanceId = binding.ownerId;
    },
  ],
  [
    'version',
    (v) => {
      v.credentialVersion = 0;
    },
  ],
  [
    'credential',
    (v) => {
      v.credential = 'unbound-private-key';
    },
  ],
  [
    'userinfo',
    (v) => {
      v.baseUrl = 'https://user:private@example.com/ai/v1';
    },
  ],
  [
    'query',
    (v) => {
      v.baseUrl += '?key=private';
    },
  ],
  [
    'path',
    (v) => {
      v.baseUrl = 'https://portal.example.com/other';
    },
  ],
  [
    'empty models',
    (v) => {
      v.models = [];
    },
  ],
  [
    'unknown context',
    (v) => {
      delete v.models[0].contextWindow;
    },
  ],
  [
    'unknown output',
    (v) => {
      delete v.models[0].maxOutputTokens;
    },
  ],
  [
    'invalid output',
    (v) => {
      v.models[0].maxOutputTokens = 32768;
    },
  ],
])
  test('manual model material fails closed: ' + name, async (t) => {
    const f = await fixture(t);
    change(f.value);
    await f.write();
    await assert.rejects(
      loadManagedModelMaterial({ ...binding, file: f.file }),
      (error) => !/amc\.|unbound-private-key|key=private/.test(error.message),
    );
  });
test('oversized private material is refused before parsing', async (t) => {
  const f = await fixture(t);
  await writeFile(f.file, ' '.repeat(1048577));
  await assert.rejects(
    loadManagedModelMaterial({ ...binding, file: f.file }),
    /private model file/,
  );
});
