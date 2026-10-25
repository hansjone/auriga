import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  parseAddress,
  parseAddressText,
  formatAddress,
} from '../../../src/network/host/address.js';

const cases = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/address-cases.json',
      import.meta.url,
    ),
    'utf8',
  ),
);

for (const c of cases.text) {
  test(`contract text: ${c.input}`, () => {
    if (c.error)
      assert.throws(() => parseAddressText(c.input, c.realm), {
        code: c.error,
      });
    else assert.deepEqual(parseAddressText(c.input, c.realm), c.expect);
  });
}
for (const [a, b] of cases.equivalent) {
  test(`contract equivalent: ${a}`, () =>
    assert.deepEqual(parseAddressText(a, '2'), parseAddressText(b, '2')));
}
for (const c of cases.format) {
  test(`contract format: ${c.full}`, () => {
    assert.equal(formatAddress(c.address), c.full);
    assert.equal(formatAddress(c.address, '2'), c.short);
    assert.equal(formatAddress(c.address, '3'), c.full);
  });
}
for (const c of cases.json) {
  test(`contract JSON: ${c.name}`, () => {
    const run = () =>
      parseAddress(c.input, { realm: c.realm ?? '2', role: c.role });
    if (c.error) assert.throws(run, { code: c.error });
    else assert.deepEqual(run(), c.input);
  });
}
