import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from '../shell-runtime-peer.js';
import {
  DIAGNOSTIC_PATH,
  registerBaseTools,
} from '../../src/network/base-tools.js';
import { fixture } from './host/native/fixtures.js';

for (const [status, available] of [
  [200, false],
  [404, false],
  [401, false],
  [500, false],
  [503, false],
  [200, true],
])
  test(`fixed broker diagnostic HTTP ${status}, API available ${available} preserves typed reachability evidence`, async (t) => {
    const native = fixture(),
      agent = native.attach('session-a'),
      tools = new Map();
    native.ctx.tools = {
      register(tool) {
        tools.set(tool.name, tool);
        return () => tools.delete(tool.name);
      },
    };
    const f = await shellRuntimePeer(t, { ctx: native.ctx }),
      original = f.connection.ipc.call.bind(f.connection.ipc),
      calls = [];
    f.connection.ipc.call = async (message) => {
      if (message.call?.operation !== 'provider_diagnostic')
        return original(message);
      calls.push(message.call);
      return {
        requestId: message.requestId,
        operation: 'provider_diagnostic',
        ...(status >= 400
          ? {
              error: {
                code:
                  status === 401
                    ? 'common.unauthenticated'
                    : 'common.unavailable',
                retryable: false,
                upstreamStatus: status,
              },
            }
          : { output: { statusCode: status, body: { available } } }),
      };
    };
    t.mock.method(globalThis, 'fetch', () =>
      assert.fail('no plugin diagnostic fetch'),
    );
    const dispose = registerBaseTools({
      ctx: native.ctx,
      ...f.runtime.baseTools,
    });
    t.after(dispose);
    const result = await tools
      .get('auriga_net_diagnose')
      .execute({}, { agent });
    assert.equal(result.segments[1].segment, 'ingress');
    assert.equal(result.segments[1].status, 'ready');
    assert.equal(result.segments[2].segment, 'hq_api');
    assert.equal(
      result.segments[2].status,
      available ? 'ready' : 'unavailable',
    );
    if (!available) {
      assert.equal(result.firstFailure, 'hq_api');
      assert.equal(
        result.segments[2].reason,
        status === 401 ? 'unauthorized' : 'unknown',
      );
    }
    const view = await tools.get('auriga_net_status').execute({}, { agent });
    assert.equal(view.headquarters.reachable, true);
    assert.ok(view.headquarters.lastSuccessAt);
    assert.equal(calls.length, 2);
    assert.ok(calls.every((call) => Object.keys(call.input).length === 0));
  });
test('diagnostic rejects arbitrary paths and fenced or aborted results before publishing evidence', async (t) => {
  const f = await shellRuntimePeer(t),
    before = f.connection.calls.length;
  await assert.rejects(
    f.runtime.baseTools.probe(
      'https://outside.example.com',
      new AbortController().signal,
    ),
    { code: 'common.validation_failed' },
  );
  assert.equal(f.connection.calls.length, before);
  const aborted = new AbortController();
  aborted.abort();
  await assert.rejects(
    f.runtime.baseTools.probe(DIAGNOSTIC_PATH, aborted.signal),
  );
  const gate = Promise.withResolvers(),
    entered = Promise.withResolvers(),
    original = f.connection.ipc.call.bind(f.connection.ipc);
  f.connection.ipc.call = async (message) => {
    if (message.call?.operation !== 'provider_diagnostic')
      return original(message);
    entered.resolve();
    await gate.promise;
    return {
      requestId: message.requestId,
      operation: 'provider_diagnostic',
      output: { statusCode: 200, body: { available: true } },
    };
  };
  const pending = f.runtime.baseTools.probe(
    DIAGNOSTIC_PATH,
    new AbortController().signal,
  );
  const rejected = assert.rejects(pending);
  await entered.promise;
  f.connection.logout();
  gate.resolve();
  await rejected;
  assert.equal(f.runtime.baseTools.getState().reachable, null);
});
