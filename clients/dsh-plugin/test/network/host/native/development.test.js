import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNativeHostPort,
  getNativeIsolationDecision,
  PINNED_HOST,
} from '../../../../src/network/host/native/index.js';
import { fixture } from './fixtures.js';

test('isolation is deferred at one decision point with an explicit development diagnostic', () => {
  assert.deepEqual(getNativeIsolationDecision(), {
    required: false,
    isolated: false,
    mode: 'development_unisolated',
    message: '未隔离（开发阶段）',
  });
  assert.equal(Object.isFrozen(getNativeIsolationDecision()), true);
});

test('deferring isolation does not accept a copied fingerprint instead of actual runtime artifacts', async () => {
  const f = fixture();
  await assert.rejects(
    createNativeHostPort({ ctx: f.ctx, runtime: { ...PINNED_HOST } }),
    { code: 'network.host_incompatible' },
  );
});
