import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { persistenceMarker, receiptMarker } from './plugin-send.mjs';
import { assertVerdict } from './assembly-verdict.mjs';

export const sources = {
  SkipSubmissionCommit: 'clients/dsh-plugin/src/network/durable/outbox.js',
  LocalReceiptVersion: 'clients/dsh-plugin/src/network/durable/inbox.js',
};
export function mutate(source, mode) {
  const anchor =
    mode === 'SkipSubmissionCommit'
      ? "await ledger.update((draft) => {\n          if (!draft.durable) throw new Error('producer epoch unavailable');\n          draft.durable.submissions[key] = saved;\n        }, current);"
      : 'expected_state_version: r.stateVersion,';
  assert.ok(Object.hasOwn(sources, mode), 'unknown mutation');
  assert.equal(
    source.split(anchor).length,
    2,
    'one exact production mutation required',
  );
  return source.replace(
    anchor,
    mode === 'SkipSubmissionCommit'
      ? '// Deliberately omit the durable submission commit.'
      : 'expected_state_version: String(ledger.peek().revision),',
  );
}
export function verdict(manifest, mode, exit) {
  if (mode === 'Control') {
    assertVerdict(manifest, mode, exit);
    assert.equal(
      manifest.checks.filter((v) => v.startsWith('SEND-2:')).length,
      2,
    );
    assert.equal(
      manifest.checks.filter((v) => v.startsWith('SEND-3:')).length,
      2,
    );
    return;
  }
  assert.ok(Object.hasOwn(sources, mode));
  const marker =
    mode === 'SkipSubmissionCommit' ? persistenceMarker : receiptMarker;
  assert.notEqual(exit, 0);
  assert.equal(manifest.completed, false);
  assert.equal(manifest.businessFailure?.name, 'AssertionError');
  assert.equal(manifest.businessFailure?.code, 'ERR_ASSERTION');
  assert.ok(manifest.businessFailure.message.includes(marker));
  for (const stage of [
    'ready',
    'gateway-ready',
    'gateway-closed',
    'java-closed',
  ])
    assert.ok(manifest.stages.some((v) => v.stage === stage));
  for (const stage of ['gateway-closed', 'java-closed']) {
    const closed = manifest.stages.find((v) => v.stage === stage);
    assert.equal(closed.exitCode, 0);
    assert.equal(closed.signal, null);
  }
  assert.ok(
    manifest.businessFailure.completedChecks.some((v) =>
      v.startsWith('MEMBER'),
    ),
  );
  assert.ok(manifest.failures.length > 0);
  for (const failure of manifest.failures)
    assert.ok(
      failure.message.includes(marker) ||
        failure.message === 'owned child exited 1/null',
      'setup/SQL/cleanup failure is not business counterevidence',
    );
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [op, file, mode, exit] = process.argv.slice(2);
  if (op === '--mutate')
    await writeFile(file, mutate(await readFile(file, 'utf8'), mode));
  else if (op === '--verdict')
    verdict(JSON.parse(await readFile(file, 'utf8')), mode, Number(exit));
  else throw new Error('expected --mutate or --verdict');
}
