import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { randomBytes, randomUUID } from 'node:crypto';
import {
  mkdtemp,
  readFile,
  writeFile,
  mkdir,
  rename,
  rm,
} from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createPlatform, platformConfig } from '../src/local/platform.js';
import { createVault } from '../src/local/vault.js';
import { createTeam } from '../src/local/team.js';

const names = [
  'auriga_list_endpoints',
  'auriga_open_thread',
  'auriga_reply_thread',
  'auriga_read_thread',
];
const proof = () => randomBytes(24).toString('base64url');

// Strict transport/core-contract peer and host-controller boundary doubles.
// No IAM/JWT or actual rc.3 end-to-end success is claimed by these unit tests.
async function conversation() {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-thread-map-'));
  const endpoints = new Map(),
    bindings = new Map(),
    threads = new Map(),
    deliveries = new Map(),
    dedup = new Map();
  const metrics = { opens: 0, acks: 0, failNextAck: false };
  const server = createServer(async (req, res) => {
    const send = (status, value) => {
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(value));
    };
    try {
      if (
        req.method !== 'POST' ||
        req.headers.origin !== undefined ||
        req.headers['content-type'] !== 'application/json'
      )
        return send(400, { code: 'common.validation_failed' });
      const token = req.headers.authorization?.slice(7),
        endpoint = endpoints.get(token);
      if (!endpoint) return send(401, { code: 'common.unauthenticated' });
      let raw = '';
      for await (const chunk of req) raw += chunk;
      const body = JSON.parse(raw);
      if (req.url === '/ai/mcp') {
        const media = req.headers.accept?.split(',').map((v) => v.trim());
        if (
          !media?.includes('application/json') ||
          !media.includes('text/event-stream')
        )
          return send(406, {});
        if (
          req.headers['mcp-protocol-version'] !== '2025-06-18' ||
          body.jsonrpc !== '2.0'
        )
          return send(400, {});
        const result = (value) =>
          send(200, { jsonrpc: '2.0', id: body.id, result: value });
        if (body.method === 'initialize')
          return result({
            protocolVersion: '2025-06-18',
            capabilities: { tools: {} },
            serverInfo: { name: 'contract-peer', version: '1' },
          });
        if (body.method === 'notifications/initialized') {
          if (Object.hasOwn(body, 'id')) return send(400, {});
          res.writeHead(202);
          return res.end();
        }
        if (body.method === 'tools/list')
          return result({
            tools: names.map((name) => ({
              name,
              inputSchema: { type: 'object' },
            })),
          });
        if (body.method !== 'tools/call') return send(400, {});
        const source = bindings.get(req.headers['auriga-binding-id']),
          params = body.params;
        if (
          !source ||
          source.endpointId !== endpoint.id ||
          req.headers['auriga-endpoint-id'] !== endpoint.id ||
          source.hostSessionId !== params?._meta?.['auriga/sessionId']
        )
          return send(403, {});
        const args = params.arguments,
          key = source.id + ':' + params.name + ':' + args.idempotencyKey;
        let message = dedup.get(key);
        if (!message) {
          let thread;
          if (params.name === 'auriga_open_thread') {
            if (
              ![...endpoints.values()].some(
                (e) => e.id === args.targetEndpointId,
              ) ||
              !args.text
            )
              return send(400, {});
            thread = {
              id: randomUUID(),
              participants: new Map([
                [endpoint.id, source.hostSessionId],
                [args.targetEndpointId, null],
              ]),
              messages: [],
            };
            threads.set(thread.id, thread);
            metrics.opens++;
          } else if (params.name === 'auriga_reply_thread') {
            thread = threads.get(args.threadId);
            if (
              !thread ||
              thread.participants.get(endpoint.id) !== source.hostSessionId
            )
              return send(403, {});
          } else return send(400, {});
          message = {
            id: randomUUID(),
            threadId: thread.id,
            sequence: String(thread.messages.length + 1),
            authorEndpointId: endpoint.id,
            text: args.text,
            createdAt: new Date().toISOString(),
          };
          thread.messages.push(message);
          dedup.set(key, message);
          for (const target of thread.participants.keys())
            if (target !== endpoint.id) {
              const id = randomUUID();
              deliveries.set(id, {
                id,
                message,
                targetEndpointId: target,
                status: 'pending',
              });
            }
        }
        const payload = { message };
        return result({
          structuredContent: payload,
          content: [{ type: 'text', text: JSON.stringify(payload) }],
        });
      }
      if (req.url === '/ai/team/node/register') {
        if (body.receiveEnabled !== true) return send(400, {});
        return send(200, {
          endpoint: {
            id: endpoint.id,
            label: body.label,
            receiveEnabled: true,
          },
        });
      }
      if (body.endpointId !== endpoint.id) return send(403, {});
      if (req.url === '/ai/team/node/bind') {
        const thread = body.threadId && threads.get(body.threadId);
        if (body.threadId && (!thread || !thread.participants.has(endpoint.id)))
          return send(403, {});
        if (
          thread &&
          thread.participants.get(endpoint.id) !== null &&
          thread.participants.get(endpoint.id) !== body.hostSessionId
        )
          return send(409, { code: 'common.conflict' });
        let binding = [...bindings.values()].find(
          (b) =>
            b.endpointId === endpoint.id &&
            b.hostSessionId === body.hostSessionId,
        );
        if (!binding) {
          binding = {
            id: randomUUID(),
            endpointId: endpoint.id,
            hostSessionId: body.hostSessionId,
          };
          bindings.set(binding.id, binding);
        }
        if (thread) thread.participants.set(endpoint.id, body.hostSessionId);
        return send(200, { binding });
      }
      if (req.url === '/ai/team/node/pull') {
        const items = [...deliveries.values()]
          .filter(
            (d) =>
              d.targetEndpointId === endpoint.id &&
              d.status !== 'host_accepted',
          )
          .map((d) => {
            d.leaseToken = proof();
            d.leaseExpiresAt = new Date(Date.now() + 60000).toISOString();
            d.status = 'leased';
            return d;
          });
        return send(200, { page: { items } });
      }
      if (req.url === '/ai/team/node/ack') {
        const delivery = deliveries.get(body.deliveryId),
          binding = bindings.get(body.bindingId),
          thread = delivery && threads.get(delivery.message.threadId);
        if (
          !delivery ||
          delivery.targetEndpointId !== endpoint.id ||
          delivery.leaseToken !== body.leaseToken ||
          body.hostRequestId !== delivery.id ||
          binding?.endpointId !== endpoint.id ||
          binding.hostSessionId !== body.hostSessionId ||
          thread?.participants.get(endpoint.id) !== body.hostSessionId
        )
          return send(409, {});
        if (metrics.failNextAck) {
          metrics.failNextAck = false;
          return send(503, { code: 'common.unavailable' });
        }
        delivery.status = 'host_accepted';
        metrics.acks++;
        return send(200, { delivery });
      }
      return send(404, {});
    } catch {
      return send(500, { code: 'common.unavailable' });
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port,
    nodes = [];
  async function node(label) {
    const token = proof(),
      id = randomUUID(),
      path = join(directory, label);
    endpoints.set(token, { id });
    const vault = await createVault(path),
      sessions = new Map(),
      admitted = [],
      promptCalls = [],
      createCalls = [];
    const ctx = {
      tools: {
        register() {
          return () => {};
        },
      },
      sessionController: {
        async create({ sessionId }) {
          createCalls.push(sessionId);
          if (!sessions.has(sessionId))
            sessions.set(sessionId, { id: sessionId, queue: [] });
          return { sessionId };
        },
        async prompt(request) {
          const session = sessions.get(request.sessionId);
          if (!session || request.mode !== 'queue')
            throw new Error('invalid host prompt');
          promptCalls.push(request);
          if (!session.queue.some((q) => q.requestId === request.requestId)) {
            session.queue.push(request);
            admitted.push(request);
          }
          return { accepted: true };
        },
      },
      sessions: {
        get: (id) => sessions.get(id),
        async flush(session) {
          if (sessions.get(session.id) !== session) return false;
          await writeFile(
            join(path, session.id + '.queue.json'),
            JSON.stringify(session.queue),
          );
          return true;
        },
      },
    };
    const platform = createPlatform(
      platformConfig({
        platformOrigin: origin,
        issuer: origin,
        clientId: 'transport-probe',
        providerId: 'team',
        allowLocalHttp: true,
      }),
    );
    // A mutable response adapter is used only by explicit malformed-response tests.
    let alter = (value) => value;
    const transport = {
      ...platform,
      async request(route, options) {
        return alter(await platform.request(route, options), route, options);
      },
    };
    const team = createTeam({
      platform: transport,
      vault,
      getAccess: async () => token,
      assertActive() {},
      ctx,
      label,
    });
    await team.start();
    const original = randomUUID();
    await ctx.sessionController.create({ sessionId: original });
    const exec = (sessionId) => ({
      agent: { session: ctx.sessions.get(sessionId) },
      callId: proof(),
      rootCallId: proof(),
      signal: new AbortController().signal,
    });
    const result = {
      team,
      vault,
      ctx,
      id,
      path,
      original,
      sessions,
      admitted,
      promptCalls,
      createCalls,
      exec,
      reconnect: (nextVault) =>
        createTeam({
          platform: transport,
          vault: nextVault,
          getAccess: async () => token,
          assertActive() {},
          ctx,
          label,
        }),
      alter: (fn) => {
        alter = fn;
      },
    };
    nodes.push(result);
    return result;
  }
  return {
    node,
    metrics,
    deliveries,
    async close() {
      for (const n of nodes) {
        n.team.close();
        n.vault.close();
      }
      await new Promise((resolve) => server.close(resolve));
      await rm(directory, { recursive: true, force: true });
    },
  };
}

test('A open → B receive/reply → A receive reuses the original source session without preseeded thread mapping', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    assert.deepEqual(a.vault.snapshot().threads, {});
    const result = await a.team.toolCall(
      'auriga_open_thread',
      { targetEndpointId: b.id, text: 'Hello', idempotencyKey: 'open-1' },
      a.exec(a.original),
    );
    const threadId = result.structuredContent.message.threadId,
      mapping = a.vault.snapshot().threads[threadId];
    assert.equal(mapping.sessionId, a.original);
    assert.ok(mapping.bindingId);
    await b.team.poll();
    const receiver = b.admitted[0].sessionId;
    assert.notEqual(receiver, b.original);
    assert.equal(b.admitted.length, 1);
    const received = b.admitted[0].content,
      context = JSON.parse(
        received[0].text.slice(received[0].text.indexOf('\n') + 1),
      );
    assert.deepEqual(Object.keys(context).sort(), ['messageId', 'threadId']);
    assert.equal(context.threadId, threadId);
    assert.equal(context.messageId, result.structuredContent.message.id);
    assert.equal(received[1].text, 'Hello');
    assert.equal(received.length, 2);
    await b.team.toolCall(
      'auriga_reply_thread',
      { threadId: context.threadId, text: 'Reply', idempotencyKey: 'reply-1' },
      b.exec(receiver),
    );
    run.metrics.failNextAck = true;
    await assert.rejects(a.team.poll(), (error) => error.status === 503);
    assert.equal(a.admitted.length, 1);
    assert.equal(a.admitted[0].sessionId, a.original);
    assert.equal(a.sessions.size, 1);
    await a.team.poll();
    assert.equal(a.admitted.length, 1);
    assert.equal(a.promptCalls.length, 1);
    assert.deepEqual(a.createCalls, [a.original]);
    assert.equal(run.metrics.acks, 2);
    const queue = JSON.parse(
      await readFile(join(a.path, a.original + '.queue.json'), 'utf8'),
    );
    assert.equal(queue.length, 1);
    assert.equal(queue[0].requestId, a.admitted[0].requestId);
    await a.team.toolCall(
      'auriga_reply_thread',
      { threadId, text: 'Second input for B', idempotencyKey: 'reply-2' },
      a.exec(a.original),
    );
    await b.team.poll();
    assert.equal(b.admitted.length, 2);
    assert.equal(b.admitted[1].sessionId, receiver);
    assert.deepEqual(b.createCalls, [b.original, receiver]);
    assert.equal(run.metrics.acks, 3);
  } finally {
    await run.close();
  }
});

