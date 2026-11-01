import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { randomUUID } from 'node:crypto';
import { verifyVirtualKeys, allPages } from './relay-backend.mjs';
import { request, traceState } from './run.mjs';

for (const upstreamMode of ['fixture', 'live']) {
  for (const fault of ['none', 'budget-code', 'call', 'cleanup']) {
    test(`新后端外部 Key 验收与清理：${upstreamMode}/${fault}`, async (t) => {
      const records = new Map(),
        secrets = new Map();
      let issued = 0,
        revoked = 0;
      const server = http.createServer(async (req, res) => {
        let raw = '';
        for await (const chunk of req) raw += chunk;
        const body = raw ? JSON.parse(raw) : {};
        const send = (value, status = 200) => {
          res.writeHead(status, { 'content-type': 'application/json' });
          res.end(JSON.stringify(value));
        };
        const url = new URL(req.url, 'http://localhost');
        if (url.pathname === '/virtual-keys' && req.method === 'POST') {
          const id = randomUUID(),
            secret = 'test-secret-' + randomUUID();
          issued++;
          records.set(id, { input: body, active: true, calls: 0 });
          secrets.set(secret, id);
          return send({ key: { id }, secret });
        }
        if (url.pathname.startsWith('/virtual-keys/')) {
          if (fault === 'cleanup') return send({}, 503);
          const row = records.get(url.pathname.split('/').at(-1));
          row.active = body.active;
          revoked++;
          return send({});
        }
        if (url.pathname === '/logs') {
          assert.ok(records.has(url.searchParams.get('keyId')));
          return send({
            items: [
              {
                requestId: randomUUID(),
                inputTokens: 1,
                outputTokens: 2,
                frozenMicros: 0,
              },
            ],
          });
        }
        assert.equal(url.pathname, '/ai/v1/chat/completions');
        const id = secrets.get(req.headers.authorization?.slice(7)),
          row = records.get(id);
        if (!row?.active) return send({ code: 'common.forbidden' }, 403);
        if (fault === 'call' || fault === 'cleanup')
          return send({ detail: 'private-secret' }, 502);
        if (row.input.monthlyBudgetMicros === 0)
          return send(
            {
              code:
                fault === 'budget-code'
                  ? 'common.forbidden'
                  : 'ai.budget_exceeded',
            },
            429,
          );
        if (row.calls++) return send({ code: 'common.rate_limited' }, 429);
        send({ choices: [{}], usage: { total_tokens: 3 } });
      });
      await new Promise((done) => server.listen(0, '127.0.0.1', done));
      t.after(() => {
        server.closeAllConnections();
        return new Promise((done) => server.close(done));
      });
      const origin = 'http://127.0.0.1:' + server.address().port;
      const relay = {
        baseUrl: origin + '/',
        publicBaseUrl: origin + '/ai/v1',
        upstreamMode,
        allowModelCalls: true,
        models: [
          {
            name: 'pool',
            provider: 'test',
            testOnly: upstreamMode === 'fixture' ? true : undefined,
            apiBase: 'https://models.example.com',
            upstreamModel: 'model',
          },
        ],
      };
      const run = () => verifyVirtualKeys(relay, traceState(), request);
      if (fault === 'none') {
        assert.equal(await run(), upstreamMode);
        assert.equal(issued, 2);
        assert.equal(revoked, 2);
      } else {
        await assert.rejects(
          run(),
          (error) => !error.message.includes('private-secret'),
        );
        assert.ok(issued > 0);
        if (fault !== 'cleanup')
          assert.ok([...records.values()].every((row) => !row.active));
      }
    });
  }
}
test('列表超过单页能读完且不重复游标死循环', async () => {
  let calls = 0;
  const rows = await allPages(async (path) => {
    calls++;
    return path.includes('cursor=')
      ? { items: [2] }
      : { items: [1], nextCursor: 'next' };
  }, 'logs?accountId=member');
  assert.deepEqual(rows, [1, 2]);
  assert.equal(calls, 2);
  await assert.rejects(
    allPages(async () => ({ items: [], nextCursor: 'same' }), 'providers'),
  );
});
test('缺少调用授权、公开入口或模型时不得签发 Key', async () => {
  let calls = 0;
  const api = async () => {
    calls++;
  };
  for (const relay of [
    {},
    {
      baseUrl: 'http://localhost/',
      publicBaseUrl: 'http://localhost/ai/v1/',
      models: [{ name: 'pool' }],
    },
    {
      baseUrl: 'http://localhost/',
      allowModelCalls: true,
      models: [{ name: 'pool' }],
    },
  ])
    await assert.rejects(verifyVirtualKeys(relay, traceState(), api));
  assert.equal(calls, 0);
});
