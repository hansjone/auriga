import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { BusinessProxy } from '../../../shell-auth/business.mjs';
import { fixture, login, OWNER } from '../../../shell-auth/test/fixtures.mjs';
import { catalogApi } from '../../src/resources/catalog-api.js';
import { createMemberApi } from '../../src/resources/member-api.js';
import { resourceRequest } from '../../src/resources/broker-transport.js';
import { createBroker } from '../../src/local/broker.js';
import { operations, validate } from '../broker-fixture.js';
import { resourceBroker } from './broker-fixture.js';

for (const managed of [false, true])
  test(`catalog list crosses real schema and BusinessProxy (${managed ? 'managed' : 'member'})`, async (t) => {
    const f = fixture();
    await login(f);
    const urls = [];
    const proxy = new BusinessProxy(f.broker, async (url) => {
      urls.push(new URL(url));
      assert.equal(
        urls.at(-1).searchParams.get('clientId'),
        f.identity.deviceId,
      );
      return Response.json({ items: [] });
    });
    let unsubscribe;
    const client = await createBroker({
      validate,
      ipc: {
        call: (message) =>
          message.call
            ? proxy.call(message, OWNER)
            : f.broker.command(message, OWNER),
        subscribe(command, callback) {
          unsubscribe = f.broker.subscribe(command, OWNER, callback);
        },
        close() {
          unsubscribe?.();
        },
      },
    });
    t.after(async () => {
      client.close();
      proxy.close();
      await f.broker.close();
    });
    const runtime = {
      config: f.platform.config,
      operations,
      identity: () => ({
        tenantId: f.identity.tenantId,
        ownerId: f.identity.memberId,
        clientId: f.identity.deviceId,
      }),
      ...(managed
        ? {
            resourceIdentity: {
              tenantId: f.identity.tenantId,
              ownerId: f.identity.memberId,
              instanceId: randomUUID(),
            },
          }
        : {}),
      broker: client,
    };
    assert.deepEqual(
      await catalogApi({ runtime, current: () => runtime }).list(),
      [],
    );
    assert.deepEqual(
      await createMemberApi({ runtime, current: () => runtime }).list(),
      [],
    );
    assert.equal(urls.length, 2);
    assert.equal(
      urls[0].pathname,
      '/api/resource/v1/catalog/member/installations',
    );
    assert.equal(urls[0].searchParams.get('kind'), null);
    assert.equal(urls[1].pathname, '/api/resource/v1/member/installations');
    for (const url of urls)
      assert.equal(url.searchParams.get('clientId'), f.identity.deviceId);
    for (const base of [
      '/api/resource/v1/catalog/member',
      '/api/resource/v1/member',
    ])
      for (const clientId of [f.identity.deviceId, randomUUID()])
        assert.equal(
          (
            await resourceRequest(
              runtime,
              base + '/installations?clientId=' + clientId,
            )
          ).status,
          400,
        );
    assert.equal(urls.length, 2);
  });

test('resource fixture rejects broker input identity overrides using generated schema', async () => {
  let sent = false;
  const runtime = resourceBroker(
    {
      config: { platformOrigin: 'https://example.com' },
      getAccess: async () => 'fixture-token',
    },
    async () => {
      sent = true;
      return Response.json({ items: [] });
    },
  );
  await assert.rejects(
    runtime.broker.call('UnifiedListInstallations', { clientId: randomUUID() }),
    { code: 'common.validation_failed' },
  );
  assert.equal(sent, false);
});
