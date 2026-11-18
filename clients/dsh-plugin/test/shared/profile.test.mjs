import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { SHARED_PROFILE } from '../../src/shared/host.js';
import { createSharedProfileHandler } from '../../src/shared/profile.js';

function response() {
  return {
    status: 0,
    headers: {},
    body: '',
    writeHead(status, headers) {
      this.status = status;
      this.headers = headers;
    },
    end(value = '') {
      this.body = value;
    },
  };
}

test('runtime profile probes actual host configuration again on every read', () => {
  let active = true;
  const physicalInstanceId = randomUUID();
  const handler = createSharedProfileHandler({
    options: { physicalInstanceId },
    assertProfile(expected) {
      assert.equal(expected, SHARED_PROFILE);
      return active;
    },
  });
  const first = response();
  handler({ method: 'GET', url: '/_auriga/shared-profile' }, first);
  assert.equal(first.status, 200);
  assert.equal(first.headers['cache-control'], 'no-store');
  assert.deepEqual(JSON.parse(first.body), {
    schemaVersion: 1,
    physicalInstanceId,
    profile: 'auriga.shared.v1',
    capabilities: SHARED_PROFILE,
  });
  active = false;
  const next = response();
  handler({ method: 'GET', url: '/_auriga/shared-profile' }, next);
  assert.equal(next.status, 503);
  assert.deepEqual(JSON.parse(next.body), { error: 'common.unavailable' });
});

test('profile probe accepts only the canonical read path and synchronous proof', () => {
  const handler = createSharedProfileHandler({
    options: { physicalInstanceId: randomUUID() },
    assertProfile: () => Promise.resolve(true),
  });
  for (const request of [
    { method: 'POST', url: '/_auriga/shared-profile' },
    { method: 'GET', url: '/_auriga/shared-profile?ready=true' },
    { method: 'GET', url: '/_auriga/shared-profile/other' },
  ]) {
    const denied = response();
    handler(request, denied);
    assert.equal(denied.status, 404);
  }
  const denied = response();
  handler({ method: 'GET', url: '/_auriga/shared-profile' }, denied);
  assert.equal(denied.status, 503);
});
