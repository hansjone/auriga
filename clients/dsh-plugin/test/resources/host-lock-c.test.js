import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  rmdirSync,
  existsSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { acquireHostLock } from '../../src/resources/host-lock.js';
test('C only acquires a new directory and leaves incomplete, contended and expired locks for E', (t) => {
  const home = mkdtempSync(join(tmpdir(), 'auriga-clock-c-'));
  t.after(() => rmSync(home, { recursive: true, force: true }));
  let mono = 1000;
  const time = () => ({
    bootId: '2026-11-12T00:00:00.000Z',
    monotonicMillis: mono,
  });
  const lock = acquireHostLock(home, 'C', time),
    path = join(home, 'auriga/guardian/host.lock.d');
  const owner = JSON.parse(readFileSync(join(path, 'owner.json'), 'utf8'));
  assert.equal(owner.role, 'C');
  assert.equal(owner.bootId, time().bootId);
  assert.equal(owner.monotonicMillis, mono);
  assert.throws(() => acquireHostLock(home, 'C', time), { code: 'EEXIST' });
  assert.equal(
    JSON.parse(readFileSync(join(path, 'owner.json'), 'utf8')).lockId,
    owner.lockId,
  );
  lock.release();
  mkdirSync(path);
  assert.throws(() => acquireHostLock(home, 'C', time), { code: 'EEXIST' });
  assert.ok(existsSync(path));
  rmdirSync(path);
  const expired = acquireHostLock(home, 'C', time);
  mono = 31001;
  assert.throws(() => expired.check(), /expired/);
  assert.throws(() => expired.release(), /expired/);
  assert.ok(existsSync(path));
});
