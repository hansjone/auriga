import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createManagedModelStatus } from '../../src/ai/managed-status.js';
import {
  installObservability,
  logDirectory,
} from '../../src/observability/index.js';

test('actual managed status failures retain phase/status/cause through final private JSON', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'managed-diagnostic-'));
  const original = {
    local: process.env.LOCALAPPDATA,
    state: process.env.XDG_STATE_HOME,
  };
  process.env.LOCALAPPDATA = dir;
  process.env.XDG_STATE_HOME = dir;
  const cleanup = [];
  installObservability({
    effect(setup) {
      cleanup.push(setup());
    },
  });
  try {
    const binding = {
      tenantId: '00000000-0000-4000-8000-000000000001',
      ownerId: '00000000-0000-4000-8000-000000000002',
      instanceId: '00000000-0000-4000-8000-000000000003',
      file: join(dir, 'model.json'),
    };
    const raw = {
      ...binding,
      credentialVersion: 7,
      credential: `amc.${binding.tenantId}.${binding.ownerId}.${binding.instanceId}.00000000-0000-4000-8000-000000000004.${'a'.repeat(43)}`,
      baseUrl: 'https://example.com/ai/v1',
      models: [{ id: 'private-directory-model' }],
    };
    delete raw.file;
    await writeFile(binding.file, JSON.stringify(raw), { mode: 0o600 });
    const status = createManagedModelStatus(binding);
    await status.refresh({
      listModels() {
        throw Object.assign(
          new Error(
            'native failed password=sentinel-password ticket=sentinel-ticket AURIGA_TEST_TOKEN=sentinel-env',
            { cause: new Error('connection reset Bearer sentinel-bearer') },
          ),
          { code: 'native.list_failed' },
        );
      },
    });
    assert.equal(status.status.available, false);
    t.mock.method(globalThis, 'fetch', async () =>
      Response.json({}, { status: 403 }),
    );
    await status.refresh({
      listModels: async () => [{ id: 'private-directory-model' }],
    });
    assert.equal(status.status.available, false);
    status.close();
    const directory = logDirectory();
    await Promise.all(cleanup.map((close) => close()));
    const files = await readdir(directory);
    const text = (
      await Promise.all(
        files
          .filter((n) => n.endsWith('.jsonl'))
          .map((n) => readFile(join(directory, n), 'utf8')),
      )
    ).join('');
    const records = text
      .trim()
      .split('\n')
      .map(JSON.parse)
      .filter((r) => r.message === 'Managed model status failed');
    assert.equal(records.length, 2);
    assert.equal(records[0].phase, 'native');
    assert.equal(records[0].error_code, 'native.list_failed');
    assert.match(records[0].cause, /connection reset/);
    assert.match(records[0].exception, /at /);
    assert.equal(records[1].phase, 'catalog');
    assert.equal(records[1].http_status, 403);
    assert.equal(records[1].credential_version, 7);
    assert.equal(records[1].instance_id, binding.instanceId);
    assert.doesNotMatch(text, /sentinel-|amc\.|private-directory-model/);
  } finally {
    await Promise.all(cleanup.map((close) => close()));
    if (original.local === undefined) delete process.env.LOCALAPPDATA;
    else process.env.LOCALAPPDATA = original.local;
    if (original.state === undefined) delete process.env.XDG_STATE_HOME;
    else process.env.XDG_STATE_HOME = original.state;
    await rm(dir, { recursive: true, force: true });
  }
});
