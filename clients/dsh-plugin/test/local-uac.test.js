import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { createServer } from 'node:https';
import { getCACertificates, setDefaultCACertificates } from 'node:tls';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { atomicWriteJson } from '../src/local/utils/atomic-write.js';
import {
  loadOrCreateSecretKey,
  openSecret,
  sealSecret,
} from '../src/local/utils/secret-box.js';
import { createUacAdapter, formEncode } from '../src/local/uac/adapter.js';
import { createUacHttp, parseIpAddress } from '../src/local/uac/http.js';
import { readUacConfig } from '../src/local/uac/config.js';
import {
  CHALLENGE_TTL_MS,
  createLocalUacIdentity,
  tokenHash,
} from '../src/local/uac/session.js';
import { createUacRoutes } from '../src/local/uac/routes.js';
import { createTestPki } from './network/transport/support/pki.js';

const pki = createTestPki();
const defaultCertificates = getCACertificates('default');
before(() => setDefaultCACertificates([...defaultCertificates, pki.rootPem]));
after(() => setDefaultCACertificates(defaultCertificates));

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

async function replica(certificates = pki) {
  const calls = [];
  const script = { scan: [], verify: [], info: [] };
  const server = createServer(
    { key: certificates.keyPem, cert: certificates.certPem },
    async (req, res) => {
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
      res.writeHead(value.status ?? 200, {
        'content-type': 'application/json',
        ...value.headers,
      });
      res.end(value.rawBody ?? JSON.stringify(value.body));
    },
  );
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const origin = `https://127.0.0.1:${server.address().port}`;
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
  // Only the employee binding is persisted, and it is encrypted.
  const raw = await readFile(join(env.directory, 'uac-local.json'), 'utf8');
  assert.ok(!raw.includes('renewed-token') && !raw.includes('A1001'));
  const key = await loadOrCreateSecretKey(env.directory, {
    hasEncryptedData: true,
  });
  const durable = JSON.parse(openSecret(key, JSON.parse(raw)));
  assert.deepEqual(Object.keys(durable), ['version', 'owner']);
  assert.equal(durable.version, 2);
  assert.equal(durable.owner.empNo, 'A1001');
  assert.match(durable.owner.identityDomain, /^[0-9a-f]{64}$/);
});

test('local UAC: uses other.token without an echo and revalidates with it without changing the employee ID', async (t) => {
  const env = await setup(t);
  const employee = '001Ab';
  env.upstream.script.scan = [
    { body: { state: 'verified', token: 'scan-token', employee } },
  ];
  env.upstream.script.verify = [
    {
      body: {
        code: { code: '0000' },
        bo: { code: '0000' },
        other: { token: 'renewed-token' },
      },
    },
    {
      body: {
        code: { code: '0000' },
        bo: { code: '0000', user: '001AB' },
        other: { token: 'renewed-token' },
      },
    },
  ];
  const challenge = await env.identity.start();
  await env.time.advance(2000);
  assert.equal(
    (await env.identity.poll(challenge.id)).challenge.state,
    'authenticated',
  );
  assert.equal(env.identity.status().empNo, employee);
  await env.time.advance(600_000);
  const verifies = env.upstream.calls.filter((call) => call.path === '/verify');
  assert.equal(verifies.length, 2);
  assert.equal(verifies[1].body.token, 'renewed-token');
  assert.equal(env.identity.status().empNo, employee);
  const logs = JSON.stringify(env.logs);
  assert.ok(!logs.includes('renewed-token') && !logs.includes('scan-token'));
});

