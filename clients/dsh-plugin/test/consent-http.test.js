import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { Buffer } from 'node:buffer';
import { createConnectRoutes } from '../src/local/routes.js';
import { openConsent } from '../src/network/consent/index.js';
import { createMemoryLedgerStore } from '../src/network/host/store.js';
import { createConsentUIAdapter } from '../src/consent-ui/adapter.js';

test('consent HTTP bridge binds native session, origin and CSRF, returning only public projections', async (t) => {
  const identity = {
    tenant: 'tenant-test',
    realm: '2',
    node: '64',
    nodeIncarnation: 'inc-1',
  };
  const resolve = (req) =>
    req?.headers?.cookie === 'authenticated' ? { subject: 'alice' } : null;
  const policy = {
    available: true,
    revision: '1',
    sameOrganization: true,
    conversation: 'allow',
    execution: 'allow',
    privateField: 'do-not-expose',
  };
  const consent = await openConsent({
    store: createMemoryLedgerStore(),
    identity,
    resolveSource: async (source) => ({
      ...source,
      userId: 'alice',
      ownershipRevision: 'private-proof',
    }),
    organization: async () => policy,
    authorizeLocal: async (context) => !!resolve(context),
  });
  t.after(() => consent.close());
  const runtime = {
    resolve,
    checkCsrf: (req) => req.headers['x-auriga-csrf'] === 'csrf-proof',
    consentUI: (req) =>
      createConsentUIAdapter({
        consent,
        context: req,
        organizationPolicy: async () => policy,
        preview: async () => '',
      }),
  };
  const route = createConnectRoutes({
    runtime,
    admission: {
      nativeAuth: { requestRejection: () => undefined },
      sameOrigin: (req) => req.headers.origin === 'https://example.com',
    },
  });
  async function request(
    action,
    {
      method = 'GET',
      body,
      cookie = 'authenticated',
      csrf = 'csrf-proof',
      origin = 'https://example.com',
    } = {},
  ) {
    const req = Readable.from(
      body === undefined ? [] : [Buffer.from(JSON.stringify(body))],
    );
    Object.assign(req, {
      method,
      url: '/auriga/local-login/consent/' + action,
      headers: {
        cookie,
        origin,
        'content-type': 'application/json',
        'x-auriga-csrf': csrf,
      },
    });
    let code, output, headers;
    await route(req, {
      writeHead(status, h) {
        code = status;
        headers = h;
      },
      end(value) {
        output = value;
      },
    });
    return { code, output, headers };
  }
  assert.equal((await request('snapshot', { cookie: 'forged' })).code, 401);
  assert.equal(
    (
      await request('default', {
        method: 'POST',
        body: { layer: 'conversation', mode: 'deny_all' },
        csrf: 'forged',
      })
    ).code,
    403,
  );
  assert.equal(
    (await request('snapshot', { origin: 'https://evil.example.com' })).code,
    403,
  );
  assert.equal(
    (
      await request('default', {
        method: 'POST',
        body: {
          layer: 'conversation',
          mode: 'deny_all',
          subject: 'administrator',
        },
      })
    ).code,
    400,
  );
  assert.equal(
    (
      await request('default', {
        method: 'POST',
        body: { layer: 'conversation', mode: 'deny_all' },
      })
    ).code,
    200,
  );
  const snapshot = await request('snapshot');
  assert.equal(snapshot.code, 200);
  assert.equal(snapshot.headers['cache-control'], 'no-store');
  assert.equal(JSON.parse(snapshot.output).defaults.conversation, 'deny_all');
  assert.equal(snapshot.output.includes('private'), false);
  assert.equal(snapshot.output.includes('do-not-expose'), false);
});
