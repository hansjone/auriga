import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import { createLocalResources } from '../../clients/dsh-plugin/src/local-resources/service.js';

const schema = JSON.parse(
  readFileSync(
    new URL('../plugin/local-resources.schema.json', import.meta.url),
    'utf8',
  ),
);
const ajv = new Ajv2020({
  strict: true,
  allowUnionTypes: true,
  allErrors: true,
});
ajv.addSchema(schema, 'local');
const validate = (name) => ajv.getSchema(`local#/$defs/${name}`);
test('native inventory and details conform to the exact local resource schema', async () => {
  const service = createLocalResources({
    mode: 'managed',
    getHost: () => ({
      pluginManager: {
        async listPlugins() {
          return [
            {
              entryId: 'test',
              moduleName: 'example-plugin',
              patchId: 'test',
              enabled: true,
              fiberPhase: 'active',
            },
          ];
        },
      },
    }),
  });
  const list = await service.list();
  assert.equal(validate('LocalList')(list), true);
  assert.equal(
    validate('LocalDetail')(await service.detail(list.items[0].id)),
    true,
  );
  assert.equal(list.items[0].readonly, 'managed');
});
test('inputs reject spec/path injection, unrevisioned writes, nested configuration and extra fields', () => {
  const id = 'a'.repeat(64),
    revision = 'b'.repeat(64);
  assert.equal(
    validate('LocalEnableInput')({ id, revision, enabled: false }),
    true,
  );
  for (const value of [
    { id, enabled: false },
    { id: 'https://example.com/plugin.tgz', revision, enabled: true },
    { id, revision, enabled: true, path: '/tmp' },
  ])
    assert.equal(validate('LocalEnableInput')(value), false);
  assert.equal(
    validate('LocalConfigInput')({
      id,
      revision,
      configRevision: 0,
      changes: [{ key: 'label', value: 'example' }],
    }),
    true,
  );
  assert.equal(
    validate('LocalConfigInput')({
      id,
      revision,
      configRevision: 0,
      changes: [{ key: 'label', value: { command: 'example' } }],
    }),
    false,
  );
});
