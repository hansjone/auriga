import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  gatewayVerdict,
  trustClientIdentity,
  marker,
} from './support/gateway-send-verdict.mjs';

test('identity mutant changes the gateway authority boundary exactly once', async () => {
  const source = await readFile(
    new URL(
      '../../infra/ai-gateway/internal/network/submit.go',
      import.meta.url,
    ),
    'utf8',
  );
  const changed = trustClientIdentity(source);
  assert.notEqual(source, changed);
  assert.ok(changed.includes('b.Tenant, b.Node, b.Realm = claims.Tenant'));
  assert.ok(changed.includes('h.verify(ctx, strings.TrimPrefix'));
  assert.ok(changed.includes('h.client.Call(ctx, b, "submit", raw)'));
  assert.throws(() => trustClientIdentity(''));
  assert.throws(() => trustClientIdentity(source + source));
});

const mutant = () => ({
  completed: false,
  stages: ['ready', 'gateway-ready', 'gateway-closed', 'java-closed'].map(
    (stage) => ({ stage, exitCode: 0, signal: null }),
  ),
  businessFailure: {
    name: 'AssertionError',
    code: 'ERR_ASSERTION',
    message: marker,
    completedChecks: ['SEND-2: real device HTTPS -> PG -> FETCH'],
  },
  failures: [
    { message: 'plugin matrix failed: ' + marker },
    { message: 'owned child exited 1/null' },
  ],
});

test('negative verdict requires cross-identity assertion after real positive control and clean services', () => {
  gatewayVerdict(mutant(), 'TrustClientIdentity', 1);
  assert.throws(() => gatewayVerdict(mutant(), 'TrustClientIdentity', 0));
  for (const change of [
    (m) => {
      m.businessFailure.name = 'SQLException';
    },
    (m) => {
      m.businessFailure.message = 'initialization failed';
    },
    (m) => {
      m.businessFailure.completedChecks = [];
    },
    (m) => {
      m.stages[2].exitCode = 1;
    },
    (m) => {
      m.failures.push({ message: 'PG survived cleanup' });
    },
  ]) {
    const m = mutant();
    change(m);
    assert.throws(() => gatewayVerdict(m, 'TrustClientIdentity', 1));
  }
});
