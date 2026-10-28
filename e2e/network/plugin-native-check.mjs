// Real rc.3 Context, ToolRuntime and native provider. The remote source and
// submission peers are explicit contract fixtures, not PG/platform acceptance.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { createNativeHost } from './support/assembly-native.mjs';
import { setup } from '../../clients/dsh-plugin/test/network/source-control/fixtures.js';
import { saveProducerEpoch } from '../../clients/dsh-plugin/src/network/durable/outbox.js';
import { createDeviceMessaging } from '../../clients/dsh-plugin/src/network/durable/capability.js';
import { createNativeHostPort } from '../../clients/dsh-plugin/src/network/host/native/host-port.js';

test('真实 ToolRuntime 接受生产发送 schema；丢响应后复用账本原键，真实 HostPort 接收并持久保存正文', async (t) => {
  const directory = await mkdtemp(
    new URL('./target/plugin-native-', import.meta.url),
  );
  const native = await createNativeHost({
    directory,
    cwd: join(directory, 'cwd'),
    ...(process.env.AURIGA_DSH_RUNTIME
      ? { runtime: process.env.AURIGA_DSH_RUNTIME }
      : {}),
  });
  let capability;
  // Registered first, so it executes last, after the fixture releases its ledger.
  t.after(async () => {
    capability?.close();
    await capability?.idle();
    await native.close();
    await rm(directory, { recursive: true, force: true });
  });
  const agent = await native.create('session-a');
  let scoped;
  const injection = native.ctx.inject(
    [
      'tools',
      'sessions',
      'sessionController',
      'agents',
      'sessionPersistence',
      'workspaceRegistry',
    ],
    (ctx) => {
      scoped = ctx;
    },
  );
  await injection;
  const f = await setup(t, { native: { ctx: scoped, agents: scoped.agents } });
  await saveProducerEpoch(f.ledger, '42');
  await f.control.establish('1');
  await f.control.idle();
  assert.equal(
    f.ledger.peek().sourceControl.entries['1'].observation.source.state,
    'active',
  );
  const abort = new AbortController(),
    requests = [];
  const prepared = {
    async submit(request) {
      requests.push(request);
      const saved = Object.values(f.store.snapshot().durable.submissions)[0];
      assert.equal(saved.request.idempotency.key, request.idempotency.key);
      assert.equal(saved.sourceSnapshot.binding.hostSessionId, agent.id);
      if (requests.length === 1) throw new Error('contract peer lost response');
      return Buffer.from(
        JSON.stringify({
          status: 'platform_stored',
          from: {
            version: 1,
            realm: '2',
            node: '64',
            local: '1',
            type: 'session',
          },
          to: request.to,
          message_id: 'original-message',
        }),
      );
    },
  };
  capability = createDeviceMessaging({
    ctx: scoped,
    ledger: f.ledger,
    prepared,
    source: f.control,
    transport: f.transport,
    ready: f.ready,
    trust: f.trust,
    signal: abort.signal,
    assertCurrent() {},
    now: f.signer.now,
  });
  await capability.start();
  assert.ok(scoped.tools.get('auriga_send_message'));
  const execute = () =>
    scoped.tools.execute({
      callId: randomUUID(),
      name: 'auriga_send_message',
      arguments: {
        target: 'aur1:2.3f.1',
        body: { subtype: 'text', text: 'native production send' },
      },
      agent,
      signal: abort.signal,
    });
  const failed = await execute();
  assert.equal(failed.isError, false);
  assert.equal(failed.value.status, 'error');
  const recovered = await execute();
  assert.equal(recovered.isError, false);
  assert.equal(recovered.value.status, 'request_submitted');
  assert.equal(requests.length, 2);
  assert.deepEqual(
    requests[0],
    requests[1],
    'real tool retry must retain original producer key and source',
  );
  assert.equal(Object.keys(f.store.snapshot().durable.submissions).length, 1);
  capability.close();
  abort.abort();
  await capability.idle();
  const host = await createNativeHostPort({ ctx: scoped });
  const requestId = randomUUID(),
    text = 'native durable receiving body';
  assert.equal(
    (await host.prompt(agent.id, { requestId, text, mode: 'queue' })).accepted,
    true,
  );
  assert.equal(await host.flush(agent.id), true);
  const reader = await scoped.sessionPersistence.open(agent.id, 'read');
  try {
    assert.ok(JSON.stringify(await reader.read()).includes(text));
  } finally {
    await reader.close();
  }
  assert.deepEqual(native.ctx.llm.listProviders(), []);
});
