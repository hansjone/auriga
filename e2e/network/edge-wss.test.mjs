import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { assertBoundary, verdict } from './support/edge-wss-verdict.mjs';
const proof = () => ({
  before: { edge: { dials: 0, tlsErrors: [] }, redeems: [], decisions: [] },
  after: { edge: { dials: 0, tlsErrors: [] }, redeems: [], decisions: [] },
  response: {
    observations: {
      httpStatus: 403,
      problem: 'common.forbidden',
      upgraded: false,
    },
  },
});
test('entry refusal requires exact phase and zero Broker dial/Redeem', () => {
  assertBoundary(proof(), 'origin');
  const wrong = proof();
  wrong.after.edge.dials = 1;
  assert.throws(() => assertBoundary(wrong, 'origin'), /entry reached Broker/);
  wrong.response.observations.httpStatus = 502;
  assert.throws(() => assertBoundary(wrong, 'origin'));
});
test('TLS refusal requires real typed x509 error after dial, without Redeem', () => {
  const p = proof();
  Object.assign(p.response.observations, {
    httpStatus: 502,
    problem: 'edge.upstream_error',
  });
  p.after.edge = { dials: 1, tlsErrors: ['unknown-authority'] };
  assertBoundary(p, 'wrong-ca');
  p.after.edge.tlsErrors = ['other'];
  assert.throws(() => assertBoundary(p, 'wrong-ca'));
  p.after.edge.tlsErrors = ['hostname'];
  assertBoundary(p, 'wrong-name');
});
test('CONNECT refusal cannot pass on any generic network error', () => {
  const p = proof();
  Object.assign(p.response.observations, {
    upgraded: true,
    tlsVerified: true,
    authorized: false,
    natsAuthError: true,
  });
  p.response.errorCode = 'unauthorized';
  p.after.redeems = [{ code: 'PermissionDenied' }];
  p.after.decisions = [{ clientType: 'websocket', allowed: false }];
  assertBoundary(p, 'auth');
  p.response.observations.natsAuthError = false;
  assert.throws(() => assertBoundary(p, 'auth'));
});
test('counterevidence never accepts setup or cleanup failures', () => {
  const stages = ['java', 'gateway', 'client', 'valid'].map((name) => ({
    stage: name + '-closed',
    exitCode: 0,
  }));
  const m = {
    stages,
    completed: false,
    failurePhase: 'wrong-ca',
    failures: [
      {
        name: 'AssertionError',
        message: 'upstream TLS must reject HTTP upgrade',
      },
    ],
  };
  verdict(m, 'BypassUpstreamTLS', 1);
  assert.throws(() =>
    verdict({ ...m, failurePhase: 'initialization' }, 'BypassUpstreamTLS', 1),
  );
  assert.throws(() =>
    verdict(
      { ...m, failures: [...m.failures, { message: 'cleanup failed' }] },
      'BypassUpstreamTLS',
      1,
    ),
  );
  stages[0].exitCode = 1;
  assert.throws(() => verdict(m, 'BypassUpstreamTLS', 1));
});
test('driver retains real assembly and explicit opt-in; test-only overlay observes callout', async () => {
  const driver = await readFile(
    new URL('./source-edge-wss.mjs', import.meta.url),
    'utf8',
  );
  const build = await readFile(
    new URL('./support/edge-wss-build.mjs', import.meta.url),
    'utf8',
  );
  assert.match(driver, /javaClasspath\(root\)/);
  assert.match(driver, /NetworkSourceJavaHost/);
  assert.match(driver, /--run/);
  assert.match(driver, /assertBoundary\(proof, expected\)/);
  assert.match(build, /r\.redeemer\.onDecision\(a, c, e\)/);
  assert.doesNotMatch(build, /InsecureSkipVerify/);
});
