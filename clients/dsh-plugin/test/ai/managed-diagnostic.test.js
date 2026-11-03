import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  createManagedModels,
  MANAGED_MODEL_REF,
} from '../../src/managed/models.js';
import {
  installObservability,
  logDirectory,
} from '../../src/observability/index.js';

test('actual manual discovery retains sanitized transport diagnostics without exposing its credential', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'manual-model-diagnostic-'));
  const original = {
    local: process.env.LOCALAPPDATA,
    state: process.env.XDG_STATE_HOME,
  };
  process.env.LOCALAPPDATA = process.env.XDG_STATE_HOME = dir;
  const cleanup = [];
  installObservability({
    effect(setup) {
      cleanup.push(setup());
    },
  });
  const value = {
    providers: {
      'auriga-managed': {
        api: 'openai-completions',
        baseURL: 'https://example.com/ai/v1',
        apiKeyEnv: MANAGED_MODEL_REF,
        compat: { supportsStore: false },
        models: [],
      },
    },
  };
  const model = createManagedModels({
    loadMaterial: async () => ({
      origin: 'https://example.com',
      value: {
        baseUrl: 'https://example.com/ai/v1',
        credential: 'sentinel-private',
        models: [],
      },
    }),
  });
  try {
    t.mock.method(globalThis, 'fetch', async () => {
      throw Object.assign(
        new Error(
          'network unavailable Bearer sentinel-bearer password=sentinel-password',
          { cause: new Error('connection reset token=sentinel-token') },
        ),
        { code: 'ECONNRESET' },
      );
    });
    const llm = {
      discoverModels() {},
      prepareCall() {},
      listModels() {},
      stream() {},
      ctx: { on: () => () => {} },
    };
    model.attach(
      llm,
      { describe: () => [{ ns: 'llm-pi-ai', value }] },
      {
        configuration: () => [
          { entry: { options: { id: 'llm-pi-ai', config: value } } },
        ],
      },
      { resolve() {}, describe() {} },
    );
    await model.ready;
    await assert.rejects(
      llm.discoverModels('llm-pi-ai', { provider: 'auriga-managed' }),
    );
    const directory = logDirectory();
    await Promise.all(cleanup.map((close) => close()));
    const files = await readdir(directory);
    const text = (
      await Promise.all(
        files
          .filter((name) => name.endsWith('.jsonl'))
          .map((name) => readFile(join(directory, name), 'utf8')),
      )
    ).join('');
    const records = text.trim().split('\n').map(JSON.parse);
    const failure = records.find(
      (row) => row.message === 'HTTP request failed',
    );
    assert.ok(failure);
    assert.equal(failure.error_code, 'ECONNRESET');
    assert.match(failure.exception, /network unavailable/);
    assert.match(failure.cause, /connection reset/);
    assert.doesNotMatch(text, /sentinel-/);
  } finally {
    model.close();
    await Promise.all(cleanup.map((close) => close()));
    if (original.local === undefined) delete process.env.LOCALAPPDATA;
    else process.env.LOCALAPPDATA = original.local;
    if (original.state === undefined) delete process.env.XDG_STATE_HOME;
    else process.env.XDG_STATE_HOME = original.state;
    await rm(dir, { recursive: true, force: true });
  }
});