test('new receiver without durable native session cannot publish a binding or ACK', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    const opened = await a.team.toolCall(
      'auriga_open_thread',
      {
        targetEndpointId: b.id,
        text: 'Not durable',
        idempotencyKey: 'no-native-flush',
      },
      a.exec(a.original),
    );
    b.ctx.sessions.flush = async () => false;
    await assert.rejects(
      b.team.poll(),
      /created DSH session persistence unavailable/,
    );
    const thread =
      b.vault.snapshot().threads[opened.structuredContent.message.threadId];
    assert.equal(thread.bindingId, null);
    assert.deepEqual(b.vault.snapshot().deliveries, {});
    assert.equal(b.promptCalls.length, 0);
    assert.equal(run.metrics.acks, 0);
  } finally {
    await run.close();
  }
});

test('fast reply before open response defers unknown mapping, then reaches the original source on retry', async () => {
  const run = await conversation(),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  let opening;
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    a.alter(async (value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.name === 'auriga_open_thread'
      ) {
        entered.resolve(value.result.structuredContent.message.threadId);
        await release.promise;
      }
      return value;
    });
    opening = a.team.toolCall(
      'auriga_open_thread',
      {
        targetEndpointId: b.id,
        text: 'Fast reply',
        idempotencyKey: 'fast-open',
      },
      a.exec(a.original),
    );
    const settledOpen = opening.catch(() => {});
    const threadId = await entered.promise;
    await b.team.poll();
    await b.team.toolCall(
      'auriga_reply_thread',
      {
        threadId,
        text: 'Before open returns',
        idempotencyKey: 'fast-reply',
      },
      b.exec(b.admitted[0].sessionId),
    );
    const incoming = [...run.deliveries.values()].find(
      (d) => d.targetEndpointId === a.id,
    );
    incoming.leaseToken = proof();
    incoming.leaseExpiresAt = new Date(Date.now() + 60000).toISOString();
    await assert.rejects(a.team.receive(incoming));
    assert.equal(a.vault.snapshot().threads[threadId], undefined);
    assert.deepEqual(a.createCalls, [a.original]);
    release.resolve();
    await opening;
    await settledOpen;
    await a.team.poll();
    assert.equal(a.admitted[0].sessionId, a.original);
    assert.deepEqual(a.createCalls, [a.original]);
    assert.equal(run.metrics.opens, 1);
  } finally {
    release.resolve();
    await opening?.catch(() => {});
    await run.close();
  }
});

