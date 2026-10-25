import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const run = promisify(execFile);
test('committed network tool artifacts are byte-for-byte fresh from the current contracts', async () => {
  const generator = fileURLToPath(
    new URL('../../../scripts/gen-network-tools.mjs', import.meta.url),
  );
  const { stdout, stderr } = await run(
    process.execPath,
    [generator, '--check'],
    { timeout: 30_000 },
  );
  assert.equal(stdout, '');
  assert.equal(stderr, '');
});
