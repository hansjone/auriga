import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNativeHostPort,
  getNativeIsolationDecision,
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

test('deferring isolation keeps public durability capabilities mandatory', async () => {
  const f = fixture();
  delete f.ctx.sessionPersistence.flush;
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(host.getDiagnostics().capabilities.durability.available, false);
  assert.equal(await host.flush('session-a'), false);
  assert.equal((await host.getSession('session-a')).id, 'session-a');
});
