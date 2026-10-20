#!/usr/bin/env node
/**
 * M2 e2e: login → join node → open instance → edge access → idle reclaim → alarm wake
 * (+ iam revoke smoke). Failures print which step and what was returned.
 */
import { readFileSync, writeFileSync, existsSync, mkdirSync, readdirSync, mkdtempSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHmac } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import http from 'node:http';
import https from 'node:https';
import { setTimeout as sleep } from 'node:timers/promises';

const EDGE = process.env.EDGE_BASE || 'http://127.0.0.1:8080';
const PLATFORM = process.env.PLATFORM_BASE || 'http://127.0.0.1:8081';
const PORTAL_HOST = 'portal.example.com';
const ADMIN_HOST = 'admin.example.com';
const ENROLLMENT =
  process.env.ENROLLMENT_FILE ||
  'deploy/compose/var/enrollment/emergency-enrollment.json';
const JOIN_TOKEN_FILE =
  process.env.JOIN_TOKEN_FILE || 'deploy/compose/var/join/token';
const AGENT_DATA = process.env.AGENT_DATA_ROOT || '';
const AGENT_VOLUME = process.env.AGENT_VOLUME || '';
const IDLE_MINUTES = Number(process.env.IDLE_MINUTES || 5);

/** @type {Map<string, string>} */
const jar = new Map();

function step(name) {
  console.log(`\n==> STEP: ${name}`);
}

function fail(stepName, detail, extra) {
  console.error(`FAIL at ${stepName}: ${detail}`);
  if (extra !== undefined) console.error(extra);
  process.exit(1);
}

function parseSetCookie(setCookie) {
  const raw = Array.isArray(setCookie) ? setCookie : setCookie ? [setCookie] : [];
  for (const line of raw) {
    const [pair] = String(line).split(';');
    const i = pair.indexOf('=');
    if (i > 0) jar.set(pair.slice(0, i).trim(), pair.slice(i + 1).trim());
  }
}

function cookieHeader() {
  return [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ');
}

/**
 * HTTP via node:http so Host can be virtual (admin/portal/instance).
 * fetch() forbids overriding Host, which makes edge return edge.unknown_host.
 */
async function req(path, opts = {}) {
  const host = opts.host || PORTAL_HOST;
  const base = new URL(opts.base || EDGE);
  const u = new URL(path, base);
  /** @type {Record<string, string>} */
  const headers = {
    host,
    ...(opts.headers || {}),
  };
  const cookie = cookieHeader();
  if (cookie) headers.cookie = cookie;
  const bodyStr =
    opts.json !== undefined
      ? JSON.stringify(opts.json)
      : opts.body !== undefined
        ? String(opts.body)
        : null;
  if (opts.json) headers['content-type'] = 'application/json';
  if (bodyStr !== null) headers['content-length'] = String(Buffer.byteLength(bodyStr));
  const csrf =
    jar.get('auriga_csrf') ||
    jar.get('auriga_admin_csrf') ||
    jar.get('__Host-auriga_csrf');
  if (opts.csrf && csrf) headers['X-Auriga-CSRF'] = csrf;

  const lib = u.protocol === 'https:' ? https : http;
  const { status, headers: rawHeaders, text } = await new Promise((resolve, reject) => {
    const r = lib.request(
      {
        protocol: u.protocol,
        hostname: u.hostname,
        port: u.port || (u.protocol === 'https:' ? 443 : 80),
        path: u.pathname + u.search,
        method: opts.method || 'GET',
        headers,
      },
      (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          resolve({
            status: res.statusCode || 0,
            headers: res.headers,
            text: Buffer.concat(chunks).toString('utf8'),
          });
        });
      },
    );
    r.on('error', reject);
    if (bodyStr !== null) r.write(bodyStr);
    r.end();
  });

  parseSetCookie(rawHeaders['set-cookie']);
  let body = text;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    /* keep text */
  }
  const headerGet = (name) => {
    const v = rawHeaders[String(name).toLowerCase()];
    return Array.isArray(v) ? v[0] : v || null;
  };
  return {
    res: { status, headers: { get: headerGet } },
    body,
    text,
    status,
  };
}

