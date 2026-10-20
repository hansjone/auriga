/**
 * Minimal WebSocket client over node:net (no extra deps) for e2e revoke checks.
 */
import net from 'node:net';
import { createHash, randomBytes } from 'node:crypto';
import { setTimeout as sleep } from 'node:timers/promises';

/**
 * @param {{
 *   hostname: string,
 *   port: number,
 *   path?: string,
 *   hostHeader: string,
 *   headers?: Record<string, string>,
 *   timeoutMs?: number,
 * }} opts
 */
export async function openWebSocket(opts) {
  const path = opts.path || '/ws';
  const timeoutMs = opts.timeoutMs || 15_000;
  const key = randomBytes(16).toString('base64');
  const socket = net.connect({
    host: opts.hostname,
    port: opts.port,
  });

  const closed = new Promise((resolve) => {
    socket.once('close', () => resolve('close'));
    socket.once('error', () => resolve('error'));
    socket.once('end', () => resolve('end'));
  });

  await new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('ws connect timeout')), timeoutMs);
    socket.once('connect', () => {
      clearTimeout(t);
      resolve();
    });
    socket.once('error', (e) => {
      clearTimeout(t);
      reject(e);
    });
  });

  const hdrs = {
    Host: opts.hostHeader,
    Upgrade: 'websocket',
    Connection: 'Upgrade',
    'Sec-WebSocket-Key': key,
    'Sec-WebSocket-Version': '13',
    Origin: `http://${opts.hostHeader}`,
    ...(opts.headers || {}),
  };
  let req = `GET ${path} HTTP/1.1\r\n`;
  for (const [k, v] of Object.entries(hdrs)) req += `${k}: ${v}\r\n`;
  req += '\r\n';
  socket.write(req);

  const { status, rest } = await readHttpResponse(socket, timeoutMs);
  if (status !== 101) {
    socket.destroy();
    throw new Error(`ws upgrade status ${status}`);
  }
  const expect = createHash('sha1')
    .update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
    .digest('base64');
  // Accept header already consumed in status parse; frame buffer starts at rest.
  let buf = rest;

  function sendText(text) {
    const payload = Buffer.from(String(text), 'utf8');
    const mask = randomBytes(4);
    const out = Buffer.alloc(2 + 4 + payload.length);
    out[0] = 0x81;
    out[1] = 0x80 | payload.length;
    mask.copy(out, 2);
    for (let i = 0; i < payload.length; i++) {
      out[6 + i] = payload[i] ^ mask[i % 4];
    }
    socket.write(out);
  }

  async function readTextFrame(ms = timeoutMs) {
    const deadline = Date.now() + ms;
    while (Date.now() < deadline) {
      while (buf.length < 2) {
        buf = Buffer.concat([buf, await readChunk(socket, deadline - Date.now())]);
      }
      const b0 = buf[0];
      const b1 = buf[1];
      const opcode = b0 & 0x0f;
      const masked = (b1 & 0x80) !== 0;
      let len = b1 & 0x7f;
      let off = 2;
      if (len === 126) {
        while (buf.length < 4) {
          buf = Buffer.concat([buf, await readChunk(socket, deadline - Date.now())]);
        }
        len = buf.readUInt16BE(2);
        off = 4;
      }
      const maskLen = masked ? 4 : 0;
      while (buf.length < off + maskLen + len) {
        buf = Buffer.concat([buf, await readChunk(socket, deadline - Date.now())]);
      }
      let payload = buf.subarray(off + maskLen, off + maskLen + len);
      if (masked) {
        const mask = buf.subarray(off, off + 4);
        payload = Buffer.from(payload);
        for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      }
      buf = buf.subarray(off + maskLen + len);
      if (opcode === 0x8) {
        socket.destroy();
        throw new Error('ws closed by peer');
      }
      if (opcode === 0x1) return payload.toString('utf8');
    }
    throw new Error('ws read timeout');
  }

  async function waitClosed(ms) {
    const raced = await Promise.race([
      closed,
      sleep(ms).then(() => 'timeout'),
    ]);
    return raced !== 'timeout';
  }

  function close() {
    try {
      socket.end();
    } catch {
      /* ignore */
    }
    try {
      socket.destroy();
    } catch {
      /* ignore */
    }
  }

  return { sendText, readTextFrame, waitClosed, close, expectAccept: expect };
}

/**
 * @param {import('node:net').Socket} socket
 * @param {number} timeoutMs
 */
async function readHttpResponse(socket, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let buf = Buffer.alloc(0);
  while (Date.now() < deadline) {
    buf = Buffer.concat([buf, await readChunk(socket, deadline - Date.now())]);
    const idx = buf.indexOf('\r\n\r\n');
    if (idx < 0) continue;
    const head = buf.subarray(0, idx).toString('utf8');
    const rest = buf.subarray(idx + 4);
    const m = /^HTTP\/1\.[01] (\d{3})/.exec(head);
    const status = m ? Number(m[1]) : 0;
    return { status, head, rest };
  }
  throw new Error('ws handshake timeout');
}

/**
 * @param {import('node:net').Socket} socket
 * @param {number} ms
 */
function readChunk(socket, ms) {
  return new Promise((resolve, reject) => {
    if (ms <= 0) {
      reject(new Error('ws read timeout'));
      return;
    }
    const t = setTimeout(() => {
      cleanup();
      reject(new Error('ws read timeout'));
    }, ms);
    function onData(c) {
      cleanup();
      resolve(c);
    }
    function onErr(e) {
      cleanup();
      reject(e);
    }
    function onClose() {
      cleanup();
      reject(new Error('ws closed'));
    }
    function cleanup() {
      clearTimeout(t);
      socket.off('data', onData);
      socket.off('error', onErr);
      socket.off('close', onClose);
    }
    socket.once('data', onData);
    socket.once('error', onErr);
    socket.once('close', onClose);
  });
}