test('lost open response persists source intent across restart and same-key retry recovers without random binding', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    const args = {
      targetEndpointId: b.id,
      text: 'Lost response',
      idempotencyKey: 'lost-open',
    };
    a.alter((value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.name === 'auriga_open_thread'
      )
        throw new Error('response lost');
      return value;
    });
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
      /response lost/,
    );
    a.team.close();
    a.vault.close();
    a.vault = await createVault(a.path);
    // Keep the same authenticated transport/controller; restart product Team/vault only.
    a.alter((value) => value);
    a.team = a.reconnect(a.vault);
    await a.team.start();
    await b.team.poll();
    const threadId = JSON.parse(
      b.admitted[0].content[0].text.split('\n')[1],
    ).threadId;
    await b.team.toolCall(
      'auriga_reply_thread',
      {
        threadId,
        text: 'Reply after restart',
        idempotencyKey: 'restart-reply',
      },
      b.exec(b.admitted[0].sessionId),
    );
    await a.team.poll().catch(() => {});
    assert.equal(a.vault.snapshot().threads[threadId], undefined);
    assert.deepEqual(a.createCalls, [a.original]);
    await a.team.toolCall('auriga_open_thread', args, a.exec(a.original));
    await a.team.poll();
    assert.equal(a.admitted[0].sessionId, a.original);
    assert.equal(run.metrics.opens, 1);
    assert.deepEqual(a.vault.snapshot().pendingOpens, {});
  } finally {
    await run.close();
  }
});