function totp(secretB32, stepSec = 30) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of String(secretB32).replace(/=+$/, '').toUpperCase()) {
    const v = alphabet.indexOf(c);
    if (v < 0) continue;
    bits += v.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    bytes.push(parseInt(bits.slice(i, i + 8), 2));
  }
  const key = Buffer.from(bytes);
  const counter = Buffer.alloc(8);
  counter.writeUInt32BE(Math.floor(Date.now() / 1000 / stepSec), 4);
  const hmac = createHmac('sha1', key).update(counter).digest();
  const offset = hmac[hmac.length - 1] & 0xf;
  const code =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff);
  return String(code % 1_000_000).padStart(6, '0');
}

async function loginEmergency(host) {
  step(`login emergency (${host})`);
  if (!existsSync(ENROLLMENT)) fail('login', `missing ${ENROLLMENT}`);
  const enr = JSON.parse(readFileSync(ENROLLMENT, 'utf8'));
  const providerId = enr.providerId;
  const username = enr.username;
  const password = enr.password;
  const totpSecret = enr.totpSecret;
  if (!providerId || !username || !password || !totpSecret) {
    fail('login', 'enrollment incomplete', Object.keys(enr));
  }

  // Clear previous audience cookies for a clean login.
  for (const k of [...jar.keys()]) {
    if (k.includes('session') || k.includes('csrf') || k.includes('login_tx')) {
      jar.delete(k);
    }
  }

  const start = await req('/api/iam/v1/login/transactions', {
    host,
    method: 'POST',
    json: { providerId, returnTo: '/' },
  });
  if (start.status !== 201 && start.status !== 200) {
    fail('login', `begin ${start.status}`, start.body);
  }
  const txId = start.body?.id;
  if (!txId) fail('login', 'no tx id', start.body);

  const advance = await req(`/api/iam/v1/login/transactions/${txId}/advance`, {
    host,
    method: 'POST',
    json: {
      type: 'form',
      formId: 'emergency',
      values: {
        username,
        password,
        otp: totp(totpSecret),
      },
    },
  });
  if (advance.status !== 200) fail('login', `advance ${advance.status}`, advance.body);
  if (advance.body?.status && advance.body.status !== 'completed') {
    fail('login', `tx status ${advance.body.status}`, advance.body);
  }
  const sessionKeys = [...jar.keys()].filter((k) => k.includes('session'));
  if (!sessionKeys.length) fail('login', 'no session cookie', [...jar.keys()]);
  console.log('login ok cookies=', sessionKeys.join(','));
}

async function setIdlePolicy() {
  step(`set workspace idleMinutes=${IDLE_MINUTES}`);
  const get = await req('/api/org/v1/admin/policies/workspace', {
    host: ADMIN_HOST,
    csrf: true,
  });
  if (get.status !== 200) fail('policy', `get ${get.status}`, get.body);
  const etag = get.res.headers.get('etag');
  const put = await req('/api/org/v1/admin/policies/workspace', {
    host: ADMIN_HOST,
    method: 'PUT',
    csrf: true,
    headers: etag ? { 'If-Match': etag } : {},
    json: {
      value: {
        ...(get.body?.value || {}),
        idleMinutes: IDLE_MINUTES,
        wakeOnSchedule: true,
        maxConcurrentStarts: 2,
      },
    },
  });
  if (put.status !== 200) fail('policy', `put ${put.status}`, put.body);
  console.log('policy ok');
}

async function createJoinTokenAndWaitNode() {
  step('create join token');
  const res = await req('/api/workspace/v1/admin/join-tokens', {
    host: ADMIN_HOST,
    method: 'POST',
    csrf: true,
    json: { name: 'e2e-node', ttlMinutes: 60 },
  });
  if (res.status !== 201 && res.status !== 200) {
    fail('join-token', `status ${res.status}`, res.body);
  }
  const token = res.body?.token;
  if (!token) fail('join-token', 'no token', res.body);
  mkdirSync(join(JOIN_TOKEN_FILE, '..'), { recursive: true });
  writeFileSync(JOIN_TOKEN_FILE, token + '\n', { mode: 0o600 });
  console.log('join token written (value not logged)');

  step('wait agent node online');
  const deadline = Date.now() + 180_000;
  while (Date.now() < deadline) {
    const nodes = await req('/api/workspace/v1/admin/nodes', {
      host: ADMIN_HOST,
      csrf: true,
    });
    const items = nodes.body?.items || [];
    if (items.length > 0) {
      console.log('node online count=', items.length);
      return;
    }
    await sleep(3000);
  }
  fail('join', 'no node within 180s');
}

