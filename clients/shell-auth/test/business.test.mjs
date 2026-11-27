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
test('bound query and path identities come from broker context and fail closed before token exchange', async (t) => {
  const f = fixture();
  await login(f);
  let exchanges = 0;
  const tokenFor = f.broker.tokenFor.bind(f.broker);
  f.broker.tokenFor = (...args) => {
    exchanges++;
    return tokenFor(...args);
  };
  const seen = [];
  const proxy = new BusinessProxy(f.broker, async (url) => {
    seen.push(new URL(url));
    return Response.json({ items: [] });
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const context = f.broker.guard(f.broker.epoch, f.broker.revision);
  for (const operation of [
    'UnifiedListInstallations',
    'listMemberResourceInstallations',
  ]) {
    assert.ok(
      (await proxy.call(call(f.broker, operation, { limit: 200 }), OWNER))
        .output,
    );
    assert.equal(seen.at(-1).searchParams.get('clientId'), f.identity.deviceId);
    for (const clientId of [f.identity.deviceId, randomUUID()])
      await assert.rejects(
        proxy.call(call(f.broker, operation, { clientId }), OWNER),
        { code: 'common.validation_failed' },
      );
  }
  const schema = 'ShellAuthBoundParam_UnifiedListInstallations_clientId';
  const definition = {
    operation: 'UnifiedListInstallations',
    route: '/api/resource/v1/{tenantId}/{ownerId}/{memberId}/{clientId}',
    method: 'get',
    audience: 'auriga',
    requestHeaders: [],
    responseHeaders: [],
    output: 'ShellAuthOutput_UnifiedListInstallations',
    boundParams: [
      {
        name: 'tenantId',
        identity: 'tenantId',
        in: 'path',
        required: true,
        schema,
      },
      {
        name: 'ownerId',
        identity: 'memberId',
        in: 'path',
        required: true,
        schema,
      },
      {
        name: 'memberId',
        identity: 'memberId',
        in: 'path',
        required: true,
        schema,
      },
      {
        name: 'clientId',
        identity: 'deviceId',
        in: 'path',
        required: true,
        schema,
      },
      {
        name: 'deviceId',
        identity: 'deviceId',
        in: 'query',
        required: true,
        schema,
      },
      {
        name: 'optionalId',
        identity: 'absent',
        in: 'query',
        required: false,
        schema,
      },
    ],
  };
  await proxy.execute(definition, { limit: 200 }, context, 25000, OWNER);
  assert.equal(
    seen.at(-1).pathname,
    `/api/resource/v1/${f.identity.tenantId}/${f.identity.memberId}/${f.identity.memberId}/${f.identity.deviceId}`,
  );
  assert.equal(seen.at(-1).searchParams.get('deviceId'), f.identity.deviceId);
  assert.equal(seen.at(-1).searchParams.has('optionalId'), false);
  const requests = seen.length;
  const previousExchanges = exchanges;
  for (const value of [undefined, '', randomUUID() + '/escape']) {
    await assert.rejects(
      proxy.execute(
        definition,
        {},
        { ...context, identity: { ...context.identity, deviceId: value } },
        25000,
        OWNER,
      ),
      { code: 'common.unauthenticated' },
    );
  }
  assert.equal(seen.length, requests);
  assert.equal(exchanges, previousExchanges);
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

test('catalog upload sends fixed multipart and only returns recognized corrections', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-catalog-upload-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const f = fixture();
  await login(f);
  const packageId = randomUUID();
  const manifest = Buffer.from('{"schemaVersion":1}');
  const archive = Buffer.from('fixture archive');
  const expectedDigest =
    'sha256:' + createHash('sha256').update(manifest).digest('hex');
  let rule = 'bundle_patch_missing';
  let requests = 0;
  const proxy = new BusinessProxy(
    f.broker,
    async (url, options) => {
      requests++;
      const path = new URL(url).pathname;
      if (path.endsWith('/versions')) {
        assert.equal(
          path,
          '/api/resource/v1/catalog/packages/' + packageId + '/versions',
        );
        assert.equal(options.method, 'POST');
        assert.equal(
          options.headers.Authorization,
          'Bearer ' + f.tokens.access_token,
        );
        assert.equal(options.headers['if-match'], '"1"');
        assert.equal(options.headers['idempotency-key'], 'upload');
        assert.equal(options.headers['Content-Type'], undefined);
        assert.deepEqual(
          JSON.parse(await options.body.get('metadata').text()),
          {
            version: '1.0.0',
            expectedDigest,
          },
        );
        assert.deepEqual(
          Buffer.from(await options.body.get('manifest').arrayBuffer()),
          manifest,
        );
        assert.deepEqual(
          Buffer.from(await options.body.get('artifacts').arrayBuffer()),
          archive,
        );
      }
      return Response.json(
        {
          code: 'common.validation_failed',
          detail: 'private upstream message',
          params: { rule, path: '/private/file' },
        },
        { status: 400 },
      );
    },
    directory,
  );
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const context = f.broker.guard();
  const stage = (bytes, mediaType) => {
    const upload = proxy.streams.openUpload(
      {
        sizeBytes: bytes.length,
        sha256: createHash('sha256').update(bytes).digest('hex'),
        mediaType,
      },
      context,
      OWNER,
    ).body;
    proxy.streams.command(
      {
        streamId: upload.streamId,
        shellEpoch: f.broker.epoch,
        expectedRevision: f.broker.revision,
        action: 'upload_chunk',
        sequence: 0,
        data: bytes.toString('base64'),
      },
      OWNER,
    );
    return proxy.streams.finishUpload(upload.uploadId, context, OWNER).body;
  };
  const input = () => ({
    packageId,
    headers: { 'if-match': '"1"', 'idempotency-key': 'upload' },
    body: {
      metadata: { version: '1.0.0', expectedDigest },
      manifest: stage(manifest, 'application/json'),
      artifacts: [stage(archive, 'application/gzip')],
    },
  });
  const post = call(f.broker, 'UnifiedUploadResourceVersion', input());
  const failure = await proxy.call(post, OWNER);
  assert.deepEqual(failure.error, {
    code: 'common.validation_failed',
    retryable: false,
    upstreamStatus: 400,
    rule,
  });
  assert.deepEqual(await proxy.call(post, OWNER), failure);
  assert.equal(requests, 1);
  assert.equal(
    (
      await proxy.call(
        call(f.broker, 'UnifiedGetResourcePackage', { packageId }),
        OWNER,
      )
    ).error.rule,
    undefined,
  );
  rule = '/private/file';
  assert.equal(
    (
      await proxy.call(
        call(f.broker, 'UnifiedUploadResourceVersion', input()),
        OWNER,
      )
    ).error.rule,
    undefined,
  );
});

test('BusinessProxy self-heals expired access token and transient unreachable state on demand', async (t) => {
  const f = fixture();
  await login(f);
  let refreshes = 0;
  const originalRefresh = f.platform.refresh.bind(f.platform);
  f.platform.refresh = async (...args) => {
    refreshes++;
    return originalRefresh(...args);
  };
  const proxy = new BusinessProxy(f.broker, async () =>
    Response.json({ items: [] }),
  );
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  // Simulate access token expiry while refresh_token is still valid.
  f.broker.tokenUntil = f.broker.now() - 1000;
  f.broker.tokenMonoUntil = f.broker.mono() - 1000;
  assert.equal(f.broker.snapshot().state, 'reauth_required');
  const healed = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  assert.ok(healed.output, healed.error?.code);
  assert.equal(refreshes, 1);
  assert.equal(f.broker.snapshot().state, 'authenticated');

  // Simulate transient unreachable state recovering on demand.
  f.broker.transition('unreachable', 'offline');
  assert.equal(f.broker.snapshot().state, 'unreachable');
  const recovered = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  assert.ok(recovered.output, recovered.error?.code);
  assert.equal(refreshes, 2);
  assert.equal(f.broker.snapshot().state, 'authenticated');
});

test('BusinessProxy transparently refreshes and replays safe upstream 401 calls but never replays non-idempotent POST', async (t) => {
  const f = fixture();
  await login(f);
  f.platform.audience = async () => f.tokens;
  let refreshes = 0;
  const originalRefresh = f.platform.refresh.bind(f.platform);
  f.platform.refresh = async (...args) => {
    refreshes++;
    return {
      ...(await originalRefresh(...args)),
      access_token: 'refreshed-access-' + refreshes,
    };
  };
  const seenAuth = [];
  let getAttempts = 0;
  let postNoIdemAttempts = 0;
  const proxy = new BusinessProxy(f.broker, async (url, options) => {
    seenAuth.push(options.headers.Authorization ?? options.headers.authorization);
    if (options.method === 'GET') {
      getAttempts++;
      if (getAttempts === 1) {
        return new Response(
          JSON.stringify({ error: { code: 'common.unauthenticated' } }),
          { status: 401, headers: { 'content-type': 'application/json' } },
        );
      }
      return Response.json({ items: [] });
    }
    postNoIdemAttempts++;
    return new Response(
      JSON.stringify({ error: { code: 'common.unauthenticated' } }),
      { status: 401, headers: { 'content-type': 'application/json' } },
    );
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });

  const getRes = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  assert.ok(getRes.output, getRes.error?.code);
  assert.equal(getAttempts, 2);
  assert.equal(refreshes, 1);
  assert.notEqual(seenAuth[0], seenAuth[1]);

  // Non-idempotent POST without idempotency-key must NOT replay on 401.
  const postRes = await proxy.call(
    call(f.broker, 'createAiChatCompletion', {
      body: {
        model: 'test',
        max_tokens: 16,
        messages: [{ role: 'user', content: 'hi' }],
      },
    }),
    OWNER,
  );
  assert.equal(postRes.error?.code, 'common.unauthenticated');
  assert.equal(postNoIdemAttempts, 1);
});

test('in-flight business call survives transient unreachable but aborts on explicit logout', async (t) => {
  const f = fixture();
  await login(f);
  let releaseFirst;
  let secondAborted = false;
  let callIndex = 0;
  const proxy = new BusinessProxy(f.broker, async (_url, options) => {
    const idx = ++callIndex;
    if (idx === 1) {
      await new Promise((resolve, reject) => {
        releaseFirst = resolve;
        options.signal.addEventListener('abort', () =>
          reject(new Error('aborted')),
        );
      });
      return Response.json({ items: [] });
    }
    return new Promise((_resolve, reject) => {
      options.signal.addEventListener('abort', () => {
        secondAborted = true;
        reject(new Error('aborted'));
      });
    });
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });

  const projection = structuredClone(f.broker.projection);
  const first = proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  await new Promise((r) => setTimeout(r, 10));
  // Transient unreachable + recovery should NOT abort the in-flight call.
  f.broker.transition('unreachable', 'offline');
  f.broker.transition('authenticated', undefined, projection);
  releaseFirst();
  const firstRes = await first;
  assert.ok(firstRes.output, firstRes.error?.code);

  // Explicit logout MUST abort an in-flight call immediately.
  const second = proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  await new Promise((r) => setTimeout(r, 10));
  await f.broker.logout();
  const secondRes = await second;
  assert.equal(secondAborted, true);
  assert.ok(
    ['common.unauthenticated', 'common.unavailable'].includes(
      secondRes.error?.code,
    ),
  );
});

test('BusinessProxy distinguishes network unavailable from revoked credentials during self-heal', async (t) => {
  const f = fixture();
  await login(f);
  const proxy = new BusinessProxy(f.broker, async () =>
    Response.json({ items: [] }),
  );
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });

  // Network outage during refresh -> common.unavailable (not unauthenticated).
  f.platform.refresh = async () => {
    throw Object.assign(new Error('connect ECONNREFUSED'), { status: 503 });
  };
  f.broker.transition('unreachable', 'offline');
  const offlineRes = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', {
      limit: 10,
      kind: 'skill',
    }),
    OWNER,
  );
  assert.equal(offlineRes.error?.code, 'common.unavailable');

  // Real remote revocation (401) -> common.unauthenticated and broker enters revoked.
  f.platform.refresh = async () => {
    throw Object.assign(new Error('revoked'), {
      status: 401,
      code: 'iam.invalid_token',
    });
  };
  const revokedRes = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', {
      limit: 10,
      kind: 'skill',
    }),
    OWNER,
  );
  assert.equal(revokedRes.error?.code, 'common.unauthenticated');
  assert.equal(f.broker.snapshot().state, 'revoked');
});

