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
  assertInstanceUnauthorized,
  assertStoppedEvidence,
  assertRevocationClose,
  assertAlarmWake,
} from './lib/assert.mjs';
import { openWebSocket } from './lib/ws-client.mjs';
import { waitInstancePage } from './lib/instance-page.mjs';
import { verifyStaticWeb } from './lib/static-web.mjs';
import {
  readRawHeartbeats,
  assertRawHeartbeat,
  assertRawQuiet,
} from './lib/raw-heartbeat.mjs';
import {
  realDshPageOk,
  identitySigner,
  probeRealDoor,
  assertRuntimeIsolation,
  assertRealHealth,
  realImageDigest,
  assertRealRuntimeImage,
} from './lib/real-dsh.mjs';

const ACCESS_FILE = process.env.ACCESS_FILE || 'deploy/compose/var/access.json';
const access = JSON.parse(readFileSync(ACCESS_FILE, 'utf8'));
if (access.mode !== 'domain' || access.instances.mode !== 'subdomain')
  throw new Error(
    'M2 virtual-Host client requires the domain bootstrap fixture',
  );
const origin = (value) =>
  new URL(`${value.scheme}://${value.host}:${value.port}`);
const PORTAL_HOST = origin(access.portalOrigin).host;
const ADMIN_HOST = origin(access.adminOrigin).host;
const instanceAuthority = (id) =>
  origin({
    ...access.instances,
    host: `${id}.${access.instances.parentDomain}`,
  }).host;
const EDGE =
  process.env.EDGE_BASE || `http://127.0.0.1:${access.portalOrigin.port}`;
const ADMIN_EDGE =
  process.env.ADMIN_EDGE_BASE || `http://127.0.0.1:${access.adminOrigin.port}`;
const IAM_CLIENT_FILE =
  process.env.IAM_CLIENT_FILE || 'deploy/compose/var/iam-client.json';
const iamClient = JSON.parse(readFileSync(IAM_CLIENT_FILE, 'utf8'));
const PLATFORM = process.env.PLATFORM_BASE || iamClient.baseURL;
if (new URL(PLATFORM).protocol !== 'https:')
  throw new Error('M2 combined backend requires authenticated HTTPS');