test('aborted or abandoned open and a new-key retry do not block first messages from the same peer or a third peer', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B'),
      c = await run.node('C');
    a.alter((value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.arguments?.idempotencyKey === 'abandoned'
      )
        throw new Error('aborted after uncertain remote commit');
      return value;
    });
    await assert.rejects(
      a.team.toolCall(
        'auriga_open_thread',
        {
          targetEndpointId: b.id,
          text: 'Uncertain',
          idempotencyKey: 'abandoned',
        },
        a.exec(a.original),
      ),
      /aborted/,
    );
    await a.team.toolCall(
      'auriga_open_thread',
      {
        targetEndpointId: b.id,
        text: 'Retry with new key',
        idempotencyKey: 'new-key',
      },
      a.exec(a.original),
    );
    for (const peer of [b, c])
      await peer.team.toolCall(
        'auriga_open_thread',
        {
          targetEndpointId: a.id,
          text: 'Independent incoming',
          idempotencyKey: 'incoming',
        },
        peer.exec(peer.original),
      );
    await a.team.poll();
    assert.equal(a.admitted.length, 2);
    assert.equal(Object.keys(a.vault.snapshot().pendingOpens).length, 1);
    assert.equal(run.metrics.acks, 2);
  } finally {
    await run.close();
  }
});

