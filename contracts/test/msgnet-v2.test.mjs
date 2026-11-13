import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPublicKey, verify, X509Certificate } from 'node:crypto';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import formats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { evaluate } from '../network/fixtures/msgnet-v2-oracle.mjs';
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
for (const v of fixture.cases)
  test(v.id, () => assert.equal(evaluate(v), v.expected));
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
