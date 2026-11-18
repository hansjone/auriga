import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { BusinessProxy } from '../../../shell-auth/business.mjs';
import { fixture, login, OWNER } from '../../../shell-auth/test/fixtures.mjs';
import { catalogApi } from '../../src/resources/catalog-api.js';
import { createMemberApi } from '../../src/resources/member-api.js';
import { resourceRequest } from '../../src/resources/broker-transport.js';
import { createBroker } from '../../src/local/broker.js';
import { createDoorLock } from '../../src/managed/door.js';
import { installHostAuthentication } from '../../src/managed/host-auth.js';
import { createResourceHandler } from '../../src/resources/channel.js';
import { signIdentity } from '../managed-helpers.js';
import { operations, validate } from '../broker-fixture.js';
import { resourceBroker } from './broker-fixture.js';

function installation(tenantId, deviceId, instance) {
  const packageId = randomUUID(),
    versionId = randomUUID(),
    manifestDigest = 'sha256:' + '1'.repeat(64),
    issuedAt = new Date().toISOString(),
    expiresAt = new Date(Date.now() + 3600000).toISOString();
  const identity = {
    tenantId,
    kind: 'skill',
    packageId,
    versionId,
    version: '1.0.0',
    manifestDigest,
  };
  const row = {
    clientId: deviceId,
    resource: {
      identity,
      envelope: {
        schemaVersion: 2,
        tenantId,
        packageId,
        versionId,
        releaseSequence: 1,
        notAfter: expiresAt,
        statement: {
          purpose: 'resource',
          kind: 'skill',
          name: 'market-example',
          version: '1.0.0',
          manifestDigest,
          artifactDigests: [manifestDigest],
          issuedAt,
        },
        signature: {
          alg: 'Ed25519',
          kid: 'fixture',
          value: Buffer.alloc(64).toString('base64url'),
        },
      },
      manifestDownloadPath:
        '/api/resource/v1/catalog/packages/' +
        packageId +
        '/versions/' +
        versionId +
        '/manifest',
      artifacts: [
        {
          artifactId: 'payload',
          digest: manifestDigest,
          sizeBytes: 42,
          downloadPath:
            '/api/resource/v1/catalog/packages/' +
            packageId +
            '/versions/' +
            versionId +
            '/artifacts/payload',
        },
      ],
      lease: {
        envelopeNotAfter: expiresAt,
        revocationsExpiresAt: expiresAt,
        expiresAt,
        revocationRevision: 1,
      },
      enabled: true,
      desiredState: 'ENABLED',
      releaseSequence: 1,
      installationRevision: 4,
      installationMode: 'optional',
    },
    installationRevision: 4,
    selectionRevision: 9,
    generation: 38,
    state: 'applied',
    reportSource: instance ? 'node' : 'member',
  };
  if (instance)
    row.report = {
      clientId: instance,
      identity,
      installationRevision: 4,
      selectionRevision: 9,
      generation: 38,
      observedGeneration: 38,
      state: 'applied',
      reportSource: 'node',
      observedAt: issuedAt,
    };
  return row;
}