test('source intent deadline is fixed across retries and expires to bounded unknown outcomes after restart', async (t) => {
  const run = await conversation();
  let now = Date.now();
  t.mock.method(Date, 'now', () => now);
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    a.alter((value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.name === 'auriga_open_thread'
      )
        throw new Error('uncertain response');
      return value;
    });
    const args = {
      targetEndpointId: b.id,
      text: 'Unknown outcome',
      idempotencyKey: 'expiring',
    };
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
      /uncertain/,
    );
    const [key, first] = Object.entries(a.vault.snapshot().pendingOpens)[0];
    assert.ok(first.expiresAt > now && first.expiresAt <= now + 60000);
    now += 1000;
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
      /uncertain/,
    );
    assert.equal(
      a.vault.snapshot().pendingOpens[key].expiresAt,
      first.expiresAt,
    );
    a.team.close();
    a.vault.close();
    a.vault = await createVault(a.path);
    a.team = a.reconnect(a.vault);
    now = first.expiresAt + 1;
    await a.team.start();
    await a.team.poll();
    assert.deepEqual(a.vault.snapshot().pendingOpens, {});
    assert.equal(a.vault.snapshot().openOutcomes[key].status, 'result_unknown');
    await b.team.toolCall(
      'auriga_open_thread',
      {
        targetEndpointId: a.id,
        text: 'After expiry',
        idempotencyKey: 'incoming',
      },
      b.exec(b.original),
    );
    await a.team.poll();
    assert.equal(a.admitted.length, 1);
    await a.vault.update((s) => {
      for (let i = 0; i < 300; i++)
        s.pendingOpens['expired-' + i] = { ...first, expiresAt: now - 1 };
    });
    await a.team.poll();
    assert.equal(Object.keys(a.vault.snapshot().openOutcomes).length, 256);
    assert.deepEqual(a.vault.snapshot().pendingOpens, {});
  } finally {
    await run.close();
  }
});

test('same-key isError keeps uncertain source protection and expiry never guesses a random source session', async (t) => {
  const run = await conversation();
  let now = Date.now();
  t.mock.method(Date, 'now', () => now);
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    const args = {
      targetEndpointId: b.id,
      text: 'Original open',
      idempotencyKey: 'same-key-error',
    };
    a.alter((value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.name === 'auriga_open_thread'
      )
        throw new Error('response lost');
      return value;
    });
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
      /response lost/,
    );
    a.alter((value, route, options) =>
      route === '/ai/mcp' && options.body?.params?.name === 'auriga_open_thread'
        ? { ...value, result: { isError: true } }
        : value,
    );
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
      /tool rejected/,
    );
    assert.equal(Object.keys(a.vault.snapshot().pendingOpens).length, 1);
    await b.team.poll();
    const threadId = JSON.parse(
      b.admitted[0].content[0].text.split('\n')[1],
    ).threadId;
    await b.team.toolCall(
      'auriga_reply_thread',
      { threadId, text: 'Late reply', idempotencyKey: 'late' },
      b.exec(b.admitted[0].sessionId),
    );
    now += 60001;
    await assert.rejects(a.team.poll(), /source.*(unknown|unavailable)/);
    assert.equal(a.vault.snapshot().threads[threadId], undefined);
    assert.deepEqual(a.createCalls, [a.original]);
    a.alter((value) => value);
    await a.team.toolCall('auriga_open_thread', args, a.exec(a.original));
    await a.team.poll();
    assert.equal(a.admitted[0].sessionId, a.original);
    assert.equal(run.metrics.opens, 1);
  } finally {
    await run.close();
  }
});

