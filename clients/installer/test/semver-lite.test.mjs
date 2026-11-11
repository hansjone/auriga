import test from 'node:test';
import assert from 'node:assert/strict';
import { compare, satisfiesMin } from '../scripts/lib/semver-lite.mjs';

test('prerelease ordering follows semver', () => {
  assert.equal(compare('0.2.1-rc.3', '0.2.1-rc.10'), -1);
  assert.equal(compare('0.2.1-rc.3', '0.2.1'), -1);
  assert.equal(compare('0.2.1-alpha.5', '0.2.1-rc.1'), -1);
  assert.equal(compare('0.2.2-alpha.2', '0.2.1-rc.5'), 1);
  assert.equal(compare('1.0.0', '1.0.0+build'), 0);
});

test('engines.dsh minimum gate', () => {
  assert.equal(satisfiesMin('0.2.1-rc.3', '>=0.2.1-rc.3'), true);
  assert.equal(satisfiesMin('0.2.1-rc.4', '>=0.2.1-rc.3'), true);
  assert.equal(satisfiesMin('0.2.0-rc.2', '>=0.2.1-rc.3'), false);
  assert.equal(satisfiesMin('0.2.2-rc.1', '>=0.2.1-rc.3'), true);
});

test('unsupported ranges and invalid versions fail closed', () => {
  assert.throws(() => satisfiesMin('0.2.1', '^0.2.1'), /unsupported/);
  assert.throws(() => satisfiesMin('0.2', '>=0.2.1'), /invalid semver/);
});
