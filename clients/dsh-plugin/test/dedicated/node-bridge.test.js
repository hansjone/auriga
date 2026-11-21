import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { createDedicatedNodeBridge } from '../../src/dedicated/node-bridge.js';

test('dedicated bridge pins internal factory lookup/view responses and both sides of asynchronous calls', async () => {
  const view = JSON.parse(
      readFileSync(new URL('./node-wire.json', import.meta.url), 'utf8'),
    ),
    binding = view.binding;
  let value = { binding },
    active = true,
    calls = 0,
    change = false;
  const bridge = createDedicatedNodeBridge({
    binding,
    settings: () => {
      if (!active) throw new Error('revoked');
    },
    transport: {
      call: async () => {
        calls++;
        if (change) active = false;
        return value;
      },
      close() {},
      stream() {},
    },
  });
  assert.deepEqual(await bridge.call('lookup', {}), { binding });
  for (const replacement of [
    { memberId: randomUUID() },
    { mode: 'shared' },
    { physicalInstanceId: randomUUID() },
    { epoch: binding.epoch + 1 },
  ]) {
    value = { binding: { ...binding, ...replacement } };
    await assert.rejects(bridge.call('lookup', {}));
  }
  value = { view };
  assert.deepEqual(await bridge.call('resourceSnapshot', { binding }), {
    view,
  });
  await assert.rejects(
    bridge.call('beginTask', {
      binding: { ...binding, memberId: randomUUID() },
    }),
  );
  value = {};
  change = true;
  await assert.rejects(bridge.call('beginTask', { binding }));
  const before = calls;
  await assert.rejects(bridge.call('lookup', {}));
  assert.equal(calls, before);
  await bridge.call('finishTask', { binding });
  assert.equal(
    calls,
    before + 1,
    'pinned cleanup remains possible after revocation',
  );
});
