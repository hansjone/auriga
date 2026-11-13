import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture, login } from './fixtures.mjs';
import { randomUUID, randomBytes } from 'node:crypto';
import { BusinessProxy } from '../business.mjs';
test('artifact download streams original bytes with bounded credit and never returns upstream auth headers', async () => {
  const f = fixture();
  await login(f);
  const bytes = randomBytes(200000);
  const proxy = new BusinessProxy(
    f.broker,
    async () =>
      new Response(bytes, {
        headers: {
          'content-type': 'application/octet-stream',
          authorization: 'secret',
        },
      }),
  );
  const result = await proxy.call(
    {
      protocolVersion: 1,
      requestId: randomUUID(),
      shellEpoch: f.broker.epoch,
      expectedRevision: f.broker.revision,
      expiresAt: new Date(Date.now() + 25000).toISOString(),
      call: {
        operation: 'UnifiedDownloadArtifact',
        input: {
          headers: {
            'idempotency-key': randomUUID(),
            'if-match': 'test-revision',
          },
          body: {
            clientId: f.identity.deviceId,
            ticket: randomUUID(),
            artifactId: 'archive',
          },
        },
      },
    },
    'one',
  );
  assert.ok(result.output, result.error?.code);
  assert.equal(result.output.headers, undefined);
  const events = [];
  let done;
  const ended = new Promise((r) => {
    done = r;
  });
  const subscription = {
    streamId: result.output.body.streamId,
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
  };
  proxy.streams.subscribe(subscription, 'one', (event) => {
    events.push(event);
    if (event.type === 'end' || event.type === 'error') done();
  });
  await proxy.streams.command(
    { ...subscription, action: 'credit', creditBytes: 1048576 },
    'one',
  );
  await ended;
  assert.equal(events.at(-1).type, 'end');
  assert.deepEqual(
    Buffer.concat(
      events
        .filter((e) => e.type === 'data')
        .map((e) => Buffer.from(e.data, 'base64')),
    ),
    bytes,
  );
  proxy.close();
  await f.broker.close();
});
