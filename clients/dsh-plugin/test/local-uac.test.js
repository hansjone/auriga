import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { createUacAdapter, formEncode } from '../src/local/uac/adapter.js';
import { createUacHttp } from '../src/local/uac/http.js';
import { readUacConfig } from '../src/local/uac/config.js';
import {
  CHALLENGE_TTL_MS,
  createLocalUacIdentity,
  tokenHash,
} from '../src/local/uac/session.js';
import { createUacRoutes } from '../src/local/uac/routes.js';

// Synthetic protocol only (see services/iam/impl/UAC.zh.md). A loopback
// replica stands in for the private enterprise service; no real hosts.
function settings(origin) {
  return {
    allowedDestinations: [{ origin, addresses: ['127.0.0.1'] }],
    clientIP: '',
    constants: { origin: 'desk' },
    tokenVerifyUrl: origin + '/verify',
    qr: {
      path: '/scan',
      contentTemplate: 'example:${x}:${y}:${systemCode}',
      derived: {
        integrity: {
          algorithm: 'md5',
          template: '${x}${y}${clientIP}${systemCode}${origin}',
        },
      },
      body: { a: '${x}', b: '${y}', check: '${integrity}' },
      statusPointer: '/state',
      waiting: 'waiting',
      scanned: 'scanned',
      success: 'verified',
      tokenPointer: '/token',
      empNoPointer: '/employee',
    },
    department: {
      url: origin + '/info',
      body: { credential: '${token}', employee: '${empNo}' },
      successPointer: '/ok',
      successValue: true,
      listPointer: '/rows',
      empNoPointer: '/employee',
      departmentPointers: ['/department'],
    },
  };
}

async function replica() {
  const calls = [];
  const script = { scan: [], verify: [], info: [] };
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    const parsed = JSON.parse(body || 'null');
    calls.push({ path: req.url, body: parsed, headers: req.headers });
    const queue = script[req.url.slice(1)] ?? [];
    const next = queue.length > 1 ? queue.shift() : queue[0];
    if (!next) {
      res.writeHead(500).end();
      return;
    }
    const value = typeof next === 'function' ? await next(parsed) : next;
    res.writeHead(value.status ?? 200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(value.body));
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const origin = `http://127.0.0.1:${server.address().port}`;
  return {
    origin,
    calls,
    script,
    close: () => new Promise((r) => server.close(r)),
  };
}

const ok = (user, token = 'renewed-token') => ({
  body: { code: { code: '0000' }, bo: { code: '0000', user, token } },
});

function clock(start = 1_800_000_000_000) {
  let value = start;
  const timers = new Set();
  return {
    now: () => value,
    setTimer(fn, ms) {
      const t = { at: value + ms, fn };
      timers.add(t);
      return t;
    },
    clearTimer(t) {
      timers.delete(t);
    },
    async advance(ms) {
      value += ms;
      for (const t of [...timers])
        if (t.at <= value) {
          timers.delete(t);
          await t.fn();
        }
    },
    pending: () => timers.size,
  };
}

async function setup(t, { config = true, ...rest } = {}) {
  const upstream = await replica();
  const directory = await mkdtemp(join(tmpdir(), 'auriga-uac-'));
  t.after(async () => {
    await upstream.close();
    await rm(directory, { recursive: true, force: true });
  });
  if (config) {
    await mkdir(join(directory, 'uac'));
    await writeFile(
      join(directory, 'uac', 'adapter.json'),
      JSON.stringify(settings(upstream.origin)),
    );
    await writeFile(
      join(directory, 'uac', 'provider.json'),
      JSON.stringify({
        baseUrl: upstream.origin,
        systemCode: 'SYS1',
        revalidation: {
          intervalSeconds: 600,
          retryIntervalSeconds: 30,
          maxRetries: 1,
        },
      }),
    );
  }
  const time = clock();
  const logs = [];
  const make = () =>
    createLocalUacIdentity({
      directory,
      createAdapter: (s, p) => createUacAdapter(s, p, { testLoopback: true }),
      now: time.now,
      setTimer: time.setTimer,
      clearTimer: time.clearTimer,
      log: (level, message, fields) => logs.push({ level, message, ...fields }),
      ...rest,
    });
  const identity = make();
  await identity.ready;
  return { upstream, directory, time, logs, identity, make };
}

async function loginFlow(env, empNo = 'A1001') {
  const { upstream, identity, time } = env;
  upstream.script.scan = [
    { body: { state: 'waiting' } },
    { body: { state: 'scanned' } },
    { body: { state: 'verified', token: 'scan-token', employee: empNo } },
  ];
  upstream.script.verify = [ok(empNo)];
  const challenge = await identity.start();
  const states = [];
  for (let i = 0; i < 3; i++) {
    await time.advance(2000);
    states.push((await identity.poll(challenge.id)).challenge.state);
  }
  return { challenge, states };
}

