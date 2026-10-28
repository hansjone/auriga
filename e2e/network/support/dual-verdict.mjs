import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { mutate as originalMutation } from './plugin-send-verdict.mjs';

export const persistenceMarker =
  'SEND-4 original submission must be durable before HTTPS';

export const sourcePath = 'clients/dsh-plugin/src/network/durable/outbox.js';
export const mutate = (source) =>
  originalMutation(source, 'SkipSubmissionCommit');
export function verdict(manifest, mode, exit) {
  assert.match(manifest.head, /^[0-9a-f]{40}$/);
  assert.equal(manifest.host.version, '0.2.1-rc.3');
  const ready = manifest.stages.filter((v) => v.stage === 'dsh-ready');
  assert.equal(ready.length, 2);
  assert.notEqual(ready[0].pid, ready[1].pid);
  assert.notEqual(ready[0].profile, ready[1].profile);
  assert.notEqual(ready[0].dshHome, ready[1].dshHome);
  assert.notEqual(ready[0].cwd, ready[1].cwd);
  for (const name of ['A', 'B']) {
    const closed = manifest.stages.find(
      (v) => v.stage === 'dsh-closed' && v.name === name,
    );
    assert.equal(closed?.exitCode, 0);
    assert.equal(closed.signal, null);
  }
  for (const stage of ['gateway-closed', 'java-closed']) {
    const closed = manifest.stages.find((v) => v.stage === stage);
    assert.equal(closed?.exitCode, 0);
    assert.equal(closed.signal, null);
  }
  assert.equal(
    manifest.checks.filter((v) => v.startsWith('SEND-4: independent')).length,
    1,
  );
  if (mode === 'Control') {
    assert.equal(exit, 0);
    assert.equal(manifest.completed, true);
    assert.deepEqual(manifest.failures, []);
    for (const prefix of [
      'SEND-4: A to B',
      'SEND-4: B to A',
      'SEND-4: explicit receive-off',
    ])
      assert.equal(
        manifest.checks.filter((v) => v.startsWith(prefix)).length,
        1,
      );
  } else {
    assert.equal(mode, 'SkipSubmissionCommit');
    assert.notEqual(exit, 0);
    assert.equal(manifest.completed, false);
    assert.equal(manifest.businessFailure?.name, 'AssertionError');
    assert.equal(manifest.businessFailure?.code, 'ERR_ASSERTION');
    assert.ok(manifest.businessFailure.message.includes(persistenceMarker));
    assert.ok(manifest.failures.length > 0);
    for (const failure of manifest.failures)
      assert.ok(
        failure.message.includes(persistenceMarker),
        'setup/SQL/cleanup errors are not counterevidence',
      );
  }
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [op, file, mode, exit] = process.argv.slice(2);
  if (op === '--mutate')
    await writeFile(file, mutate(await readFile(file, 'utf8')));
  else if (op === '--verdict')
    verdict(JSON.parse(await readFile(file, 'utf8')), mode, Number(exit));
  else throw new Error('expected --mutate or --verdict');
}
