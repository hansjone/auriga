import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  assertRevokeSuccess,
  containerIsRunning,
  edgeInstanceOk,
  instanceReadyRunning,
  phaseReached,
  safeSummary,
} from './assert.mjs';

describe('phaseReached (strict actual phase)', () => {
  it('rejects desired-only stop (old waitPhase false positive)', () => {
    assert.equal(
      phaseReached({ phase: 'running', desired: 'stopped' }, 'stopped'),
      false,
    );
    assert.equal(
      phaseReached({ phase: 'stopping', desired: 'stopped' }, 'stopped'),
      false,
    );
  });

  it('accepts only actual phase match', () => {
    assert.equal(
      phaseReached({ phase: 'stopped', desired: 'stopped' }, 'stopped'),
      true,
    );
    assert.equal(
      phaseReached({ phase: 'running', desired: 'running' }, 'running'),
      true,
    );
  });
});

describe('instanceReadyRunning', () => {
  it('requires ready + running phase', () => {
    assert.equal(instanceReadyRunning({ phase: 'running', ready: true }), true);
    assert.equal(instanceReadyRunning({ phase: 'running', ready: false }), false);
    assert.equal(instanceReadyRunning({ phase: 'starting', ready: true }), false);
  });
});

describe('assertRevokeSuccess', () => {
  it('rejects 4xx soft-pass (old revokeSmoke false positive)', () => {
    assert.throws(() => assertRevokeSuccess(404, { title: 'not found' }), {
      code: 'revoke_not_ok',
    });
    assert.throws(() => assertRevokeSuccess(400, {}), { code: 'revoke_not_ok' });
    assert.throws(() => assertRevokeSuccess(500, {}), { code: 'revoke_not_ok' });
  });

  it('accepts contract success statuses', () => {
    assert.doesNotThrow(() => assertRevokeSuccess(204));
    assert.doesNotThrow(() => assertRevokeSuccess(200, { revoked: 1 }));
  });
});

describe('containerIsRunning', () => {
  it('parses docker inspect Running field', () => {
    assert.equal(containerIsRunning('true'), true);
    assert.equal(containerIsRunning('false'), false);
    assert.equal(containerIsRunning('exited'), false);
  });
});

describe('edgeInstanceOk', () => {
  it('requires 200 and fake-dsh body', () => {
    assert.equal(edgeInstanceOk({ status: 200, text: 'fake-dsh ok\n' }), true);
    assert.equal(edgeInstanceOk({ status: 502, text: 'fake-dsh ok\n' }), false);
    assert.equal(edgeInstanceOk({ status: 200, text: 'starting…' }), false);
  });
});

describe('safeSummary', () => {
  it('redacts cookie and token-like material', () => {
    const s = safeSummary({
      cookie: 'auriga_session=supersecret',
      Authorization: 'Bearer abc.def',
    });
    assert.doesNotMatch(s, /supersecret/);
    assert.doesNotMatch(s, /abc\.def/);
    assert.match(s, /redacted/i);
  });
});
