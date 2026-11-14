import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { nativeContexts } from '../host-context-fixture.js';
import { createBrokerNetworkPort } from '../../src/network/broker-port.js';

async function fixture(t) {
  const f = await nativeContexts(t),
    original = f.connection.ipc.call.bind(f.connection.ipc),
    streamId = randomUUID(),
    calls = [],
    commands = [];
  let listener;
  f.connection.ipc.call = async (message) => {
    if (!message.call) return original(message);
    f.connection.validate('ShellAuthBusinessCall', message);
    calls.push(message.call);
    return {
      requestId: message.requestId,
      operation: message.call.operation,
      output: {
        statusCode: 200,
        body:
          message.call.operation === 'network_open'
            ? { streamId }
            : message.call.operation === 'network_send'
              ? { sequence: 0 }
              : { resultWire: Buffer.from('{}').toString('base64') },
      },
    };
  };
  f.connection.ipc.subscribeStream = async (_command, receive) => {
    listener = receive;
  };
  f.connection.ipc.streamCommand = async (command) => {
    commands.push(command);
  };
  const port = createBrokerNetworkPort({
    broker: f.broker,
    contexts: f.contexts,
  });
  t.after(() => port.close());
  return {
    ...f,
    port,
    calls,
    commands,
    streamId,
    emit(sequence, data) {
      const s = f.connection.snapshot();
      listener({
        streamId,
        shellEpoch: s.shellEpoch,
        revision: s.revision,
        sequence,
        type: 'data',
        data: Buffer.from(data).toString('base64'),
      });
    },
  };
}
test('network bridge opens only from a native session, grants bounded credit and sends fixed wire operations', async (t) => {
  const f = await fixture(t),
    received = [];
  const connection = await f.port.open(f.execution, (bytes) =>
    received.push(bytes.toString()),
  );
  assert.equal(
    f.connection.contexts.size,
    1,
    'completed open tool is revoked; only its session remains',
  );
  assert.deepEqual(
    f.calls.map((call) => call.operation),
    ['network_open'],
  );
  f.emit(0, '{}');
  assert.deepEqual(received, ['{}']);
  await connection.send(Buffer.from('{}'));
  assert.equal(f.calls.at(-1).operation, 'network_send');
  assert.equal(
    f.calls.at(-1).input.frameWire,
    Buffer.from('{}').toString('base64'),
  );
  assert.ok(
    f.commands
      .filter((c) => c.action === 'credit')
      .every((c) => c.creditBytes <= 65536),
  );
});
test('network bridge rejects forged execution before registration and platform work', async (t) => {
  const f = await fixture(t);
  await assert.rejects(
    f.port.open({ ...f.execution, agent: { ...f.agent } }, () => {}),
    { code: 'common.forbidden' },
  );
  assert.equal(f.calls.length, 0);
  assert.equal(f.connection.contexts.size, 0);
});
test('network bridge rejects stream reordering and cancels before releasing any payload', async (t) => {
  const f = await fixture(t),
    received = [];
  const connection = await f.port.open(f.execution, (bytes) =>
    received.push(bytes),
  );
  f.emit(1, '{}');
  assert.equal(received.length, 0);
  assert.ok(f.commands.some((c) => c.action === 'cancel'));
  await assert.rejects(connection.send(Buffer.from('{}')), {
    code: 'network.unauthorized',
  });
});
test('network submit uses a tool context and drops it after the bounded fixed operation', async (t) => {
  const f = await fixture(t);
  await f.port.submit(f.execution, Buffer.from('{}'));
  assert.equal(f.calls.at(-1).operation, 'network_submit');
  assert.equal(f.connection.contexts.size, 1);
  assert.equal(JSON.stringify(f.calls).includes('authorization'), false);
  await assert.rejects(f.port.submit(f.execution, Buffer.alloc(65537)), {
    code: 'network.unauthorized',
  });
});

test('parallel opens for one native session share a stream and each receiver', async (t) => {
  const f = await fixture(t),
    a = [],
    b = [];
  const [first, second] = await Promise.all([
    f.port.open(f.execution, (bytes) => a.push(bytes.toString())),
    f.port.open(f.execution, (bytes) => b.push(bytes.toString())),
  ]);
  assert.equal(first, second);
  assert.equal(f.calls.filter((c) => c.operation === 'network_open').length, 1);
  f.emit(0, '{}');
  assert.deepEqual(a, ['{}']);
  assert.deepEqual(b, ['{}']);
  await first.close();
  assert.equal(f.commands.filter((c) => c.action === 'cancel').length, 1);
});
test('closing while open is pending cancels the late stream and cannot revive it', async (t) => {
  const f = await fixture(t),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  const call = f.connection.ipc.call;
  f.connection.ipc.call = async (message) => {
    if (message.call?.operation === 'network_open') {
      entered.resolve();
      await release.promise;
    }
    return call(message);
  };
  const pending = f.port.open(f.execution, () =>
    assert.fail('closed receiver'),
  );
  const rejected = assert.rejects(pending, { code: 'network.unauthorized' });
  await entered.promise;
  f.port.close();
  release.resolve();
  await rejected;
  assert.ok(f.commands.some((c) => c.action === 'cancel'));
});
test('invalidating the source fences send and receive even before the next snapshot', async (t) => {
  const f = await fixture(t);
  const connection = await f.port.open(f.execution, () =>
    assert.fail('retired source'),
  );
  f.switchTenant();
  f.emit(0, '{}');
  await assert.rejects(connection.send(Buffer.from('{}')), {
    code: 'network.unauthorized',
  });
  assert.equal(f.commands.filter((c) => c.action === 'cancel').length, 1);
});
