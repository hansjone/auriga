#!/usr/bin/env node
/**
 * Minimal fake DSH for M2 e2e: HTTP + /_auriga/health + Auriga-Identity door
 * + minimal WebSocket echo (for session-revoke e2e). Not a real DSH runtime.
 */
import http from 'node:http';
import { createHash } from 'node:crypto';
import { readFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { createPublicKey, verify as cryptoVerify } from 'node:crypto';
import { join } from 'node:path';

const port = Number(process.env.PORT || process.env.AURIGA_LISTEN_PORT || 7860);
const instanceId = process.env.AURIGA_INSTANCE_ID || '';
const ownerId = process.env.AURIGA_OWNER_ID || '';
const keysFile =
  process.env.AURIGA_IDENTITY_KEYS_FILE || '/run/auriga/identity-keys.json';
const home = process.env.DSH_HOME || '/home/dsh';

function loadKeys() {
  const raw = JSON.parse(readFileSync(keysFile, 'utf8'));
  const map = new Map();
  for (const k of raw.keys || []) {
    if (k.kty !== 'OKP' || k.crv !== 'Ed25519' || !k.kid || !k.x) continue;
    map.set(
      k.kid,
      createPublicKey({
        key: { kty: 'OKP', crv: 'Ed25519', x: k.x },
        format: 'jwk',
      }),
    );
  }
  return map;
}

function rawPath(url) {
  const q = url.indexOf('?');
  return q < 0 ? url : url.slice(0, q);
}

function verifyIdentity(req) {
  const path = rawPath(req.url || '/');
  const method = String(req.method || 'GET').toUpperCase();
  if (path === '/_auriga/health' && (method === 'GET' || method === 'HEAD')) {
    return { ok: true, bypass: true };
  }
  const token = req.headers['auriga-identity'];
  if (!token || typeof token !== 'string')
    return { ok: false, reason: 'missing' };
  const parts = token.split('.');
  if (parts.length !== 3) return { ok: false, reason: 'malformed' };
  let header;
  let claims;
  try {
    header = JSON.parse(Buffer.from(parts[0], 'base64url').toString('utf8'));
    claims = JSON.parse(Buffer.from(parts[1], 'base64url').toString('utf8'));
  } catch {
    return { ok: false, reason: 'malformed' };
  }
  if (header.alg !== 'EdDSA' || header.typ !== 'auriga-id+jwt') {
    return { ok: false, reason: 'header' };
  }
  const keys = loadKeys();
  const pub = keys.get(header.kid);
  if (!pub) return { ok: false, reason: 'kid' };
  const ok = cryptoVerify(
    null,
    Buffer.from(`${parts[0]}.${parts[1]}`),
    pub,
    Buffer.from(parts[2], 'base64url'),
  );
  if (!ok) return { ok: false, reason: 'sig' };
  if (claims.iss !== 'auriga-edge') return { ok: false, reason: 'iss' };
  if (instanceId && claims.aud !== instanceId)
    return { ok: false, reason: 'aud' };
  if (ownerId && claims.sub !== ownerId) return { ok: false, reason: 'sub' };
  if (String(claims.htm || '').toUpperCase() !== method)
    return { ok: false, reason: 'htm' };
  if (claims.htu !== path) return { ok: false, reason: 'htu' };
  const now = Math.floor(Date.now() / 1000);
  if (typeof claims.exp !== 'number' || now - 5 > claims.exp) {
    return { ok: false, reason: 'exp' };
  }
  return { ok: true, claims };
}

// Optional wake schedule for observer (ops-cron).
try {
  const cronDir = join(home, 'ops-cron');
  mkdirSync(cronDir, { recursive: true });
  if (!existsSync(join(cronDir, 'store.json'))) {
    writeFileSync(
      join(cronDir, 'store.json'),
      JSON.stringify({ jobs: [], settings: { timezone: 'UTC' } }),
    );
  }
} catch {
  /* ignore */
}

const server = http.createServer((req, res) => {
  const path = rawPath(req.url || '/');
  if (
    path === '/_auriga/health' &&
    (req.method === 'GET' || req.method === 'HEAD')
  ) {
    const body = JSON.stringify({
      status: 'ok',
      ready: true,
      protocol: 1,
      pluginVersion: '0.0.0-fake',
    });
    res.writeHead(200, { 'content-type': 'application/json' });
    if (req.method === 'HEAD') {
      res.end();
      return;
    }
    res.end(body);
    return;
  }
  const v = verifyIdentity(req);
  if (!v.ok) {
    res.writeHead(401, { 'content-type': 'application/problem+json' });
    res.end(
      JSON.stringify({
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        code: 'common.unauthenticated',
        detail: v.reason,
      }),
    );
    return;
  }
  res.writeHead(200, { 'content-type': 'text/plain; charset=utf-8' });
  res.end('fake-dsh ok\n');
});

/** Minimal RFC6455 text-frame echo (e2e revoke fixture only). */
server.on('upgrade', (req, socket, head) => {
  if (String(req.headers.upgrade || '').toLowerCase() !== 'websocket') {
    socket.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
    return;
  }
  const path = rawPath(req.url || '/');
  if (path !== '/ws' && path !== '/') {
    socket.write('HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n');
    socket.destroy();
    return;
  }
  const v = verifyIdentity(req);
  if (!v.ok) {
    socket.end('HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n');
    return;
  }
  const key = req.headers['sec-websocket-key'];
  if (!key || typeof key !== 'string') {
    socket.write('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
    socket.destroy();
    return;
  }
  const accept = createHash('sha1')
    .update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
    .digest('base64');
  socket.write(
    'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n` +
      '\r\n',
  );
  let buf = Buffer.alloc(0);
  function frame(opcode, payload) {
    const extended = payload.length < 126 ? 0 : payload.length <= 65535 ? 2 : 8;
    const out = Buffer.alloc(2 + extended + payload.length);
    out[0] = 0x80 | opcode;
    out[1] = extended === 0 ? payload.length : extended === 2 ? 126 : 127;
    if (extended === 2) out.writeUInt16BE(payload.length, 2);
    if (extended === 8) out.writeBigUInt64BE(BigInt(payload.length), 2);
    payload.copy(out, 2 + extended);
    return out;
  }
  function consume(chunk) {
    buf = Buffer.concat([buf, chunk]);
    while (buf.length >= 2) {
      const b0 = buf[0];
      const b1 = buf[1];
      const opcode = b0 & 0x0f;
      const masked = (b1 & 0x80) !== 0;
      let len = b1 & 0x7f;
      let off = 2;
      if (len === 126) {
        if (buf.length < 4) return;
        len = buf.readUInt16BE(2);
        off = 4;
      } else if (len === 127) {
        if (buf.length < 10) return;
        const large = buf.readBigUInt64BE(2);
        if (large > 1024n * 1024n) {
          socket.destroy();
          return;
        }
        len = Number(large);
        off = 10;
      }
      if (
        !masked ||
        !(b0 & 0x80) ||
        b0 & 0x70 ||
        len > 1024 * 1024 ||
        (opcode >= 8 && len > 125)
      ) {
        socket.destroy();
        return;
      }
      const maskLen = masked ? 4 : 0;
      if (buf.length < off + maskLen + len) return;
      let payload = buf.subarray(off + maskLen, off + maskLen + len);
      if (masked) {
        const mask = buf.subarray(off, off + 4);
        payload = Buffer.from(payload);
        for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      }
      buf = buf.subarray(off + maskLen + len);
      if (opcode === 0x8) {
        socket.end(frame(8, payload));
        return;
      }
      if (opcode === 0x9) {
        // ping → pong
        socket.write(frame(10, payload));
        continue;
      }
      if (opcode === 0x1 || opcode === 0x2) {
        // echo unmasked
        socket.write(frame(opcode, payload));
      }
    }
  }
  socket.on('data', consume);
  if (head?.length) consume(head);
  socket.on('error', () => {
    try {
      socket.destroy();
    } catch {
      /* ignore */
    }
  });
});

server.listen(port, '0.0.0.0', () => {
  process.stdout.write(`fake-dsh listening on ${server.address().port}\n`);
});
