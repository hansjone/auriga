import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { generateKeyPairSync } from 'node:crypto';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const read = (name) =>
  JSON.parse(readFileSync(new URL('../' + name, import.meta.url), 'utf8'));
const schema = read('plugin/local-login.schema.json'),
  ajv = new Ajv2020({ strict: true, allowUnionTypes: true, allErrors: true });
addFormats(ajv);
ajv.addSchema(
  read('network/address.schema.json'),
  new URL('../network/address.schema.json', import.meta.url).href,
);
ajv.addSchema(
  read('identity/steps.schema.json'),
  new URL('../identity/steps.schema.json', import.meta.url).href,
);
const localUri = new URL('../plugin/local-login.schema.json', import.meta.url)
  .href;
ajv.addSchema(schema, localUri);
const validate = (name) => ajv.compile({ $ref: localUri + '#/$defs/' + name });
test('local input permits only host-owned begin/resume and existing form StepInput', () => {
  const input = validate('LoginInput');
  for (const value of [
    { action: 'begin' },
    { action: 'resume' },
    {
      action: 'advance',
      input: { type: 'form', formId: 'totp', values: { code: '123456' } },
    },
  ])
    assert.equal(input(value), true, JSON.stringify(input.errors));
  for (const value of [
    { action: 'complete' },
    { action: 'begin', deviceCode: 'invented' },
    { action: 'resume', accountId: 'alice' },
    { action: 'advance', input: { type: 'callback', params: {} } },
    {
      action: 'advance',
      input: {
        type: 'form',
        formId: 'totp',
        values: { code: '123456' },
        _meta: {},
      },
    },
  ])
    assert.equal(input(value), false);
});
test('installation configuration cannot carry credentials or arbitrary ignored safety fields', () => {
  const config = validate('ConnectConfig'),
    value = {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
    };
  assert.equal(config(value), true);
  for (const extra of [
    { accessToken: 'private' },
    { skipAuth: true },
    { clientSecret: 'private' },
    { deviceName: 'x'.repeat(65) },
  ])
    assert.equal(config({ ...value, ...extra }), false);
});

test('network installation shape is complete, bounded and separate from local login requests', () => {
  const config = validate('ConnectConfig');
  const value = {
    platformOrigin: 'https://192.0.2.1:8443',
    issuer: 'https://192.0.2.1:8443',
    clientId: 'auriga-dsh',
    providerId: 'team',
  };
  const root = {
    kid: 'root-1',
    public_key_spki: generateKeyPairSync('ed25519')
      .publicKey.export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  };
  const network = {
    realm: '18446744073709551615',
    wssUrl: 'wss://network.example.com:9443/custom/ws',
    roots: [root],
  };
  assert.equal(
    config({ ...value, network }),
    true,
    JSON.stringify(config.errors),
  );
  for (const invalid of [
    null,
    {},
    { ...network, realm: '0' },
    { ...network, realm: '01' },
    { ...network, realm: 1 },
    { ...network, realm: '18446744073709551616' },
    { ...network, roots: [] },
    { ...network, roots: Array(17).fill(root) },
    { ...network, roots: [{ ...root, private_key: 'secret' }] },
    {
      ...network,
      roots: [{ ...root, public_key_spki: root.public_key_spki + '=' }],
    },
    { ...network, wssUrl: 'ws://localhost/ws' },
    { ...network, wssUrl: 'wss://alice@network.example.com/ws' },
    { ...network, wssUrl: 'wss://network.example.com/ws?' },
    { ...network, wssUrl: 'wss://network.example.com/ws#' },
  ])
    assert.equal(
      config({ ...value, network: invalid }),
      false,
      JSON.stringify(invalid),
    );
  for (const key of [
    'rootUrl',
    'TOFU',
    'skipVerify',
    'initialize',
    'created',
    'privateKey',
    'fingerprint',
  ])
    assert.equal(
      config({ ...value, network: { ...network, [key]: true } }),
      false,
    );
  for (const patch of [
    { realm: '1\n' },
    { wssUrl: network.wssUrl + '\n' },
    { roots: [{ ...root, kid: 'root\n' }] },
  ])
    assert.equal(
      config({ ...value, network: { ...network, ...patch } }),
      false,
    );
  for (const key of ['realm', 'wssUrl', 'roots']) {
    const missing = { ...network };
    delete missing[key];
    assert.equal(config({ ...value, network: missing }), false);
  }
  assert.equal(validate('LoginInput')({ action: 'begin', network }), false);
  assert.equal(validate('LoginInput')({ action: 'resume', network }), false);
});

test('local connection status exposes bounded counters without source or delivery identities', () => {
  const status = validate('Status');
  const value = {
    available: true,
    loggedIn: true,
    connected: true,
    account: { subject: 'alice' },
    connection: {
      endpointId: null,
      received: 0,
      unsupported: 1,
      pendingSourceOpens: 1,
      unknownSourceOpens: 1,
    },
  };
  assert.equal(status(value), true, JSON.stringify(status.errors));
  assert.equal(
    status({
      ...value,
      connection: { ...value.connection, unknownSourceOpens: -1 },
    }),
    false,
  );
  assert.equal(
    status({ ...value, connection: { ...value.connection, unsupported: -1 } }),
    false,
  );
  assert.equal(
    status({
      ...value,
      connection: { ...value.connection, pendingSourceOpens: -1 },
    }),
    false,
  );
  assert.equal(
    status({
      ...value,
      connection: { ...value.connection, sessionId: 'private-source' },
    }),
    false,
  );
});