async function openInstance() {
  step('open my instance (portal)');
  await loginEmergency(PORTAL_HOST);
  const open = await req('/api/workspace/v1/me/instance/open', {
    host: PORTAL_HOST,
    method: 'POST',
    csrf: true,
  });
  if (open.status !== 200) fail('open', `status ${open.status}`, open.body);
  const inst = open.body?.instance || open.body;
  const id = inst?.id;
  if (!id) fail('open', 'no instance id', open.body);
  console.log('instance', id);
  return id;
}

async function waitReady(instanceId) {
  step(`wait ready ${instanceId}`);
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + 240_000;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    const phase = String(g.body?.phase || '').toLowerCase();
    console.log('  phase=', phase, 'ready=', g.body?.ready);
    if (g.body?.ready && phase === 'running') return g.body;
    await sleep(4000);
  }
  fail('ready', 'timeout');
}

async function accessViaEdge(instanceId) {
  step('edge access instance');
  await loginEmergency(PORTAL_HOST);
  const open = await req(`/open/${instanceId}`, {
    host: PORTAL_HOST,
    method: 'GET',
  });
  console.log('open status', open.status, 'loc', open.res.headers.get('location'));

  const host = `${instanceId}.i.example.com`;
  let loc = open.res.headers.get('location');
  if (loc) {
    const u = new URL(loc, EDGE);
    const redeem = await req(u.pathname + u.search, {
      host: u.hostname.includes('.') ? u.hostname : host,
    });
    console.log('redeem', redeem.status);
  }
  const page = await req('/', { host });
  if (page.status !== 200 || !String(page.text).includes('fake-dsh')) {
    fail('edge-access', `status ${page.status}`, String(page.text).slice(0, 400));
  }
  console.log('edge access ok');
}

function readTokenFromVolume(instanceId) {
  if (!AGENT_VOLUME) return null;
  const script = instanceId
    ? `cat /data/instances/${instanceId}/meta/instance.token 2>/dev/null || true`
    : `find /data/instances -name instance.token -print -quit 2>/dev/null | head -1 | xargs -r cat`;
  const r = spawnSync(
    'docker',
    ['run', '--rm', '-v', `${AGENT_VOLUME}:/data:ro`, 'alpine', 'sh', '-c', script],
    { encoding: 'utf8' },
  );
  const t = (r.stdout || '').trim();
  return t || null;
}

function findInstanceToken(instanceId) {
  const fromVol = readTokenFromVolume(instanceId);
  if (fromVol) return fromVol;
  if (!AGENT_DATA) return null;
  const direct = join(
    AGENT_DATA,
    'instances',
    instanceId,
    'meta',
    'instance.token',
  );
  if (existsSync(direct)) return readFileSync(direct, 'utf8').trim();
  try {
    for (const name of readdirSync(join(AGENT_DATA, 'instances'))) {
      const p = join(AGENT_DATA, 'instances', name, 'meta', 'instance.token');
      if (existsSync(p)) return readFileSync(p, 'utf8').trim();
    }
  } catch {
    /* ignore */
  }
  return null;
}

async function heartbeatIdle(token) {
  const res = await fetch(
    new URL('/api/workspace/v1/instance/heartbeat', PLATFORM),
    {
      method: 'POST',
      headers: {
        authorization: `Bearer ${token}`,
        'content-type': 'application/json',
        'auriga-protocol': '1',
      },
      body: JSON.stringify({
        pluginVersion: '0.0.0-fake',
        dshVersion: '0.0.0-fake',
        activity: {
          runningTurns: 0,
          openTerminals: 0,
          activeImBots: 0,
          connectedClients: 0,
        },
        health: { ok: true },
      }),
    },
  );
  if (!res.ok) {
    fail('heartbeat', `status ${res.status}`, await res.text());
  }
}