test('unsupported first delivery is terminal without ACK while ordinary same-page delivery proceeds and re-leases stay isolated', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    const threads = [];
    for (const idempotencyKey of ['unsupported', 'ordinary']) {
      const opened = await a.team.toolCall(
        'auriga_open_thread',
        { targetEndpointId: b.id, text: idempotencyKey, idempotencyKey },
        a.exec(a.original),
      );
      threads.push(opened.structuredContent.message.threadId);
    }
    await b.team.poll();
    const unsupported = b.admitted[0].sessionId;
    b.sessions.get(unsupported).header = { origin: 'subagent' };
    // A late/cold native resolver can discover unsupported ownership at prompt time.
    const prompt = b.ctx.sessionController.prompt;
    let attempts = 0;
    b.ctx.sessionController.prompt = async (request) => {
      if (request.sessionId === unsupported) {
        attempts++;
        throw Object.assign(new Error('ordinary routing rejects subagent'), {
          code: 'session/agent-busy',
        });
      }
      return prompt(request);
    };
    for (const threadId of threads)
      await a.team.toolCall(
        'auriga_reply_thread',
        { threadId, text: 'Again', idempotencyKey: threadId },
        a.exec(a.original),
      );
    await b.team.poll();
    const failed = [...run.deliveries.values()].find(
      (d) => d.message.threadId === threads[0] && d.message.text === 'Again',
    );
    const ordinary = [...run.deliveries.values()].find(
      (d) => d.message.threadId === threads[1] && d.message.text === 'Again',
    );
    assert.equal(
      b.vault.snapshot().deliveries[failed.id].status,
      'unsupported',
    );
    assert.equal(b.vault.snapshot().deliveries[failed.id].accepted, false);
    assert.notEqual(failed.status, 'host_accepted');
    assert.equal(ordinary.status, 'host_accepted');
    assert.equal(b.admitted.length, 3);
    await b.team.poll();
    assert.equal(attempts, 0);
    assert.equal(b.admitted.length, 3);
    await assert.rejects(
      b.team.toolCall(
        'auriga_open_thread',
        {
          targetEndpointId: a.id,
          text: 'Child send',
          idempotencyKey: 'child-send',
        },
        b.exec(unsupported),
      ),
      /unsupported.*origin/,
    );
  } finally {
    await run.close();
  }
});

test('remote open committed but actual vault write failed: same idempotency key safely recovers its original source mapping', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B'),
      args = {
        targetEndpointId: b.id,
        text: 'Hello',
        idempotencyKey: 'disk-retry',
      };
    const file = join(a.path, 'connect.json'),
      saved = join(a.path, 'preserved.json');
    a.alter(async (value, route, options) => {
      if (
        route === '/ai/mcp' &&
        options.body?.params?.name === 'auriga_open_thread'
      ) {
        await rename(file, saved);
        await mkdir(file);
      }
      return value;
    });
    await assert.rejects(
      a.team.toolCall('auriga_open_thread', args, a.exec(a.original)),
    );
    assert.deepEqual(a.vault.snapshot().threads, {});
    assert.equal(run.metrics.opens, 1);
    await rm(file, { recursive: true });
    await rename(saved, file);
    a.alter((value) => value);
    const retry = await a.team.toolCall(
      'auriga_open_thread',
      args,
      a.exec(a.original),
    );
    assert.equal(run.metrics.opens, 1);
    assert.equal(
      a.vault.snapshot().threads[retry.structuredContent.message.threadId]
        .sessionId,
      a.original,
    );
  } finally {
    await run.close();
  }
});

