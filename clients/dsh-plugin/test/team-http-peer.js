import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { createPlatform, platformConfig } from '../src/local/platform.js';

export const protocol = '2025-06-18';
export const accept = 'application/json, text/event-stream';
export const names = [
  'auriga_list_endpoints',
  'auriga_open_thread',
  'auriga_reply_thread',
  'auriga_read_thread',
];

// Transport-only peer: validates real HTTP negotiation and request order. This
// does not implement IAM or claim that its random transport proof is a JWT.
export async function peer() {
  const state = {
    phase: 'new',
    available: true,
    requests: [],
    rejected: [],
    endpointId: randomUUID(),
  };
  const server = createServer(async (req, res) => {
    const reject = (status) => {
      state.rejected.push(status);
      res.writeHead(status);
      res.end();
    };
    if (!state.available) return reject(503);
    if (req.method !== 'POST') return reject(405);
    if (req.headers.origin !== undefined) return reject(403);
    if (req.headers['content-type'] !== 'application/json') return reject(415);
    let text = '';
    for await (const chunk of req) text += chunk;
    let body;
    try {
      body = JSON.parse(text);
    } catch {
      return reject(400);
    }
    const json = (value) => {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify(value));
    };
    if (req.url === '/ai/mcp') {
      const media = req.headers.accept?.split(',').map((item) => item.trim());
      if (
        !media?.includes('application/json') ||
        !media.includes('text/event-stream')
      )
        return reject(406);
      if (
        req.headers['mcp-protocol-version'] !== protocol ||
        body.jsonrpc !== '2.0'
      )
        return reject(400);
      if (body.method === 'initialize') {
        if (
          state.phase !== 'new' ||
          body.params?.protocolVersion !== protocol ||
          !Number.isInteger(body.id)
        )
          return reject(400);
        state.phase = 'initialized';
        state.requests.push(body.method);
        return json({
          jsonrpc: '2.0',
          id: body.id,
          result: {
            protocolVersion: protocol,
            capabilities: { tools: {} },
            serverInfo: { name: 'transport-test', version: '1' },
          },
        });
      }
      if (body.method === 'notifications/initialized') {
        if (
          state.phase !== 'initialized' ||
          Object.hasOwn(body, 'id') ||
          Object.hasOwn(body, 'params')
        )
          return reject(400);
        state.phase = 'notified';
        state.requests.push(body.method);
        res.writeHead(202);
        return res.end();
      }
      if (body.method === 'tools/list') {
        if (state.phase !== 'notified' || !Number.isInteger(body.id))
          return reject(400);
        state.phase = 'listed';
        state.requests.push(body.method);
        return json({
          jsonrpc: '2.0',
          id: body.id,
          result: {
            tools: names.map((name) => ({
              name,
              description: 'Transport registration probe',
              inputSchema: {
                type: 'object',
                additionalProperties: false,
                properties: {},
              },
            })),
          },
        });
      }
      return reject(400);
    }
    if (req.url === '/ai/team/node/register') {
      if (
        state.phase !== 'listed' ||
        body.label !== 'Handshake probe' ||
        body.receiveEnabled !== true ||
        Object.keys(body).sort().join(',') !== 'label,receiveEnabled'
      )
        return reject(400);
      state.phase = 'registered';
      state.requests.push('register');
      return json({
        endpoint: {
          id: state.endpointId,
          label: body.label,
          receiveEnabled: true,
        },
      });
    }
    return reject(404);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const platform = createPlatform(
    platformConfig({
      platformOrigin: origin,
      issuer: origin,
      clientId: 'transport-test',
      providerId: 'team',
      allowLocalHttp: true,
    }),
  );
  return {
    state,
    platform,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}
