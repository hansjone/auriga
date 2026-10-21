/** Bounded RFC6455 client for E2E transport liveness and session revocation. */
import net from 'node:net';
import { createHash, randomBytes } from 'node:crypto';

const MAX_PAYLOAD = 1024 * 1024;

export async function openWebSocket(opts) {
  const timeoutMs = opts.timeoutMs || 15_000;
  const key = randomBytes(16).toString('base64');
  const socket = net.connect({ host: opts.hostname, port: opts.port });
  let closedInfo = null;
  let peerClose = null;
  let handshake = true;
  let pending = Buffer.alloc(0);
  let fragment = null;
  const messages = [];
  const pongs = [];
  const waiters = new Set();
  const notify = () => {
    for (const wake of waiters) wake();
  };
  socket.on('connect', notify);
  const markClosed = (event) => {
    closedInfo ||= { at: Date.now(), event };
    notify();
  };
  // Keep the error listener until final close; subsequent errors cannot become
  // uncaught exceptions after a first end/error event.
  socket.on('error', () => markClosed('error'));
  socket.on('close', () => markClosed('close'));
  socket.on('end', () => markClosed('end'));
  function maskedFrame(opcode, payload = Buffer.alloc(0)) {
    if (payload.length > MAX_PAYLOAD) throw new Error('ws payload too large');
    const extended = payload.length < 126 ? 0 : payload.length <= 65535 ? 2 : 8;
    const mask = randomBytes(4);
    const out = Buffer.alloc(2 + extended + 4 + payload.length);
    out[0] = 0x80 | opcode;
    out[1] =
      0x80 | (extended === 0 ? payload.length : extended === 2 ? 126 : 127);
    if (extended === 2) out.writeUInt16BE(payload.length, 2);
    if (extended === 8) out.writeBigUInt64BE(BigInt(payload.length), 2);
    mask.copy(out, 2 + extended);
    for (let i = 0; i < payload.length; i++)
      out[6 + extended + i] = payload[i] ^ mask[i % 4];
    return out;
  }
  function parseFrames() {
    while (!handshake && pending.length >= 2) {
      const fin = !!(pending[0] & 0x80);
      const opcode = pending[0] & 0x0f;
      let length = pending[1] & 0x7f;
      let offset = 2;
      if (length === 126) {
        if (pending.length < 4) return;
        length = pending.readUInt16BE(2);
        offset = 4;
      } else if (length === 127) {
        if (pending.length < 10) return;
        const large = pending.readBigUInt64BE(2);
        if (large > BigInt(MAX_PAYLOAD)) {
          socket.destroy();
          return;
        }
        length = Number(large);
        offset = 10;
      }
      if (
        pending[0] & 0x70 ||
        pending[1] & 0x80 ||
        length > MAX_PAYLOAD ||
        (opcode >= 8 && (!fin || length > 125))
      ) {
        socket.destroy();
        return;
      }
      if (pending.length < offset + length) return;
      const payload = pending.subarray(offset, offset + length);
      pending = pending.subarray(offset + length);
      if (opcode === 8) {
        peerClose = {
          code: payload.length >= 2 ? payload.readUInt16BE(0) : null,
        };
        markClosed('peer-close');
        socket.end(maskedFrame(8, payload));
        return;
      }
      if (opcode === 9) socket.write(maskedFrame(10, payload));
      else if (opcode === 10) {
        pongs.push(payload.toString('hex'));
        notify();
      } else if (opcode === 1 || opcode === 2) {
        if (fragment) {
          socket.destroy();
          return;
        }
        if (fin) {
          if (opcode === 1) messages.push(payload.toString('utf8'));
        } else fragment = { opcode, payload: Buffer.from(payload) };
        notify();
      } else if (opcode === 0) {
        if (!fragment || fragment.payload.length + length > MAX_PAYLOAD) {
          socket.destroy();
          return;
        }
        fragment.payload = Buffer.concat([fragment.payload, payload]);
        if (fin) {
          if (fragment.opcode === 1)
            messages.push(fragment.payload.toString('utf8'));
          fragment = null;
          notify();
        }
      } else {
        socket.destroy();
        return;
      }
    }
  }
  socket.on('data', (chunk) => {
    pending = Buffer.concat([pending, chunk]);
    if (!handshake) parseFrames();
    notify();
  });
  async function until(predicate, ms, allowClosed = false) {
    const deadline = Date.now() + ms;
    for (;;) {
      const value = predicate();
      if (value !== undefined) return value;
      if (closedInfo && !allowClosed) throw new Error('ws closed');
      const remaining = deadline - Date.now();
      if (remaining <= 0) throw new Error('ws timeout');
      await new Promise((resolve) => {
        const wake = () => {
          clearTimeout(timer);
          waiters.delete(wake);
          resolve();
        };
        const timer = setTimeout(wake, remaining);
        waiters.add(wake);
      });
    }
  }
  let keepalive;
  try {
    await until(() => (!socket.connecting ? true : undefined), timeoutMs);
    const headers = {
      Host: opts.hostHeader,
      Upgrade: 'websocket',
      Connection: 'Upgrade',
      'Sec-WebSocket-Key': key,
      'Sec-WebSocket-Version': '13',
      Origin: `http://${opts.hostHeader}`,
      ...(opts.headers || {}),
    };
    let request = `GET ${opts.path || '/ws'} HTTP/1.1\r\n`;
    for (const [name, value] of Object.entries(headers))
      request += `${name}: ${value}\r\n`;
    socket.write(request + '\r\n');
    const index = await until(() => {
      const i = pending.indexOf('\r\n\r\n');
      if (pending.length > 16_384) throw new Error('ws handshake too large');
      return i >= 0 ? i : undefined;
    }, timeoutMs);
    const head = pending.subarray(0, index).toString('utf8');
    pending = pending.subarray(index + 4);
    const status = Number(/^HTTP\/1\.[01] (\d{3})/.exec(head)?.[1]);
    if (status !== 101) {
      const error = new Error(`ws upgrade status ${status}`);
      error.status = status;
      throw error;
    }
    const expect = createHash('sha1')
      .update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
      .digest('base64');
    if (/^sec-websocket-accept:\s*(\S+)\s*$/im.exec(head)?.[1] !== expect)
      throw new Error('ws invalid Sec-WebSocket-Accept');
    handshake = false;
    parseFrames();
    if (opts.keepAliveMs > 0) {
      keepalive = setInterval(() => {
        if (!closedInfo) socket.write(maskedFrame(10));
      }, opts.keepAliveMs);
      keepalive.unref?.();
    }
    socket.once('close', () => clearInterval(keepalive));
    return {
      sendText(text) {
        if (closedInfo) throw new Error('ws closed');
        socket.write(maskedFrame(1, Buffer.from(String(text))));
      },
      readTextFrame(ms = timeoutMs) {
        return until(
          () => (messages.length ? messages.shift() : undefined),
          ms,
        );
      },
      async probe(ms = timeoutMs) {
        if (closedInfo) throw new Error('ws closed before probe');
        const payload = randomBytes(12);
        const wanted = payload.toString('hex');
        socket.write(maskedFrame(9, payload));
        await until(() => {
          const index = pongs.indexOf(wanted);
          if (index < 0) return undefined;
          pongs.splice(index, 1);
          return true;
        }, ms);
        if (closedInfo) throw new Error('ws closed during probe');
        return { at: Date.now(), kind: 'ping-pong' };
      },
      async waitClosed(ms) {
        try {
          await until(() => closedInfo || undefined, ms, true);
          return true;
        } catch {
          return false;
        }
      },
      closedInfo: () => closedInfo,
      peerClose: () => peerClose,
      close() {
        clearInterval(keepalive);
        socket.destroy();
      },
    };
  } catch (error) {
    clearInterval(keepalive);
    socket.destroy();
    throw error;
  }
}