test('cold subagent rejection stays terminal after restart while a transient ordinary failure remains retryable', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    const ids = [];
    for (const idempotencyKey of ['cold-child', 'temporary', 'healthy']) {
      const opened = await a.team.toolCall(
        'auriga_open_thread',
        { targetEndpointId: b.id, text: idempotencyKey, idempotencyKey },
        a.exec(a.original),
      );
      ids.push(opened.structuredContent.message.threadId);
    }
    await b.team.poll();
    const [child, temporary] = b.admitted.map((r) => r.sessionId);
    const prompt = b.ctx.sessionController.prompt;
    let childCalls = 0,
      temporaryCalls = 0;
    b.ctx.sessionController.prompt = async (request) => {
      if (request.sessionId === child) {
        childCalls++;
        b.sessions.get(child).header = { origin: 'subagent' };
        throw Object.assign(new Error('cold subagent ownership'), {
          code: 'session/agent-busy',
        });
      }
      if (request.sessionId === temporary && temporaryCalls++ === 0)
        throw Object.assign(new Error('ordinary agent temporarily busy'), {
          code: 'session/agent-busy',
        });
      return prompt(request);
    };
    for (const threadId of ids)
      await a.team.toolCall(
        'auriga_reply_thread',
        { threadId, text: 'After cold restore', idempotencyKey: threadId },
        a.exec(a.original),
      );
    await assert.rejects(b.team.poll(), /temporarily busy/);
    assert.equal(b.admitted.length, 4);
    const failed = [...run.deliveries.values()].find(
      (d) =>
        d.message.threadId === ids[0] &&
        d.message.text === 'After cold restore',
    );
    assert.equal(
      b.vault.snapshot().deliveries[failed.id].status,
      'unsupported',
    );
    b.team.close();
    b.vault.close();
    b.vault = await createVault(b.path);
    b.team = b.reconnect(b.vault);
    await b.team.start();
    await b.team.poll();
    assert.equal(childCalls, 1);
    assert.equal(temporaryCalls, 2);
    assert.equal(b.admitted.length, 5);
    assert.notEqual(failed.status, 'host_accepted');
  } finally {
    await run.close();
  }
});

test('global 401 or 403 aborts a pull page before unrelated deliveries are submitted', async () => {
  for (const status of [401, 403]) {
    const run = await conversation();
    try {
      const a = await run.node('A'),
        b = await run.node('B');
      for (const idempotencyKey of ['first', 'second'])
        await a.team.toolCall(
          'auriga_open_thread',
          { targetEndpointId: b.id, text: idempotencyKey, idempotencyKey },
          a.exec(a.original),
        );
      b.alter((value, route) => {
        if (route === '/ai/team/node/bind')
          throw Object.assign(new Error('global authorization invalid'), {
            status,
          });
        return value;
      });
      await assert.rejects(b.team.poll(), (error) => error.status === status);
      assert.equal(b.createCalls.length, 2);
      assert.equal(b.promptCalls.length, 0);
      assert.equal(run.metrics.acks, 0);
    } finally {
      await run.close();
    }
  }
});

test('different trusted session or binding cannot overwrite an established source mapping', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B'),
      args = {
        targetEndpointId: b.id,
        text: 'Hello',
        idempotencyKey: 'original',
      };
    const first = await a.team.toolCall(
        'auriga_open_thread',
        args,
        a.exec(a.original),
      ),
      threadId = first.structuredContent.message.threadId,
      before = a.vault.snapshot().threads[threadId];
    const other = randomUUID();
    await a.ctx.sessionController.create({ sessionId: other });
    a.alter((value, route, options) =>
      route === '/ai/mcp' && options.body?.method === 'tools/call'
        ? { ...value, result: first }
        : value,
    );
    await assert.rejects(
      a.team.toolCall(
        'auriga_open_thread',
        { ...args, idempotencyKey: 'other' },
        a.exec(other),
      ),
      /source thread binding conflict/,
    );
    assert.deepEqual(a.vault.snapshot().threads[threadId], before);
  } finally {
    await run.close();
  }
});

test('malformed remote UUID or foreign author endpoint cannot publish a source mapping', async () => {
  const run = await conversation();
  try {
    const a = await run.node('A'),
      b = await run.node('B');
    for (const corrupt of [
      (m) => {
        m.id = 'invalid';
      },
      (m) => {
        m.threadId = 'invalid';
      },
      (m) => {
        m.authorEndpointId = b.id;
      },
    ]) {
      a.alter((value, route, options) => {
        if (route === '/ai/mcp' && options.body?.method === 'tools/call')
          corrupt(value.result.structuredContent.message);
        return value;
      });
      await assert.rejects(
        a.team.toolCall(
          'auriga_open_thread',
          { targetEndpointId: b.id, text: 'Hello', idempotencyKey: proof() },
          a.exec(a.original),
        ),
        /invalid Team source thread response/,
      );
      assert.deepEqual(a.vault.snapshot().threads, {});
    }
  } finally {
    await run.close();
  }
});