test('local UAC: distinguishes missing and invalid verify fields before legacy fallback', async (t) => {
  const upstream = await replica();
  t.after(() => upstream.close());
  const adapter = createUacAdapter(settings(upstream.origin), {
    baseUrl: upstream.origin,
    systemCode: 'SYS1',
  });
  const verify = async (bo, extra = {}) => {
    upstream.script.verify = [
      {
        body: { code: { code: '0000' }, bo: { code: '0000', ...bo }, ...extra },
      },
    ];
    return adapter.verify({ empNo: '001Ab', token: 'scan-token' });
  };
  const legacy = { user: '001AB', token: 'legacy-token' };
  const valid = await verify(legacy, { other: {} });
  assert.equal(valid.success, true);
  assert.equal(valid.token, 'legacy-token');
  assert.equal(valid.empNo, '001Ab');
  assert.equal(
    (await verify(legacy, { other: { token: 'primary-token' } })).token,
    'primary-token',
  );
  for (const other of [
    null,
    [],
    { token: null },
    { token: 42 },
    { token: '' },
  ]) {
    const result = await verify(legacy, { other });
    assert.equal(result.failureClass, 'upstream_fault');
    assert.equal(result.faultReason, 'invalid_success_payload');
  }
  for (const user of [null, 42, '']) {
    const result = await verify(
      { user },
      { other: { token: 'primary-token' } },
    );
    assert.equal(result.failureClass, 'upstream_fault');
    assert.equal(result.faultReason, 'invalid_success_payload');
  }
  assert.equal(
    (await verify({}, { other: {} })).faultReason,
    'invalid_success_payload',
  );
  assert.equal(
    (await verify({ user: '002Ab' }, { other: { token: 'primary-token' } }))
      .failureClass,
    'identity_mismatch',
  );
  const rejected = await verify(
    { code: 'REJECTED' },
    { other: { token: 'primary-token' } },
  );
  assert.equal(rejected.failureClass, 'business_rejected');
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
  assert.equal(s.challenge, undefined);
  // Rejection signs out immediately.
  await loginFlow(env);
  env.upstream.script.verify = [
    { body: { code: { code: '0000' }, bo: { code: '9999' } } },
  ];
  await env.time.advance(600_000);
  assert.equal(env.identity.status().state, 'signed_out');
  assert.equal(env.identity.status().lastOutcome, 'revalidation_rejected');
  assert.equal(env.identity.status().challenge, undefined);
  // A restart retains the owner, but requires a fresh scan.
  await loginFlow(env);
  await env.identity.close();
  const before = env.upstream.calls.length;
  const restarted = env.make();
  await restarted.ready;
  assert.equal(restarted.status().state, 'signed_out');
  assert.equal(restarted.status().ownerBound, true);
  await env.time.advance(600_000);
  assert.equal(env.upstream.calls.length, before);
  assert.deepEqual((await loginFlow({ ...env, identity: restarted })).states, [
    'waiting',
    'scanned',
    'authenticated',
  ]);
  await restarted.close();
});

for (const outcome of [
  'user_logout',
  'revalidation_rejected',
  'revalidation_unreachable',
])
  test(
    'local UAC: write failure cannot restore an invalidated session: ' +
      outcome,
    async (t) => {
      let writable = false;
      let writes = 0;
      const env = await setup(t, {
        async writeOwner(path, value) {
          writes++;
          if (!writable)
            throw Object.assign(new Error('synthetic disk fault'), {
              code: 'EACCES',
            });
          await atomicWriteJson(path, value);
        },
      });
      assert.equal((await loginFlow(env)).states.at(-1), 'failed');
      assert.equal(env.identity.status().state, 'signed_out');
      assert.equal(env.identity.status().ownerBound, false);
      writable = true;
      await loginFlow(env);
      assert.equal(env.identity.status().state, 'authenticated');
      const path = join(env.directory, 'uac-local.json');
      const saved = await readFile(path, 'utf8');
      writable = false;

      if (outcome === 'user_logout') await env.identity.logout();
      else {
        env.upstream.script.verify = [
          outcome === 'revalidation_rejected'
            ? { body: { code: { code: '0000' }, bo: { code: '9999' } } }
            : { status: 502, body: {} },
        ];
        await env.time.advance(600_000);
        if (outcome === 'revalidation_unreachable')
          await env.time.advance(30_000);
      }
      assert.equal(env.identity.status().state, 'signed_out');
      assert.equal(env.identity.status().lastOutcome, outcome);
      assert.equal(env.identity.status().challenge, undefined);
      assert.equal(writes, 2);
      assert.equal(await readFile(path, 'utf8'), saved);
      await env.identity.close();

      const before = env.upstream.calls.length;
      const restarted = env.make();
      await restarted.ready;
      assert.deepEqual(restarted.status(), {
        available: true,
        state: 'signed_out',
        ownerBound: true,
      });
      await env.time.advance(600_000);
      assert.equal(env.upstream.calls.length, before);
      await restarted.close();
    },
  );

