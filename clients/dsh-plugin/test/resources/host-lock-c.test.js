import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  rmdirSync,
  existsSync,
  realpathSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { acquireHostLock, durableJson } from '../../src/resources/host-lock.js';
test('C only acquires a new directory and leaves incomplete, contended and expired locks for E', (t) => {
  const home = realpathSync.native(
    mkdtempSync(join(tmpdir(), 'auriga-clock-c-')),
  );
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
  assert.throws(() => acquireHostLock(home, 'C', time), {
    code: 'EEXIST',
    hostLockOccupied: true,
  });
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

test(
  'Windows durably creates and replaces JSON with long Unicode paths',
  { skip: process.platform !== 'win32' },
  (t) => {
    const home = realpathSync.native(
      mkdtempSync(join(tmpdir(), 'auriga-long-commit-')),
    );
    t.after(() => rmSync(home, { recursive: true, force: true }));
    const parent = join(home, '中文 data', 'x'.repeat(100));
    mkdirSync(parent, { recursive: true });
    for (const length of [245, 300]) {
      const file = join(
        parent,
        '测' + 'a'.repeat(length - parent.length - 7) + '.json',
      );
      assert.equal(file.length, length);
      durableJson(file, { revision: 1 });
      durableJson(file, { revision: 2 });
      assert.deepEqual(JSON.parse(readFileSync(file, 'utf8')), { revision: 2 });
    }
  },
);
