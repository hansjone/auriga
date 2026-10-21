import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHash } from 'node:crypto';
import { openWebSocket } from './ws-client.mjs';

async function fixture(t, onUpgrade) {
  const server = http.createServer();
  const sockets = new Set();
  server.on('connection', (socket) => {
    sockets.add(socket);
    socket.once('close', () => sockets.delete(socket));
  });
  server.on('upgrade', (req, socket) => {
    const accept = createHash('sha1')
      .update(
        req.headers['sec-websocket-key'] +
          '258EAFA5-E914-47DA-95CA-C5AB0DC85B11',
      )
      .digest('base64');
    onUpgrade(req, socket, accept);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {
    for (const socket of sockets) socket.destroy();
    server.close();
  });
  return {
    hostname: '127.0.0.1',
    port: server.address().port,
    hostHeader: 'instance.i.example.com',
    timeoutMs: 1000,
  };
}
function upgrade(socket, accept) {
  socket.write(
    `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
  );
}
function serverFrame(opcode, payload, fin = true) {
  const extra = payload.length < 126 ? 0 : payload.length <= 65535 ? 2 : 8;
  const frame = Buffer.alloc(2 + extra + payload.length);
  frame[0] = (fin ? 128 : 0) | opcode;
  frame[1] = extra === 0 ? payload.length : extra === 2 ? 126 : 127;
  if (extra === 2) frame.writeUInt16BE(payload.length, 2);
  if (extra === 8) frame.writeBigUInt64BE(BigInt(payload.length), 2);
  payload.copy(frame, 2 + extra);
  return frame;
}
function clientFrames(socket, consume) {
  let pending = Buffer.alloc(0);
  socket.on('data', (chunk) => {
    pending = Buffer.concat([pending, chunk]);
    while (pending.length >= 2) {
      let length = pending[1] & 127;
      let offset = 2;
      if (length === 126) {
        if (pending.length < 4) return;
        length = pending.readUInt16BE(2);
        offset = 4;
      }
      if (length === 127) {
        if (pending.length < 10) return;
        length = Number(pending.readBigUInt64BE(2));
        offset = 10;
      }
      if (pending.length < offset + 4 + length) return;
      assert.ok(pending[1] & 128, 'client frames must be masked');
      const payload = Buffer.from(
        pending.subarray(offset + 4, offset + 4 + length),
      );
      const mask = pending.subarray(offset, offset + 4);
      for (let i = 0; i < length; i++) payload[i] ^= mask[i % 4];
      const opcode = pending[0] & 15;
      pending = pending.subarray(offset + 4 + length);
      consume(opcode, payload);
    }
  });
}

test('rejects forged upgrade Accept and exposes explicit HTTP reject status', async (t) => {
  const forged = await fixture(t, (_req, socket) => upgrade(socket, 'wrong'));
  await assert.rejects(openWebSocket(forged), /invalid Sec-WebSocket-Accept/);
  const rejected = await fixture(t, (_req, socket) =>
    socket.end('HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n'),
  );
  await assert.rejects(openWebSocket(rejected), { status: 401 });
});

test('echoes 126/127 lengths and fragmented text, probes unique Ping/Pong', async (t) => {
  const opts = await fixture(t, (_req, socket, accept) => {
    upgrade(socket, accept);
    clientFrames(socket, (opcode, payload) => {
      if (opcode === 9) socket.write(serverFrame(10, payload));
      if (opcode === 1) {
        const half = Math.floor(payload.length / 2);
        socket.write(serverFrame(1, payload.subarray(0, half), false));
        socket.write(serverFrame(0, payload.subarray(half)));
      }
    });
  });
  const ws = await openWebSocket(opts);
  t.after(() => ws.close());
  for (const size of [13, 126, 65536]) {
    const text = 'x'.repeat(size);
    ws.sendText(text);
    assert.equal(await ws.readTextFrame(), text);
  }
  assert.equal((await ws.probe()).kind, 'ping-pong');
  assert.equal(ws.closedInfo(), null);
  assert.equal(await ws.waitClosed(5), false);
});

test('records and acknowledges Close before TCP end; premature close stays detectable', async (t) => {
  let peer;
  let ack;
  const acknowledged = new Promise((resolve) => {
    ack = resolve;
  });
  const opts = await fixture(t, (_req, socket, accept) => {
    peer = socket;
    upgrade(socket, accept);
    clientFrames(socket, (opcode, payload) => {
      if (opcode === 8) {
        ack(payload);
        socket.end();
      }
    });
  });
  const ws = await openWebSocket(opts);
  t.after(() => ws.close());
  peer.write(serverFrame(8, Buffer.from([0x03, 0xf0])));
  assert.equal(await ws.waitClosed(1000), true);
  assert.deepEqual(ws.peerClose(), { code: 1008 });
  assert.ok(ws.closedInfo().at <= Date.now());
  assert.deepEqual(await acknowledged, Buffer.from([0x03, 0xf0]));
  await assert.rejects(ws.probe(), /closed/);
});
