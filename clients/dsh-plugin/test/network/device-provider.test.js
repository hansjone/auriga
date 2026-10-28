/* global ReadableStream */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { setImmediate } from 'node:timers';
import { openTrustStore } from '../../src/network/trust/store.js';
import {
  publicationFixture,
  signedKeyset,
  signedAnnouncement,
  testRoots,
} from './trust/keyset-fixtures.js';

async function deviceFixture(t, clockOffset = 0) {
  const { createDeviceProvider } = await import(
    process.env.AURIGA_DEVICE_PROVIDER_MODULE ??
      '../../src/network/device-provider.js'
  );
  const { payload, now } = publicationFixture();
  const identity = {
    tenant: '11111111-1111-4111-8111-111111111111',
    realm: '2',
    node: '64',
    device: '33333333-3333-4333-8333-333333333333',
    systemAddress: 'aur1:2.40.0',
  };
  const vector = JSON.parse(
    await readFile(
      new URL(
        '../../../../contracts/network/fixtures/client-subjects.json',
        import.meta.url,
      ),
    ),
  )[0];
  const responses = {
    register: { ...identity, created: true },
    tickets: {
      ...vector,
      identity,
      serverAudience: 'broker-test',
      expiresAt: new Date(now() + 30000).toISOString(),
      connectionExpiresAt: new Date(now() + 3600000).toISOString(),
      protocolVersion: 1,
    },
  };
  const claims = { tid: identity.tenant, did: identity.device, sub: 'subject' };
  let respond;
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init });
    if (respond) return respond(url, init);
    return Response.json(responses[url.pathname.split('/').at(-1)]);
  });
  const provider = createDeviceProvider({
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
      network: {
        realm: '2',
        roots: testRoots,
        wssUrl: 'wss://network.example.com/',
      },
    },
    getDevice: async () => ({ accessToken: 'device', claims: { ...claims } }),
    now: () => now() + clockOffset,
  });
  const controller = new AbortController();
  const prepared = await provider.prepare({ signal: controller.signal });
  assert.equal(prepared.origin, 'https://portal.example.com');
  assert.notEqual(new URL(prepared.url).host, new URL(prepared.origin).host);
  return {
    prepared,
    responses,
    claims,
    controller,
    calls,
    payload,
    respond(fn) {
      respond = fn;
    },
  };
}

test('device submit uses the fixed HTTPS path, device bearer and complete original bytes', async (t) => {
  const f = await deviceFixture(t);
  const raw = '{"status":"platform_stored","optional":{"preserved":true}}';
  f.respond(async () => new Response(raw));
  const request = {
    kind: 'message',
    source: { source_binding: 'source-1' },
    idempotency: { producer_epoch: '42', key: 'original' },
    body: { subtype: 'text', text: 'message' },
  };
  const result = await f.prepared.submit(request, f.controller.signal);
  assert.equal(Buffer.from(result).toString(), raw);
  assert.equal(
    f.calls[0].url.href,
    'https://portal.example.com/api/ai/v1/network/submit',
  );
  assert.equal(f.calls[0].init.headers.authorization, 'Bearer device');
  assert.equal(f.calls[0].init.body, JSON.stringify(request));
  assert.equal(f.calls[0].init.credentials, 'omit');
  assert.equal(f.calls[0].init.redirect, 'error');
  f.claims.did = 'different-device';
  await assert.rejects(
    f.prepared.submit(request, f.controller.signal),
    (error) => error.status === 401,
  );
  assert.equal(f.calls.length, 1);
});

for (const offset of [-200, -59000, 59000])
  test('ticket tolerates client clock offset ' + offset + 'ms', async (t) => {
    const f = await deviceFixture(t, offset);
    await f.prepared.register(f.controller.signal);
    const ticket = await f.prepared.getTicket(f.controller.signal);
    assert.equal(
      ticket.expiresAtMs,
      Date.parse(f.responses.tickets.connectionExpiresAt),
    );
  });

