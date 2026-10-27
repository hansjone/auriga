import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const failureMarker =
  'corrupt Get must not persist an active observation';
export function signatureBypass(source) {
  const anchor = 'await verifyEnvelopeWithRefresh(result, {';
  if (source.split(anchor).length !== 2)
    throw new Error('signature overlay must match exactly once');
  return source.replace(
    anchor,
    "if (result.type !== 'source_get_result') await verifyEnvelopeWithRefresh(result, {",
  );
}
export function assertVerdict(manifest, mode, exit) {
  assert.equal(manifest.brokerVersion, '2.15.1');
  assert.match(manifest.head, /^[a-f0-9]{40}$/);
  for (const stage of [
    'ready',
    'gateway-ready',
    'gateway-closed',
    'java-closed',
  ])
    assert.ok(
      manifest.stages.some((v) => v.stage === stage),
      'incomplete real lifecycle',
    );
  if (mode === 'Control') {
    assert.equal(exit, 0);
    assert.equal(manifest.completed, true);
    assert.deepEqual(manifest.failures, []);
    for (const prefix of [
      'P1:',
      'P2/P4/F2:',
      'F6:',
      'MEMBER',
      'P3:',
      'P5/P6:',
      'Broker 2.15.1:',
      'critical: live-C',
      'critical: real deadline',
      'critical: actual JDBC',
      'critical: genuine late',
      'critical: real Org',
    ])
      assert.equal(
        manifest.checks.filter((v) => v.startsWith(prefix)).length,
        1,
        'missing business case',
      );
    return;
  }
  assert.equal(mode, 'SignatureBypass');
  assert.notEqual(exit, 0);
  assert.equal(manifest.completed, false);
  assert.equal(manifest.businessFailure?.name, 'AssertionError');
  assert.equal(manifest.businessFailure?.code, 'ERR_ASSERTION');
  assert.ok(manifest.businessFailure.message.includes(failureMarker));
  assert.ok(manifest.failures.length > 0);
  for (const failure of manifest.failures)
    assert.ok(
      failure.message.includes(failureMarker) ||
        failure.message === 'owned child exited 1/null',
      'environment/cleanup failure is not counterevidence',
    );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [mode, file, variant, exit] = process.argv.slice(2);
  if (mode === '--mutate')
    await writeFile(file, signatureBypass(await readFile(file, 'utf8')));
  else if (mode === '--verdict')
    assertVerdict(
      JSON.parse(await readFile(file, 'utf8')),
      variant,
      Number(exit),
    );
  else throw new Error('expected --mutate or --verdict');
}
