#!/usr/bin/env node
/**
 * M2 e2e (strict): login → join → open → edge → idle reclaim → alarm wake →
 * dual-session revoke (HTTP + WebSocket). Failures print redacted step summaries.
 */
import {
  readFileSync,
  writeFileSync,
  existsSync,
  mkdirSync,
  readdirSync,
  mkdtempSync,
  rmSync,
} from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHmac, randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import http from 'node:http';
import https from 'node:https';
import { setTimeout as sleep } from 'node:timers/promises';
import { CookieJar, jarForHost } from './lib/cookie-jar.mjs';
import {
  assertRevokeSuccess,
  containerIsRunning,
  edgeInstanceOk,
  instanceReadyRunning,
  phaseReached,
  safeSummary,
} from './lib/assert.mjs';
import { openWebSocket } from './lib/ws-client.mjs';
import { waitInstancePage } from './lib/instance-page.mjs';
import {
  realDshPageOk,
  identitySigner,
  probeRealDoor,
} from './lib/real-dsh.mjs';

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
const FIXTURE = process.env.DSH_FIXTURE || 'fake';
if (!['fake', 'real'].includes(FIXTURE))
  throw new Error('DSH_FIXTURE must be fake or real');
const instancePageOk = FIXTURE === 'real' ? realDshPageOk : edgeInstanceOk;
const WS_PATH = FIXTURE === 'real' ? '/api/remote.mux' : '/ws';
const evidence = { fixture: FIXTURE, status: 'failed', checks: [] };

/** @type {{ admin: CookieJar, portal: CookieJar, instance: CookieJar }} */
const jars = {
  admin: new CookieJar(),
  portal: new CookieJar(),
  instance: new CookieJar(),
};
const hosts = { admin: ADMIN_HOST, portal: PORTAL_HOST };

/** Temp dirs created during the run (cleaned in finally). */
const tempDirs = [];
/** @type {Array<{ close: () => void }>} */
const openSockets = [];

function step(name) {
  evidence.currentStep = name;
  console.log(`\n==> STEP: ${name}`);
}

function fail(stepName, detail, extra) {
  evidence.failedStep = stepName;
  console.error(`FAIL at ${stepName}: ${safeSummary(detail)}`);
  if (extra !== undefined) console.error(safeSummary(extra));
  process.exitCode = 1;
  throw new Error(`FAIL at ${stepName}`);
}

function edgeUrl() {
  return new URL(EDGE);
}

/**
 * HTTP via node:http so Host can be virtual (admin/portal/instance).
 * Each request uses the audience jar for that Host only.
 */