for (const offset of [-60001, 90001])
  test(
    'ticket rejects client clock beyond lifetime tolerance ' + offset + 'ms',
    async (t) => {
      const f = await deviceFixture(t, offset);
      await f.prepared.register(f.controller.signal);
      await assert.rejects(
        f.prepared.getTicket(f.controller.signal),
        /invalid device network ticket/,
      );
    },
  );

test('ticket connection deadline has its own 60 second upper tolerance', async (t) => {
  const f = await deviceFixture(t);
  await f.prepared.register(f.controller.signal);
  f.responses.tickets.connectionExpiresAt = new Date(
    Date.parse(f.responses.tickets.connectionExpiresAt) + 60001,
  ).toISOString();
  await assert.rejects(
    f.prepared.getTicket(f.controller.signal),
    /invalid device network ticket/,
  );
});

for (const [name, mutate] of [
  ['missing created', (r) => delete r.created],
  ['extra field', (r) => (r.initialize = true)],
  ['created string', (r) => (r.created = 'true')],
  ['tenant', (r) => (r.tenant = '22222222-2222-4222-8222-222222222222')],
  ['device', (r) => (r.device = '22222222-2222-4222-8222-222222222222')],
  ['realm', (r) => (r.realm = '3')],
  ['node overflow', (r) => (r.node = '18446744073709551616')],
  ['systemAddress', (r) => (r.systemAddress = 'aur1:2.41.0')],
])
  test('registration rejects ' + name, async (t) => {
    const f = await deviceFixture(t);
    mutate(f.responses.register);
    await assert.rejects(
      f.prepared.register(f.controller.signal),
      /registration/,
    );
  });

for (const [name, mutate] of [
  [
    'upSubject binding',
    (r) => (r.upSubject = 'auriga.net.up.a.' + 'a'.repeat(26)),
  ],
  ['down subject', (r) => (r.downSubject += 'x')],
  ['reply prefix', (r) => (r.replyPrefix += '.*')],
  [
    'missing shard',
    (r) => (r.upSubject = r.upSubject.replace(/up\.[^.]+\./, 'up.')),
  ],
  ['ingress', (r) => (r.ingress = 'a'.repeat(26))],
  ['ticket derivation', (r) => (r.ticket = 'a'.repeat(43))],
  [
    'cross device',
    (r) => (r.identity.device = '22222222-2222-4222-8222-222222222222'),
  ],
  ['cross node', (r) => (r.identity.node = '65')],
  ['protocol', (r) => (r.protocolVersion = 2)],
  ['expired redemption', (r) => (r.expiresAt = '2020-01-01T00:00:00Z')],
  ['long redemption', (r) => (r.expiresAt = '2099-01-01T00:00:00Z')],
  ['missing connection deadline', (r) => delete r.connectionExpiresAt],
  ['long connection', (r) => (r.connectionExpiresAt = '2099-01-01T00:00:00Z')],
])
  test('ticket rejects ' + name, async (t) => {
    const f = await deviceFixture(t);
    await f.prepared.register(f.controller.signal);
    mutate(f.responses.tickets);
    await assert.rejects(
      f.prepared.getTicket(f.controller.signal),
      /ticket|registration/,
    );
  });

test('credential replacement and late aborted registration never create ticket authority', async (t) => {
  const f = await deviceFixture(t);
  f.claims.did = 'new-device';
  await assert.rejects(
    f.prepared.register(f.controller.signal),
    (e) => e.status === 401,
  );
  assert.equal(f.calls.length, 0);
});
test('raw duplicate registration keys and response budget fail closed', async (t) => {
  for (const raw of ['{"created":false,"created":true}', 'x'.repeat(65537)]) {
    const f = await deviceFixture(t);
    f.respond(() => new Response(raw));
    await assert.rejects(
      f.prepared.register(f.controller.signal),
      /duplicate|too large/,
    );
  }
});
for (const status of [401, 403, 429, 503])
  test('control preserves HTTP ' + status, async (t) => {
    const f = await deviceFixture(t);
    f.respond(() => Response.json({}, { status }));
    await assert.rejects(
      f.prepared.register(f.controller.signal),
      (e) => e.status === status,
    );
  });

