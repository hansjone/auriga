import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { fixture, login, OWNER } from './fixtures.mjs';
import { BusinessProxy } from '../business.mjs';
import { valid } from '../schema.mjs';
const vectors = JSON.parse(
  readFileSync(
    new URL('../../../contracts/test/auth-k3-fixtures.json', import.meta.url),
  ),
);
const definitions = JSON.parse(
  readFileSync(
    new URL(
      '../../../contracts/plugin/shell-business-operations.json',
      import.meta.url,
    ),
  ),
).operations;
const envelope = (broker) => ({
  protocolVersion: 1,
  requestId: randomUUID(),
  shellEpoch: broker.epoch,
  expectedRevision: broker.revision,
  expiresAt: new Date(Date.now() + 25000).toISOString(),
});

test('profile and provider upstream responses project to fixed DTOs; invalid data fails closed', async (t) => {
  const f = fixture();
  await login(f);
  let body;
  const proxy = new BusinessProxy(f.broker, async () => Response.json(body));
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const call = (operation) =>
    proxy.call(
      { ...envelope(f.broker), call: { operation, input: {} } },
      OWNER,
    );
  body = {
    account: {
      id: f.identity.memberId,
      tenantId: f.identity.tenantId,
      displayName: 'test',
      email: 'hidden',
    },
    session: { secret: 'hidden' },
  };
  assert.deepEqual((await call('profile_read')).output.body, {
    memberId: f.identity.memberId,
    displayName: 'test',
  });
  for (const account of [
    {},
    { id: randomUUID(), tenantId: f.identity.tenantId, displayName: 'wrong' },
    { id: f.identity.memberId, tenantId: f.identity.tenantId, displayName: 12 },
  ]) {
    body = { account };
    assert.ok((await call('profile_read')).error);
  }
  body = {
    items: [
      {
        id: f.identity.providerId,
        connectorType: 'uac',
        displayName: { zh: 'test' },
        order: 1,
        primaryStep: 'form',
        private: 'hidden',
      },
    ],
  };
  assert.deepEqual((await call('provider_diagnostic')).output.body, {
    available: true,
  });
  body = { items: [] };
  assert.deepEqual((await call('provider_diagnostic')).output.body, {
    available: false,
  });
  body = {
    items: [
      {
        id: randomUUID(),
        connectorType: 'uac',
        displayName: { zh: 'test' },
        order: 1,
        primaryStep: 'form',
      },
    ],
  };
  assert.deepEqual((await call('provider_diagnostic')).output.body, {
    available: false,
  });
  for (body of [
    { available: true },
    { items: null },
    { items: [null] },
    { items: [{ id: 'bad' }] },
  ])
    assert.ok((await call('provider_diagnostic')).error);
});

test('ticket operations preserve raw lexical evidence before JSON parsing and enforce bounds and epoch', async (t) => {
  const f = fixture();
  await login(f);
  let raw;
  const proxy = new BusinessProxy(f.broker, async () => new Response(raw));
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  for (const operation of [
    'UnifiedCreateDownloadTicket',
    'UnifiedRenewInstallation',
    'createMemberResourceDownloadTicket',
  ]) {
    const definition = definitions.find((d) => d.operation === operation);
    for (const vector of vectors.wireCases) {
      raw = vector.raw;
      const output = await proxy.execute(
        definition,
        {},
        f.broker.guard(),
        25000,
        OWNER,
      );
      assert.equal(output.body.wire, vector.wire);
      assert.ok(valid(definition.output, output));
    }
    raw = 'x'.repeat(65537);
    await assert.rejects(
      proxy.execute(definition, {}, f.broker.guard(), 25000, OWNER),
    );
  }
  const context = f.broker.guard();
  await f.broker.logout();
  raw = '{}';
  await assert.rejects(
    proxy.execute(
      definitions.find((d) => d.operation === 'UnifiedCreateDownloadTicket'),
      {},
      context,
      25000,
      OWNER,
    ),
  );
});

test('network registration comes from C and binds live session, epoch and revision', async (t) => {
  const f = fixture();
  await login(f);
  f.platform.config.network = { realm: '2' };
  const proxy = new BusinessProxy(f.broker, undefined, undefined, {
    networkCore: {},
  });
  t.after(async () => {
    proxy.close();
    await f.broker.close();
  });
  const peer = proxy.host.connect({
    verified: true,
    connectionId: randomUUID(),
    sid: randomUUID(),
    processStart: String(Date.now()),
    role: 'plugin-backend',
  });
  const executionContextId = (
    await proxy.host.command(
      {
        ...envelope(f.broker),
        action: 'register',
        context: {
          kind: 'session',
          hostSessionId: 'native',
          expiresAt: new Date(Date.now() + 60000).toISOString(),
        },
      },
      peer,
    )
  ).context.executionContextId;
  const registration = {
    ...vectors.registration,
    tenant: f.identity.tenantId,
    device: f.identity.deviceId,
  };
  const record = {
    owner: peer.id,
    context: f.broker.guard(),
    registration,
    pending: new Map(),
    sources: new Map(),
    opening: Promise.resolve(),
    streams: new Set(),
    controller: new AbortController(),
  };
  proxy.network.links.set(peer.id, record);
  const message = {
    ...envelope(f.broker),
    call: { operation: 'network_open', input: { executionContextId } },
  };
  const result = await proxy.call(message, peer.id);
  assert.deepEqual(result.output.body.registration, registration);
  assert.deepEqual(result.output.body.binding, {
    executionContextId,
    shellEpoch: f.broker.epoch,
    revision: f.broker.revision,
  });
  assert.ok(valid('ShellAuthBusinessResult', result));
  for (const patch of [
    { created: 'false' },
    { tenant: randomUUID() },
    { device: randomUUID() },
    { realm: '3' },
    { systemAddress: 'aur1:2.41.0' },
    { node: '18446744073709551616' },
  ]) {
    record.registration = { ...registration, ...patch };
    assert.ok(
      (await proxy.call({ ...message, requestId: randomUUID() }, peer.id))
        .error,
    );
  }
  record.registration = registration;
  await assert.rejects(
    proxy.call(
      {
        ...message,
        requestId: randomUUID(),
        call: {
          operation: 'network_open',
          input: { executionContextId, tenant: registration.tenant },
        },
      },
      peer.id,
    ),
  );
  proxy.host.drop(executionContextId);
  assert.ok((await proxy.call(message, peer.id)).error);
});