async function req(path, opts = {}) {
  const host = opts.host || PORTAL_HOST;
  const base = new URL(opts.base || EDGE);
  const u = new URL(path, base);
  const jar = opts.jar || jarForHost(host, hosts, jars);
  /** @type {Record<string, string>} */
  const headers = {
    host,
    ...(opts.headers || {}),
  };
  const cookie = jar.cookieHeader({
    host,
    path: u.pathname,
    secure: u.protocol === 'https:',
  });
  if (cookie) headers.cookie = cookie;
  const bodyStr =
    opts.json !== undefined
      ? JSON.stringify(opts.json)
      : opts.body !== undefined
        ? String(opts.body)
        : null;
  if (opts.json)
    headers['content-type'] = opts.contentType || 'application/json';
  if (bodyStr !== null)
    headers['content-length'] = String(Buffer.byteLength(bodyStr));

  if (opts.csrf) {
    const csrfHdr = jar.cookieHeader({
      host,
      path: u.pathname,
      secure: u.protocol === 'https:',
    });
    const csrf =
      pickCookie(csrfHdr, 'auriga_csrf') ||
      pickCookie(csrfHdr, '__Host-auriga_csrf');
    if (csrf) headers['X-Auriga-CSRF'] = csrf;
  }

  const lib = u.protocol === 'https:' ? https : http;
  const {
    status,
    headers: rawHeaders,
    text,
  } = await new Promise((resolve, reject) => {
    const r = lib.request(
      {
        protocol: u.protocol,
        hostname: u.hostname,
        port: u.port || (u.protocol === 'https:' ? 443 : 80),
        path: u.pathname + u.search,
        method: opts.method || 'GET',
        headers,
        signal: AbortSignal.timeout(opts.timeoutMs ?? 15_000),
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
    r.setTimeout(opts.timeoutMs ?? 15_000, () =>
      r.destroy(new Error('e2e HTTP timeout')),
    );
    if (bodyStr !== null) r.write(bodyStr);
    r.end();
  });

  jar.storeFromResponse(rawHeaders['set-cookie'], {
    host,
    path: u.pathname,
    secure: u.protocol === 'https:',
  });

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

function pickCookie(header, name) {
  if (!header) return null;
  for (const part of header.split('; ')) {
    const i = part.indexOf('=');
    if (i > 0 && part.slice(0, i) === name) return part.slice(i + 1);
  }
  return null;
}

/** Last emergency TOTP time-step consumed (replay rejected via lastCounter). */
let lastTotpStep = -1;

function totpAt(secretB32, step, stepSec = 30) {
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
  counter.writeUInt32BE(step >>> 0, 4);
  const hmac = createHmac('sha1', key).update(counter).digest();
  const offset = hmac[hmac.length - 1] & 0xf;
  const code =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff);
  return String(code % 1_000_000).padStart(6, '0');
}

async function nextTotp(secretB32, stepSec = 30) {
  let step = Math.floor(Date.now() / 1000 / stepSec);
  if (step <= lastTotpStep) {
    const waitMs = Math.ceil(
      (stepSec - ((Date.now() / 1000) % stepSec) + 0.25) * 1000,
    );
    console.log(`waiting ${waitMs}ms for fresh TOTP step`);
    await sleep(waitMs);
    step = Math.floor(Date.now() / 1000 / stepSec);
  }
  lastTotpStep = step;
  return totpAt(secretB32, step, stepSec);
}

function sessionCookieName(host) {
  return host === ADMIN_HOST ? 'auriga_admin_session' : 'auriga_session';
}

/**
 * @param {string} host
 * @param {CookieJar} [jar]
 */
async function loginEmergency(host, jar) {
  step(`login emergency (${host})`);
  const useJar = jar || jarForHost(host, hosts, jars);
  const sessionName = sessionCookieName(host);
  if (useJar.wouldSend(sessionName, { host, path: '/' })) {
    const me = await req('/api/iam/v1/me', { host, jar: useJar });
    if (me.status === 200) {
      console.log('reuse session for', host);
      return;
    }
    console.log('session expired (status', me.status + '); logging in again');
    useJar.deleteByName(sessionName);
    useJar.deleteByName('__Host-' + sessionName);
  }
  if (!existsSync(ENROLLMENT)) fail('login', `missing ${ENROLLMENT}`);
  const enr = JSON.parse(readFileSync(ENROLLMENT, 'utf8'));
  const providerId = enr.providerId;
  const username = enr.username;
  const password = enr.password;
  const totpSecret = enr.totpSecret;
  if (!providerId || !username || !password || !totpSecret) {
    fail('login', 'enrollment incomplete', Object.keys(enr));
  }

  useJar.deleteByName(sessionName);
  useJar.deleteByName('__Host-' + sessionName);
  useJar.deleteByName('auriga_login_tx');
  useJar.deleteByName('__Host-auriga_login_tx');
  useJar.deleteByName('auriga_csrf');
  useJar.deleteByName('__Host-auriga_csrf');

  const start = await req('/api/iam/v1/login/transactions', {
    host,
    jar: useJar,
    method: 'POST',
    json: { providerId, returnTo: '/' },
  });
  if (start.status !== 201 && start.status !== 200) {
    fail('login', `begin ${start.status}`, start.body);
  }
  const txId = start.body?.id;
  if (!txId) fail('login', 'no tx id', start.body);

  const otp = await nextTotp(totpSecret);
  const advance = await req(`/api/iam/v1/login/transactions/${txId}/advance`, {
    host,
    jar: useJar,
    method: 'POST',
    json: {
      type: 'form',
      formId: 'emergency',
      values: { username, password, otp },
    },
  });
  if (advance.status !== 200)
    fail('login', `advance ${advance.status}`, advance.body);
  if (advance.body?.status && advance.body.status !== 'completed') {
    fail('login', `tx status ${advance.body.status}`, advance.body);
  }
  const names = useJar.names().filter((k) => k.includes('session'));
  if (!names.length) fail('login', 'no session cookie', useJar.names());
  console.log('login ok cookieNames=', names.join(','));
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
      step('label node for the e2e template');
      const nodeId = items[0].id;
      const patch = await req(`/api/workspace/v1/admin/nodes/${nodeId}`, {
        host: ADMIN_HOST,
        method: 'PATCH',
        csrf: true,
        contentType: 'application/merge-patch+json',
        json: {
          labels: {
            template: `${FIXTURE === 'real' ? 'real' : 'fake'}-dsh-e2e`,
          },
        },
      });
      if (patch.status !== 200)
        fail('label-node', `status ${patch.status}`, patch.body);
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
    if (instanceReadyRunning(g.body)) return g.body;
    await sleep(4000);
  }
  const nodes = await req('/api/workspace/v1/admin/nodes', {
    host: ADMIN_HOST,
    csrf: true,
  });
  console.log('nodes at timeout:', safeSummary(nodes.body, 800));
  const inst = await req(`/api/workspace/v1/instances/${instanceId}`, {
    host: ADMIN_HOST,
    csrf: true,
  });
  console.log('instance at timeout:', safeSummary(inst.body, 800));
  fail('ready', 'timeout');
}

/**
 * Redeem ticket into the instance jar and GET /.
 * @param {string} instanceId
 * @param {CookieJar} [portalJar]
 * @param {CookieJar} [instanceJar]
 */
async function accessViaEdge(instanceId, portalJar, instanceJar, deadline) {
  step('edge access instance');
  const pJar = portalJar || jars.portal;
  const iJar = instanceJar || jars.instance;
  await loginEmergency(PORTAL_HOST, pJar);
  const open = await req(`/open/${instanceId}`, {
    host: PORTAL_HOST,
    jar: pJar,
    method: 'GET',
  });
  console.log(
    'open status',
    open.status,
    'loc',
    open.res.headers.get('location') ? '(set)' : '(none)',
  );

  const host = `${instanceId}.i.example.com`;
  // Domain open-nonce → instance jar
  iJar.absorbMatchingDomainCookies(pJar, host);
  let loc = open.res.headers.get('location');
  if (loc) {
    const u = new URL(loc, EDGE);
    const redeemHost = u.hostname.includes('.') ? u.hostname : host;
    iJar.absorbMatchingDomainCookies(pJar, redeemHost);
    const redeem = await req(u.pathname + u.search, {
      host: redeemHost,
      jar: iJar,
    });
    console.log('redeem', redeem.status);
  }
  try {
    await waitInstancePage(
      (timeoutMs) => req('/', { host, jar: iJar, timeoutMs }),
      instancePageOk,
      { deadline: Math.min(deadline ?? Infinity, Date.now() + 15_000) },
    );
  } catch (error) {
    fail('edge-access', error.message);
  }
  // Admin jar must never be required for instance access.
  if (
    jars.admin.wouldSend('auriga_admin_session', { host, path: '/' }) ||
    jars.admin.wouldSend('__Host-auriga_admin_session', { host, path: '/' })
  ) {
    fail('edge-access', 'admin session would be sent to instance host');
  }
  console.log('edge access ok');
  return { host, instanceJar: iJar, portalJar: pJar };
}

async function verifyRealHost(instanceId, instance) {
  step('verify immutable real runtime and direct plugin door');
  const metadata = JSON.parse(
    readFileSync('deploy/compose/var/real-dsh.json', 'utf8'),
  );
  const container = `auriga-inst-${instanceId}`;
  const inspected = spawnSync('docker', ['inspect', container], {
    encoding: 'utf8',
  });
  if (inspected.status !== 0)
    fail('real-runtime', 'cannot inspect real instance');
  const info = JSON.parse(inspected.stdout)[0];
  if (
    info.Config.Image !== metadata.image ||
    metadata.dshVersion !== '0.2.0-rc.2' ||
    metadata.nodeVersion !== '22.19.0' ||
    metadata.target !== 'linux/amd64'
  ) {
    fail(
      'real-runtime',
      'instance image/version differs from pinned build metadata',
    );
  }
  const runtime = spawnSync(
    'docker',
    [
      'exec',
      container,
      'node',
      '-e',
      'const m=require("/opt/fixture/manifest.json");process.stdout.write(JSON.stringify({dshVersion:m.dshVersion,nodeVersion:process.versions.node,pluginVersion:m.pluginVersion,lockSha256:m.lockSha256}))',
    ],
    { encoding: 'utf8' },
  );
  if (runtime.status !== 0)
    fail('real-runtime', 'real image manifest unavailable');
  const actual = JSON.parse(runtime.stdout);
  for (const field of [
    'dshVersion',
    'nodeVersion',
    'pluginVersion',
    'lockSha256',
  ]) {
    if (actual[field] !== metadata[field])
      fail('real-runtime', `manifest mismatch: ${field}`);
  }
  evidence.runtime = { image: metadata.image, ...actual };
  const published = info.NetworkSettings.Ports?.['7860/tcp']?.[0];
  const ip =
    info.NetworkSettings.Networks?.[`auriga-net-${instanceId}`]?.IPAddress;
  const direct = published
    ? `http://127.0.0.1:${published.HostPort}`
    : ip
      ? `http://${ip}:7860`
      : '';
  if (!direct) fail('real-runtime', 'no direct container endpoint');
  const host = `${instanceId}.i.example.com`;
  const directRequest = (path, options = {}) =>
    req(path, {
      base: direct,
      host,
      jar: new CookieJar(),
      method: options.method,
      headers: options.identity ? { 'Auriga-Identity': options.identity } : {},
    });
  const signer = identitySigner({
    keysFile: 'deploy/compose/var/identity-signing-keys.json',
    instanceId,
    ownerId: instance.ownerId,
    tenantId: instance.tenantId,
  });
  evidence.door = await probeRealDoor(
    directRequest,
    signer,
    actual.pluginVersion,
  );
  evidence.checks.push('direct-real-door-and-health');

  // While activity is "active" (connectedClients/terminals/turns > 0) the
  // platform projects lastActivityAt = now on every heartbeat, which would
  // make the interaction step pass without lastInteractionAt. Quiet windows
  // reject that interference when heartbeats arrive, but cannot establish the
  // raw counts or even heartbeat delivery. This remains projection evidence.
  const quietWindow = async (label) => {
    const start = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    const stable = start.body?.lastActivityAt;
    const quietUntil = Date.now() + 40_000;
    while (Date.now() < quietUntil) {
      await directRequest('/_auriga/health');
      const response = await req(`/api/workspace/v1/instances/${instanceId}`, {
        host: ADMIN_HOST,
      });
      if (response.body?.lastActivityAt !== stable) {
        fail(
          label,
          'activity advanced without a user request (active counts, health polling or host refresh)',
        );
      }
      await sleep(3000);
    }
    return start;
  };
  step('real platform activity projection must stay quiet before interaction');
  const before = await quietWindow('real-quiet-before');
  evidence.checks.push('real-quiet-before-interaction');

  step(
    'real user page request must advance the platform interaction projection',
  );
  // Plugin timestamps have second precision; make the new action distinguishable.
  const prior = Date.parse(before.body?.lastActivityAt);
  const clockDeadline = Date.now() + 5000;
  while (Date.now() <= prior + 1000 && Date.now() < clockDeadline) {
    await sleep(100);
  }
  const started = Date.now();
  const page = await req('/', { host, jar: jars.instance });
  if (!realDshPageOk(page))
    fail('real-interaction', 'user request did not reach DSH');
  let observed;
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    const response = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    if (
      Date.parse(response.body?.lastActivityAt) >= started - 1000 &&
      (Number.isNaN(prior) || Date.parse(response.body?.lastActivityAt) > prior)
    ) {
      observed = response.body.lastActivityAt;
      break;
    }
    await sleep(3000);
  }
  if (!observed)
    fail(
      'real-interaction',
      'no real heartbeat interaction observed; check unknown host counters and agent platform URL; synthetic heartbeats are forbidden',
    );
  evidence.interaction = {
    before: before.body.lastActivityAt,
    after: observed,
    source:
      'quiet window -> real GET / -> platform lastActivityAt projection -> quiet window',
    rawHeartbeatVerified: false,
    limitation:
      'Raw heartbeat counts and lastInteractionAt are not observed; projection alone cannot complete W3 interaction acceptance.',
  };
  // Direct unauthenticated health polling must not advance the activity clock.
  const after = await quietWindow('real-static-heartbeat');
  if (after.body?.lastActivityAt !== observed)
    fail('real-static-heartbeat', 'activity moved after the interaction');
  evidence.checks.push('real-interaction-projection-and-static-health');
}

