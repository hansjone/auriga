import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { brokerRejected } from './edge-wss-wire.mjs';
export function assertBoundary({ before, response, after }, expected) {
  assert.equal(
    response.failure,
    undefined,
    'client initialization/operation failed',
  );
  const o = response.observations;
  assert.ok(o);
  const calls = after.redeems.slice(before.redeems.length);
  if (expected === 'origin' || expected === 'admin') {
    assert.equal(o.httpStatus, 403, 'entry must reject HTTP upgrade');
    assert.ok(
      ['common.forbidden', 'edge.bad_origin', 'edge.forbidden'].includes(
        o.problem,
      ),
    );
    assert.equal(o.upgraded, false);
    assert.equal(after.edge.dials, before.edge.dials, 'entry reached Broker');
    assert.equal(calls.length, 0);
  } else if (expected === 'wrong-ca' || expected === 'wrong-name') {
    assert.equal(o.httpStatus, 502, 'upstream TLS must reject HTTP upgrade');
    assert.equal(o.problem, 'edge.upstream_error');
    assert.equal(o.upgraded, false);
    assert.equal(calls.length, 0);
    assert.ok(after.edge.dials > before.edge.dials);
    assert.deepEqual(after.edge.tlsErrors.slice(before.edge.tlsErrors.length), [
      expected === 'wrong-ca' ? 'unknown-authority' : 'hostname',
    ]);
  } else if (expected === 'auth') {
    assert.equal(o.upgraded, true);
    assert.equal(o.tlsVerified, true);
    assert.equal(o.authorized, false);
    assert.equal(
      brokerRejected(o.wire),
      true,
      'must receive actual Broker auth rejection',
    );
    // SDK close classification remains diagnostic. Only the actual socket's
    // whitelisted protocol rejection and exact connection/RPC match prove auth.
    assert.equal(calls.length, 1);
    assert.equal(calls[0].code, 'PermissionDenied');
    const decisions = after.decisions.slice(before.decisions.length);
    assert.equal(decisions.length, 1, 'one callout decision for this probe');
    const decision = decisions[0];
    assert.equal(decision.clientType, 'websocket');
    assert.equal(decision.allowed, false);
    assert.ok(o.wire.info, 'actual Broker INFO identity required');
    assert.equal(decision.serverId, o.wire.info.serverId);
    assert.equal(String(decision.clientId), o.wire.info.clientId);
    assert.equal(calls[0].serverId, o.wire.info.serverId);
    assert.equal(calls[0].clientId, o.wire.info.clientId);
    assert.match(response.expectedIngress, /^[a-z2-7]{26}$/);
    assert.equal(calls[0].ingress, response.expectedIngress);
  } else {
    assert.equal(expected, 'ready');
    assert.equal(o.upgraded, true);
    assert.equal(o.tlsVerified, true);
    assert.equal(o.authorized, true);
    assert.equal(response.readyResult?.type, 'ready_result');
    assert.equal(response.readyResult.status, 'ready');
    assert.equal(calls.length, 1);
    assert.equal(calls[0].code, 'OK');
    assert.equal(after.decisions.at(-1).clientType, 'websocket');
    assert.equal(after.decisions.at(-1).allowed, true);
  }
}
export async function mutate(path) {
  const source = await readFile(path, 'utf8');
  const from =
    'tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}';
  assert.equal(
    source.split(from).length,
    2,
    'exact production upstream TLS configuration required',
  );
  await writeFile(
    path,
    source.replace(
      from,
      'tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName, InsecureSkipVerify: true}',
    ),
  );
}
export function verdict(manifest, mode, exit) {
  const closed = manifest.stages.filter((v) => v.stage.endsWith('-closed'));
  assert.ok(closed.some((v) => v.stage === 'java-closed'));
  assert.ok(closed.some((v) => v.stage === 'gateway-closed'));
  assert.ok(closed.some((v) => v.stage === 'client-closed'));
  assert.ok(closed.some((v) => v.stage === 'valid-closed'));
  for (const event of closed) assert.equal(event.exitCode, 0);
  if (mode === 'Control') {
    assert.equal(exit, 0);
    assert.equal(manifest.completed, true);
    assert.deepEqual(manifest.failures, []);
    assert.deepEqual(manifest.checks, [
      'wrong-origin',
      'admin-entry',
      'wrong-ca',
      'wrong-name',
      'invalid-ticket',
      'verified-ready',
      'websocket-ingress-binding',
      'replayed-ticket',
    ]);
  } else {
    assert.equal(mode, 'BypassUpstreamTLS');
    assert.notEqual(exit, 0);
    assert.equal(manifest.completed, false);
    assert.equal(manifest.failurePhase, 'wrong-ca');
    assert.equal(manifest.failures.length, 1);
    assert.equal(manifest.failures[0].name, 'AssertionError');
    assert.match(
      manifest.failures[0].message,
      /upstream TLS must reject HTTP upgrade/,
    );
  }
}
if (process.argv[2] === '--mutate') await mutate(process.argv[3]);
if (process.argv[2] === '--verdict')
  verdict(
    JSON.parse(await readFile(process.argv[3], 'utf8')),
    process.argv[4],
    Number(process.argv[5]),
  );
