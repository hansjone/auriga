import { test } from 'node:test';
import assert from 'node:assert/strict';
import initialize, { selectMode, PROTOCOL_VERSION } from '../src/index.js';
test('defaults to standalone without platform access', () =>
  assert.equal(selectMode({}), 'standalone'));
for (const mode of ['standalone', 'managed', 'connect'])
  test('selects ' + mode, () =>
    assert.equal(initialize({ AURIGA_MODE: mode }).mode, mode),
  );
for (const mode of ['', 'MANAGED', 'unknown'])
  test('rejects invalid mode ' + JSON.stringify(mode), () =>
    assert.throws(
      () => selectMode({ AURIGA_MODE: mode }),
      /Invalid AURIGA_MODE/,
    ),
  );
test('uses protocol v1', () => assert.equal(PROTOCOL_VERSION, 1));