for (const code of [
  'network.revoked',
  'network.unauthorized',
  'common.forbidden',
])
  test(
    'HTTP denial only propagates an explicit revocation code ' + code,
    async (t) => {
      const f = await deviceFixture(t);
      f.respond(() => Response.json({ code }, { status: 403 }));
      await assert.rejects(
        f.prepared.register(f.controller.signal),
        (e) =>
          e.status === 403 &&
          e.code === (code === 'network.revoked' ? code : undefined),
      );
    },
  );

test('native fetch and body-stream failure are transient control errors, not validation errors', async (t) => {
  const { DeviceControlUnavailable } =
    await import('../../src/network/device-provider.js');
  const f = await deviceFixture(t);
  await f.prepared.register(f.controller.signal);
  f.respond(() => {
    throw new TypeError('fetch failed');
  });
  await assert.rejects(
    f.prepared.getTicket(f.controller.signal),
    DeviceControlUnavailable,
  );
  f.respond(
    () =>
      new Response(
        new ReadableStream({
          start(controller) {
            controller.error(new TypeError('stream failed'));
          },
        }),
      ),
  );
  await assert.rejects(
    f.prepared.getTicket(f.controller.signal),
    DeviceControlUnavailable,
  );
});

for (const [name, mutate] of [
  ['bad root', (a) => (a.root_kid = 'unknown')],
  ['bad signature', (a) => (a.root_signature = 'a'.repeat(86))],
  ['missing digest', (a) => delete a.manifest_digest],
  ['bad version', (a) => (a.keyset_version = '0')],
  ['extra field', (a) => (a.realm = '2')],
])
  test('announcement rejects ' + name, async () => {
    const { payload, now } = publicationFixture();
    const trust = await openTrustStore({
      cache: { async load() {}, async commit() {} },
      roots: testRoots,
      config: signedKeyset(payload),
      realm: '2',
      requireAnnouncement: true,
      now,
    });
    const announcement = signedAnnouncement(payload);
    mutate(announcement);
    await assert.rejects(trust.announce(announcement));
    assert.throws(() => trust.assertUsable());
  });
test('validly signed wrong digest/deadline and wrong publication realm cannot open the gate', async () => {
  const { payload, now } = publicationFixture();
  const trust = await openTrustStore({
    cache: { async load() {}, async commit() {} },
    roots: testRoots,
    config: signedKeyset(payload),
    realm: '2',
    requireAnnouncement: true,
    now,
  });
  await trust.announce(
    signedAnnouncement(payload, {
      manifest_digest: 'sha256:' + 'a'.repeat(64),
    }),
  );
  assert.throws(() => trust.assertUsable(), /announcement/);
  await assert.rejects(
    trust.update(signedKeyset({ ...payload, realm: '3', keyset_version: '6' })),
    /realm/,
  );
  await assert.rejects(trust.announce(signedAnnouncement(payload)), /rollback/);
});
test('announcement write uncertainty fences old keys and late lower replies even after a failed commit', async () => {
  const { payload, now } = publicationFixture();
  let rejectWrite = false;
  const trust = await openTrustStore({
    cache: {
      async load() {},
      async commit() {
        if (rejectWrite) throw new Error('fsync unknown');
      },
    },
    roots: testRoots,
    config: signedKeyset(payload),
    realm: '2',
    requireAnnouncement: true,
    now,
  });
  await trust.announce(signedAnnouncement(payload));
  rejectWrite = true;
  await assert.rejects(
    trust.announce(signedAnnouncement({ ...payload, keyset_version: '6' })),
    /fsync unknown/,
  );
  assert.throws(
    () => trust.key('response', 'protocol_response', '5'),
    /announcement/,
  );
  await assert.rejects(trust.announce(signedAnnouncement(payload)), /rollback/);
});
test('newer announcement observed while an older publication writes cannot be cleared by its late completion', async () => {
  const { payload, now } = publicationFixture();
  let block = false,
    release,
    entered = false;
  const gate = new Promise((r) => {
    release = r;
  });
  const trust = await openTrustStore({
    roots: testRoots,
    config: signedKeyset(payload),
    realm: '2',
    requireAnnouncement: true,
    now,
    cache: {
      async load() {},
      async commit() {
        if (block) {
          entered = true;
          await gate;
        }
      },
    },
  });
  await trust.announce(signedAnnouncement(payload));
  block = true;
  const six = { ...payload, keyset_version: '6' },
    seven = { ...payload, keyset_version: '7' };
  const old = trust.update(signedKeyset(six));
  while (!entered) await new Promise((r) => setImmediate(r));
  const announced = trust.announce(signedAnnouncement(seven));
  assert.throws(() => trust.key('response', 'protocol_response', '5'));
  release();
  await old;
  await announced;
  assert.throws(() => trust.assertUsable(), /announcement/);
  await assert.rejects(trust.announce(signedAnnouncement(six)), /rollback/);
  await trust.update(signedKeyset(seven));
  trust.assertUsable();
});
test('root signed announcement hard deadline does not extend on repeated GET', async () => {
  const { payload, now } = publicationFixture();
  let clock = now();
  const trust = await openTrustStore({
    roots: testRoots,
    config: signedKeyset(payload),
    realm: '2',
    requireAnnouncement: true,
    now: () => clock,
    cache: { async load() {}, async commit() {} },
  });
  const announcement = signedAnnouncement(payload);
  await trust.announce(announcement);
  const deadline = trust.expiresAt;
  clock = deadline;
  await assert.rejects(trust.announce(announcement), /expired/);
  assert.throws(() => trust.key('response', 'protocol_response', '5'));
  assert.equal(trust.expiresAt, deadline);
});

