// Cross-language contract check. All keys and signatures in the input are TEST ONLY.
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import Ajv2020 from '../../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../../services/contracts/node_modules/ajv-formats/dist/index.js';
import {
  parseStrict,
  checkDownstream,
  publicKeyFromSpki,
  checkKeyset,
} from '../../test/network-lib.mjs';

const read = (file) =>
  JSON.parse(readFileSync(new URL(file, import.meta.url), 'utf8'));
const limits = read('../limits.json');
const errors = read('../errors.json').errors;
const boundaries = read('raw-boundaries.json');
for (const c of boundaries.cases) {
  let raw;
  if (c.kind === 'valueTree') {
    const names =
      'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_';
    raw = JSON.stringify(
      Array.from({ length: c.branches }, (_, i) =>
        Object.fromEntries(
          Array.from(
            { length: i === c.branches - 1 ? c.lastMembers : c.members },
            (_, n) => [names[n], {}],
          ),
        ),
      ),
    );
  } else if (c.kind.startsWith('nested')) {
    raw =
      '['.repeat(c.containers) +
      (c.kind === 'nestedScalar' ? '0' : '') +
      ']'.repeat(c.containers);
  } else {
    const text = c.unit.repeat(c.repeat) + (c.suffix ?? '');
    raw =
      c.kind === 'key'
        ? `{${JSON.stringify(text)}:0}`
        : JSON.stringify({ value: text });
  }
  if (c.accept)
    assert.doesNotThrow(() => parseStrict(raw, limits.encoding), c.name);
  else
    assert.throws(
      () => parseStrict(raw, limits.encoding),
      (e) => e.code === 'network.protocol_error',
      c.name,
    );
}

const samples = read('java-signed-samples.json');
assert.equal(samples.test_only, true);
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const file of readdirSync(new URL('../', import.meta.url)).filter((f) =>
  f.endsWith('.schema.json'),
)) {
  ajv.addSchema(read('../' + file));
}
const keysetSchema = ajv.getSchema(
  'https://auriga.example.com/contracts/network/keyset.schema.json',
);
assert.ok(keysetSchema(samples.keyset), JSON.stringify(keysetSchema.errors));
checkKeyset(samples.keyset, keysetSchema);
const keys = Object.fromEntries(
  samples.keyset.keys.map((k) => [
    k.kid,
    { purpose: k.purpose, key: publicKeyFromSpki(k.public_key_spki) },
  ]),
);
const definitions = {
  delivery: 'envelope#Delivery',
  head: 'protocol#Head',
  error: 'protocol#Error',
  register_result: 'protocol#RegisterResult',
  fetch_result: 'protocol#FetchResult',
};
const validate = (frame) => {
  const [file, definition] = definitions[frame.type].split('#');
  return ajv.getSchema(
    `https://auriga.example.com/contracts/network/${file}.schema.json#/$defs/${definition}`,
  )(frame);
};
let count = 0;
for (const [name, frame] of Object.entries(samples.frames)) {
  const ctx = {
    ...samples.context,
    keys,
    errors,
    validate,
    limits: { ...limits.encoding, ...limits.serverAuth },
    requestId: frame.page?.request_id ?? frame.server_auth?.request_id,
  };
  const encoded = JSON.stringify(frame);
  assert.ok(Buffer.byteLength(encoded, 'utf8') <= limits.fetch.maxBytes);
  parseStrict(encoded, limits.encoding);
  assert.equal(checkDownstream(frame, ctx), null, name);
  const tampered = structuredClone(frame);
  if (frame.type === 'fetch_result') tampered.items.pop();
  else tampered.app_version = '1.1';
  assert.equal(
    checkDownstream(tampered, ctx),
    'network.signature_invalid',
    name + ' tamper',
  );
  count++;
}
console.log(
  `Node verified ${count} Java frames, tamper negatives, keyset schema and ${boundaries.cases.length} shared parsing boundaries`,
);