const iamTLS = {
  ca: readFileSync(join(dirname(IAM_CLIENT_FILE), iamClient.caFile)),
  cert: readFileSync(join(dirname(IAM_CLIENT_FILE), iamClient.certFile)),
  key: readFileSync(join(dirname(IAM_CLIENT_FILE), iamClient.keyFile)),
  servername: iamClient.serverName,
  rejectUnauthorized: true,
};
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
const evidence = {
  fixture: FIXTURE,
  entryTest: 'domain synthetic HTTP client (virtual Host), not an IP browser',
  status: 'failed',
  lifecycleVerified: false,
  checks: [],
};

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
  const base = new URL(opts.base || (host === ADMIN_HOST ? ADMIN_EDGE : EDGE));
  const u = new URL(path, base);
  const jar = opts.jar || jarForHost(host, hosts, jars);
  /** @type {Record<string, string>} */
  const headers = {
    host,
    ...(opts.headers || {}),
  };
  const method = opts.method || 'GET';
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && !opts.platform)
    headers.origin ??= `${u.protocol}//${host}`;
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
        method,
        headers,
        ...(opts.platform ? iamTLS : {}),
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
  const txCookie =
    host === ADMIN_HOST ? 'auriga_admin_login_tx' : 'auriga_portal_login_tx';
  useJar.deleteByName(txCookie);
  useJar.deleteByName('__Host-' + txCookie);
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
  if (advance.body?.status !== 'completed') {
    fail('login', `tx status ${advance.body.status}`, advance.body);
  }
  const names = useJar.names().filter((k) => k.includes('session'));
  if (!names.length) fail('login', 'no session cookie', useJar.names());
  const me = await req('/api/iam/v1/me', { host, jar: useJar });
  if (me.status !== 200) fail('login-me', `status ${me.status}`, me.body);
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

  const host = instanceAuthority(instanceId);
  // Domain open-nonce → instance jar
  iJar.absorbMatchingDomainCookies(pJar, host);
  let loc = open.res.headers.get('location');
  if (loc) {
    const u = new URL(loc, EDGE);
    const redeemHost = u.hostname.includes('.') ? u.host : host;
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
  const netInspect = spawnSync(
    'docker',
    ['network', 'inspect', `auriga-net-${instanceId}`],
    { encoding: 'utf8' },
  );
  const edgeInspect = spawnSync(
    'docker',
    ['inspect', `${process.env.COMPOSE_PROJECT_NAME || 'auriga-m2'}-edge`],
    { encoding: 'utf8' },
  );
  if (netInspect.status !== 0 || edgeInspect.status !== 0)
    fail(
      'real-isolation',
      'cannot inspect actual instance network and trusted edge',
    );
  const network = JSON.parse(netInspect.stdout)[0];
  const edgeInfo = JSON.parse(edgeInspect.stdout)[0];
  assertRuntimeIsolation(
    info,
    network,
    edgeInfo,
    instanceId,
    access.instanceControl.dnsAlias,
  );
  evidence.isolation = {
    internal: true,
    network: network.Name,
    members: Object.keys(network.Containers).length,
    user: info.Config.User,
    readOnlyRoot: true,
    capDropAll: true,
    noNewPrivileges: true,
    metaReadOnly: true,
    memoryBytes: info.HostConfig.Memory,
    nanoCpus: info.HostConfig.NanoCpus,
    pidsLimit: info.HostConfig.PidsLimit,
  };
  evidence.checks.push('actual-runtime-isolation');
  const manifestDigest = realImageDigest(metadata);
  const imageInspect = spawnSync(
    'docker',
    ['image', 'inspect', metadata.image],
    { encoding: 'utf8' },
  );
  if (imageInspect.error || imageInspect.status !== 0)
    fail('real-runtime', 'cannot inspect exact immutable real image');
  const images = JSON.parse(imageInspect.stdout);
  if (!Array.isArray(images) || images.length !== 1)
    fail('real-runtime', 'exact real image inspect must return one image');
  const proofDir = join(
    'deploy/compose/var/runtime-proofs',
    manifestDigest.slice('sha256:'.length),
  );
  const engineImageId = assertRealRuntimeImage(
    metadata,
    images[0],
    info,
    readFileSync(join(proofDir, 'manifest.json')),
    readFileSync(join(proofDir, 'config.json')),
  );
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
  evidence.runtime = { image: metadata.image, engineImageId, ...actual };
  const published = info.NetworkSettings.Ports?.['7860/tcp']?.[0];
  const ip =
    info.NetworkSettings.Networks?.[`auriga-net-${instanceId}`]?.IPAddress;
  const direct = published
    ? `http://127.0.0.1:${published.HostPort}`
    : ip
      ? `http://${ip}:7860`
      : '';
  if (!direct) fail('real-runtime', 'no direct container endpoint');
  const host = instanceAuthority(instanceId);
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
  const doorStartedAt = Date.now();
  evidence.door = await probeRealDoor(
    directRequest,
    signer,
    actual.pluginVersion,
  );
  evidence.checks.push('direct-real-door-and-health');

  const rawFile = process.env.RAW_HEARTBEAT_FILE;
  const raw = (window) => readRawHeartbeats(rawFile, instanceId, window);
  // Settle the successful signed door request before opening the quiet window.
  // Otherwise its delayed real heartbeat would be mistaken for health activity.
  const settleDeadline = Date.now() + 90_000;
  let initialRaw;
  let initialProblem;
  while (Date.now() < settleDeadline) {
    const last = raw().at(-1);
    const projected = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    if (
      last &&
      Date.parse(last.activity?.lastInteractionAt) >= doorStartedAt - 1000 &&
      Date.parse(projected.body?.lastActivityAt) >=
        Date.parse(last.activity.lastInteractionAt)
    ) {
      try {
        assertRawHeartbeat(last, actual);
        initialRaw = last;
        break;
      } catch (error) {
        initialProblem = error.message;
      }
    }
    await sleep(1000);
  }
  if (!initialRaw)
    fail(
      'real-raw-heartbeat',
      `signed door interaction never settled as a healthy idle actual heartbeat: ${initialProblem || 'no matching request'}`,
    );

  const quietWindow = async (label, interactionAt) => {
    const start = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    const stable = start.body?.lastActivityAt;
    const from = Date.now();
    const quietUntil = from + 65_000;
    while (Date.now() < quietUntil) {
      assertRealHealth(
        await directRequest('/_auriga/health'),
        actual.pluginVersion,
      );
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
    const until = Date.now();
    const entries = raw({ after: from, before: until });
    assertRawQuiet(entries, actual, interactionAt, { until });
    evidence.rawQuietWindows ??= [];
    evidence.rawQuietWindows.push({
      label,
      from: new Date(from).toISOString(),
      until: new Date(until).toISOString(),
      acceptedHeartbeats: entries.length,
      lastInteractionAt: interactionAt,
    });
    return start;
  };
  step('real platform activity projection must stay quiet before interaction');
  const before = await quietWindow(
    'real-quiet-before',
    initialRaw.activity.lastInteractionAt,
  );
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
  let interactionRaw;
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    const response = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    if (
      Date.parse(response.body?.lastActivityAt) >= started - 1000 &&
      (Number.isNaN(prior) || Date.parse(response.body?.lastActivityAt) > prior)
    ) {
      const last = raw({ after: started }).at(-1);
      if (
        last &&
        Date.parse(last.activity?.lastInteractionAt) >= started - 1000 &&
        Date.parse(last.activity.lastInteractionAt) >
          Date.parse(initialRaw.activity.lastInteractionAt)
      ) {
        assertRawHeartbeat(last, actual);
        observed = response.body.lastActivityAt;
        interactionRaw = last;
        break;
      }
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
      'accepted actual plugin heartbeat -> quiet health polling -> real GET / -> accepted raw lastInteractionAt -> quiet health polling',
    rawHeartbeatVerified: false,
    requestStartedAt: new Date(started).toISOString(),
    rawBefore: initialRaw,
    rawAfter: interactionRaw,
  };
  // Direct unauthenticated health polling must not advance the activity clock.
  const after = await quietWindow(
    'real-static-heartbeat',
    interactionRaw.activity.lastInteractionAt,
  );
  if (after.body?.lastActivityAt !== observed)
    fail('real-static-heartbeat', 'activity moved after the interaction');
  evidence.interaction.rawHeartbeatVerified = true;
  evidence.checks.push('real-interaction-projection-and-static-health');
}

function dockerInspectRunning(instanceId) {
  const name = `auriga-inst-${instanceId}`;
  const r = spawnSync('docker', ['inspect', '-f', '{{json .State}}', name], {
    encoding: 'utf8',
  });
  if (r.status !== 0) {
    const detail = (r.stderr || r.stdout || '').trim();
    return {
      ok: false,
      running: false,
      detail: safeSummary(detail.slice(0, 200)),
    };
  }
  try {
    const state = JSON.parse(r.stdout);
    const field = String(state.Running);
    return {
      ok: true,
      running: containerIsRunning(field),
      field,
      startedAt: state.StartedAt,
      finishedAt: state.FinishedAt,
    };
  } catch {
    return { ok: false, running: false, detail: 'invalid Docker State JSON' };
  }
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

async function heartbeatIdle(token, instanceId) {
  const res = await req('/api/workspace/v1/instance/heartbeat', {
    base: PLATFORM,
    platform: true,
    host: new URL(PLATFORM).host,
    jar: new CookieJar(),
    method: 'POST',
    headers: {
      authorization: `Bearer ${token}`,
      'content-type': 'application/json',
      'auriga-protocol': '1',
    },
    json: {
      pluginVersion: '0.0.0-fake',
      dshVersion: '0.0.0-fake',
      activity: {
        runningTurns: 0,
        openTerminals: 0,
        activeImBots: 0,
        connectedClients: 0,
      },
      health: { ok: true },
    },
  });
  if (res.status < 200 || res.status >= 300) {
    // A known stopped phase is the only reason to tolerate a rejected trailing
    // fake heartbeat. Transport errors and failures while running remain fatal.
    const instance = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
    });
    if (
      instance.status === 200 &&
      ['stopping', 'stopped'].includes(instance.body?.phase)
    ) {
      return false;
    }
    fail('heartbeat', `status ${res.status}`, res.text);
  }
  return true;
}