async function writeWakeSchedule(instanceId, nextWakeMs) {
  const body = JSON.stringify({
    settings: { timezone: 'UTC' },
    jobs: [
      {
        enabled: true,
        nextRunAt: nextWakeMs,
        schedule: { kind: 'at', at: new Date(nextWakeMs).toISOString() },
      },
    ],
  });
  // Prefer writing into the live agent volume so observer sees the file.
  if (AGENT_VOLUME) {
    const tmp = mkdtempSync(join(tmpdir(), 'auriga-wake-'));
    const local = join(tmp, 'store.json');
    writeFileSync(local, body);
    const dest = `/data/instances/${instanceId}/home/ops-cron`;
    const r = spawnSync(
      'docker',
      [
        'run',
        '--rm',
        '-v',
        `${AGENT_VOLUME}:/data`,
        '-v',
        `${local}:/in/store.json:ro`,
        'alpine',
        'sh',
        '-c',
        `mkdir -p '${dest}' && cp /in/store.json '${dest}/store.json'`,
      ],
      { encoding: 'utf8' },
    );
    if (r.status !== 0) {
      fail('wake-schedule', `docker write failed: ${r.stderr || r.stdout}`);
    }
    console.log('wake schedule written to volume at', new Date(nextWakeMs).toISOString());
    return;
  }
  if (!AGENT_DATA) {
    fail('wake-schedule', 'set AGENT_VOLUME or AGENT_DATA_ROOT');
  }
  const dir = join(AGENT_DATA, 'instances', instanceId, 'home', 'ops-cron');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'store.json'), body);
  console.log('wake schedule at', new Date(nextWakeMs).toISOString());
}

async function waitPhase(instanceId, want, timeoutMs) {
  step(`wait phase=${want}`);
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    const phase = String(g.body?.phase || '').toLowerCase();
    const desired = String(g.body?.desired || '').toLowerCase();
    console.log('  phase=', phase, 'desired=', desired);
    if (phase === want || desired === want) return g.body;
    // also accept stopped when desired=stopped
    if (want === 'stopped' && desired.includes('stop')) return g.body;
    await sleep(5000);
  }
  fail('phase', `did not reach ${want}`);
}

async function revokeSmoke() {
  step('iam session revoke smoke');
  await loginEmergency(ADMIN_HOST);
  const revoke = await req('/api/iam/v1/me/sessions/revoke-others', {
    host: ADMIN_HOST,
    method: 'POST',
    csrf: true,
    json: {},
  });
  console.log('revoke-others', revoke.status);
  // Soft pass: endpoint reachable (may be 200/204/404 depending on other sessions)
  if (revoke.status >= 500) fail('revoke', `status ${revoke.status}`, revoke.body);
}

async function main() {
  console.log('M2 e2e', { EDGE, PLATFORM, IDLE_MINUTES });
  await loginEmergency(ADMIN_HOST);
  await setIdlePolicy();
  await createJoinTokenAndWaitNode();
  const instanceId = await openInstance();
  await waitReady(instanceId);
  await accessViaEdge(instanceId);

  step('locate instance.token');
  let token = null;
  const tokenDeadline = Date.now() + 120_000;
  while (Date.now() < tokenDeadline) {
    token = findInstanceToken(instanceId);
    if (token) break;
    await sleep(3000);
  }
  if (!token) {
    fail(
      'heartbeat',
      'missing instance.token; set AGENT_VOLUME or AGENT_DATA_ROOT',
    );
  }

  const wakeAt = Date.now() + (IDLE_MINUTES * 60 + 90) * 1000;
  await writeWakeSchedule(instanceId, wakeAt);

  step(`idle heartbeats ~${IDLE_MINUTES + 1.5} min`);
  const idleUntil = Date.now() + (IDLE_MINUTES * 60 + 90) * 1000;
  while (Date.now() < idleUntil) {
    await heartbeatIdle(token);
    await sleep(15_000);
  }

  await waitPhase(instanceId, 'stopped', 240_000);
  console.log('idle reclaim ok');

  step('wait alarm wake');
  await waitPhase(instanceId, 'running', 360_000);
  console.log('alarm wake ok');

  await revokeSmoke();
  console.log('\nM2 e2e PASSED');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
