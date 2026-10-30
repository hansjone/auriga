import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import {
  checkKeyset,
  checkSignedKeyset,
  jcs,
  publicKeyFromSpki,
} from './network-lib.mjs';

const dir = new URL('../network/', import.meta.url);
const read = (name) => JSON.parse(readFileSync(new URL(name, dir), 'utf8'));
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const file of readdirSync(dir).filter((f) => f.endsWith('.schema.json')))
  ajv.addSchema(read(file));
const base = 'https://auriga.example.com/contracts/network/keyset.schema.json';
const payloadSchema = ajv.getSchema(base);
const signedSchema = ajv.getSchema(base + '#/$defs/SignedKeyset');
const payload = read('fixtures/java-signed-samples.json').keyset;
const protocolError = (e) => e.code === 'network.protocol_error';
const signatureError = (e) => e.code === 'network.signature_invalid';
const input = (keyset) =>
  Buffer.from('Auriga.NetworkKeyset.v1\0' + jcs(keyset));
const signedWith = (keyset, privateKey) => ({
  keyset,
  root_auth: {
    alg: 'Ed25519',
    kid: 'test-root',
    signature: sign(null, input(keyset), privateKey).toString('base64url'),
  },
});

test('keyset accepts the Java sample and exact 24 hour boundary', () => {
  assert.doesNotThrow(() => checkKeyset(payload, payloadSchema));
  const boundary = structuredClone(payload);
  boundary.valid_until = new Date(Date.parse(boundary.issued_at) + 86400000)
    .toISOString()
    .replace('.000', '');
  assert.doesNotThrow(() => checkKeyset(boundary, payloadSchema));
});

for (const c of read('fixtures/keyset-cases.json').cases)
  test('keyset rejects ' + c.name, () => {
    const value = structuredClone(payload);
    if (c.duplicateKid) {
      const duplicate = structuredClone(value.keys[0]);
      duplicate.state = 'next';
      value.keys.push(duplicate);
    } else if (c.key) Object.assign(value.keys[0], c.key);
    else Object.assign(value, c.payload);
    // These cross-field constraints cannot be expressed by standard JSON Schema.
    assert.equal(
      payloadSchema(value),
      true,
      JSON.stringify(payloadSchema.errors),
    );
    assert.throws(() => checkKeyset(value, payloadSchema), protocolError);
  });

test('keyset rejects malformed schema fields before semantic checks', () => {
  assert.throws(
    () => checkKeyset({ ...payload, issued_at: 'invalid' }, payloadSchema),
    protocolError,
  );
});

test('root signatures use fresh keys and reject tampering, wrong roots and wrong domains', () => {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const signed = signedWith(structuredClone(payload), privateKey);
  const ctx = {
    validate: signedSchema,
    roots: new Map([['test-root', publicKey]]),
  };
  assert.doesNotThrow(() => checkSignedKeyset(signed, ctx));
  const tampered = structuredClone(signed);
  tampered.keyset.keyset_version = '999';
  assert.throws(() => checkSignedKeyset(tampered, ctx), signatureError);
  assert.throws(
    () => checkSignedKeyset(signed, { ...ctx, roots: new Map() }),
    signatureError,
  );
  assert.throws(
    () =>
      checkSignedKeyset(signed, {
        ...ctx,
        roots: new Map([
          ['test-root', generateKeyPairSync('ed25519').publicKey],
        ]),
      }),
    signatureError,
  );
  const noncanonical = structuredClone(signed);
  const alphabet =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  const signature = noncanonical.root_auth.signature;
  noncanonical.root_auth.signature =
    signature.slice(0, -1) + alphabet[alphabet.indexOf(signature.at(-1)) + 1];
  assert.deepEqual(
    Buffer.from(noncanonical.root_auth.signature, 'base64url'),
    Buffer.from(signature, 'base64url'),
  );
  assert.throws(() => checkSignedKeyset(noncanonical, ctx), protocolError);
  const malformed = structuredClone(signed);
  malformed.root_auth.alg = 'unknown';
  assert.throws(() => checkSignedKeyset(malformed, ctx), protocolError);
  for (const bytes of [
    Buffer.from(jcs(signed.keyset)),
    Buffer.from('Auriga.ServerEnvelope.v1\0' + jcs(signed.keyset)),
  ]) {
    const wrong = structuredClone(signed);
    wrong.root_auth.signature = sign(null, bytes, privateKey).toString(
      'base64url',
    );
    assert.throws(() => checkSignedKeyset(wrong, ctx), signatureError);
  }
  const invalid = structuredClone(payload);
  invalid.keys.push(structuredClone(invalid.keys[0]));
  assert.throws(
    () => checkSignedKeyset(signedWith(invalid, privateKey), ctx),
    protocolError,
  );
  // Explicit regeneration stores only public material; ordinary tests never rewrite fixtures.
  if (process.env.UPDATE_NETWORK_KEYSET_VECTORS === '1') {
    writeFileSync(
      new URL('fixtures/keyset-signature-vector.json', dir),
      JSON.stringify(
        {
          test_only: true,
          public_key_spki: publicKey
            .export({ type: 'spki', format: 'der' })
            .toString('base64url'),
          canonical: jcs(signed.keyset),
          signing_input_hex: input(signed.keyset).toString('hex'),
          signed,
        },
        null,
        2,
      ) + '\n',
    );
  }
});

test('stored root signature vector verifies and fixes canonical input bytes', () => {
  const vector = read('fixtures/keyset-signature-vector.json');
  assert.equal(vector.test_only, true);
  assert.equal(jcs(vector.signed.keyset), vector.canonical);
  assert.equal(
    input(vector.signed.keyset).toString('hex'),
    vector.signing_input_hex,
  );
  assert.doesNotThrow(() =>
    checkSignedKeyset(vector.signed, {
      validate: signedSchema,
      roots: new Map([
        ['test-root', publicKeyFromSpki(vector.public_key_spki)],
      ]),
    }),
  );
});
