import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { makeExe } from '../scripts/make-exe.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { unpackSetup } from '../runtime/setup-archive.mjs';
import { tempBase, wrapSetupFixture } from './helpers.mjs';

test('embedded payload is read as data and malformed envelopes fail closed', (t) => {
  const base = tempBase(t),
    file = join(base, 'setup.exe');
  writeFileSync(file, 'zip-payload');
  wrapSetupFixture(file);
  const original = readFileSync(file);
  const output = join(base, 'payload.zip');
  unpackSetup(file, output);
  assert.equal(readFileSync(output, 'utf8'), 'zip-payload');
  for (const mutate of [
    (b) => b.subarray(0, 10),
    (b) => {
      b[0] = 0;
      return b;
    },
    (b) => {
      b[b.length - 16] = 0;
      return b;
    },
    (b) => {
      b.writeBigInt64LE(-1n, b.length - 8);
      return b;
    },
    (b) => {
      b.writeBigInt64LE(999999n, b.length - 8);
      return b;
    },
  ]) {
    writeFileSync(file, mutate(Buffer.from(original)));
    assert.throws(
      () => unpackSetup(file, join(base, 'rejected.zip')),
      /invalid setup/,
    );
  }
});

test(
  'Windows setup silently forwards output, arguments and failure exit status',
  { skip: process.platform !== 'win32' },
  (t) => {
    const base = tempBase(t),
      stage = join(base, 'stage');
    mkdirSync(stage);
    writeFileSync(
      join(stage, 'install.ps1'),
      `param([int]$ExitCode = 0)
Write-Output '{"installed":true}'
exit $ExitCode
`,
    );
    const exe = makeExe({ stage, out: join(base, 'setup.exe') });
    const success = spawnSync(exe, ['/VERYSILENT'], {
      encoding: 'utf8',
      timeout: 30000,
    });
    assert.equal(success.status, 0, success.stderr);
    assert.match(success.stdout, /"installed":true/);
    const failure = spawnSync(exe, ['/VERYSILENT', '-ExitCode', '7'], {
      encoding: 'utf8',
      timeout: 30000,
    });
    assert.equal(failure.status, 7, failure.stderr);
  },
);
