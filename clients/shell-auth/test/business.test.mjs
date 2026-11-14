import { OWNER, OTHER } from './fixtures.mjs';
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
test('MCP uses K2 session and tool handles and server-derived endpoint binding', async (t) => {
  const f = fixture();
  await login(f);
  f.platform.audience = async () => f.tokens;
  const endpointId = randomUUID(),
    bindingId = randomUUID(),
    seen = [];
  const proxy = new BusinessProxy(f.broker, async (url, options) => {
    const request = JSON.parse(options.body);
    if (String(url).endsWith('/register'))
      return new Response(JSON.stringify({ endpoint: { id: endpointId } }));
    if (String(url).endsWith('/bind'))
      return new Response(
        JSON.stringify({
          binding: {
            id: bindingId,
            endpointId,
            hostSessionId: request.hostSessionId,
          },
        }),
      );
    seen.push(request);
    return new Response(
      JSON.stringify({
        jsonrpc: '2.0',
        id: request.id,
        result: { content: [] },
      }),
    );
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const a = proxy.host.connect({
    verified: true,
    connectionId: randomUUID(),
    sid: randomUUID(),
    processStart: String(Date.now()),
    role: 'plugin-backend',
  });
  const envelope = () => ({
    protocolVersion: 1,
    requestId: randomUUID(),
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
    expiresAt: new Date(Date.now() + 25000).toISOString(),
  });
  const session = (
    await proxy.host.command(
      {
        ...envelope(),
        action: 'register',
        context: {
          kind: 'session',
          hostSessionId: 'real-host-session',
          expiresAt: new Date(Date.now() + 600000).toISOString(),
        },
      },
      a,
    )
  ).context;
  const invalid = await proxy.call(
    call(f.broker, 'team_list_endpoints', {
      executionContextId: randomUUID(),
      arguments: { limit: 10 },
    }),
    a.id,
  );
  assert.equal(invalid.error.code, 'common.forbidden');
  assert.ok(
    (
      await proxy.call(
        call(f.broker, 'team_node_register', {
          executionContextId: session.executionContextId,
          arguments: { label: 'test', receiveEnabled: true },
        }),
        a.id,
      )
    ).output,
  );
  assert.ok(
    (
      await proxy.call(
        call(f.broker, 'team_node_bind', {
          executionContextId: session.executionContextId,
          arguments: { endpointId, hostSessionId: 'real-host-session' },
        }),
        a.id,
      )
    ).output,
  );
  const tool = (
    await proxy.host.command(
      {
        ...envelope(),
        action: 'register',
        context: {
          kind: 'tool',
          hostSessionId: 'real-host-session',
          toolCallId: randomUUID(),
          rootCallId: randomUUID(),
          parentExecutionContextId: session.executionContextId,
          expiresAt: new Date(Date.now() + 600000).toISOString(),
        },
      },
      a,
    )
  ).context;
  const result = await proxy.call(
    call(f.broker, 'team_list_endpoints', {
      executionContextId: tool.executionContextId,
      arguments: { limit: 10 },
    }),
    a.id,
  );
  assert.ok(result.output);
  assert.equal(seen[0].params._meta['auriga/sessionId'], 'real-host-session');
  await f.broker.logout();
  assert.equal(proxy.host.contexts.size, 0);
});
test('SSE preserves UTF-8 bytes across chunks and requires DONE before end', async () => {
  const f = fixture();
  await login(f);
  f.platform.audience = async () => f.tokens;
  const raw = Buffer.from('data: {"text":"你好"}\n\ndata: [DONE]\n\n');
  const proxy = new BusinessProxy(
    f.broker,
    async () =>
      new Response(
        new ReadableStream({
          start(controller) {
            for (const byte of raw) controller.enqueue(Uint8Array.of(byte));
            controller.close();
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } },
      ),
  );
  const result = await proxy.call(
    call(f.broker, 'createAiChatCompletionStream', {
      body: {
        model: 'test',
        max_tokens: 16,
        messages: [{ role: 'user', content: 'test' }],
        stream: true,
      },
    }),
    OWNER,
  );
  assert.ok(result.output, result.error?.code);
  await new Promise((resolve) => setTimeout(resolve, 0));
  const events = [],
    subscription = {
      streamId: result.output.body.streamId,
      shellEpoch: f.broker.epoch,
      expectedRevision: f.broker.revision,
    };
  let ended;
  const end = new Promise((resolve) => {
    ended = resolve;
  });
  proxy.streams.subscribe(subscription, OWNER, (e) => {
    events.push(e);
    if (e.type === 'end' || e.type === 'error') ended();
  });
  await proxy.streams.command(
    { ...subscription, action: 'credit', creditBytes: 1048576 },
    OWNER,
  );
  await end;
  assert.equal(events.at(-1).type, 'end');
  assert.deepEqual(
    Buffer.concat(
      events
        .filter((e) => e.type === 'data')
        .map((e) => Buffer.from(e.data, 'base64')),
    ),
    raw,
  );
  proxy.close();
  await f.broker.close();
});
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
    result = await proxy.call(request, OWNER);
  assert.equal(result.output.statusCode, 200);
  assert.equal(result.output.headers['set-cookie'], undefined);
  assert.ok(valid('ShellAuthBusinessResult', result));
  await proxy.call(request, OWNER);
  assert.equal(requests, 1);
  await assert.rejects(
    proxy.call({ ...request, url: 'https://elsewhere.example.com' }, OWNER),
    /validation_failed/,
  );
  await f.broker.logout();
  assert.equal(
    (await proxy.call(request, OWNER)).error.code,
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
  const pending = proxy.call(call(f.broker, 'listResourcePackages', {}), OWNER);
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
    stream = streams.create(context, OWNER),
    events = [];
  const subscription = {
    streamId: stream.id,
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
  };
  streams.subscribe(subscription, OWNER, (e) => events.push(e));
  streams.push(stream, Buffer.from('你好'));
  assert.equal(events.length, 0);
  await streams.command(
    { ...subscription, action: 'credit', creditBytes: 100 },
    OWNER,
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
    OWNER,
  ).body;
  const command = {
    streamId: upload.streamId,
    shellEpoch: f.broker.epoch,
    expectedRevision: f.broker.revision,
    action: 'upload_chunk',
    sequence: 0,
    data: bytes.toString('base64'),
  };
  assert.throws(() => streams.command(command, OTHER), /forbidden/);
  assert.equal(streams.command(command, OWNER).type, 'upload_ack');
  assert.equal(readdirSync(directory).length, 1);
  const reference = streams.finishUpload(upload.uploadId, context, OWNER).body;
  assert.deepEqual(streams.consume(reference, context, OWNER).bytes, bytes);
  assert.equal(readdirSync(directory).length, 0);
  assert.throws(() => streams.consume(reference, context, OWNER), /forbidden/);
  streams.close();
  await f.broker.close();
});
