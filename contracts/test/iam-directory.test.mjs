import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
const validate = ajv.compile(
  JSON.parse(
    readFileSync(
      new URL('../network/system-directory.schema.json', import.meta.url),
      'utf8',
    ),
  ),
);
const entry = () => ({
  systemAddress: 'aur1:ffffffffffffffff.ffffffffffffffff.0',
  displayName: 'DSH',
  ownerUserId: randomUUID(),
  ownerDisplayName: 'Alice',
});

test('directory permits empty/offline pages and exact uint64 system addresses', () => {
  assert.equal(validate({ items: [] }), true);
  assert.equal(validate({ items: [entry()] }), true);
  assert.equal(
    validate({ items: [entry()], nextCursor: 'opaque-position' }),
    true,
  );
});

test('directory cannot publish session addresses, locals or raw identity payloads', () => {
  for (const address of [
    'aur1:1.1.1',
    'aur1:1.1',
    'aur1:1.0.1',
    'aur1:01.1.0',
    'aur1:1.1.00',
    'aur1:FFFFFFFFFFFFFFFF.1.0',
    'aur1:10000000000000000.1.0',
  ]) {
    assert.equal(
      validate({ items: [{ ...entry(), systemAddress: address }] }),
      false,
      address,
    );
  }
  for (const field of [
    'local',
    'sessions',
    'deviceId',
    'account',
    'onlineState',
  ]) {
    assert.equal(
      validate({ items: [{ ...entry(), [field]: 'private' }] }),
      false,
      field,
    );
  }
});

test('directory page, cursor and display fields have hard bounds', () => {
  assert.equal(validate({ items: Array.from({ length: 200 }, entry) }), true);
  assert.equal(validate({ items: Array.from({ length: 201 }, entry) }), false);
  for (const cursor of ['', 'x'.repeat(129)])
    assert.equal(validate({ items: [], nextCursor: cursor }), false);
  assert.equal(
    validate({ items: [{ ...entry(), displayName: 'x'.repeat(65) }] }),
    false,
  );
  assert.equal(
    validate({ items: [{ ...entry(), ownerDisplayName: 'x'.repeat(129) }] }),
    false,
  );
});
