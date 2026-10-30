import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const schema = JSON.parse(readFileSync(new URL('../plugin/team-mcp.schema.json', import.meta.url)));
const ajv = new Ajv2020({ strict: true });
addFormats(ajv);
ajv.addSchema(schema, 'team');
const validate = name => ajv.getSchema(`team#/$defs/${name}`);
const id = '0192a1b2-0000-7000-8000-000000000001';

test('model tool arguments reject actor/session/meta injection and unknown fields', () => {
  for (const name of ['OpenThread', 'ReplyThread']) {
    const data = { text: 'hello', idempotencyKey: 'persistent-key', [name === 'OpenThread' ? 'targetEndpointId' : 'threadId']: id };
    assert.equal(validate(name)(data), true);
    for (const extra of ['actor', 'tenantId', 'accountId', 'sessionId', '_meta', 'bindingId']) assert.equal(validate(name)({ ...data, [extra]: id }), false);
    for (const text of ['', '   ', 'x'.repeat(16385)]) assert.equal(validate(name)({ ...data, text }), false);
  }
});

test('bounded pages and opaque DSH meta are separate from tool arguments', () => {
  assert.equal(validate('ListEndpoints')({ limit: 100 }), true);
  for (const limit of [0, 101, -1, 1.5]) assert.equal(validate('ListEndpoints')({ limit }), false);
  const meta = { 'auriga/sessionId': 'session-not-a-uuid', 'auriga/toolCallId': 'call-1', 'auriga/rootCallId': 'root-1' };
  assert.equal(validate('SessionMeta')(meta), true);
  assert.equal(validate('SessionMeta')({ ...meta, tenantId: id }), false);
  delete meta['auriga/sessionId'];
  assert.equal(validate('SessionMeta')(meta), false);
});