test('local UAC: legacy ciphertext cannot restore or move an unscoped owner', async (t) => {
  const env = await setup(t, {
    writeOwner: async () => {
      throw new Error('synthetic disk fault');
    },
  });
  await env.identity.close();
  const key = await loadOrCreateSecretKey(env.directory);
  const now = env.time.now();
  const path = join(env.directory, 'uac-local.json');
  const legacy = sealSecret(
    key,
    JSON.stringify({
      version: 1,
      owner: { empNo: 'A1001' },
      session: {
        empNo: 'A1001',
        token: 'legacy-synthetic-token',
        clientIP: '',
        loginAt: now,
        verifiedAt: now,
        nextRevalidateAt: now + 600_000,
        attempt: 0,
      },
    }),
  );
  await writeFile(path, JSON.stringify(legacy));
  const current = env.make();
  await current.ready;
  assert.deepEqual(current.status(), {
    available: true,
    reason: 'owner_domain',
    state: 'signed_out',
    ownerBound: true,
  });
  await assert.rejects(current.start(), { code: 'unavailable' });
  await env.time.advance(600_000);
  assert.equal(env.upstream.calls.length, 0);
  assert.equal(await readFile(path, 'utf8'), JSON.stringify(legacy));
  assert.doesNotMatch(JSON.stringify(env.logs), /legacy-synthetic-token|A1001/);
  await current.close();
});

test('local UAC: same identity domain accepts transport changes; other domains cannot reuse the owner', async (t) => {
  const env = await setup(t);
  await loginFlow(env);
  await env.identity.close();
  const other = await replica();
  t.after(() => other.close());
  const adapterPath = join(env.directory, 'uac', 'adapter.json');
  const providerPath = join(env.directory, 'uac', 'provider.json');
  const adapter = settings(env.upstream.origin);
  adapter.qr.timeoutSeconds = 8;
  adapter.allowedDestinations[0].addresses.push('::1');
  await writeFile(adapterPath, JSON.stringify(adapter));
  await writeFile(
    providerPath,
    JSON.stringify({
      baseUrl: env.upstream.origin,
      systemCode: 'SYS1',
      revalidation: { intervalSeconds: 300 },
    }),
  );
  const same = env.make();
  await same.ready;
  assert.deepEqual(same.status(), {
    available: true,
    state: 'signed_out',
    ownerBound: true,
  });
  assert.deepEqual((await loginFlow({ ...env, identity: same })).states, [
    'waiting',
    'scanned',
    'authenticated',
  ]);
  await same.close();

  const before = env.upstream.calls.length;
  for (const change of [
    'service address',
    'system identifier',
    'protocol mapping',
  ]) {
    const origin =
      change === 'service address' ? other.origin : env.upstream.origin;
    const next = settings(origin);
    if (change === 'protocol mapping')
      next.qr.contentTemplate = 'changed:${x}:${y}';
    await writeFile(adapterPath, JSON.stringify(next));
    await writeFile(
      providerPath,
      JSON.stringify({
        baseUrl: origin,
        systemCode: change === 'system identifier' ? 'SYS2' : 'SYS1',
      }),
    );
    const moved = env.make();
    await moved.ready;
    assert.deepEqual(
      moved.status(),
      {
        available: true,
        reason: 'owner_domain',
        state: 'signed_out',
        ownerBound: true,
      },
      change,
    );
    await assert.rejects(moved.start(), { code: 'unavailable' });
    await env.time.advance(600_000);
    assert.equal(env.upstream.calls.length, before);
    assert.equal(other.calls.length, 0);
    await moved.close();
  }
});

