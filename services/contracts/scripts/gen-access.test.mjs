import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { accessProjection, embeddedSchema } from './gen-access.mjs';

const source = readFileSync(new URL('../../../contracts/deployment/access.schema.json', import.meta.url), 'utf8');
const schema = JSON.parse(source);

test('access projection derives model fields and tagged branches without changing the schema', () => {
  const before = JSON.stringify(schema);
  const spec = accessProjection(schema);
  assert.equal(JSON.stringify(schema), before);
  assert.deepEqual(accessProjection(schema), spec);
  const models = spec.components.schemas;
  assert.deepEqual(Object.keys(models.Access.properties), Object.keys(schema.properties));
  assert.deepEqual(models.Access.required, schema.required);
  assert.deepEqual(models.Access.properties.schemaVersion.enum, [1]);
  assert.equal(models.Access.properties.schemaVersion.type, 'integer');
  for (const [field, tag] of [['ingress', 'kind'], ['instances', 'mode']]) {
    const union = models['Access' + field[0].toUpperCase() + field.slice(1)];
    assert.equal(union.oneOf.length, schema.properties[field].oneOf.length);
    for (const branch of schema.properties[field].oneOf) {
      const ref = union.discriminator.mapping[branch.properties[tag].const];
      const model = models[ref.split('/').at(-1)];
      assert.deepEqual(Object.keys(model.properties), Object.keys(branch.properties));
      assert.deepEqual(model.required, branch.required);
      assert.deepEqual(model.properties[tag].enum, [branch.properties[tag].const]);
    }
  }
  assert.equal(models.AccessInstanceControl.properties.origin.$ref, '#/components/schemas/Origin');
  // Conditional, not, and narrowed allOf constraints stay in the complete raw
  // schema consumed by Go; they are not claimed as model validation.
  assert.equal(models.Access.allOf, undefined);
  assert(schema.allOf && schema.$defs.DnsName.not);
});

test('embedded schema is reproducible, complete, and preserves backticks safely', () => {
  assert(embeddedSchema(source).includes(source.replaceAll('\r\n', '\n')));
  assert.equal(embeddedSchema(source), embeddedSchema(source.replaceAll('\r\n', '\n')));
  assert(embeddedSchema('{"description":"`"}').includes('` + "`" + `'));
});
