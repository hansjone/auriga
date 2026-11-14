import test from 'node:test';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createEnterpriseAuth } from '../src/local/enterprise.js';
import { createEnterpriseEnvironment } from '../src/local/enterprise-environment.js';

async function fixture(t) {
  let sent;
  const sending = new Promise((resolve) => {
    sent = resolve;
  });
  let snapshot = {
    state: 'authenticated',
    shellEpoch: 'epoch',
    revision: 1,
    identity: {
      tenantId: 'tenant',
      memberId: 'member',
      deviceId: 'device',
      providerId: 'provider',
    },
  };
  let config = {
    outboundAllowedHosts: ['service.example.com'],
    identityHeaders: { empNo: 'X-Test-Employee', token: 'X-Test-Credential' },
  };
  const initialToken = randomBytes(32).toString('base64url');
  let token = initialToken,
    time = Date.now(),
    ticks = 0,
    pending;
  const auth = createEnterpriseAuth({
    snapshot: () => snapshot,
    getOwner: (id) => (id === 'session' ? 'member' : null),
    credentials: async () => ({ empNo: '00001', token, updatedAt: null }),
    config: () => config,
    now: () => time,
    monotonic: () => ticks,
    send: async (_url, options) => {
      assert.equal(options.headers['X-Test-Credential'], token);
      assert.equal(options.headers.authorization, undefined);
      assert.equal(options.headers.cookie, undefined);
      assert.equal(options.headers.origin, undefined);
      assert.equal(options.timeoutMs, 60000);
      sent();
      await pending;
      return {
        statusCode: 500,
        headers: {
          'set-cookie': 'secret',
          'x-test-credential': token,
          'x-token': token,
          'content-type': 'application/json',
        },
        body: '{"ok":false}',
        json: { ok: false },
      };
    },
  });
  const server = createServer((req, res) => {
    void auth.handle(req, res);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    auth.close();
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  });
  const port = server.address().port,
    base = `http://127.0.0.1:${port}/dsh-team`;
  const cap = auth.mint('session');
  const headers = {
    'X-DSH-Session-Id': 'session',
    'X-Dsh-Team-Task-Capability': cap,
    'content-type': 'application/json',
  };
  const raw = (extra = {}) =>
    fetch(base + '/agent-credentials', { headers: { ...headers, ...extra } });
  const outbound = (input = {}) =>
    fetch(base + '/outbound', {
      method: 'POST',
      headers,
      body: JSON.stringify({
        url: 'https://service.example.com/operation',
        timeoutMs: 60001,
        headers: {
          Authorization: 'ignored',
          Cookie: 'ignored',
          Origin: 'ignored',
        },
        ...input,
      }),
    });
  return {
    auth,
    headers,
    raw,
    outbound,
    base,
    port,
    sending,
    token: () => token,
    initialToken,
    rotate() {
      token = randomBytes(32).toString('base64url');
    },
    disable() {
      config = { ...config, allowRawAgentToken: false };
    },
    enable() {
      config = { ...config, allowRawAgentToken: true };
    },
    logout() {
      snapshot = { state: 'signed_out', shellEpoch: 'epoch', revision: 2 };
    },
    expire() {
      time += 600001;
    },
    rollback() {
      time -= 600001;
      ticks += 600001;
    },
    delay(p) {
      pending = p;
    },
  };
}