test('local UAC: platform-only adapter changes preserve the bound owner across restart', async (t) => {
  const env = await setup(t);
  await loginFlow(env);
  await env.identity.close();
  const adapterPath = join(env.directory, 'uac', 'adapter.json');
  const vaultPath = join(env.directory, 'uac-local.json');
  const binding = await readFile(vaultPath, 'utf8');
  for (const change of [
    (adapter) => (adapter.diagnostics = { enabled: true }),
    (adapter) => (adapter.cookies = { name: 'synthetic-portal-cookie' }),
    (adapter) => (adapter.qr.derived.integrity.description = 'portal metadata'),
  ]) {
    const adapter = settings(env.upstream.origin);
    change(adapter);
    await writeFile(adapterPath, JSON.stringify(adapter));
    const restarted = env.make();
    await restarted.ready;
    assert.deepEqual(restarted.status(), {
      available: true,
      state: 'signed_out',
      ownerBound: true,
    });
    assert.deepEqual(
      (await loginFlow({ ...env, identity: restarted })).states,
      ['waiting', 'scanned', 'authenticated'],
    );
    assert.equal(await readFile(vaultPath, 'utf8'), binding);
    await restarted.close();
  }
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

test('local UAC: incomplete, malformed, or ambiguous response mappings disable scan', async (t) => {
  const env = await setup(t);
  await env.identity.close();
  for (const modify of [
    (value) => delete value.qr.success,
    (value) => delete value.qr.statusPointer,
    (value) => (value.qr.waiting = null),
    (value) => (value.qr.scanned = 1),
    (value) => delete value.qr.tokenPointer,
    (value) => delete value.qr.empNoPointer,
    (value) => (value.qr.statusPointer = 'state'),
    (value) => (value.qr.tokenPointer = '/token~2'),
    (value) => (value.qr.statusPointer = value.qr.tokenPointer),
    (value) => (value.qr.scanned = value.qr.waiting),
    (value) => (value.qr.waiting = value.qr.success),
    (value) => (value.qr.success = ' '),
    (value) => delete value.department.successValue,
    (value) => (value.department.successPointer = null),
    (value) => delete value.department.listPointer,
    (value) => (value.department.listPointer = '/rows~'),
    (value) => delete value.department.empNoPointer,
    (value) => (value.department.departmentPointers = []),
  ]) {
    const value = settings(env.upstream.origin);
    modify(value);
    await writeFile(
      join(env.directory, 'uac', 'adapter.json'),
      JSON.stringify(value),
    );
    const invalid = env.make();
    await invalid.ready;
    assert.equal(invalid.status().available, true);
    assert.equal(invalid.status().reason, 'invalid');
    assert.equal(invalid.status().state, 'signed_out');
    await assert.rejects(invalid.start(), { code: 'unavailable' });
    await invalid.close();
  }
  assert.equal(env.upstream.calls.length, 0);
});

for (const pending of [['waiting'], ['scanned'], []])
  test(
    'local UAC: optional scan status mappings: ' +
      (pending.join(', ') || 'none'),
    async (t) => {
      const env = await setup(t);
      await env.identity.close();
      const adapter = settings(env.upstream.origin);
      for (const state of ['waiting', 'scanned'])
        if (!pending.includes(state)) delete adapter.qr[state];
      await writeFile(
        join(env.directory, 'uac', 'adapter.json'),
        JSON.stringify(adapter),
      );
      const identity = env.make();
      await identity.ready;

      env.upstream.script.scan = [
        { body: { token: 'synthetic', employee: 'A1001' } },
      ];
      const malformed = await identity.start();
      assert.equal(
        (await identity.poll(malformed.id)).challenge.failureCode,
        'invalid_scan_state',
      );
      assert.equal(
        env.upstream.calls.some((call) => call.path === '/verify'),
        false,
      );

      env.upstream.script.scan = [
        ...pending.map((state) => ({ body: { state } })),
        { body: { state: 'verified', token: 'synthetic', employee: 'A1001' } },
      ];
      env.upstream.script.verify = [ok('A1001')];
      const challenge = await identity.start();
      for (const state of [...pending, 'authenticated']) {
        await env.time.advance(2000);
        assert.equal(
          (await identity.poll(challenge.id)).challenge.state,
          state,
        );
      }
      assert.equal(identity.status().empNo, 'A1001');
      assert.equal(
        env.upstream.calls.filter((call) => call.path === '/verify').length,
        1,
      );
      await identity.close();
    },
  );

test('local UAC: missing, malformed, or duplicate upstream status never reaches verify', async (t) => {
  const env = await setup(t);
  for (const [response, reason] of [
    [{ body: { token: 'synthetic', employee: 'A1001' } }, 'invalid_scan_state'],
    [
      { body: { state: '', token: 'synthetic', employee: 'A1001' } },
      'invalid_scan_state',
    ],
    [
      { body: { state: true, token: 'synthetic', employee: 'A1001' } },
      'invalid_scan_state',
    ],
    [
      {
        rawBody:
          '{"state":"denied","state":"verified","token":"synthetic","employee":"A1001"}',
      },
      'invalid_response',
    ],
  ]) {
    env.upstream.script.scan = [response];
    const challenge = await env.identity.start();
    const result = await env.identity.poll(challenge.id);
    assert.equal(result.challenge.state, 'failed');
    assert.equal(result.challenge.failureCode, reason);
    assert.equal(result.state, 'signed_out');
  }
  assert.ok(env.upstream.calls.every((call) => call.path === '/scan'));
  env.upstream.script.scan = [{ body: { state: 'waiting', unrelated: 0.5 } }];
  const waiting = await env.identity.start();
  assert.equal(
    (await env.identity.poll(waiting.id)).challenge.state,
    'waiting',
  );
});

test('local UAC: department revalidation requires explicit success and a matching row', async (t) => {
  const env = await setup(t);
  await env.identity.close();
  await writeFile(
    join(env.directory, 'uac', 'provider.json'),
    JSON.stringify({
      baseUrl: env.upstream.origin,
      systemCode: 'SYS1',
      revalidation: { strategy: 'department', intervalSeconds: 60 },
    }),
  );
  const identity = env.make();
  await identity.ready;
  await loginFlow({ ...env, identity });
  const approved = {
    rows: [{ employee: 'A1001', department: 'Fixture team' }],
  };
  env.upstream.script.info = [{ body: { ok: true, ...approved } }];
  await env.time.advance(60_000);
  assert.equal(identity.status().state, 'authenticated');
  assert.equal(
    identity.status().verifiedAt,
    new Date(env.time.now()).toISOString(),
  );
  env.upstream.script.info = [{ body: approved }];
  await env.time.advance(60_000);
  assert.equal(identity.status().state, 'signed_out');
  assert.equal(identity.status().lastOutcome, 'revalidation_rejected');
  assert.equal(identity.status().challenge, undefined);
  await identity.close();
});

test('local UAC: department checks the explicitly approved list at the RFC 6901 root', async (t) => {
  const env = await setup(t);
  await env.identity.close();
  const adapter = settings(env.upstream.origin);
  adapter.department.listPointer = '';
  adapter.department.successPointer = '/0/ok';
  await writeFile(
    join(env.directory, 'uac', 'adapter.json'),
    JSON.stringify(adapter),
  );
  await writeFile(
    join(env.directory, 'uac', 'provider.json'),
    JSON.stringify({
      baseUrl: env.upstream.origin,
      systemCode: 'SYS1',
      revalidation: { strategy: 'department', intervalSeconds: 60 },
    }),
  );
  const identity = env.make();
  await identity.ready;
  await loginFlow({ ...env, identity });
  const row = { ok: true, employee: 'A1001', department: 'Fixture team' };
  env.upstream.script.info = [{ body: [row] }];
  await env.time.advance(60_000);
  assert.equal(identity.status().state, 'authenticated');
  assert.equal(
    identity.status().verifiedAt,
    new Date(env.time.now()).toISOString(),
  );
  env.upstream.script.info = [{ body: { 0: row } }];
  await env.time.advance(60_000);
  assert.equal(identity.status().state, 'signed_out');
  assert.equal(identity.status().lastOutcome, 'revalidation_rejected');
  await identity.close();
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
    {
      status: 302,
      headers: { location: upstream.origin + '/verify' },
      body: { state: 'verified', token: 't', employee: 'A' },
    },
  ];
  const adapter = createUacAdapter(settings(upstream.origin), {
    baseUrl: upstream.origin,
    systemCode: 'S',
  });
  const result = await adapter.scan({ x: '0'.repeat(32), y: '1'.repeat(32) });
  assert.deepEqual(result, {
    state: 'unavailable',
    failureCode: 'transport_error',
  });
  assert.equal(upstream.calls.length, 1);
});

test('local UAC http: only canonical numeric address families reach DNS pinning', () => {
  assert.deepEqual(parseIpAddress('127.0.0.1'), {
    address: '127.0.0.1',
    family: 4,
  });
  assert.deepEqual(parseIpAddress('0:0:0:0:0:0:0:1'), {
    address: '::1',
    family: 6,
  });
  assert.deepEqual(parseIpAddress('::ffff:127.0.0.1'), {
    address: '::ffff:7f00:1',
    family: 6,
  });
  for (const address of [
    '127.1',
    '0x7f000001',
    '127.0.0.01',
    '256.0.0.1',
    '127.0.0.1\n',
    'fe80::1%eth0',
    ':::1',
    'dead.beef',
  ])
    assert.equal(parseIpAddress(address), null, address);
});

test('local UAC http: the pinned TLS socket keeps the authorized origin and certificate identity', async (t) => {
  const upstream = await replica();
  t.after(() => upstream.close());
  upstream.script.scan = [{ body: { state: 'waiting' } }];
  const target = new URL(upstream.origin);
  target.hostname = 'localhost';
  target.pathname = '/scan';
  const requests = [];
  const port = createUacHttp(settings(target.origin), {
    lookup: async (host) => {
      requests.push(host);
      return [{ address: '127.0.0.1', family: 4 }];
    },
  });
  const body = { sample: 'synthetic' };
  const result = await port.post(target, body, {}, 3);
  assert.equal(result.status, 200);
  assert.deepEqual(requests, ['localhost']);
  assert.equal(upstream.calls[0].headers.host, target.host);
  assert.deepEqual(upstream.calls[0].body, body);

  const otherPort = new URL(target);
  otherPort.port = target.port === '1' ? '2' : '1';
  await assert.rejects(
    port.post(otherPort, body, {}, 3),
    /UAC destination rejected/,
  );
  await assert.rejects(
    port.post(target, body, { hOsT: 'other.invalid' }, 3),
    /invalid enterprise HTTPS target/,
  );
  const mixed = createUacHttp(settings(target.origin), {
    lookup: async () => [
      { address: '127.0.0.1', family: 4 },
      { address: '192.0.2.99', family: 4 },
    ],
  });
  await assert.rejects(
    mixed.post(target, body, {}, 3),
    /UAC destination rejected/,
  );

  const otherHostname = new URL(target);
  otherHostname.hostname = 'different-uac.invalid';
  const wrongIdentity = createUacHttp(settings(otherHostname.origin), {
    lookup: async () => [{ address: '127.0.0.1', family: 4 }],
  });
  await assert.rejects(wrongIdentity.post(otherHostname, body, {}, 3), {
    code: 'ERR_TLS_CERT_ALTNAME_INVALID',
  });
  assert.equal(upstream.calls.length, 1);
});

test('local UAC http: an untrusted certificate prevents any credential POST', async (t) => {
  const upstream = await replica(createTestPki());
  t.after(() => upstream.close());
  await assert.rejects(
    createUacHttp(settings(upstream.origin)).post(
      new URL(upstream.origin + '/verify'),
      { sample: 'synthetic' },
      {},
      3,
    ),
    (error) =>
      ['UNABLE_TO_GET_ISSUER_CERT_LOCALLY', 'CERT_SIGNATURE_FAILURE'].includes(
        error.code,
      ),
  );
  assert.equal(upstream.calls.length, 0);
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
