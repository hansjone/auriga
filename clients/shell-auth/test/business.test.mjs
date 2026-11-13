import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { mkdtempSync, rmSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fixture, login } from './fixtures.mjs';
import { BusinessProxy, mapBusinessError } from '../business.mjs';
import { Streams } from '../streams.mjs';
import { valid } from '../schema.mjs';
const call = (broker, operation, input) => ({
  protocolVersion: 1,
  requestId: randomUUID(),
  shellEpoch: broker.epoch,
  expectedRevision: broker.revision,
  expiresAt: new Date(Date.now() + 25000).toISOString(),
  call: { operation, input },
});
test('fixed proxy binds origin, bearer, headers and schema; retries reuse result and errors omit upstream text', async () => {
  const f = fixture();
  await login(f);
  let requests = 0;
  const proxy = new BusinessProxy(f.broker, async (url, options) => {
    requests++;
    assert.equal(new URL(url).origin, 'https://example.com');
    assert.equal(
      options.headers.Authorization,
      'Bearer ' + f.tokens.access_token,
    );
    return new Response(JSON.stringify({ items: [] }), {
      status: 200,
      headers: { 'content-type': 'application/json', 'set-cookie': 'private' },
    });
  });
  const request = call(f.broker, 'listResourcePackages', {}),
    result = await proxy.call(request, 'one');
  assert.equal(result.output.statusCode, 200);
  assert.equal(result.output.headers['set-cookie'], undefined);
  assert.ok(valid('ShellAuthBusinessResult', result));
  await proxy.call(request, 'one');
  assert.equal(requests, 1);
  await assert.rejects(
    proxy.call({ ...request, url: 'https://elsewhere.example.com' }, 'one'),
    /validation_failed/,
  );
  await f.broker.logout();
  assert.equal(
    (await proxy.call(request, 'one')).error.code,
    'common.unauthenticated',
  );
  assert.deepEqual(mapBusinessError(503, 'private upstream text', '12'), {
    code: 'common.unavailable',
    retryable: true,
    upstreamStatus: 503,
    retryAfterSeconds: 12,
  });
  proxy.close();
  await f.broker.close();
});
test('in-flight business response is rejected after identity change', async () => {
  const f = fixture();
  await login(f);
  let release, started;
  const begin = new Promise((r) => {
    started = r;
  });
  const proxy = new BusinessProxy(f.broker, () => {
    started();
    return new Promise((r) => {
      release = r;
    });
  });
  const pending = proxy.call(call(f.broker, 'listResourcePackages', {}), 'one');
  await begin;
  await f.broker.logout();
  release(new Response('{"items":[]}'));
  const result = await pending;
  assert.equal(result.error.code, 'common.unauthenticated');
  proxy.close();
  await f.broker.close();
});
test('stream requires credit, preserves byte order and closes on logout', async () => {
  const f = fixture();
  await login(f);
  const streams = new Streams(f.broker),
    context = f.broker.guard(),
    stream = streams.create(context, 'one'),
    events = [];
  const subscription = {
    streamId: stream.id,
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
  };
  streams.subscribe(subscription, 'one', (e) => events.push(e));
  streams.push(stream, Buffer.from('你好'));
  assert.equal(events.length, 0);
  await streams.command(
    { ...subscription, action: 'credit', creditBytes: 100 },
    'one',
  );
  assert.equal(Buffer.from(events[0].data, 'base64').toString(), '你好');
  await f.broker.logout();
  assert.equal(events.at(-1).type, 'closed');
  assert.ok(events.every((e) => valid('ShellAuthStreamEvent', e)));
  streams.close();
  await f.broker.close();
});
test('upload is durable, digest checked, owner-bound, one-use and removed after consume', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-upload-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const f = fixture();
  await login(f);
  const streams = new Streams(f.broker, directory),
    context = f.broker.guard(),
    bytes = Buffer.from('signed artifact bytes'),
    sha256 = createHash('sha256').update(bytes).digest('hex');
  const upload = streams.openUpload(
    { sizeBytes: bytes.length, sha256, mediaType: 'application/octet-stream' },
    context,
    'one',
  ).body;
  const command = {
    streamId: upload.streamId,
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
    action: 'upload_chunk',
    sequence: 0,
    data: bytes.toString('base64'),
  };
  assert.throws(() => streams.command(command, 'other'), /forbidden/);
  assert.equal(streams.command(command, 'one').type, 'upload_ack');
  assert.equal(readdirSync(directory).length, 1);
  const reference = streams.finishUpload(upload.uploadId, context, 'one').body;
  assert.deepEqual(streams.consume(reference, context, 'one').bytes, bytes);
  assert.equal(readdirSync(directory).length, 0);
  assert.throws(() => streams.consume(reference, context, 'one'), /forbidden/);
  streams.close();
  await f.broker.close();
});