test('BusinessProxy refuses stale revision self-heal after account switch and preserves replay transport errors', async (t) => {
  const f = fixture();
  await login(f);
  const staleCall = call(f.broker, 'UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
  let upstreamCalls = 0;
  const proxy = new BusinessProxy(f.broker, async () => {
    upstreamCalls++;
    return Response.json({ items: [] });
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });

  // Switch to another identity (B), then drop B to unreachable and replay A's stale revision.
  await f.broker.logout();
  f.identity.memberId = randomUUID();
  await login(f);
  f.broker.transition('unreachable', 'offline');
  const staleRes = await proxy.call(staleCall, OWNER);
  assert.ok(
    ['common.conflict', 'common.unauthenticated'].includes(
      staleRes.error?.code,
    ),
  );
  assert.equal(upstreamCalls, 0);

  // Restore B and verify transport error on 401 replay maps to retryable common.unavailable.
  await f.broker.check(true);
  let attempts = 0;
  const replayProxy = new BusinessProxy(f.broker, async () => {
    attempts++;
    if (attempts === 1) {
      return new Response(
        JSON.stringify({ error: { code: 'common.unauthenticated' } }),
        { status: 401, headers: { 'content-type': 'application/json' } },
      );
    }
    throw new Error('ECONNRESET');
  });
  t.after(() => replayProxy.close());
  const replayErr = await replayProxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  assert.equal(attempts, 2);
  assert.equal(replayErr.error?.code, 'common.unavailable');
  assert.equal(replayErr.error?.retryable, true);
});

test('BusinessProxy preserves 401 mapping for managed authorities without self-heal methods', async (t) => {
  const f = fixture();
  await login(f);
  const managedAuthority = {
    epoch: f.broker.epoch,
    revision: f.broker.revision,
    platform: f.broker.platform,
    on: f.broker.on.bind(f.broker),
    off: f.broker.off.bind(f.broker),
    guard: f.broker.guard.bind(f.broker),
    assert: f.broker.assert.bind(f.broker),
    tokenFor: f.broker.tokenFor.bind(f.broker),
  };
  const proxy = new BusinessProxy(
    managedAuthority,
    async () =>
      new Response(
        JSON.stringify({ error: { code: 'common.unauthenticated' } }),
        { status: 401, headers: { 'content-type': 'application/json' } },
      ),
  );
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const res = await proxy.call(
    call(f.broker, 'UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    OWNER,
  );
  assert.equal(res.error?.code, 'common.unauthenticated');
});

test('BusinessProxy enforces absolute expiresAt after AI token exchange, maps AI exchange 5xx to unavailable, and rejects stale cached SSE stream', async (t) => {
  const f = fixture();
  await login(f);
  let upstreamCalls = 0;
  const proxy = new BusinessProxy(f.broker, async () => {
    upstreamCalls++;
    return new Response(
      JSON.stringify({ error: { code: 'common.unauthenticated' } }),
      { status: 401, headers: { 'content-type': 'application/json' } },
    );
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });

  // 1) AI audience token exchange exceeds expiresAt -> never hits upstream.
  f.platform.audience = async () => {
    await new Promise((r) => setTimeout(r, 80));
    return f.tokens;
  };
  const shortReq = {
    ...call(f.broker, 'listAiModels', {}),
    expiresAt: new Date(Date.now() + 30).toISOString(),
  };
  const expiredRes = await proxy.call(shortReq, OWNER);
  assert.equal(expiredRes.error?.code, 'common.unavailable');
  assert.equal(expiredRes.error?.retryable, true);
  assert.equal(upstreamCalls, 0);

  // 2) Upstream 401 on GET listAiModels -> refresh succeeds, AI audience exchange hits 503 -> common.unavailable (retryable).
  let audienceCount = 0;
  f.platform.audience = async () => {
    audienceCount++;
    if (audienceCount === 1) return f.tokens;
    throw Object.assign(new Error('iam 503'), { status: 503 });
  };
  const ai503Res = await proxy.call(call(f.broker, 'listAiModels', {}), OWNER);
  assert.equal(ai503Res.error?.code, 'common.unavailable');
  assert.equal(ai503Res.error?.retryable, true);

  // 3) Cached SSE stream request must not return a closed stream ID after same-identity revision advance.
  f.platform.audience = async () => f.tokens;
  const sseProxy = new BusinessProxy(
    f.broker,
    async () =>
      new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(Buffer.from('data: {"ok":true}\n\ndata: [DONE]\n\n'));
            controller.close();
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } },
      ),
  );
  t.after(() => sseProxy.close());
  const sseReq = call(f.broker, 'createAiChatCompletionStream', {
    body: {
      model: 'test',
      max_tokens: 16,
      messages: [{ role: 'user', content: 'hi' }],
      stream: true,
    },
  });
  const firstSse = await sseProxy.call(sseReq, OWNER);
  assert.ok(firstSse.output?.body?.streamId);
  const projection = structuredClone(f.broker.projection);
  f.broker.transition('authenticated', undefined, projection);
  const cachedSse = await sseProxy.call(sseReq, OWNER);
  assert.equal(cachedSse.error?.code, 'common.unauthenticated');
});
