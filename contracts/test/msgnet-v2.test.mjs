import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPublicKey, verify, X509Certificate } from 'node:crypto';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import formats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { evaluate } from '../network/fixtures/msgnet-v2-oracle.mjs';
import {
  evaluateIssuer,
  evaluateBoundary,
} from '../network/fixtures/msgnet-v2-boundary-oracle.mjs';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';
const fixture = JSON.parse(
  readFileSync(
    new URL(
      '../network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
  ),
);
const schema = JSON.parse(
  readFileSync(
    new URL('../network/auto-renewal-v2.schema.json', import.meta.url),
  ),
);
const ajv = new Ajv({ strict: true });
formats(ajv);
ajv.addSchema(schema);
const vectorSchema = JSON.parse(
  readFileSync(
    new URL('../network/msgnet-v2-vectors.schema.json', import.meta.url),
  ),
);
ajv.addSchema(vectorSchema);
test('all shared input groups conform to vector schema', () => {
  const validate = ajv.getSchema(vectorSchema.$id);
  assert.ok(validate(fixture), JSON.stringify(validate.errors));
});
test('OpenAPI permissions and status errors bind to catalog', () => {
  const catalog = JSON.parse(
    readFileSync(new URL('../catalog/msgnet-v2.json', import.meta.url)),
  );
  const api = YAML.parse(
    readFileSync(
      new URL('../openapi/network-keys-v2.yaml', import.meta.url),
      'utf8',
    ),
  );
  for (const path of Object.values(api.paths))
    for (const operation of Object.values(path))
      if (operation['x-permission'])
        assert.ok(catalog.permissions.includes(operation['x-permission']));
  assert.deepEqual(
    new Set(
      schema.$defs.RenewalStatus.properties.failure_code.enum.filter(
        (x) => x !== 'none',
      ),
    ),
    new Set(catalog.errors.map((x) => x.code)),
  );
  for (const v of [
    ...fixture.cases,
    ...fixture.issuer_cases,
    ...fixture.boundary_cases,
  ])
    if (v.expected !== 'accept')
      assert.ok(
        catalog.errors.some((x) => x.code === 'collab.msgnet_' + v.expected),
        v.id,
      );
});
for (const v of fixture.cases)
  test(v.id, () => assert.equal(evaluate(v), v.expected));
for (const v of fixture.issuer_cases)
  test(v.id, () => assert.equal(evaluateIssuer(v), v.expected));
for (const v of fixture.boundary_cases)
  test(v.id, () => assert.equal(evaluateBoundary(v), v.expected));
for (const v of fixture.signatures)
  test('signature ' + v.id, () =>
    assert.equal(
      verify(
        null,
        Buffer.from(v.domain + '\0' + v.canonical),
        createPublicKey({
          key: Buffer.from(v.public_key_spki, 'base64url'),
          format: 'der',
          type: 'spki',
        }),
        Buffer.from(v.signature, 'base64url'),
      ),
      v.valid,
    ),
  );
test('valid publication conforms to strict schema', () => {
  const validate = ajv.getSchema(schema.$id);
  assert.ok(
    validate(fixture.cases[0].input.publication),
    JSON.stringify(validate.errors),
  );
});
for (const field of ['not_after', 'delegate'])
  test('authority rejects ' + field, () => {
    const p = structuredClone(
      fixture.cases[0].input.publication.authority.payload,
    );
    p[field] = 'unexpected';
    assert.equal(
      ajv.getSchema(schema.$id + '#/$defs/RuntimeAuthority')(p),
      false,
    );
  });
test('old/new internal CA leaf chains and foreign CA refusal', () => {
  const chains = fixture.certificates.map((x) => ({
    ca: new X509Certificate(Buffer.from(x.ca_der, 'base64url')),
    leaf: new X509Certificate(Buffer.from(x.leaf_der, 'base64url')),
  }));
  for (const chain of chains) {
    assert.ok(chain.leaf.verify(chain.ca.publicKey));
    assert.equal(
      chain.leaf.checkHost('submit.example.com'),
      'submit.example.com',
    );
    assert.equal(chain.leaf.checkHost('foreign.example.com'), undefined);
  }
  assert.equal(chains[0].leaf.verify(chains[1].ca.publicKey), false);
});
