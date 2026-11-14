import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { createMemberRoutes } from '../../src/resources/member-routes.js';
import { createMemberApi, installation } from './broker-fixture.js';
import { id } from './fixtures.js';

const identity = {};
const input = {
  packageId: id(4),
  versionId: id(5),
  action: 'install',
  key: 'intent',
};
function response() {
  return {
    status: 0,
    body: '',
    writeHead(status) {
      this.status = status;
    },
    end(body) {
      this.body = body;
    },
  };
}
test('local member writes require origin, CSRF and the same owner after reading the body; injected identity is refused', async () => {
  let origin = true,
    csrf = true,
    current,
    owner = identity;
  const runtime = { resolve: () => owner, checkCsrf: () => csrf };
  current = runtime;
  const routes = createMemberRoutes({
    directory: 'unused',
    ctx: { get: () => assert.fail('must not open host') },
    sameOrigin: () => origin,
    current: () => current,
  });
  try {
    for (const scenario of [
      'origin',
      'csrf',
      'identity',
      'extra',
      'logout-body',
    ]) {
      origin = scenario !== 'origin';
      csrf = scenario !== 'csrf';
      owner = scenario === 'identity' ? null : identity;
      current = runtime;
      const value =
        scenario === 'extra' ? { ...input, clientId: id(9) } : input;
      const req = Readable.from(
        (async function* () {
          yield Buffer.from(JSON.stringify(value));
          if (scenario === 'logout-body') current = undefined;
        })(),
      );
      Object.assign(req, {
        method: 'POST',
        url: '/auriga/resources/local/actions',
        headers: { 'content-type': 'application/json' },
      });
      const res = response();
      await routes.handle(runtime, req, res);
      assert.equal(
        res.status,
        scenario === 'extra'
          ? 400
          : scenario === 'identity' || scenario === 'logout-body'
            ? 401
            : 403,
      );
      assert(!res.body.includes(id(9)));
    }
  } finally {
    await routes.close();
  }
});
test('member API discards responses when verified DEVICE identity changes during a request', async () => {
  let did = id(3);
  const runtime = {
    config: { platformOrigin: 'https://example.com' },
    vault: {
      snapshot: () => ({
        credential: { claims: { tid: id(1), sub: id(2), did } },
      }),
    },
    getAccess: async () => 'private-token',
  };
  const api = createMemberApi({
    runtime,
    fetchImpl: async (url, init) => {
      assert.equal(url.origin, 'https://example.com');
      assert.equal(init.headers.authorization, 'Bearer private-token');
      assert.equal(init.redirect, 'error');
      assert.equal(init.credentials, 'omit');
      did = id(9);
      return Response.json({ items: [] });
    },
  });
  await assert.rejects(api.list(), { code: 'common.unauthenticated' });
});
test('member installation DTO rejects upstream extra secrets before local projection', () => {
  assert.throws(
    () =>
      installation(
        {
          clientId: id(3),
          packageId: id(4),
          versionId: id(5),
          digest: 'sha256:' + 'a'.repeat(64),
          releaseSequence: 1,
          revision: 1,
          level: 'l3',
          desired: 'enabled',
          state: 'applied',
          reportSource: 'member',
          ticket: 'secret',
        },
        id(3),
      ),
    { code: 'common.validation_failed' },
  );
});
