import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createTeam } from '../src/local/team.js';
import { createVault } from '../src/local/vault.js';

import { peer, protocol, accept, names } from './team-http-peer.js';

test('actual HTTP notification rejects JSON-only with 406 and accepts JSON plus SSE with empty 202', async () => {
  const remote = await peer();
  try {
    await remote.platform.request('/ai/mcp', {
      method: 'POST',
      headers: { accept, 'MCP-Protocol-Version': protocol },
      body: {
        jsonrpc: '2.0',
        id: 1,
        method: 'initialize',
        params: { protocolVersion: protocol },
      },
    });
    const notification = {
      method: 'POST',
      headers: { 'MCP-Protocol-Version': protocol },
      body: { jsonrpc: '2.0', method: 'notifications/initialized' },
    };
    await assert.rejects(
      remote.platform.request('/ai/mcp', notification),
      (error) => error.status === 406,
    );
    assert.equal(remote.state.phase, 'initialized');
    assert.deepEqual(
      await remote.platform.request('/ai/mcp', {
        ...notification,
        headers: { ...notification.headers, accept },
      }),
      {},
    );
    assert.equal(remote.state.phase, 'notified');
    assert.deepEqual(remote.state.rejected, [406]);
  } finally {
    await remote.close();
  }
});

async function clientHandshake(dropNotificationSse) {
  const remote = await peer(),
    directory = await mkdtemp(join(tmpdir(), 'auriga-handshake-'));
  const vault = await createVault(directory),
    registered = new Set();
  const platform = dropNotificationSse
    ? {
        ...remote.platform,
        request: (path, options) =>
          remote.platform.request(
            path,
            options.body?.method === 'notifications/initialized'
              ? {
                  ...options,
                  headers: { ...options.headers, accept: 'application/json' },
                }
              : options,
          ),
      }
    : remote.platform;
  const team = createTeam({
    platform,
    vault,
    getAccess: async () => randomBytes(24).toString('base64url'),
    assertActive() {},
    label: 'Handshake probe',
    ctx: {
      tools: {
        register(tool) {
          assert.ok(names.includes(tool.name));
          assert.equal(typeof tool.execute, 'function');
          registered.add(tool.name);
          return () => registered.delete(tool.name);
        },
      },
    },
  });
  try {
    if (dropNotificationSse) {
      await assert.rejects(team.start(), (error) => error.status === 406);
      assert.equal(registered.size, 0);
      assert.equal(vault.snapshot().endpoint, null);
      assert.deepEqual(remote.state.requests, ['initialize']);
    } else {
      await team.start();
      assert.deepEqual([...registered].sort(), [...names].sort());
      assert.equal(vault.snapshot().endpoint, remote.state.endpointId);
      assert.deepEqual(remote.state.requests, [
        'initialize',
        'notifications/initialized',
        'tools/list',
        'register',
      ]);
      assert.deepEqual(remote.state.rejected, []);
    }
  } finally {
    team.close();
    assert.equal(registered.size, 0);
    vault.close();
    await remote.close();
    await rm(directory, { recursive: true, force: true });
  }
}
test('complete Node start handshake negotiates before tool discovery and private registration', () =>
  clientHandshake(false));
test('dropping notification SSE fails the complete handshake before endpoint/tool publication', () =>
  clientHandshake(true));
