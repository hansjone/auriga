import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertVerdict } from './assembly-verdict.mjs';

export const marker = 'SEND-2 cross-identity zero writes';
export function trustClientIdentity(source) {
  const anchor = '\tif err != nil || !validSubmitJSON(raw) {';
  assert.equal(
    source.split(anchor).length,
    2,
    'identity overlay must match exactly once',
  );
  return source
    .replace(
      anchor,
      `	// Deliberately bad implementation for compiled negative control only.
	var claims struct { Tenant string ${'`json:"tenant"`'}; Node string ${'`json:"node"`'}; Connection struct {
		Realm string ${'`json:"realm"`'}; Ingress string ${'`json:"ingress"`'}; Server string ${'`json:"server_id"`'}; Client string ${'`json:"client_id"`'}; Account string ${'`json:"client_account"`'}
	} ${'`json:"connection"`'} }
	if json.Unmarshal(raw, &claims) == nil && claims.Tenant != "" && claims.Node != "" {
		b.Tenant, b.Node, b.Realm = claims.Tenant, claims.Node, claims.Connection.Realm
		b.Ingress, b.ServerID, b.Account = claims.Connection.Ingress, claims.Connection.Server, claims.Connection.Account
		_, _ = fmt.Sscan(claims.Connection.Client, &b.ClientID)
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw, &body)
		delete(body, "tenant"); delete(body, "node"); delete(body, "connection")
		raw, err = json.Marshal(body)
	}
${anchor}`,
    )
    .replace('"encoding/json"', '"encoding/json"\n\t"fmt"');
}

export function gatewayVerdict(manifest, mode, exit) {
  if (mode === 'Control') {
    assertVerdict(manifest, mode, exit);
    assert.equal(
      manifest.checks.filter((v) => v.startsWith('SEND-2:')).length,
      2,
    );
    return;
  }
  assert.equal(mode, 'TrustClientIdentity');
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
    assert.ok(
      manifest.stages.some((v) => v.stage === stage),
      'real lifecycle required',
    );
  for (const stage of ['gateway-closed', 'java-closed']) {
    const closed = manifest.stages.find((v) => v.stage === stage);
    assert.equal(closed.exitCode, 0);
    assert.equal(closed.signal, null);
  }
  assert.ok(
    manifest.businessFailure.completedChecks.some((v) =>
      v.startsWith('SEND-2: real device HTTPS'),
    ),
  );
  assert.ok(manifest.failures.length > 0);
  for (const failure of manifest.failures)
    assert.ok(
      failure.message.includes(marker) ||
        failure.message === 'owned child exited 1/null',
      'environment/cleanup failure is not counterevidence',
    );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [op, file, mode, exit] = process.argv.slice(2);
  if (op === '--mutate')
    await writeFile(file, trustClientIdentity(await readFile(file, 'utf8')));
  else if (op === '--verdict') {
    const manifest = JSON.parse(await readFile(file, 'utf8'));
    // runAssembly keeps completedChecks on businessFailure as emitted by plugin.
    gatewayVerdict(manifest, mode, Number(exit));
  } else throw new Error('expected --mutate or --verdict');
}