/** Keep a control-only native transport alive; application frames are discarded. */
export function startControlKeepalive(
  socket,
  intervalMs,
  initial = Buffer.alloc(0),
  onPeerClose = () => {},
) {
  function controlFrame(opcode, payload = Buffer.alloc(0)) {
    const mask = randomBytes(4);
    const frame = Buffer.alloc(6 + payload.length);
    frame[0] = 0x80 | opcode;
    frame[1] = 0x80 | payload.length;
    mask.copy(frame, 2);
    for (let i = 0; i < payload.length; i++)
      frame[i + 6] = payload[i] ^ mask[i % 4];
    return frame;
  }
  let pending = Buffer.alloc(0);
  const timer = setInterval(() => {
    if (socket.destroyed) return;
    // Client control frames are masked, including an empty Pong payload.
    socket.write(controlFrame(0x0a));
  }, intervalMs);
  timer.unref?.();
  const stop = () => {
    clearInterval(timer);
    socket.off('close', stop);
    socket.off('data', consume);
  };
  function consume(chunk) {
    pending = Buffer.concat([pending, chunk]);
    while (pending.length >= 2) {
      const opcode = pending[0] & 0x0f;
      let length = pending[1] & 0x7f;
      let offset = 2;
      if (length === 126) {
        if (pending.length < 4) return;
        length = pending.readUInt16BE(2);
        offset = 4;
      } else if (length === 127) {
        if (pending.length < 10) return;
        const large = pending.readBigUInt64BE(2);
        if (large > 1024n * 1024n) {
          socket.destroy();
          return;
        }
        length = Number(large);
        offset = 10;
      }
      if (
        pending[1] & 0x80 ||
        length > 1024 * 1024 ||
        (opcode >= 8 && (!(pending[0] & 0x80) || length > 125))
      ) {
        socket.destroy();
        return;
      }
      if (pending.length < offset + length) return;
      const payload = pending.subarray(offset, offset + length);
      pending = pending.subarray(offset + length);
      if (opcode === 0x09) socket.write(controlFrame(0x0a, payload));
      if (opcode === 0x08) {
        // A peer Close already means this transport is no longer alive.
        try {
          onPeerClose(payload.length >= 2 ? payload.readUInt16BE(0) : null);
        } catch {
          /* ignore */
        }
        stop();
        socket.end(controlFrame(0x08, payload), () => socket.destroy());
        return;
      }
    }
  }
  socket.once('close', stop);
  socket.on('data', consume);
  consume(initial);
  // Consume server Ping/Close frames so a FIN cannot wait behind unread data.
  socket.resume();
  return stop;
}
