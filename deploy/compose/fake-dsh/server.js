#!/usr/bin/env node
/**
 * Minimal fake DSH for M2 e2e: HTTP + /_auriga/health + Auriga-Identity door.
 * Not a real DSH runtime (see docs/dev/m2.md).
 */
import http from 'node:http';
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
  if (!token || typeof token !== 'string') return { ok: false, reason: 'missing' };
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
  if (instanceId && claims.aud !== instanceId) return { ok: false, reason: 'aud' };
  if (ownerId && claims.sub !== ownerId) return { ok: false, reason: 'sub' };
  if (String(claims.htm || '').toUpperCase() !== method) return { ok: false, reason: 'htm' };
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
  if (path === '/_auriga/health' && (req.method === 'GET' || req.method === 'HEAD')) {
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

server.listen(port, '0.0.0.0', () => {
  process.stdout.write(`fake-dsh listening on ${port}\n`);
});
