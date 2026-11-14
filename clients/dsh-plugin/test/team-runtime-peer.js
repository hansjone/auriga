import { randomUUID, createHash } from 'node:crypto';
import { join } from 'node:path';
import { setImmediate as nextTurn } from 'node:timers/promises';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { createVault } from '../src/local/vault.js';

// Schema-checked IPC only; no platform HTTP, external broker or native-host claim.
export async function teamRuntimePeer(t, options = {}) {
  const tools = new Map(),
    streams = new Map();
  const ctx = {
    tools: {
      register(tool) {
        tools.set(tool.name, tool);
        return () => tools.delete(tool.name);
      },
    },
    ...options.ctx,
  };
  const f = await shellRuntimePeer(t, {
    ctx,
    async setup(directory, connection) {
      if (!options.saved) {
        await options.setup?.(directory, connection);
        return;
      }
      const who = connection.identity;
      const partition = createHash('sha256')
        .update(
          JSON.stringify([
            who.tenantId,
            who.memberId,
            who.deviceId,
            who.providerId,
          ]),
        )
        .digest('hex');
      const vault = await createVault(join(directory, 'team', partition));
      await vault.update(options.saved);
      await vault.close();
      await options.setup?.(directory, connection);
    },
    configure(connection) {
      const call = connection.ipc.call.bind(connection.ipc);
      connection.ipc.call = async (message) => {
        const operation = message.call?.operation;
        if (!operation?.startsWith('team_')) return call(message);
        connection.calls.push(message);
        connection.validate('ShellAuthBusinessCall', message);
        await options.before?.(operation, message.call.input);
        let body;
        if (operation === 'team_initialize') {
          const streamId = randomUUID();
          streams.set(streamId, {
            body: Buffer.from(
              JSON.stringify({
                jsonrpc: '2.0',
                id: randomUUID(),
                result: { protocolVersion: '2025-06-18' },
              }),
            ),
          });
          body = { streamId };
        } else if (operation === 'team_list_tools')
          body = {
            tools: [
              'auriga_list_endpoints',
              'auriga_open_thread',
              'auriga_reply_thread',
              'auriga_read_thread',
            ].map((name) => ({ name, inputSchema: { type: 'object' } })),
          };
        else if (operation === 'team_ping') body = {};
        else throw new Error('unexpected fixed Team operation');
        return {
          requestId: message.requestId,
          operation,
          output: { statusCode: 200, body },
        };
      };
      connection.ipc.subscribeStream = async (command, receive) => {
        streams.get(command.streamId).receive = receive;
      };
      connection.ipc.streamCommand = async (command) => {
        const stream = streams.get(command.streamId);
        if (!stream) return;
        if (command.action === 'cancel') {
          streams.delete(command.streamId);
          return;
        }
        if (command.action !== 'credit' || stream.sent) return;
        stream.sent = true;
        const snapshot = connection.snapshot();
        const envelope = {
          streamId: command.streamId,
          shellEpoch: snapshot.shellEpoch,
          revision: snapshot.revision,
        };
        stream.receive({
          ...envelope,
          sequence: 0,
          type: 'data',
          data: stream.body.toString('base64'),
        });
        stream.receive({ ...envelope, sequence: 1, type: 'end' });
      };
      options.configure?.(connection);
    },
  });
  return {
    ...f,
    tools,
    streams,
    async settled() {
      const deadline = Date.now() + 3000;
      while (
        tools.size !== 4 &&
        f.runtime.status(f.request).communication !== 'unavailable'
      ) {
        assert.ok(Date.now() < deadline, 'Team initialization did not settle');
        await nextTurn();
      }
    },
  };
}
