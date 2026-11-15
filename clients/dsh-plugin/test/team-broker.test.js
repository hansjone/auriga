import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createTeam } from '../src/local/team.js';
import { createVault } from '../src/local/vault.js';
import { nativeContexts } from './host-context-fixture.js';
import { validate } from './broker-fixture.js';

async function fixture(t) {
  const f = await nativeContexts(t),
    directory = await mkdtemp(join(tmpdir(), 'auriga-team-broker-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const vault = await createVault(directory);
  t.after(() => vault.close());
  const calls = [],
    streams = new Map(),
    endpointId = randomUUID(),
    bindingId = randomUUID(),
    tools = new Map();
  const names = [
    'auriga_list_endpoints',
    'auriga_open_thread',
    'auriga_reply_thread',
    'auriga_read_thread',
  ];
  const stream = (body) => {
    const streamId = randomUUID();
    streams.set(streamId, Buffer.from(JSON.stringify(body)));
    return { statusCode: 200, body: { streamId } };
  };
  const broker = {
    revoke: f.broker.revoke,
    async call(operation, input) {
      const s = f.connection.snapshot();
      validate('ShellAuthBusinessCall', {
        protocolVersion: 1,
        requestId: randomUUID(),
        shellEpoch: s.shellEpoch,
        expectedRevision: s.revision,
        expiresAt: new Date(Date.now() + 10000).toISOString(),
        call: { operation, input },
      });
      calls.push({ operation, input });
      if (operation === 'team_initialize')
        return stream({
          jsonrpc: '2.0',
          id: randomUUID(),
          result: { protocolVersion: '2025-06-18' },
        });
      if (operation === 'team_list_tools')
        return {
          statusCode: 200,
          body: {
            tools: names.map((name) => ({
              name,
              description: name,
              inputSchema: { type: 'object' },
            })),
          },
        };
      if (operation === 'team_node_register')
        return stream({ endpoint: { id: endpointId, revoked: false } });
      if (operation === 'team_node_bind')
        return stream({
          binding: { id: bindingId, endpointId, hostSessionId: f.session.id },
        });
      if (operation === 'team_list_endpoints')
        return stream({
          jsonrpc: '2.0',
          id: randomUUID(),
          result: { content: [], structuredContent: { endpoints: [] } },
        });
      if (operation === 'team_node_pull')
        return stream({ page: { items: [] } });
      throw new Error('unexpected broker operation');
    },
  };
  const team = createTeam({
    broker,
    vault,
    ctx: {
      ...f.ctx,
      tools: {
        register(tool) {
          tools.set(tool.name, tool);
          return () => tools.delete(tool.name);
        },
      },
    },
    label: 'Test DSH',
    assertActive() {},
    getSessionContext: f.contexts.session,
    getToolContext: f.contexts.tool,
    readStream: async (id) => streams.get(id),
  });
  t.after(() => team.close());
  await team.start();
  return { ...f, team, calls, tools };
}
test('Team starts with fixed broker MCP operations and no premature session registration', async (t) => {
  const f = await fixture(t);
  assert.deepEqual(
    f.calls.map((c) => c.operation),
    ['team_initialize', 'team_list_tools'],
  );
  assert.equal(f.tools.size, 4);
});
test('trusted tool registers and binds its session before the fixed operation', async (t) => {
  const f = await fixture(t);
  const result = await f.tools
    .get('auriga_list_endpoints')
    .execute({ limit: 20 }, f.execution);
  assert.deepEqual(result.structuredContent, { endpoints: [] });
  assert.deepEqual(
    f.calls.slice(2).map((c) => c.operation),
    ['team_node_register', 'team_node_bind', 'team_list_endpoints'],
  );
  assert.ok(f.calls.slice(2).every((c) => c.input.executionContextId));
  assert.equal(JSON.stringify(f.calls).includes('authorization'), false);
});
test('forged native execution cannot create an endpoint or call Team', async (t) => {
  const f = await fixture(t),
    before = f.calls.length;
  await assert.rejects(
    f.tools
      .get('auriga_list_endpoints')
      .execute({}, { ...f.execution, agent: { ...f.agent } }),
  );
  assert.equal(f.calls.length, before);
});
