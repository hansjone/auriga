import test from 'node:test';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createEnterpriseAuth } from '../src/local/enterprise.js';
import { createEnterpriseEnvironment } from '../src/local/enterprise-environment.js';

function collectDuringDispatch(env, execution) {
  return env.run(execution, () => {
    const value = env.contributor.resolve(execution);
    assert.equal(typeof value.then, 'undefined');
    return value;
  });
}

async function fixture(t, { empNo = '00001', beforeCredentials } = {}) {
  let authGeneration = 1;
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
    binding: () =>
      snapshot.state === 'signed_out'
        ? null
        : JSON.stringify([snapshot.shellEpoch, authGeneration]),
    getOwner: (id) => (id === 'session' ? 'member' : null),
    credentials: async () => {
      await beforeCredentials?.();
      return { empNo, token, updatedAt: null };
    },
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
      authGeneration++;
      snapshot = { state: 'signed_out', shellEpoch: 'epoch', revision: 2 };
    },
    progress() {
      snapshot.revision++;
    },
    offline() {
      snapshot = {
        ...snapshot,
        state: 'unreachable',
        revision: snapshot.revision + 1,
      };
    },
    recover() {
      snapshot = {
        ...snapshot,
        state: 'authenticated',
        revision: snapshot.revision + 1,
      };
    },
    reauthenticate() {
      authGeneration++;
    },
    hosts(outboundAllowedHosts) {
      config = { ...config, outboundAllowedHosts };
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
test('capability survives transient revisions, returns temporary outage, and requires the same authentication generation', async (t) => {
  const f = await fixture(t);
  f.progress();
  assert.equal((await f.raw()).status, 200);
  f.offline();
  assert.equal(f.auth.mint('session'), null);
  assert.equal((await f.raw()).status, 503);
  f.recover();
  f.rotate();
  assert.equal((await (await f.raw()).json()).token, f.token());
  f.reauthenticate();
  assert.equal((await f.raw()).status, 401);
});
test('enterprise outbound uses 443 by default and requires an explicitly allowed non-default port', async (t) => {
  const f = await fixture(t);
  assert.equal(
    (await f.outbound({ url: 'https://service.example.com:443/operation' }))
      .status,
    500,
  );
  assert.equal(
    (await f.outbound({ url: 'https://service.example.com:8443/operation' }))
      .status,
    403,
  );
  f.hosts(['service.example.com:8443']);
  assert.equal(
    (await f.outbound({ url: 'https://service.example.com:8443/operation' }))
      .status,
    500,
  );
  assert.equal((await f.outbound()).status, 403);
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
  const values = await collectDuringDispatch(env, execution);
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
  const values = await collectDuringDispatch(env, execution);
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

test('an old session starts each tool with the current UAC pair and a running tool can refresh', async (t) => {
  const f = await fixture(t, { empNo: '000123' });
  const env = createEnterpriseEnvironment({
    auth: f.auth,
    getServer: () => ({ port: f.port }),
    prepare: async (execution) =>
      execution.agent.session.header.id === 'session'
        ? 'trusted-tool'
        : undefined,
  });
  t.after(() => env.dispose());
  const task = (id = 'session') => ({
    token: {},
    agent: { session: { header: { id } } },
    signal: new AbortController().signal,
  });
  const parent = [process.env.DSH_AURIGA_ID, process.env.DSH_AURIGA_TOKEN];
  const firstTask = task();
  assert.equal(env.contributor.resolve(firstTask).DSH_AURIGA_TOKEN, '');
  const first = await collectDuringDispatch(env, firstTask);
  const firstHeaders = {
    'X-Dsh-Team-Task-Capability': first.DSH_TEAM_TASK_CAPABILITY,
  };
  assert.equal(first.DSH_AURIGA_ID, '000123');
  assert.equal(first.DSH_AURIGA_TOKEN, f.initialToken);
  assert.equal(env.contributor.resolve(firstTask).DSH_AURIGA_TOKEN, '');
  f.rotate();
  const resumedTask = { ...task(), agent: firstTask.agent };
  const current = await collectDuringDispatch(env, resumedTask);
  assert.equal(current.DSH_AURIGA_ID, '000123');
  assert.equal(current.DSH_AURIGA_TOKEN, f.token());
  assert.equal(first.DSH_AURIGA_TOKEN, f.initialToken);
  assert.equal((await (await f.raw(firstHeaders)).json()).token, f.token());
  const foreign = await collectDuringDispatch(env, task('other-session'));
  assert.deepEqual([foreign.DSH_AURIGA_ID, foreign.DSH_AURIGA_TOKEN], ['', '']);
  f.disable();
  const disabled = await collectDuringDispatch(env, task());
  assert.deepEqual(
    [disabled.DSH_AURIGA_ID, disabled.DSH_AURIGA_TOKEN],
    ['', ''],
  );
  assert.ok(disabled.DSH_TEAM_TASK_CAPABILITY, 'outbound must stay available');
  assert.equal((await f.raw(firstHeaders)).status, 403);
  f.enable();
  f.logout();
  const signedOut = await collectDuringDispatch(env, task());
  assert.deepEqual(
    [signedOut.DSH_AURIGA_ID, signedOut.DSH_AURIGA_TOKEN],
    ['', ''],
  );
  assert.equal((await f.raw(firstHeaders)).status, 401);
  assert.deepEqual(
    [process.env.DSH_AURIGA_ID, process.env.DSH_AURIGA_TOKEN],
    parent,
  );
});

test('discards the launch pair when an in-flight credential read outlives its task', async (t) => {
  let finish, started;
  const pending = new Promise((resolve) => (finish = resolve));
  const reading = new Promise((resolve) => (started = resolve));
  const f = await fixture(t, {
    beforeCredentials: () => {
      started();
      return pending;
    },
  });
  const env = createEnterpriseEnvironment({
    auth: f.auth,
    getServer: () => ({ port: f.port }),
    prepare: async () => 'trusted-tool',
  });
  t.after(() => env.dispose());
  const controller = new AbortController();
  const launch = collectDuringDispatch(env, {
    agent: { session: { header: { id: 'session' } } },
    signal: controller.signal,
  });
  await reading;
  controller.abort();
  finish();
  const value = await launch;
  assert.deepEqual([value.DSH_AURIGA_ID, value.DSH_AURIGA_TOKEN], ['', '']);
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
  const values = await collectDuringDispatch(env, execution);
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
