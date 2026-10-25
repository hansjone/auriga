import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const schema = JSON.parse(readFileSync(new URL('../plugin/team-transport.schema.json', import.meta.url), 'utf8').replace(/^\uFEFF/, ''));
const ajv = new Ajv2020({ strict: true });
addFormats(ajv);
ajv.addSchema(schema, 'transport');
const id = '0192a1b2-0000-7000-8000-000000000001';
test('trusted Node operations expose no actor or tenant override', () => {
  const bodies = { Register: { label: 'node', receiveEnabled: true }, Bind: { endpointId: id, hostSessionId: 'real-session' }, Pull: { endpointId: id, limit: 100 }, Ack: { endpointId: id, bindingId: id, hostSessionId: 'real-session', deliveryId: id, leaseToken: id, hostRequestId: id }, Revoke: { endpointId: id } };
  for (const [name, body] of Object.entries(bodies)) {
    const validate = ajv.getSchema(`transport#/$defs/${name}`);
    assert.equal(validate(body), true, name);
    for (const field of ['actor', 'tenantId', 'accountId', 'deviceId', '_meta']) assert.equal(validate({ ...body, [field]: id }), false, `${name}:${field}`);
  }
});
test('four public model tools and five private routes remain distinct', () => {
  const transport = schema.$defs.Transport.const;
  assert.equal(Object.keys(transport.tools).length, 4);
  assert.equal(Object.keys(transport.nodeRoutes).length, 5);
  assert.equal(transport.protocolVersion, '2025-06-18');
  for (const name of Object.keys(transport.tools)) assert.doesNotMatch(name, /ack|pull|register|revoke|bind/);
});