async function waitWakeRegistered(instanceId, wakeAt) {
  step('wait nextWakeAt registered');
  await loginEmergency(ADMIN_HOST);
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const g = await req(`/api/workspace/v1/instances/${instanceId}`, {
      host: ADMIN_HOST,
      csrf: true,
    });
    if (g.status === 401) await loginEmergency(ADMIN_HOST);
    if (g.status === 200 && Date.parse(g.body?.nextWakeAt) === wakeAt) {
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
  const stoppedAt = Date.now();
  evidence.stopped = {
    phase: body.phase,
    running: insp.running,
    inspect: insp.field,
    observedAt: stoppedAt,
    finishedAt: insp.finishedAt,
  };

  const host = instanceAuthority(instanceId);
  const page = await req('/', { host, jar: jars.instance });
  try {
    assertStoppedEvidence(insp, page);
  } catch (error) {
    fail('idle-stop-evidence', error.message);
  }
  console.log('container stopped (Running=false)');
  console.log(
    'edge no longer serves healthy instance (status=',
    page.status,
    ')',
  );
  evidence.stopped.edgeStatus = page.status;
  return { body, stoppedAt };
}

async function waitWakeStrict(instanceId, wakeAt, stoppedAt) {
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
      // Pre-warm is expected 120s before the alarm; the at schedule is only
      // consumed by the observer after its actual due time. Keep both facts.
      const startedAt = Date.parse(insp.startedAt);
      if (
        !Number.isFinite(startedAt) ||
        startedAt <= stoppedAt ||
        startedAt < wakeAt - 120_000 - 5000
      ) {
        fail(
          'wake-cause',
          'container started outside this alarm pre-warm window',
        );
      }
      evidence.prewarmed = {
        startedAt: insp.startedAt,
        alarmAt: new Date(wakeAt).toISOString(),
        wakeLeadSeconds: 120,
        stoppedAt,
      };
      if (
        Date.now() < wakeAt ||
        (g.body?.nextWakeAt && Date.parse(g.body.nextWakeAt) <= wakeAt)
      ) {
        await sleep(5000);
        continue;
      }
      try {
        assertAlarmWake(insp, wakeAt, stoppedAt, g.body);
      } catch (error) {
        fail('wake-evidence', error.message);
      }
      // Re-redeem / access via edge after wake.
      jars.instance.clear();
      await accessViaEdge(instanceId, undefined, undefined, deadline);
      evidence.woken = {
        phase: g.body.phase,
        ready: g.body.ready,
        running: insp.running,
        startedAt: insp.startedAt,
        alarmAt: new Date(wakeAt).toISOString(),
        nextWakeAt: g.body?.nextWakeAt ?? null,
        scheduleConsumedObservedAt: Date.now(),
        edge: 'page predicate passed',
      };
      console.log(
        'alarm pre-warm and schedule consumption ok (phase+container+edge)',
      );
      return g.body;
    }
    await sleep(5000);
  }
  fail('wake', 'did not reach running+ready with container+edge');
}

