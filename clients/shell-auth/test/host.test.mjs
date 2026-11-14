import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture, login } from './fixtures.mjs';
import { HostChannel } from '../host.mjs';
import { Streams } from '../streams.mjs';
export const envelope = (broker) => ({
  protocolVersion: 1,
  requestId: randomUUID(),
  shellEpoch: broker.epoch,
  expectedRevision: broker.revision,
  expiresAt: new Date(Date.now() + 25000).toISOString(),
});
export const register = (broker, hostSessionId = 'host-session') => ({
  ...envelope(broker),
  action: 'register',
  context: {
    kind: 'session',
    hostSessionId,
    expiresAt: new Date(Date.now() + 600000).toISOString(),
  },
});
export const peer = (host) =>
  host.connect({
    verified: true,
    connectionId: randomUUID(),
    sid: randomUUID(),
    processStart: String(Date.now()),
    role: 'plugin-backend',
  });
test('K2 contexts derive owner and random handles; enforce role, parent, deadline and request idempotency', async (t) => {
  const f = fixture();
  await login(f);
  const streams = new Streams(f.broker),
    host = new HostChannel(f.broker, streams);
  t.after(async () => {
    host.close();
    streams.close();
    await f.broker.close();
  });
  assert.throws(
    () => host.connect({ verified: true, role: 'page' }),
    /peer_not_allowed/,
  );
  const a = peer(host),
    b = peer(host),
    request = register(f.broker),
    result = await host.command(request, a);
  assert.ok(result.context);
  assert.notEqual(result.context.executionContextId, request.requestId);
  assert.equal(result.context.ownerId, a.id);
  assert.deepEqual(await host.command(request, a), result);
  assert.equal(
    (
      await host.command(
        {
          ...request,
          context: { ...request.context, hostSessionId: 'tampered' },
        },
        a,
      )
    ).error.reason,
    'stale_identity',
  );
  const toolRequest = {
    ...envelope(f.broker),
    action: 'register',
    context: {
      kind: 'tool',
      hostSessionId: 'host-session',
      toolCallId: randomUUID(),
      rootCallId: randomUUID(),
      parentExecutionContextId: result.context.executionContextId,
      expiresAt: new Date(Date.now() + 600000).toISOString(),
    },
  };
  assert.equal(
    (await host.command(toolRequest, b)).error.reason,
    'context_revoked',
  );
  const tool = await host.command(
    { ...toolRequest, requestId: randomUUID() },
    a,
  );
  assert.ok(tool.context);
  const revoked = await host.command(
    {
      ...envelope(f.broker),
      action: 'revoke',
      executionContextId: result.context.executionContextId,
    },
    a,
  );
  assert.equal(
    revoked.revokedExecutionContextId,
    result.context.executionContextId,
  );
  assert.throws(
    () => host.resolve(a.id, tool.context.executionContextId),
    /context_revoked/,
  );
  assert.equal(
    (
      await host.command(
        {
          ...envelope(f.broker),
          action: 'register',
          context: {
            kind: 'session',
            hostSessionId: 'x',
            expiresAt: new Date(Date.now() - 1).toISOString(),
          },
        },
        a,
      )
    ).error.reason,
    'expired',
  );
});
test('K2 credentials re-read on same request, validate binding, live raw policy and revocation race', async (t) => {
  const f = fixture();
  await login(f);
  let config = {},
    version = 1;
  const original = f.platform.enterprise;
  f.platform.enterprise = async () => ({
    ...(await original()),
    credentialVersion: version,
  });
  const host = new HostChannel(f.broker, undefined, {
    readConfig: () => config,
  });
  t.after(async () => {
    host.close();
    await f.broker.close();
  });
  const a = peer(host),
    b = peer(host),
    session = (await host.command(register(f.broker), a)).context;
  const request = {
    ...envelope(f.broker),
    executionContextId: session.executionContextId,
    purpose: 'raw',
  };
  let response = await host.credentials(request, a);
  assert.ok(response.credentials);
  version = 2;
  response = await host.credentials(request, a);
  assert.equal(response.credentials.credentialVersion, 2);
  assert.equal(
    (await host.credentials({ ...request, requestId: randomUUID() }, b)).error
      .reason,
    'context_revoked',
  );
  config = { allowRawAgentToken: false };
  assert.equal(
    (await host.credentials(request, a)).error.reason,
    'raw_token_disabled',
  );
  assert.ok(
    (
      await host.credentials(
        { ...request, requestId: randomUUID(), purpose: 'outbound' },
        a,
      )
    ).credentials,
  );
  config = {};
  let release, entered;
  const started = new Promise((r) => {
    entered = r;
  });
  f.platform.enterprise = () => {
    entered();
    return new Promise((r) => {
      release = r;
    });
  };
  const pending = host.credentials(request, a);
  await started;
  config.allowRawAgentToken = false;
  release({ ...(await original()), credentialVersion: 3 });
  assert.equal((await pending).error.reason, 'raw_token_disabled');
  f.platform.enterprise = original;
  config = {};
  const invalid = await original();
  invalid.binding.deviceId = randomUUID();
  invalid.credentialVersion = 3;
  f.platform.enterprise = async () => invalid;
  assert.equal(
    (await host.credentials(request, a)).error.reason,
    'stale_identity',
  );
  host.disconnect(a);
  assert.equal(host.contexts.size, 0);
  assert.equal(
    (await host.credentials(request, a)).error.reason,
    'peer_not_allowed',
  );
});

test('expired host request cannot extend its lease by replay after wall-clock rollback', async (t) => {
  const f = fixture();
  await login(f);
  let utc = Date.now(),
    mono = 0;
  const host = new HostChannel(f.broker, undefined, {
    now: () => utc,
    mono: () => mono,
  });
  t.after(async () => {
    host.close();
    await f.broker.close();
  });
  const a = peer(host),
    request = register(f.broker);
  assert.ok((await host.command(request, a)).context);
  mono = 30001;
  utc -= 1000;
  assert.equal((await host.command(request, a)).error.reason, 'expired');
});
