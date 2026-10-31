import assert from 'node:assert/strict';
import { test } from 'node:test';
import { classifyModelExposure } from './model-exposure.mjs';

const secret = 'amc.' + crypto.randomUUID();
const view = (
  ns = 'llm-pi-ai',
  section = 'value',
  provider = 'auriga-managed',
  field = 'Authorization',
) => ({
  namespaces: [
    {
      ns,
      [section]: {
        providers: { [provider]: { headers: { [field]: secret } } },
      },
    },
  ],
});
test('only accepted describe value becomes a warning, regardless of namespace index', () => {
  const data = view();
  data.namespaces.unshift({ ns: 'other', value: {} });
  const result = classifyModelExposure(data, 'describe');
  assert.equal(result.warnings.length, 1);
  assert.equal(result.failures.length, 0);
  assert.ok(!JSON.stringify(result).includes(secret));
});
for (const [name, data, surface] of [
  ['other namespace', view('other'), 'describe'],
  ['base', view('llm-pi-ai', 'base'), 'describe'],
  ['user', view('llm-pi-ai', 'user'), 'describe'],
  ['other provider', view('llm-pi-ai', 'value', 'other'), 'describe'],
  [
    'other header',
    view('llm-pi-ai', 'value', 'auriga-managed', 'X-Key'),
    'describe',
  ],
  ['mutate response', view(), 'mutate'],
  ['unrelated location', { error: secret }, 'describe'],
  ['object key', { [secret]: 'value' }, 'describe'],
  [
    'nested header',
    view('llm-pi-ai', 'value', 'auriga-managed', 'Authorization'),
    'profile',
  ],
])
  test(name + ' remains a failure', () => {
    const result = classifyModelExposure(data, surface);
    assert.equal(result.warnings.length, 0);
    assert.equal(result.failures.length, 1);
    assert.ok(!JSON.stringify(result).includes(secret));
  });
test('accepted field cannot hide a non-string payload or another leak', () => {
  const data = view();
  data.namespaces[0].value.providers['auriga-managed'].headers.Authorization = {
    nested: secret,
  };
  assert.equal(classifyModelExposure(data, 'describe').failures.length, 1);
  const mixed = view();
  mixed.error = secret;
  const result = classifyModelExposure(mixed, 'describe');
  assert.equal(result.warnings.length, 1);
  assert.equal(result.failures.length, 1);
});