test('local UAC: waiting → scanned → success, verify by client, correlation IDs, no raw token in logs', async (t) => {
  const env = await setup(t);
  assert.equal(env.identity.status().available, true);
  const { challenge, states } = await loginFlow(env);
  assert.deepEqual(states, ['waiting', 'scanned', 'authenticated']);
  assert.ok(challenge.qr.path.startsWith('M'));
  assert.match(challenge.correlationId, /^[0-9a-f-]{36}$/);
  const status = env.identity.status();
  assert.equal(status.state, 'authenticated');
  assert.equal(status.empNo, 'A1001');
  assert.equal(status.ownerBound, true);
  // Client-side verify uses the fixed platform token-verify request.
  const verify = env.upstream.calls.find((c) => c.path === '/verify');
  assert.equal(verify.body.account, 'A1001');
  assert.equal(verify.body.systemCode, 'SYS1');
  assert.equal(
    verify.body.verifyCode,
    createHash('md5').update('A1001scan-tokenSYS1').digest('hex'),
  );
  assert.equal(verify.headers['x-emp-no'], 'A1001');
  // Derived integrity follows the private mapping.
  const scan = env.upstream.calls.find((c) => c.path === '/scan');
  assert.equal(
    scan.body.check,
    createHash('md5')
      .update(scan.body.a + scan.body.b + 'SYS1desk')
      .digest('hex'),
  );
  const text = JSON.stringify(env.logs);
  assert.ok(!text.includes('scan-token') && !text.includes('renewed-token'));
  assert.ok(text.includes(tokenHash('scan-token')));
  for (const entry of env.logs.filter((l) => l.stage === 'scan'))
    assert.equal(entry.parentCorrelationId, challenge.correlationId);
  // Stored credentials are encrypted at rest.
  const raw = await readFile(join(env.directory, 'uac-local.json'), 'utf8');
  assert.ok(!raw.includes('renewed-token') && !raw.includes('A1001'));
});

test('local UAC: expired, denied and token-rejected branches never create a session', async (t) => {
  const env = await setup(t);
  env.upstream.script.scan = [{ body: { state: 'waiting' } }];
  let c = await env.identity.start();
  await env.time.advance(CHALLENGE_TTL_MS + 1);
  assert.equal((await env.identity.poll(c.id)).challenge.state, 'expired');
  assert.equal(env.identity.status().challenge.qr, undefined);

  env.upstream.script.scan = [{ body: { state: 'refused' } }];
  c = await env.identity.start();
  await env.time.advance(2000);
  let s = await env.identity.poll(c.id);
  assert.equal(s.challenge.state, 'denied');
  assert.equal(s.challenge.failureCode, 'upstream_rejected');

  env.upstream.script.scan = [
    { body: { state: 'verified', token: 'bad', employee: 'A1001' } },
  ];
  env.upstream.script.verify = [
    { body: { code: { code: '0000' }, bo: { code: '1001' } } },
  ];
  c = await env.identity.start();
  await env.time.advance(2000);
  s = await env.identity.poll(c.id);
  assert.equal(s.challenge.state, 'rejected');
  assert.equal(s.challenge.failureCode, 'token_rejected');
  assert.equal(s.state, 'signed_out');
  assert.equal(s.ownerBound, false);
});

test('local UAC: owner binding rejects a different employee', async (t) => {
  const env = await setup(t);
  await loginFlow(env, 'A1001');
  await env.identity.logout();
  assert.equal(env.identity.status().state, 'signed_out');
  assert.equal(env.identity.status().ownerBound, true);
  const before = env.upstream.calls.filter((c) => c.path === '/verify').length;
  env.upstream.script.scan = [
    { body: { state: 'verified', token: 'other', employee: 'B2002' } },
  ];
  const c = await env.identity.start();
  await env.time.advance(2000);
  const s = await env.identity.poll(c.id);
  assert.equal(s.challenge.state, 'rejected');
  assert.equal(s.challenge.failureCode, 'owner_mismatch');
  assert.equal(s.state, 'signed_out');
  assert.equal(
    env.upstream.calls.filter((c) => c.path === '/verify').length,
    before,
    'mismatched owner token is never sent for verification',
  );
  // Same owner, different case, is accepted (platform ASCII fold).
  await loginFlow(env, 'a1001');
  assert.equal(env.identity.status().state, 'authenticated');
});

