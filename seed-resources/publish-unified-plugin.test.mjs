import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { manifestFor, publishPlugin } from './publish-unified-plugin.mjs';

test('seed uses unified multipart, distinct review roles, exact digest and revision', async () => {
  const payload = Buffer.from('test payload');
  const digest = (bytes) =>
    'sha256:' + createHash('sha256').update(bytes).digest('hex');
  const calls = [];
  let manifestDigest;
  const send = async (url, request) => {
    assert.equal(url.origin, 'http://192.0.2.10:8080');
    assert.equal(request.redirect, 'error');
    calls.push({ path: url.pathname, ...request });
    let result;
    switch (calls.length) {
      case 1:
        assert.deepEqual(JSON.parse(request.body), {
          name: 'dsh-memwiki',
          kind: 'plugin',
          visibility: 'tenant',
        });
        result = { id: 'package-id', revision: 1 };
        break;
      case 2: {
        assert.equal(request.headers['If-Match'], '"1"');
        assert(request.body instanceof FormData);
        const raw = Buffer.from(
          await request.body.get('manifest').arrayBuffer(),
        );
        const manifest = JSON.parse(raw);
        manifestDigest = digest(raw);
        assert.equal(
          JSON.parse(await request.body.get('metadata').text()).expectedDigest,
          manifestDigest,
        );
        assert.equal(manifest.artifacts[0].digest, digest(payload));
        assert.deepEqual(
          Buffer.from(await request.body.get('artifacts').arrayBuffer()),
          payload,
        );
        result = { id: 'version-id', revision: 1 };
        break;
      }
      case 3:
        result = {
          reviewId: 'review-id',
          revision: 1,
          version: { revision: 2, digest: manifestDigest },
        };
        break;
      case 4:
      case 5: {
        const body = JSON.parse(request.body);
        assert.equal(body.role, calls.length === 4 ? 'general' : 'security');
        assert.equal(body.digest, manifestDigest);
        assert.equal(body.versionRevision, 2);
        assert.equal(
          request.headers['If-Match'],
          calls.length === 4 ? '"1"' : '"2"',
        );
        result = {
          reviewId: 'review-id',
          revision: calls.length - 2,
          version: { revision: 2, digest: manifestDigest },
        };
        break;
      }
      case 6:
        assert.equal(request.headers['If-Match'], '"2"');
        result = { state: 'published' };
        break;
      default:
        assert.fail('unexpected request');
    }
    return { ok: true, json: async () => result };
  };
  const receipt = await publishPlugin(
    new URL('http://192.0.2.10:8080'),
    payload,
    { version: '1.0.0' },
    { author: 'a', general: 'g', security: 's', publisher: 'p' },
    send,
  );
  assert.equal(receipt.state, 'published');
  assert.deepEqual(
    calls.map((call) => call.headers.Authorization),
    ['Bearer a', 'Bearer a', 'Bearer a', 'Bearer g', 'Bearer s', 'Bearer p'],
  );
  assert(calls.every((call) => call.path.includes('/catalog/')));
  assert.equal(
    new Set(calls.map((call) => call.headers['Idempotency-Key'])).size,
    6,
  );
  assert.equal(
    manifestFor(payload, { version: '1.0.0' }).plugin.packageName,
    'dsh-memwiki',
  );
});

test('seed stops on rejected upload without leaking response or token', async () => {
  let count = 0;
  await assert.rejects(
    publishPlugin(
      'https://example.com',
      Buffer.from('payload'),
      { version: '1.0.0' },
      { author: 'secret-value' },
      async () => {
        count++;
        return count === 1
          ? { ok: true, json: async () => ({ id: 'p', revision: 1 }) }
          : { ok: false, status: 422, text: async () => 'secret-value' };
      },
    ),
    (error) =>
      !error.message.includes('secret-value') && error.message.includes('422'),
  );
  assert.equal(count, 2);
});
