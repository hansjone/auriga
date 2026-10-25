import assert from 'node:assert/strict';
import test from 'node:test';
import { createNativeHostPort } from '../../../../src/network/host/native/index.js';
import { fixture } from './fixtures.js';

test('capability probe requires neither package files nor version metadata', async () => {
  const f = fixture();
  const input = {
    ctx: f.ctx,
    runtime: { version: 'future', controllerPath: '/missing' },
  };
  const host = await createNativeHostPort(input);
  assert.equal(host.getDiagnostics().capabilities.read.available, true);
  assert.equal((await host.getSession('session-a')).id, 'session-a');
  input.runtime.version = 'older';
  assert.equal(await host.flush('session-a'), true);
});