test('local UAC: revalidation renews, retries faults, then returns to scan', async (t) => {
  const env = await setup(t);
  await loginFlow(env);
  env.upstream.script.verify = [ok('A1001', 'rotated')];
  await env.time.advance(600_000);
  assert.equal(env.identity.status().state, 'authenticated');
  assert.equal(env.identity.status().revalidation, 'ok');
  // Transport/protocol faults are retried within the bounded budget.
  env.upstream.script.verify = [{ status: 502, body: {} }];
  await env.time.advance(600_000);
  assert.equal(env.identity.status().revalidation, 'retrying');
  await env.time.advance(30_000);
  const s = env.identity.status();
  assert.equal(s.state, 'signed_out');
  assert.equal(s.lastOutcome, 'revalidation_unreachable');
  // Rejection signs out immediately.
  await loginFlow(env);
  env.upstream.script.verify = [
    { body: { code: { code: '0000' }, bo: { code: '9999' } } },
  ];
  await env.time.advance(600_000);
  assert.equal(env.identity.status().state, 'signed_out');
  assert.equal(env.identity.status().lastOutcome, 'revalidation_rejected');
  // Restart keeps the owner binding and the persisted schedule.
  await loginFlow(env);
  const restarted = env.make();
  await restarted.ready;
  assert.equal(restarted.status().state, 'authenticated');
  assert.equal(restarted.status().ownerBound, true);
  await restarted.close();
});

test('local UAC: works with the platform offline and hides without private config', async (t) => {
  // No platform runtime is involved at all: the identity only talks to UAC.
  const env = await setup(t);
  await loginFlow(env);
  assert.equal(env.identity.status().state, 'authenticated');

  const bare = await setup(t, { config: false });
  assert.deepEqual(bare.identity.status(), {
    available: false,
    state: 'signed_out',
    ownerBound: false,
  });
  await assert.rejects(bare.identity.start(), { code: 'unavailable' });
  assert.equal(bare.upstream.calls.length, 0);
});

test('local UAC: a cancelled or expired scan cannot publish a late verification', async (t) => {
  const env = await setup(t);
  const { identity, upstream, time } = env;
  upstream.script.scan = [
    {
      body: { state: 'verified', token: 'synthetic-scan', employee: 'A1001' },
    },
  ];
  for (const ending of ['cancel', 'expire']) {
    let resolveVerify;
    let notifyStarted;
    const waitingForVerify = new Promise(
      (resolve) => (notifyStarted = resolve),
    );
    const verify = new Promise((resolve) => (resolveVerify = resolve));
    upstream.script.verify = [
      () => {
        notifyStarted();
        return verify;
      },
    ];
    const challenge = await identity.start();
    assert.equal((await identity.start()).id, challenge.id);
    const pending = identity.poll(challenge.id);
    await waitingForVerify;
    if (ending === 'cancel') await identity.cancel(challenge.id);
    else await time.advance(CHALLENGE_TTL_MS);
    resolveVerify(ok('A1001'));
    const status = await pending;
    assert.equal(status.state, 'signed_out');
    assert.equal(status.ownerBound, false);
    assert.equal(
      status.challenge?.state,
      ending === 'expire' ? 'expired' : undefined,
    );
  }
});

test('local UAC config: platform-isomorphic adapter.json, strict placement and validation', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-uac-cfg-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  assert.equal(await readUacConfig(directory), null);
  await mkdir(join(directory, 'uac'));
  await writeFile(
    join(directory, 'uac', 'adapter.json'),
    JSON.stringify(settings('https://identity.example.com')),
  );
  await assert.rejects(readUacConfig(directory), /invalid UAC configuration/);
  await writeFile(
    join(directory, 'uac', 'provider.json'),
    JSON.stringify({
      baseUrl: 'https://identity.example.com',
      systemCode: 'S',
    }),
  );
  const value = await readUacConfig(directory);
  assert.equal(value.revalidation.strategy, 'token-verify');
  assert.equal(value.revalidation.intervalSeconds, 7200);
  createUacAdapter(value.adapter, value.provider).validate();
  await writeFile(
    join(directory, 'uac', 'provider.json'),
    '{"baseUrl":"https://identity.example.com","systemCode":"S","systemCode":"T"}',
  );
  await assert.rejects(readUacConfig(directory));
  const identity = createLocalUacIdentity({ directory });
  await identity.ready;
  assert.equal(identity.status().available, true);
  assert.equal(identity.status().reason, 'invalid');
  await assert.rejects(identity.start(), { code: 'unavailable' });
  await identity.close();
  // Destinations outside the allowlist or plain HTTP fail validation.
  const bad = settings('https://identity.example.com');
  bad.tokenVerifyUrl = 'https://other.example.com/verify';
  assert.throws(() =>
    createUacAdapter(bad, {
      baseUrl: 'https://identity.example.com',
      systemCode: 'S',
    }).validate(),
  );
  assert.throws(() =>
    createUacAdapter(settings('http://identity.example.com'), {
      baseUrl: 'http://identity.example.com',
      systemCode: 'S',
    }).validate(),
  );
  const reserved = settings('https://identity.example.com');
  reserved.constants = { token: 'x' };
  assert.throws(() =>
    createUacAdapter(reserved, {
      baseUrl: 'https://identity.example.com',
      systemCode: 'S',
    }).validate(),
  );
  assert.equal(formEncode("a b!*'~"), 'a+b%21*%27%7E');
});

