import { test } from 'node:test';
import assert from 'node:assert/strict';
import { request } from 'node:http';
import { randomUUID, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { createLoopback } from '../loopback.mjs';

const require = createRequire(
  new URL('../../dsh-plugin/package.json', import.meta.url),
);
const Ajv = require('ajv/dist/2020.js').default;
const ajv = new Ajv({ strict: true });
ajv.addFormat(
  'uuid',
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
);
ajv.addFormat('date-time', (s) => Number.isFinite(Date.parse(s)));
for (const file of [
  'auth/uac-login-info',
  'auth/uac-session-binding',
  'plugin/shell-auth',
  'plugin/shell-auth-control',
]) {
  const url = new URL(
    `../../../contracts/${file}.schema.json`,
    import.meta.url,
  );
  ajv.addSchema(JSON.parse(readFileSync(url, 'utf8')), url.href);
}
const validators = Object.fromEntries(
  [
    'ShellAuthLoopbackExchange',
    'ShellAuthPageCommand',
    'ShellAuthPageSession',
    'ShellAuthEvent',
  ].map((name) => {
    const file =
      name === 'ShellAuthEvent' ? 'shell-auth' : 'shell-auth-control';
    return [
      name,
      ajv.getSchema(
        new URL(
          `../../../contracts/plugin/${file}.schema.json`,
          import.meta.url,
        ).href +
          '#/$defs/' +
          name,
      ),
    ];
  }),
);
const validate = (name, value) => validators[name](value);
const secret = () => randomBytes(32).toString('base64url');
function http(origin, path, body, headers = {}, method = 'POST') {
  return new Promise((resolve, reject) => {
    const req = request(
      origin + path,
      {
        method,
        headers: {
          Origin: origin,
          'Content-Type': 'application/json',
          'Sec-Fetch-Site': 'same-origin',
          'Sec-Fetch-Mode': 'cors',
          'Sec-Fetch-Dest': 'empty',
          ...headers,
        },
      },
      (res) => {
        let raw = '';
        res.setEncoding('utf8');
        res.on('data', (s) => {
          raw += s;
        });
        res.on('end', () => {
          let data;
          try {
            data = JSON.parse(raw);
          } catch {
            data = raw;
          }
          resolve({ status: res.statusCode, headers: res.headers, data });
        });
      },
    );
    req.on('error', reject);
    req.end(body === undefined ? undefined : JSON.stringify(body));
  });
}
async function fixture(t) {
  let time = 0,
    calls = 0,
    projection;
  const broker = {
    epoch: randomUUID(),
    async pageCommand(command) {
      calls++;
      return (
        projection ?? {
          protocolVersion: 1,
          requestId: command.requestId,
          type: 'snapshot',
          snapshot: {
            protocolVersion: 1,
            shellEpoch: this.epoch,
            revision: 0,
            state: 'signed_out',
            expiresAt: new Date(Date.now() + 30000).toISOString(),
          },
        }
      );
    },
  };
  const loop = await createLoopback({ broker, validate, now: () => time });
  t.after(() => loop.close());
  const exchange = (url = loop.issue(), headers = {}) =>
    http(
      loop.origin,
      '/loopback/exchange',
      { ticket: new URL(url).hash.slice(1), clientNonce: secret() },
      headers,
    );
  return {
    loop,
    broker,
    exchange,
    advance: (n) => {
      time += n;
    },
    calls: () => calls,
    projection: (p) => {
      projection = p;
    },
  };
}
test('static page contains no identity or launch ticket and has restrictive headers', async (t) => {
  const { loop } = await fixture(t);
  const url = loop.issue();
  const response = await http(loop.origin, '/', undefined, {}, 'GET');
  assert.equal(response.status, 200);
  assert.ok(!response.data.includes(new URL(url).hash.slice(1)));
  assert.match(
    response.headers['content-security-policy'],
    /frame-ancestors 'none'/,
  );
  assert.equal(response.headers['cache-control'], 'no-store');
  assert.equal(response.headers['referrer-policy'], 'no-referrer');
  assert.equal(response.headers['set-cookie'], undefined);
});
test('ticket exchange is single-use even with concurrent requests; session matches frozen schema', async (t) => {
  const { loop, exchange } = await fixture(t);
  const url = loop.issue();
  const results = await Promise.all([exchange(url), exchange(url)]);
  assert.deepEqual(results.map((r) => r.status).sort(), [200, 403]);
  const session = results.find((r) => r.status === 200).data;
  assert.ok(validate('ShellAuthPageSession', session));
  assert.notEqual(session.pageToken, session.csrf);
});
test('ticket expires at 30 seconds, and epoch change invalidates it', async (t) => {
  const f = await fixture(t);
  const url = f.loop.issue();
  f.advance(30000);
  assert.equal((await f.exchange(url)).status, 403);
  const next = f.loop.issue();
  f.broker.epoch = randomUUID();
  assert.equal((await f.exchange(next)).status, 403);
});
test('wrong Host, Origin, fetch metadata, and cross-port ticket are rejected', async (t) => {
  const f = await fixture(t),
    other = await fixture(t);
  for (const headers of [
    { Host: 'localhost:' + new URL(f.loop.origin).port },
    { Origin: other.loop.origin },
    { 'Sec-Fetch-Site': 'cross-site' },
    { 'Sec-Fetch-Mode': 'navigate' },
    { 'Sec-Fetch-Dest': 'iframe' },
    { 'Sec-Fetch-Site': '' },
  ]) {
    assert.equal((await f.exchange(f.loop.issue(), headers)).status, 403);
  }
  assert.equal((await other.exchange(f.loop.issue())).status, 403);
});
test('messages require both memory secrets; cookies grant no access; page commands exclude IPC logout', async (t) => {
  const f = await fixture(t),
    session = (await f.exchange()).data;
  const command = {
    protocolVersion: 1,
    requestId: randomUUID(),
    shellEpoch: f.broker.epoch,
    expectedRevision: 0,
    action: 'snapshot',
  };
  const headers = {
    'X-Auriga-Shell-Token': session.pageToken,
    'X-Auriga-Shell-CSRF': session.csrf,
  };
  for (const invalid of [
    {},
    { Cookie: 'platform=anything' },
    { 'X-Auriga-Shell-Token': session.pageToken },
    { ...headers, 'X-Auriga-Shell-CSRF': secret() },
  ]) {
    assert.equal(
      (await http(f.loop.origin, '/loopback/messages', command, invalid))
        .status,
      403,
    );
  }
  assert.equal(f.calls(), 0);
  for (const body of [
    { ...command, action: 'logout' },
    { ...command, url: 'https://example.com' },
    { ...command, expectedRevision: -1 },
  ]) {
    assert.equal(
      (await http(f.loop.origin, '/loopback/messages', body, headers)).status,
      403,
    );
  }
  const response = await http(f.loop.origin, '/loopback/messages', command, {
    ...headers,
    Cookie: 'unrelated=dsh',
  });
  assert.equal(response.status, 200);
  assert.ok(validate('ShellAuthEvent', response.data));
  assert.equal(f.calls(), 1);
});
test('page sessions expire at 15 minutes and broker restarts invalidate sessions', async (t) => {
  const f = await fixture(t),
    session = (await f.exchange()).data;
  const headers = {
    'X-Auriga-Shell-Token': session.pageToken,
    'X-Auriga-Shell-CSRF': session.csrf,
  };
  f.advance(900000);
  assert.equal(
    (await http(f.loop.origin, '/loopback/messages', {}, headers)).status,
    403,
  );
  const next = (await f.exchange()).data;
  f.broker.epoch = randomUUID();
  assert.equal(
    (
      await http(
        f.loop.origin,
        '/loopback/messages',
        {},
        {
          'X-Auriga-Shell-Token': next.pageToken,
          'X-Auriga-Shell-CSRF': next.csrf,
        },
      )
    ).status,
    403,
  );
});
test('invalid broker projection fails closed without leaking credentials or exceptions', async (t) => {
  const f = await fixture(t),
    session = (await f.exchange()).data;
  const token = secret();
  f.projection({ token });
  const response = await http(
    f.loop.origin,
    '/loopback/messages',
    {
      protocolVersion: 1,
      requestId: randomUUID(),
      shellEpoch: f.broker.epoch,
      expectedRevision: 0,
      action: 'snapshot',
    },
    {
      'X-Auriga-Shell-Token': session.pageToken,
      'X-Auriga-Shell-CSRF': session.csrf,
    },
  );
  assert.equal(response.status, 503);
  assert.equal(response.data.code, 'common.unavailable');
  assert.ok(!JSON.stringify(response).includes(token));
});
test('ticket/session inventories and request size are bounded', async (t) => {
  const f = await fixture(t);
  assert.equal(
    (
      await http(f.loop.origin, '/loopback/exchange', {
        value: 'x'.repeat(9000),
      })
    ).status,
    403,
  );
  for (let i = 0; i < 32; i++) f.loop.issue();
  assert.throws(() => f.loop.issue(), /common.forbidden/);
  f.advance(30000);
  assert.doesNotThrow(() => f.loop.issue());
});