/** Send each actual audience session to the instance Host, bypassing jar routing. */
async function rejectAudienceCookies(instanceId, portalJar = jars.portal) {
  const host = instanceAuthority(instanceId);
  for (const [audience, source, sourceHost] of [
    ['portal', portalJar, PORTAL_HOST],
    ['admin', jars.admin, ADMIN_HOST],
  ]) {
    const sourceHeader = source.cookieHeader({
      host: sourceHost,
      path: '/',
      secure: edgeUrl().protocol === 'https:',
    });
    const name = sessionCookieName(sourceHost);
    const cookie = sourceHeader
      .split('; ')
      .filter(
        (part) =>
          part.startsWith(name + '=') ||
          part.startsWith('__Host-' + name + '='),
      )
      .join('; ');
    if (!cookie) fail('audience-probe', `no actual ${audience} session cookie`);
    const response = await req('/', {
      host,
      jar: new CookieJar(),
      headers: { Cookie: cookie },
    });
    try {
      assertInstanceUnauthorized(response);
    } catch (error) {
      fail('audience-probe', `${audience}: ${error.message}`);
    }
  }
  evidence.checks.push('actual-portal-and-admin-cookies-rejected-at-instance');
}

/** Dual sessions retain independent instance HTTP and WS access. */
async function revokeDualSession(instanceId) {
  step('iam dual-session revoke (A HTTP+WS, B deletes A)');
  const portalA = new CookieJar();
  const instanceA = new CookieJar();
  const portalB = new CookieJar();
  const instanceB = new CookieJar();
  // Finish the second TOTP login before opening either transport: no idle gap
  // between the final A liveness probe and DELETE is attributed to revocation.
  await loginEmergency(PORTAL_HOST, portalA);
  await loginEmergency(PORTAL_HOST, portalB);
  const listA = await req('/api/iam/v1/me/sessions', {
    host: PORTAL_HOST,
    jar: portalA,
  });
  if (listA.status !== 200)
    fail('revoke-A-list', `status ${listA.status}`, listA.body);
  const sessionIdA = (listA.body?.items || []).find(
    (session) => session?.current,
  )?.id;
  if (!sessionIdA) fail('revoke-A-list', 'no current session id for A');
  const listB = await req('/api/iam/v1/me/sessions', {
    host: PORTAL_HOST,
    jar: portalB,
  });
  const sessionIdB = (listB.body?.items || []).find(
    (session) => session?.current,
  )?.id;
  if (listB.status !== 200 || !sessionIdB || sessionIdB === sessionIdA)
    fail('revoke-B-list', 'B must be a distinct active session');
  await accessViaEdge(instanceId, portalA, instanceA);
  await accessViaEdge(instanceId, portalB, instanceB);
  await rejectAudienceCookies(instanceId, portalA);
  const host = instanceAuthority(instanceId);
  const edge = edgeUrl();
  const socketOptions = (jar) => ({
    hostname: edge.hostname,
    port: Number(edge.port || (edge.protocol === 'https:' ? 443 : 80)),
    path: WS_PATH,
    hostHeader: host,
    headers: {
      Origin: `${edge.protocol}//${host}`,
      Cookie: jar.cookieHeader({
        host,
        path: WS_PATH,
        secure: edge.protocol === 'https:',
      }),
    },
    timeoutMs: 20_000,
    keepAliveMs: FIXTURE === 'real' ? 1000 : 0,
  });
  const oldOptionsA = socketOptions(instanceA);
  const wsA = await openWebSocket(oldOptionsA);
  openSockets.push(wsA);
  const wsB = await openWebSocket(socketOptions(instanceB));
  openSockets.push(wsB);
  async function probe(ws, label) {
    if (ws.closedInfo())
      fail(label, 'transport already closed before liveness probe');
    if (FIXTURE === 'fake') {
      const ping = `ping-${randomBytes(8).toString('hex')}`;
      ws.sendText(ping);
      if ((await ws.readTextFrame(10_000)) !== ping)
        fail(label, 'echo mismatch');
      return { at: Date.now(), kind: 'text-echo' };
    }
    return ws.probe(10_000);
  }
  // B is the positive control, then A is checked immediately before DELETE.
  const pageB = await req('/', { host, jar: instanceB });
  if (!instancePageOk(pageB)) fail('revoke-B-before', `HTTP ${pageB.status}`);
  await probe(wsB, 'revoke-B-before-ws');
  const pageA = await req('/', { host, jar: instanceA });
  if (!instancePageOk(pageA)) fail('revoke-A-before', `HTTP ${pageA.status}`);
  const lastAliveA = await probe(wsA, 'revoke-A-before-ws');
  if (wsA.closedInfo() || wsB.closedInfo())
    fail('revoke-before', 'a transport closed before DELETE');
  const deleteSentAt = Date.now();
  const del = await req(`/api/iam/v1/me/sessions/${sessionIdA}`, {
    host: PORTAL_HOST,
    jar: portalB,
    method: 'DELETE',
    csrf: true,
  });
  try {
    assertRevokeSuccess(del.status, del.body);
  } catch (error) {
    fail('revoke', error.message, del.body);
  }
  if (!(await wsA.waitClosed(30_000)))
    fail('revoke-ws', 'A did not close within 30s');
  evidence.revokeClose = {
    lastAliveA,
    deleteSentAt,
    closed: wsA.closedInfo(),
    peerClose: wsA.peerClose(),
  };
  try {
    assertRevocationClose(wsA.closedInfo(), deleteSentAt, wsA.peerClose());
  } catch (error) {
    fail('revoke-ws-cause', error.message);
  }
  console.log('A transport closed after DELETE', evidence.revokeClose);
  const pageAfter = await req('/', { host, jar: instanceA });
  try {
    assertInstanceUnauthorized(pageAfter);
  } catch (error) {
    fail('revoke-http', error.message);
  }
  // Reuse the exact pre-revoke instance cookie for a new handshake; a deleted
  // cookie from the HTTP rejection must not turn this into a missing-cookie test.
  let rejection;
  try {
    const unexpected = await openWebSocket(oldOptionsA);
    unexpected.close();
    fail('revoke-new-ws', 'revoked cookie opened a new transport');
  } catch (error) {
    rejection = error;
  }
  if (rejection?.status !== 401)
    fail(
      'revoke-new-ws',
      `expected 401, got ${rejection?.status ?? 'transport error'}`,
    );
  const meA = await req('/api/iam/v1/me', { host: PORTAL_HOST, jar: portalA });
  if (meA.status !== 401)
    fail('revoke-A-me', `expected 401, got ${meA.status}`);
  const meB = await req('/api/iam/v1/me', { host: PORTAL_HOST, jar: portalB });
  const pageB2 = await req('/', { host, jar: instanceB });
  if (meB.status !== 200 || !instancePageOk(pageB2))
    fail('revoke-B-alive', `B portal=${meB.status} instance=${pageB2.status}`);
  const aliveB = await probe(wsB, 'revoke-B-after-ws');
  const sessions = await req('/api/iam/v1/me/sessions', {
    host: PORTAL_HOST,
    jar: portalB,
  });
  if (
    sessions.status !== 200 ||
    !Array.isArray(sessions.body?.items) ||
    sessions.body.items.some((session) => session.id === sessionIdA) ||
    !sessions.body.items.some(
      (session) => session.id === sessionIdB && session.current,
    )
  )
    fail('revoke-B-sessions', 'A still listed or current B missing');
  evidence.revoke = {
    status: del.status,
    aHttpStatus: pageAfter.status,
    aNewWsStatus: rejection.status,
    aPortalStatus: meA.status,
    bPortalStatus: meB.status,
    bHttpStatus: pageB2.status,
    bWsAlive: aliveB,
    aAbsentFromSessions: true,
  };
  wsA.close();
  wsB.close();
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
    step('portal/admin HTML and built JS/CSS');
    for (const [audience, host] of Object.entries(hosts)) {
      const assets = await verifyStaticWeb(
        (path) => req(path, { host, jar: new CookieJar() }),
        audience,
      );
      console.log(
        `${audience} entry and ${assets.length} built assets verified`,
      );
    }
    evidence.checks.push('portal-admin-html-js-css');
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

    // Same-jar HTTP and same-daemon container positive controls precede the idle
    // window. Repeating a user GET at the idle deadline would restart real idle.
    const beforeIdle = await req('/', {
      host: instanceAuthority(instanceId),
      jar: jars.instance,
    });
    const runningBeforeIdle = dockerInspectRunning(instanceId);
    if (
      !instancePageOk(beforeIdle) ||
      !runningBeforeIdle.ok ||
      !runningBeforeIdle.running
    )
      fail(
        'idle-positive-control',
        'running instance HTTP/container positive control failed',
      );
    evidence.beforeIdle = {
      observedAt: Date.now(),
      edgeStatus: beforeIdle.status,
      running: true,
      startedAt: runningBeforeIdle.startedAt,
    };

    const wakeAt = Date.now() + (IDLE_MINUTES * 60 + 90 + 360) * 1000;
    await writeWakeSchedule(instanceId, wakeAt);
    await waitWakeRegistered(instanceId, wakeAt);

    step(`idle heartbeats ~${IDLE_MINUTES + 1.5} min`);
    const idleUntil = Date.now() + (IDLE_MINUTES * 60 + 90) * 1000;
    while (Date.now() < idleUntil) {
      if (FIXTURE === 'fake' && !(await heartbeatIdle(token, instanceId)))
        break;
      await sleep(15_000);
    }

    const { stoppedAt } = await waitStoppedStrict(instanceId);
    console.log('idle reclaim ok (phase+container+edge)');

    await waitWakeStrict(instanceId, wakeAt, stoppedAt);

    await revokeDualSession(instanceId);
    evidence.lifecycleVerified = true;
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
