import test from 'node:test';
import assert from 'node:assert/strict';
import {
  describeBusinessFailure,
  redactDiagnostics,
} from './support/assembly-failure.mjs';
import { failureMarker } from './support/assembly-verdict.mjs';

test('business failure identifies phase, assertion, observed/required values and source stack', () => {
  let failure;
  try {
    assert.equal(3, 2, 'member logical READY handshake count');
  } catch (error) {
    failure = describeBusinessFailure(error, 'P1/READY');
  }
  assert.equal(failure.phase, 'P1/READY');
  assert.equal(failure.assertion, 'member logical READY handshake count');
  assert.equal(failure.actual.value, 3);
  assert.equal(failure.expected.value, 2);
  assert.equal(failure.actual.meaning, 'observed value');
  assert.equal(failure.expected.meaning, 'required value');
  assert.match(failure.stack, /assembly-failure\.test\.mjs:\d+:\d+/);
});

test('diagnostics retain the business counterexample marker without credential payloads', () => {
  let failure;
  try {
    assert.equal(false, true, failureMarker);
  } catch (error) {
    failure = describeBusinessFailure(error, 'F6/signature');
  }
  assert.equal(failure.message, failureMarker);
  const secret = 'private-fixture-marker';
  const error = new Error(
    `failure Bearer ${secret}\n-----BEGIN PRIVATE KEY-----\nprivate-data\n-----END PRIVATE KEY-----`,
  );
  const value = describeBusinessFailure(error, 'diagnostics', [secret]);
  assert.equal(JSON.stringify(value).includes(secret), false);
  assert.equal(JSON.stringify(value).includes('private-data'), false);
  const saved = JSON.parse(
    JSON.stringify(
      redactDiagnostics({ ...failure, diagnosticNote: secret }, [secret]),
    ),
  );
  assert.equal(saved.phase, 'F6/signature');
  assert.equal(saved.assertion, failureMarker);
  assert.match(saved.stack, /assembly-failure\.test\.mjs:\d+:\d+/);
  assert.equal(saved.diagnosticNote, '[redacted]');
});