test('raw default enabled, latest credentials read every time, logout rejects', async (t) => {
  const f = await fixture(t);
  assert.equal((await (await f.raw()).json()).token, f.initialToken);
  f.rotate();
  assert.equal((await (await f.raw()).json()).token, f.token());
  f.logout();
  assert.equal((await f.raw()).status, 401);
});
test('outbound rebuilds identity headers, strips response secrets and preserves upstream 500', async (t) => {
  const f = await fixture(t),
    res = await f.outbound();
  assert.equal(res.status, 500);
  assert.equal(res.headers.get('X-Dsh-Team-Outbound-Status'), '500');
  assert.deepEqual((await res.json()).headers, {
    'content-type': 'application/json',
  });
});
test('raw live disable denies existing cap, outbound continues, new caps lack credentials scope', async (t) => {
  const f = await fixture(t);
  f.disable();
  assert.equal((await (await f.raw()).json()).error, 'raw_token_disabled');
  assert.equal((await f.outbound()).status, 500);
  const limited = f.auth.mint('session');
  f.enable();
  assert.equal(
    (await f.raw({ 'X-Dsh-Team-Task-Capability': limited })).status,
    401,
  );
});
test('forged, conflicting aliases and cross session capabilities fail closed', async (t) => {
  const f = await fixture(t);
  assert.equal(
    (await f.raw({ 'X-Dsh-Team-Task-Capability': 'forged' })).status,
    401,
  );
  assert.equal((await f.raw({ 'X-DSH-Session-Id': 'other' })).status, 401);
  const res = await fetch(f.base + '/agent-credentials', {
    method: 'POST',
    headers: f.headers,
    body: JSON.stringify({ sessionId: 'other', empNo: '00001' }),
  });
  assert.equal(res.status, 401);
});
test('expires after 600 seconds even if wall clock moves backward', async (t) => {
  const f = await fixture(t);
  f.rollback();
  assert.equal((await f.raw()).status, 401);
});
test('task cancellation and expiration revoke capability', async (t) => {
  const f = await fixture(t);
  f.auth.revoke(f.headers['X-Dsh-Team-Task-Capability']);
  assert.equal((await f.raw()).status, 401);
  f.expire();
  assert.equal((await f.raw()).status, 401);
});
test('non-loopback socket cannot be overridden by forwarded headers', async (t) => {
  const f = await fixture(t);
  let status;
  const res = {
    writeHead(value) {
      status = value;
    },
    end() {},
  };
  await f.auth.handle(
    {
      url: '/dsh-team/agent-credentials',
      method: 'GET',
      socket: { remoteAddress: '192.0.2.10' },
      headers: { ...f.headers, 'x-forwarded-for': '127.0.0.1' },
    },
    res,
  );
  assert.equal(status, 403);
});
test('host allowlist, cleartext and userinfo are rejected', async (t) => {
  const f = await fixture(t);
  assert.equal(
    (await f.outbound({ url: 'https://other.example.com' })).status,
    403,
  );
  assert.equal(
    (await f.outbound({ url: 'http://service.example.com' })).status,
    400,
  );
  assert.equal(
    (await f.outbound({ url: 'https://user:pass@service.example.com' })).status,
    400,
  );
});
test('late outbound result after logout never reaches skill', async (t) => {
  const f = await fixture(t);
  let release;
  f.delay(
    new Promise((r) => {
      release = r;
    }),
  );
  const pending = f.outbound();
  await f.sending;
  f.logout();
  release();
  assert.equal((await pending).status, 401);
});
test('shellEnv lifecycle and packaged unchanged Python request()/resolve() compatibility', async (t) => {
  const f = await fixture(t);
  const env = createEnterpriseEnvironment({
    auth: f.auth,
    getServer: () => ({ port: f.port }),
  });
  t.after(() => env.dispose());
  const execution = {
    token: {},
    agent: { session: { header: { id: 'session' } } },
    signal: new AbortController().signal,
  };
  const values = env.contributor.resolve(execution);
  assert.equal(values.DSH_TEAM_BASE, f.base);
  assert.ok(!JSON.stringify(values).includes(f.initialToken));
  const child = spawn(
    'python',
    [
      '-c',
      'from uds_skill_auth import resolve, request; c=resolve(apply_env_aliases=False); assert c["empNo"]=="00001"; r=request("POST","https://service.example.com/operation",timeout=60.001); assert r["statusCode"]==500; print("helper compatibility OK")',
    ],
    {
      env: {
        ...process.env,
        ...values,
        DSH_SESSION_ID: 'session',
        PYTHONDONTWRITEBYTECODE: '1',
        PYTHONPATH: fileURLToPath(
          new URL('../skills/uds-skill-auth/scripts/', import.meta.url),
        ),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let out = '',
    err = '';
  child.stdout.on('data', (v) => {
    out += v;
  });
  child.stderr.on('data', (v) => {
    err += v;
  });
  const [code] = await once(child, 'close');
  assert.equal(code, 0, err);
  assert.equal(out.trim(), 'helper compatibility OK');
  env.finish(execution, {}, undefined);
  assert.equal(
    (
      await f.raw({
        'X-Dsh-Team-Task-Capability': values.DSH_TEAM_TASK_CAPABILITY,
      })
    ).status,
    401,
  );
});
test('only a verified running native job retains enterprise context until its terminal event', async (t) => {
  const f = await fixture(t),
    controller = new AbortController();
  let retained = 0,
    event;
  const env = createEnterpriseEnvironment({
    auth: f.auth,
    getServer: () => ({ port: f.port }),
    prepare: async () => 'registered-tool-context',
    retain(id) {
      assert.equal(id, 'registered-tool-context');
      retained++;
      return true;
    },
  });
  t.after(() => env.dispose());
  const execution = {
    token: {},
    agent: { session: { header: { id: 'session' } } },
    signal: controller.signal,
  };
  const values = await env.contributor.resolve(execution);
  env.finish(
    execution,
    { value: { kind: 'background', jobId: 'job' } },
    {
      list: () => [{ id: 'job', status: 'running' }],
      events: {
        subscribe(_owner, callback) {
          event = callback;
          return () => {};
        },
      },
    },
  );
  assert.equal(retained, 1);
  controller.abort();
  const headers = {
    'X-Dsh-Team-Task-Capability': values.DSH_TEAM_TASK_CAPABILITY,
  };
  assert.equal((await f.raw(headers)).status, 200);
  event({ job: { id: 'job' }, type: 'settled' });
  assert.equal((await f.raw(headers)).status, 401);
});
test('failed native context retention revokes a promoted job capability immediately', async (t) => {
  const f = await fixture(t);
  const env = createEnterpriseEnvironment({
    auth: f.auth,
    getServer: () => ({ port: f.port }),
    prepare: async () => 'registered-tool-context',
    retain: () => false,
  });
  t.after(() => env.dispose());
  const execution = {
    token: {},
    agent: { session: { header: { id: 'session' } } },
    signal: new AbortController().signal,
  };
  const values = await env.contributor.resolve(execution);
  env.finish(
    execution,
    { value: { kind: 'promoted', jobId: 'job' } },
    {
      list: () => [{ id: 'job', status: 'running' }],
      events: {
        subscribe() {
          assert.fail('must not retain failed source');
        },
      },
    },
  );
  assert.equal(
    (
      await f.raw({
        'X-Dsh-Team-Task-Capability': values.DSH_TEAM_TASK_CAPABILITY,
      })
    ).status,
    401,
  );
});