test('local UAC http: resolved address must be allowlisted; redirects are not followed', async (t) => {
  const http = createUacHttp(
    {
      allowedDestinations: [
        { origin: 'https://identity.example.com', addresses: ['192.0.2.10'] }, // gitleaks:allow
      ],
    },
    { lookup: async () => [{ address: '192.0.2.99', family: 4 }] }, // gitleaks:allow
  );
  await assert.rejects(
    http.post(new URL('https://identity.example.com/scan'), {}, {}, 1),
    /UAC destination rejected/,
  );
  assert.throws(() =>
    http.validate(new URL('https://identity.example.com/scan#x')),
  );
  const upstream = await replica();
  t.after(() => upstream.close());
  upstream.script.scan = [
    { status: 302, body: { state: 'verified', token: 't', employee: 'A' } },
  ];
  const adapter = createUacAdapter(
    settings(upstream.origin),
    {
      baseUrl: upstream.origin,
      systemCode: 'S',
    },
    { testLoopback: true },
  );
  const result = await adapter.scan({ x: '0'.repeat(32), y: '1'.repeat(32) });
  assert.deepEqual(result, {
    state: 'unavailable',
    failureCode: 'transport_error',
  });
  assert.equal(upstream.calls.length, 1);
});

test('local UAC routes: native auth, same-origin, CSRF and platform association', async (t) => {
  const env = await setup(t);
  let platform;
  const routes = createUacRoutes({
    auth: { requestRejection: (req) => (req.headers.owner ? undefined : 401) },
    sameOrigin: (req, write) =>
      !write || req.headers.origin === 'http://dsh.test',
    identity: () => env.identity,
    platformEmployee: () => platform,
  });
  const call = async (method, path, body, headers = {}) => {
    const chunks =
      body === undefined ? [] : [Buffer.from(JSON.stringify(body))];
    const req = Object.assign(chunks.values(), {
      method,
      url: path,
      headers: { owner: '1', ...headers },
      [Symbol.asyncIterator]() {
        return chunks.values();
      },
    });
    let status, payload;
    const res = {
      writeHead(code) {
        status = code;
      },
      end(text) {
        payload = JSON.parse(text);
      },
    };
    await routes(req, res);
    return { status, payload };
  };
  assert.equal(
    (await call('GET', '/auriga/uac/status', undefined, { owner: '' })).status,
    401,
  );
  const { payload: first } = await call('GET', '/auriga/uac/status');
  assert.equal(first.available, true);
  assert.equal(first.platformLink, 'none');
  const write = {
    'content-type': 'application/json',
    origin: 'http://dsh.test',
  };
  assert.equal(
    (await call('POST', '/auriga/uac/start', {}, write)).status,
    403,
  );
  assert.equal(
    (
      await call(
        'POST',
        '/auriga/uac/start',
        {},
        { ...write, 'x-auriga-csrf': first.csrf, origin: 'http://evil.test' },
      )
    ).status,
    403,
  );
  assert.equal(
    (
      await call(
        'POST',
        '/auriga/uac/start',
        { extra: 1 },
        { ...write, 'x-auriga-csrf': first.csrf },
      )
    ).status,
    400,
  );
  await loginFlow(env);
  platform = 'a1001';
  assert.equal(
    (await call('GET', '/auriga/uac/status')).payload.platformLink,
    'matched',
  );
  platform = 'B2002';
  assert.equal(
    (await call('GET', '/auriga/uac/status')).payload.platformLink,
    'mismatch',
  );
  const out = await call(
    'POST',
    '/auriga/uac/logout',
    {},
    { ...write, 'x-auriga-csrf': first.csrf },
  );
  assert.equal(out.payload.state, 'signed_out');
  assert.equal(out.payload.token, undefined);
});
