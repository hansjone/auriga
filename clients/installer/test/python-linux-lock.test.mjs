import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  PYTHON_LOCK_LINUX,
  pythonLockForTarget,
  validatePythonLock,
} from '../scripts/python-runtime.mjs';

const here = dirname(fileURLToPath(import.meta.url));

test('linux python lock validates and matches linux-x64 target mapping', () => {
  assert.equal(pythonLockForTarget('linux-x64'), PYTHON_LOCK_LINUX);
  const lock = validatePythonLock(
    JSON.parse(readFileSync(PYTHON_LOCK_LINUX, 'utf8')),
  );
  assert.equal(lock.target, 'linux-x64-cp312');
  assert.match(lock.archive.filename, /linux-gnu-install_only\.tar\.gz$/);
  assert.equal(lock.wheels.length, 13);
  assert.ok(lock.wheels.some((wheel) => wheel.filename.includes('manylinux')));
  // Pure-python wheels stay shared with the Windows pin.
  assert.ok(
    lock.wheels.some((wheel) => wheel.filename.endsWith('py3-none-any.whl')),
  );
  assert.doesNotMatch(
    readFileSync(join(here, '../locks/python-linux-x64.json'), 'utf8'),
    /win_amd64/,
  );
});

test('windows python lock still validates after multi-target change', () => {
  const lock = validatePythonLock(
    JSON.parse(readFileSync(pythonLockForTarget('win-x64'), 'utf8')),
  );
  assert.equal(lock.target, 'win-x64-cp312');
});
