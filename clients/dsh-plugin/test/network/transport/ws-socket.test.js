import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { WebSocketServer } from 'ws';
import { createWsSocketFactory } from '../../../src/network/transport/ws-socket.js';

test('native socket sends canonical configured platform Origin across a different WSS host', async () => {
  const server = createServer();
  const wss = new WebSocketServer({ server });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const incoming = once(wss, 'connection');
  const socket = createWsSocketFactory({
    origin: 'https://PORTAL.example.com:443/',
  })(`ws://127.0.0.1:${server.address().port}/net/v1/ws`);
  const opened = once(socket, 'open');
  try {
    const [peer, request] = await incoming;
    await opened;
    assert.equal(request.headers.origin, 'https://portal.example.com');
    assert.equal(request.headers.host, `127.0.0.1:${server.address().port}`);
    peer.terminate();
  } finally {
    socket.terminate();
    await new Promise((ok) => wss.close(ok));
    await new Promise((ok) => server.close(ok));
  }
});

test('native socket rejects invalid Origin and header injection before opening', () => {
  for (const origin of [
    '',
    'null',
    'https://portal.example.com/path',
    'https://alice@portal.example.com',
    'https://portal.example.com?query',
    'https://portal.example.com\r\nX-Test: injected',
    'https://portal.example.com\0',
  ])
    assert.throws(() => createWsSocketFactory({ origin }), /invalidOrigin/);
});
