import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  guardManagedSettings,
  guardManagedCredentials,
} from '../src/managed/settings-policy.js';

const denied = { code: 'common.forbidden' };
function fixture() {
  const calls = [];
  const service = Object.fromEntries(
    ['update', 'replace', 'mutate', 'prepareDocument'].map((method) => [
      method,
      async (...args) => calls.push({ method, args }),
    ]),
  );
  const cleanup = guardManagedSettings(service);
  return { service, calls, cleanup };
}

test('R9 personal preferences, model routes and Agent selection remain writable', async () => {
  const { service, calls } = fixture();
  await service.update('locale', { preference: 'zh' }, 3);
  await service.replace('ui-theme', { preference: 'light', fontSize: 16 });
  await service.mutate('ui-settings', [
    { op: 'set', path: [], value: { enabled: true } },
  ]);
  await service.update('agent-default-model', {
    provider: 'personal',
    model: 'example-model',
  });
  await service.mutate('agent-preset-registry', [
    { op: 'unset', path: ['selectedDefault'] },
  ]);
  await service.update('llm-pi-ai', {
    providers: { personal: { baseURL: 'https://example.com/v1' } },
  });
  await service.mutate(
    'llm-pi-ai',
    [{ op: 'set', path: ['providers', 'personal', 'models'], value: [] }],
    8,
  );
  await service.mutate('llm-pi-ai', [
    { op: 'unset', path: ['providers', 'personal'] },
  ]);
  assert.equal(calls.length, 8);
  assert.deepEqual(calls[6].args.slice(0, 1), ['llm-pi-ai']);
  assert.equal(calls[6].args[2], 8);
});

test('R9 managed provider, ancestors, root resets and mixed atomic operations reject before persistence', async () => {
  const { service, calls } = fixture();
  for (const path of [
    [],
    ['providers'],
    ['providers', 'auriga-managed'],
    ['providers', 'auriga-managed', 'baseURL'],
    ['providers', 'auriga-managed', 'headers', 'Authorization'],
  ]) {
    for (const op of ['set', 'unset']) {
      await assert.rejects(
        service.mutate('llm-pi-ai', [{ op, path, value: {} }]),
        denied,
      );
    }
  }
  await assert.rejects(
    service.update('llm-pi-ai', {
      providers: {
        personal: {},
        'auriga-managed': { baseURL: 'https://other.example/v1' },
      },
    }),
    denied,
  );
  await assert.rejects(
    service.replace('llm-pi-ai', { providers: { personal: {} } }),
    denied,
  );
  await assert.rejects(
    service.mutate('llm-pi-ai', [
      { op: 'set', path: ['providers', 'personal'], value: {} },
      { op: 'unset', path: ['providers'] },
    ]),
    denied,
  );
  assert.equal(calls.length, 0);
});

test('managed native model rows remain editable while connection and storage fields cannot change', async () => {
  const { service, calls } = fixture();
  await service.update('llm-pi-ai', {
    providers: {
      'auriga-managed': {
        models: [
          {
            id: 'auto',
            name: '我的默认',
            contextWindow: 32768,
            maxTokens: 4096,
          },
        ],
      },
    },
  });
  await service.mutate(
    'llm-pi-ai',
    [
      {
        op: 'set',
        path: ['providers', 'auriga-managed', 'models', '0', 'name'],
        value: '名字',
      },
    ],
    4,
  );
  for (const value of [
    { api: 'anthropic-messages' },
    { headers: {} },
    { apiKeyEnv: 'PERSONAL' },
    { compat: { supportsStore: true } },
    { models: [{ id: 'auto', compat: { supportsStore: true } }] },
  ]) {
    await assert.rejects(
      service.update('llm-pi-ai', { providers: { 'auriga-managed': value } }),
      denied,
    );
  }
  await assert.rejects(
    service.mutate('llm-pi-ai', [
      {
        op: 'set',
        path: [
          'providers',
          'auriga-managed',
          'models',
          '0',
          'compat',
          'supportsStore',
        ],
        value: true,
      },
    ]),
    denied,
  );
  assert.equal(calls.length, 2);
});

test('R9 unknown, Auriga, transport, network and credential settings fail closed', async () => {
  const { service, calls } = fixture();
  for (const ns of [
    'auriga-dsh',
    'auriga-managed',
    'connection',
    'webserver',
    'permission',
    'bash-sandbox',
    'credentials-local',
    'new-plugin',
    '__proto__',
    'llm-pi-ai-copy',
  ]) {
    for (const method of ['update', 'replace', 'mutate']) {
      await assert.rejects(
        service[method](ns, method === 'mutate' ? [] : {}),
        denied,
      );
    }
  }
  await assert.rejects(service.prepareDocument(), denied);
  await assert.rejects(
    service.update('ui-settings', { transport: {} }),
    denied,
  );
  await assert.rejects(
    service.mutate('ui-theme', [
      { op: 'set', path: ['__proto__', 'x'], value: true },
    ]),
    denied,
  );
  await assert.rejects(
    service.mutate('locale', [
      { op: 'set', path: [], value: { preference: 'zh', network: {} } },
    ]),
    denied,
  );
  for (const value of [
    { apiKeyEnv: 'AURIGA_INSTANCE_TOKEN' },
    { models: [{ apiKeyEnv: 'auriga_SECRET' }] },
  ]) {
    await assert.rejects(
      service.update('llm-pi-ai', { providers: { personal: value } }),
      denied,
    );
  }
  await assert.rejects(
    service.mutate('llm-pi-ai', [
      {
        op: 'set',
        path: ['providers', 'personal', 'apiKeyEnv'],
        value: 'AURIGA_TOKEN',
      },
    ]),
    denied,
  );
  assert.equal(calls.length, 0);
});

test('R9 guards restore public methods and fail on an incomplete host seam', async () => {
  const { service, cleanup, calls } = fixture();
  cleanup();
  await service.update('auriga-dsh', {});
  assert.equal(calls.length, 1);
  assert.throws(() => guardManagedSettings({}), /settings policy unavailable/);
});

test('R9 platform credential refs reject set/unset, personal keys remain writable', async () => {
  const calls = [];
  const service = {
    set: async (...args) => calls.push(args),
    unset: async (...args) => calls.push(args),
  };
  const cleanup = guardManagedCredentials(service);
  await service.set('PERSONAL_API_KEY', 'test-only');
  await service.unset('PERSONAL_API_KEY');
  for (const ref of [
    'AURIGA_INSTANCE_TOKEN',
    'auriga_device',
    'AURIGA_MODEL_CREDENTIAL',
  ]) {
    await assert.rejects(service.set(ref, 'test-only'), denied);
    await assert.rejects(service.unset(ref), denied);
  }
  assert.equal(calls.length, 2);
  cleanup();
});