test('device factory uses exact authenticated routes, IAM sharded subjects and connection lifetime', async (t) => {
  const { createDeviceProvider } = await import(
    process.env.AURIGA_DEVICE_PROVIDER_MODULE ??
      '../../src/network/device-provider.js'
  );
  const { payload, now } = publicationFixture();
  const registration = {
    tenant: '11111111-1111-4111-8111-111111111111',
    realm: '2',
    node: '64',
    device: '33333333-3333-4333-8333-333333333333',
    systemAddress: 'aur1:2.40.0',
    created: true,
  };
  const vector = JSON.parse(
    await readFile(
      new URL(
        '../../../../contracts/network/fixtures/client-subjects.json',
        import.meta.url,
      ),
    ),
  )[0];
  const { created: _, ...identity } = registration;
  const ticket = {
    ...vector,
    identity,
    serverAudience: 'broker-test',
    expiresAt: new Date(now() + 30000).toISOString(),
    connectionExpiresAt: new Date(now() + 3600000).toISOString(),
    protocolVersion: 1,
  };
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url: url.href, init });
    const value = url.pathname.endsWith('/register')
      ? registration
      : url.pathname.endsWith('/tickets')
        ? ticket
        : url.pathname.endsWith('/announcement')
          ? signedAnnouncement(payload)
          : signedKeyset(payload);
    return Response.json(value);
  });
  const provider = createDeviceProvider({
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
      network: {
        realm: '2',
        roots: testRoots,
        wssUrl: 'wss://network.example.com/',
      },
    },
    getDevice: async () => ({
      accessToken: 'real-device',
      claims: {
        tid: registration.tenant,
        did: registration.device,
        sub: 'subject',
      },
    }),
    now,
  });
  const prepared = await provider.prepare({
    signal: new AbortController().signal,
  });
  await prepared.register(new AbortController().signal);
  const value = await prepared.getTicket(new AbortController().signal);
  assert.equal(value.subjects.upSubject, ticket.upSubject);
  assert.equal(value.expiresAtMs, Date.parse(ticket.connectionExpiresAt));
  assert.notEqual(value.expiresAtMs, Date.parse(ticket.expiresAt));
  for (const { url, init } of calls) {
    assert.equal(new URL(url).origin, 'https://portal.example.com');
    assert.equal(init.headers.authorization, 'Bearer real-device');
    assert.equal(init.headers.cookie, undefined);
    assert.equal(init.redirect, 'error');
    assert.equal(init.credentials, 'omit');
  }
});
