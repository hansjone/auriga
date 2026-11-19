// Actual production Host lifecycle against the Go Relay HTTP fixture. This
// script starts no DSH process and substitutes no Host lifecycle implementation.
import assert from 'node:assert/strict';
import { createPublicKey, randomUUID } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { createProductionSharedPorts } from '../../../../../clients/dsh-plugin/src/shared/production-ports.js';

const [origin, raw] = process.argv.slice(2);
const { bindings, publicKey } = JSON.parse(raw);
const binding = bindings[0];
const agent = { id: binding.sessionId, session: { id: binding.sessionId } };
const listeners = new Map(),
  events = [];
const ctx = {
  agents: {
    get: (id) => id === agent.id && agent,
    currentInitiator: () => agent,
  },
  sessions: { get: (id) => id === agent.session.id && agent.session },
  on(name, handler) {
    listeners.set(name, handler);
    return () => listeners.delete(name);
  },
};
async function call(operation, body, signal) {
  const response = await globalThis.fetch(
    `${origin}/_auriga/shared-rpc/${operation}`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
      signal,
    },
  );
  const result = await response.json();
  events.push({ operation, body, result, status: response.status });
  if (!response.ok) throw Object.assign(new Error(result.code), result);
  return result;
}
const keys = new Map([
  [
    'fixture',
    createPublicKey({
      key: Buffer.from(publicKey, 'base64'),
      format: 'der',
      type: 'spki',
    }),
  ],
]);
const ports = createProductionSharedPorts(ctx, {
  settings: () => ({}),
  bridge: { call, close() {} },
  Peer: class {},
  options: {
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    capabilityKeys: { 'auriga.shared.enterprise': keys },
  },
});
const request = (turn) =>
  listeners.get('agent/request')(
    { agent, turn, step: 1, signal: new globalThis.AbortController().signal },
    async () => {},
  );
async function end(turn) {
  const count = events.filter((e) => e.operation === 'finishTask').length;
  listeners.get('session/event')(agent.session, {
    type: 'turn/end',
    data: { turn },
  });
  for (
    let i = 0;
    i < 100 &&
    events.filter((e) => e.operation === 'finishTask').length === count;
    i++
  )
    await new Promise((resolve) => setTimeout(resolve, 5));
  assert.ok(events.filter((e) => e.operation === 'finishTask').length > count);
}
const memory = (closed) =>
  globalThis.fetch(`${origin}/fixture/memory${closed ? '?closed' : ''}`, {
    method: 'POST',
  });
const ordinary = (b, id) =>
  call('beginTask', {
    binding: b,
    taskId: id,
    scopes: ['task.execute'],
    audience: 'auriga.shared.enterprise',
  });
const finish = (b, id) => call('finishTask', { binding: b, taskId: id });

await memory(true);
await assert.rejects(request(1), { message: 'common.unavailable' });
const rejected = events.find((e) => e.operation === 'beginTask').body;
const ack = events.find((e) => e.operation === 'finishTask');
assert.equal(ack.status, 200);
assert.deepEqual(ack.result, {
  fenced: true,
  binding,
  taskId: rejected.taskId,
});
await memory(false);
await request(2); // Actual Host must pass its prior closing barrier.
await end(2);
await assert.rejects(ordinary(binding, rejected.taskId)); // exact UUID never replayed
assert.deepEqual(await finish(binding, rejected.taskId), ack.result);
await assert.rejects(
  finish({ ...binding, authGeneration: 2 }, rejected.taskId),
);

const existing = randomUUID();
await ordinary(binding, existing);
await assert.rejects(request(3), { message: 'common.unavailable' });
assert.equal(events.at(-1).operation, 'finishTask');
assert.equal(events.at(-1).status, 200); // cannot finish/adopt the existing root
await assert.rejects(ordinary(binding, randomUUID()));
await finish(binding, existing);
await request(4);
await end(4);

const full = bindings.slice(1).map((b) => ({ b, id: randomUUID() }));
for (const { b, id } of full) await ordinary(b, id);
await assert.rejects(request(5), { message: 'common.unavailable' });
assert.equal(events.at(-1).status, 200);
await finish(full[0].b, full[0].id);
await request(6);
await end(6);
for (const { b, id } of full.slice(1)) await finish(b, id);
await ports.close();
console.log(
  'PASS actual Host Begin catch → exact Finish → next turn: 80%, busy, full pool; replay and old binding denied',
);