function dockerInspectRunning(instanceId) {
  const name = `auriga-inst-${instanceId}`;
  const r = spawnSync('docker', ['inspect', '-f', '{{.State.Running}}', name], {
    encoding: 'utf8',
  });
  if (r.status !== 0) {
    const detail = (r.stderr || r.stdout || '').trim();
    // Removed container is not running (idle reclaim may remove after stop).
    if (/no such object|no such container/i.test(detail)) {
      return { ok: true, running: false, field: 'missing', detail };
    }
    return {
      ok: false,
      running: false,
      detail: safeSummary(detail.slice(0, 200)),
    };
  }
  const field = (r.stdout || '').trim();
  return { ok: true, running: containerIsRunning(field), field };
}

function readTokenFromVolume(instanceId) {
  if (!AGENT_VOLUME) return null;
  const script = instanceId
    ? `cat /data/instances/${instanceId}/meta/instance.token 2>/dev/null || true`
    : `find /data/instances -name instance.token -print -quit 2>/dev/null | head -1 | xargs -r cat`;
  const r = spawnSync(
    'docker',
    [
      'run',
      '--rm',
      '-v',
      `${AGENT_VOLUME}:/data:ro`,
      'alpine',
      'sh',
      '-c',
      script,
    ],
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

async function waitWakeRegistered(instanceId) {
  step('wait nextWakeAt registered');
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    if (g.status === 401) await loginEmergency(ADMIN_HOST);
    if (g.body?.nextWakeAt) {
      console.log('nextWakeAt', g.body.nextWakeAt);
      return;
    }
    await sleep(3000);
  }
  if (AGENT_VOLUME) {
    const ls = spawnSync('docker', [
      'run',
      '--rm',
      '-v',
      `${AGENT_VOLUME}:/data:ro`,
      'alpine',
      'sh',
      '-c',
      `ls -laR /data/instances/${instanceId}/home/ops-cron; cat /data/instances/${instanceId}/home/ops-cron/store.json`,
    ]);
    console.log(safeSummary(String(ls.stdout), 400));
  }
  fail('wake-registered', 'nextWakeAt not reported within 60s');
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
  if (AGENT_VOLUME) {
    const tmp = mkdtempSync(join(tmpdir(), 'auriga-wake-'));
    tempDirs.push(tmp);
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
    console.log(
      'wake schedule written to volume at',
      new Date(nextWakeMs).toISOString(),
    );
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

/**
 * Wait until actual phase matches. desired-only changes do not succeed.
 */
async function waitPhase(instanceId, want, timeoutMs) {
  step(`wait phase=${want} (actual only)`);
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    if (g.status !== 200) {
      console.log('  get instance status', g.status, safeSummary(g.body, 160));
      if (g.status === 401) await loginEmergency(ADMIN_HOST);
      await sleep(5000);
      continue;
    }
    const phase = String(g.body?.phase || '').toLowerCase();
    const desired = String(g.body?.desired || '').toLowerCase();
    console.log(
      '  phase=',
      phase,
      'desired=',
      desired,
      'idleStopAt=',
      g.body?.idleStopAt,
    );
    if (phaseReached(g.body, want)) return g.body;
    await sleep(5000);
  }
  fail('phase', `did not reach actual phase ${want}`);
}

async function waitStoppedStrict(instanceId) {
  const body = await waitPhase(instanceId, 'stopped', 240_000);
  const insp = dockerInspectRunning(instanceId);
  if (!insp.ok) {
    fail('idle-stop-container', `docker inspect failed: ${insp.detail}`);
  }
  if (insp.running) {
    fail(
      'idle-stop-container',
      'container still Running=true after phase=stopped',
      insp.field,
    );
  }
  console.log('container stopped (Running=false)');
  evidence.stopped = {
    phase: body.phase,
    running: insp.running,
    inspect: insp.field,
  };

  const host = `${instanceId}.i.example.com`;
  const page = await req('/', { host, jar: jars.instance });
  if (instancePageOk(page)) {
    fail(
      'idle-stop-edge',
      `edge still returned healthy instance response status=${page.status}`,
    );
  }
  console.log(
    'edge no longer serves healthy instance (status=',
    page.status,
    ')',
  );
  evidence.stopped.edgeStatus = page.status;
  return body;
}

async function waitWakeStrict(instanceId) {
  step('wait alarm wake (phase+container+edge)');
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + 480_000;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    if (g.status === 401) await loginEmergency(ADMIN_HOST);
    const phase = String(g.body?.phase || '').toLowerCase();
    const desired = String(g.body?.desired || '').toLowerCase();
    console.log(
      '  phase=',
      phase,
      'desired=',
      desired,
      'ready=',
      g.body?.ready,
    );
    if (instanceReadyRunning(g.body)) {
      const insp = dockerInspectRunning(instanceId);
      if (!insp.ok || !insp.running) {
        console.log('  phase ready but container not running yet', insp);
        await sleep(5000);
        continue;
      }
      // Re-redeem / access via edge after wake.
      jars.instance.clear();
      await accessViaEdge(instanceId, undefined, undefined, deadline);
      evidence.woken = {
        phase: g.body.phase,
        ready: g.body.ready,
        running: insp.running,
        edge: 'page predicate passed',
      };
      console.log('alarm wake ok (phase=running ready, container up, edge ok)');
      return g.body;
    }
    await sleep(5000);
  }
  fail('wake', 'did not reach running+ready with container+edge');
}

/**
 * Dual portal sessions: A holds instance HTTP+WS; B revokes A via DELETE /me/sessions/{id}.
 */
async function revokeDualSession(instanceId) {
  step('iam dual-session revoke (A HTTP+WS, B deletes A)');
  const portalA = new CookieJar();
  const instanceA = new CookieJar();
  const portalB = new CookieJar();

  await loginEmergency(PORTAL_HOST, portalA);
  const meA = await req('/api/iam/v1/me', { host: PORTAL_HOST, jar: portalA });
  if (meA.status !== 200) fail('revoke-A', `me A ${meA.status}`, meA.body);
  const listA = await req('/api/iam/v1/me/sessions', {
    host: PORTAL_HOST,
    jar: portalA,
  });
  if (listA.status !== 200)
    fail('revoke-A-list', `status ${listA.status}`, listA.body);
  const sessionIdA = (listA.body?.items || []).find((s) => s?.current)?.id;
  if (!sessionIdA) fail('revoke-A-list', 'no current session id for A');

  await accessViaEdge(instanceId, portalA, instanceA);
  const host = `${instanceId}.i.example.com`;
  const pageA = await req('/', { host, jar: instanceA });
  if (!instancePageOk(pageA)) {
    fail('revoke-A', `instance HTTP before revoke ${pageA.status}`);
  }

  const edge = edgeUrl();
  const cookieHdr = instanceA.cookieHeader({
    host,
    path: WS_PATH,
    secure: edge.protocol === 'https:',
  });
  const ws = await openWebSocket({
    hostname: edge.hostname,
    port: Number(edge.port || (edge.protocol === 'https:' ? 443 : 80)),
    path: WS_PATH,
    hostHeader: host,
    headers: cookieHdr ? { Cookie: cookieHdr } : {},
    timeoutMs: 20_000,
    keepAliveMs: FIXTURE === 'real' ? 1000 : 0,
  });
  openSockets.push(ws);
  if (FIXTURE === 'fake') {
    const ping = `ping-${randomBytes(4).toString('hex')}`;
    ws.sendText(ping);
    const echo = await ws.readTextFrame(10_000);
    if (echo !== ping)
      fail('revoke-ws', `echo mismatch got=${safeSummary(echo)}`);
  } else if (await ws.waitClosed(1000)) {
    fail('revoke-ws', 'real DSH transport closed before revoke');
  }
  console.log('session A HTTP+WS ok');

  // Fresh portal session B (same emergency account) revokes A by id.
  await loginEmergency(PORTAL_HOST, portalB);
  const meB = await req('/api/iam/v1/me', { host: PORTAL_HOST, jar: portalB });
  if (meB.status !== 200) fail('revoke-B', `me B ${meB.status}`, meB.body);

  const del = await req(`/api/iam/v1/me/sessions/${sessionIdA}`, {
    host: PORTAL_HOST,
    jar: portalB,
    method: 'DELETE',
    csrf: true,
  });
  try {
    assertRevokeSuccess(del.status, del.body);
  } catch (e) {
    fail('revoke', e.message, del.body);
  }
  console.log('revoke DELETE status', del.status);

  const closed = await ws.waitClosed(30_000);
  if (!closed)
    fail('revoke-ws', 'websocket did not close within 30s after revoke');
  // Edge revocation cuts the proxied TCP stream; a DSH Close frame instead
  // (timeout/error) is recorded so the cause is not silently attributed.
  evidence.revokeClose = { peerClose: ws.peerClose?.() ?? null };
  console.log('session A websocket closed after revoke', evidence.revokeClose);

  const pageAfter = await req('/', { host, jar: instanceA });
  if (pageAfter.status < 400 || instancePageOk(pageAfter)) {
    fail(
      'revoke-http',
      `revoked instance cookie still accepted status=${pageAfter.status}`,
    );
  }
  console.log('revoked instance cookie rejected status=', pageAfter.status);

  const meB2 = await req('/api/iam/v1/me', { host: PORTAL_HOST, jar: portalB });
  if (meB2.status !== 200) {
    fail('revoke-B-alive', `B session broken status=${meB2.status}`, meB2.body);
  }
  console.log('session B still valid');

  // Negative: soft 4xx must not be treated as success (unit-tested; runtime guard).
  try {
    assertRevokeSuccess(404, { title: 'nope' });
    fail('revoke-guard', 'assertRevokeSuccess unexpectedly accepted 404');
  } catch (e) {
    if (e && e.code === 'revoke_not_ok') {
      console.log('revoke 4xx correctly rejected by assert');
    } else {
      throw e;
    }
  }

  ws.close();
}

function cleanup() {
  for (const s of openSockets) {
    try {
      s.close();
    } catch {
      /* ignore */
    }
  }
  openSockets.length = 0;
  for (const d of tempDirs) {
    try {
      rmSync(d, { recursive: true, force: true });
    } catch {
      /* ignore */
    }
  }
  tempDirs.length = 0;
}

async function main() {
  console.log('M2 e2e', { EDGE, PLATFORM, IDLE_MINUTES, FIXTURE });
  try {
    await loginEmergency(ADMIN_HOST);
    await setIdlePolicy();
    await createJoinTokenAndWaitNode();
    const instanceId = await openInstance();
    const instance = await waitReady(instanceId);
    await accessViaEdge(instanceId);

    if (FIXTURE === 'real') await verifyRealHost(instanceId, instance);

    step(
      FIXTURE === 'real'
        ? 'real plugin owns heartbeats (no synthetic idle)'
        : 'locate instance.token',
    );
    let token = null;
    const tokenDeadline = Date.now() + 120_000;
    while (FIXTURE === 'fake' && Date.now() < tokenDeadline) {
      token = findInstanceToken(instanceId);
      if (token) break;
      await sleep(3000);
    }
    if (FIXTURE === 'fake' && !token) {
      fail(
        'heartbeat',
        'missing instance.token; set AGENT_VOLUME or AGENT_DATA_ROOT',
      );
    }

    const wakeAt = Date.now() + (IDLE_MINUTES * 60 + 90 + 360) * 1000;
    await writeWakeSchedule(instanceId, wakeAt);
    await waitWakeRegistered(instanceId);

    step(`idle heartbeats ~${IDLE_MINUTES + 1.5} min`);
    const idleUntil = Date.now() + (IDLE_MINUTES * 60 + 90) * 1000;
    while (Date.now() < idleUntil) {
      if (FIXTURE === 'fake') await heartbeatIdle(token);
      await sleep(15_000);
    }

    await waitStoppedStrict(instanceId);
    console.log('idle reclaim ok (phase+container+edge)');

    await waitWakeStrict(instanceId);

    await revokeDualSession(instanceId);
    evidence.status = 'passed';
    evidence.checks.push(
      'idle-phase-container-edge',
      'wake-phase-container-edge',
      'dual-session-revoke',
    );
    console.log(`\nM2 e2e PASSED (${FIXTURE} DSH)`);
  } finally {
    cleanup();
    if (FIXTURE === 'real') {
      if (evidence.status !== 'passed' && !evidence.failedStep) {
        evidence.failedStep = evidence.currentStep;
      }
      const file =
        process.env.W3_EVIDENCE_FILE || 'deploy/compose/var/w3-evidence.json';
      mkdirSync(dirname(file), { recursive: true });
      writeFileSync(file, JSON.stringify(evidence, null, 2) + '\n', {
        mode: 0o600,
      });
    }
  }
}

main().catch((e) => {
  cleanup();
  if (process.exitCode !== 1) {
    console.error(safeSummary(e && e.stack ? e.stack : e));
    process.exitCode = 1;
  }
});