for (const managed of [false, true])
  test(`catalog list crosses real schema and BusinessProxy (${managed ? 'managed' : 'member'})`, async (t) => {
    const f = fixture();
    await login(f);
    const instance = managed ? randomUUID() : undefined;
    const urls = [];
    let row;
    let writes = 0;
    const proxy = new BusinessProxy(f.broker, async (url, init) => {
      urls.push(new URL(url));
      if (init.method === 'POST') {
        writes++;
        const headers = new globalThis.Headers(init.headers);
        assert.equal(
          headers.get('authorization'),
          'Bearer ' + f.tokens.access_token,
        );
        assert.equal(headers.get('cookie'), null);
        assert.equal(headers.get('if-match'), '"4"');
        const { clientId, selection } = JSON.parse(init.body);
        assert.equal(clientId, f.identity.deviceId);
        assert.deepEqual(selection, {
          identity: row.resource.identity,
          action: 'disable',
        });
        return Response.json(
          {
            ...row,
            resource: {
              ...row.resource,
              desiredState: 'DISABLED',
              enabled: false,
              installationRevision: 5,
            },
            state: 'applying',
            installationRevision: 5,
            report: undefined,
          },
          { status: 202, headers: { etag: '"5"' } },
        );
      }
      assert.equal(
        urls.at(-1).searchParams.get('clientId'),
        f.identity.deviceId,
      );
      return Response.json({ items: row ? [row] : [] });
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
              instanceId: instance,
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
    row = installation(f.identity.tenantId, f.identity.deviceId, instance);
    const api = catalogApi({ runtime, current: () => runtime });
    assert.equal((await api.list())[0].state, 'applied');
    if (!managed) {
      assert.equal(
        (
          await api.select(
            { identity: row.resource.identity, action: 'disable' },
            4,
            'desktop',
          )
        ).reportSource,
        'member',
      );
      assert.equal(writes, 1);
      return;
    }

    assert.notEqual(instance, f.identity.deviceId);
    const keys = generateKeyPairSync('ed25519');
    const browserOrigin = {
      scheme: 'https',
      host: 'instance.example.com',
      port: 443,
    };
    const door = createDoorLock({
      instanceId: instance,
      ownerId: f.identity.memberId,
      getPublicKey: () => keys.publicKey,
    });
    const connection = {
      browserPeer: {
        principal: { kind: 'owner' },
        signal: new globalThis.AbortController().signal,
      },
      admit: () => ({ rejection: 401 }),
      authorizeIndex: () => false,
      requestRejection(req) {
        return this.admit(req).rejection;
      },
    };
    runtime.resolve = (req) =>
      door.isAuthenticated(req) ? runtime.identity() : undefined;
    const restore = installHostAuthentication(connection, door, {
      browserOrigin,
    });
    const handler = createResourceHandler({
      auth: connection,
      current: () => runtime,
    });
    const server = createServer((req, res) => void handler(req, res));
    const uninstall = door.install(server);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    t.after(async () => {
      uninstall();
      restore();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    const request = (suffix, body, aud = instance) => {
      const path = '/auriga/resources/catalog/' + suffix;
      const method = body ? 'POST' : 'GET';
      const { token } = signIdentity(keys.privateKey, 'test', {
        htm: method,
        htu: path,
        aud,
        sub: f.identity.memberId,
        tid: f.identity.tenantId,
      });
      return globalThis.fetch(
        'http://127.0.0.1:' + server.address().port + path,
        {
          method,
          headers: {
            Origin: 'https://' + browserOrigin.host,
            'Auriga-Identity': token,
            'X-Auriga-Catalog': '1',
            'Content-Type': 'application/json',
            Cookie: 'browser=fixture',
          },
          body: body && JSON.stringify(body),
        },
      );
    };
    const list = await request('installations');
    assert.equal(list.status, 200);
    const listed = await list.json();
    assert.equal(listed.cloud, true);
    assert.equal(listed.items[0].report.clientId, instance);
    assert.equal(listed.items[0].state, 'applied');
    const { packageId, versionId } = row.resource.identity;
    const input = {
      packageId,
      versionId,
      action: 'disable',
      revision: 4,
      key: 'managed',
    };
    for (const [body, audience, status] of [
      [{ ...input, instanceId: randomUUID() }, instance, 400],
      [input, randomUUID(), 401],
    ]) {
      const denied = await request('actions', body, audience);
      assert.equal(denied.status, status);
      await denied.body.cancel();
    }
    assert.equal(writes, 0);
    const action = await request('actions', input);
    assert.equal(action.status, 202);
    const accepted = await action.json();
    assert.equal(accepted.installation.reportSource, 'node');
    assert.equal(accepted.installation.state, 'applying');
    assert.equal(writes, 1);
    const verified = row.report;
    delete row.report;
    await assert.rejects(api.list(), { code: 'common.validation_failed' });
    row.report = verified;
    row.report.clientId = randomUUID();
    await assert.rejects(api.list(), { code: 'common.validation_failed' });
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
