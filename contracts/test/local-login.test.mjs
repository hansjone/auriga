import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const read = (name) =>
  JSON.parse(readFileSync(new URL('../' + name, import.meta.url), 'utf8'));
const schema = read('plugin/local-login.schema.json'),
  ajv = new Ajv2020({ strict: true, allowUnionTypes: true, allErrors: true });
addFormats(ajv);
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
