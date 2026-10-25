import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createLocalLoginController } from '../src/login/local-controller.js';
const form = {
  type: 'form',
  formId: 'password',
  fields: [{ name: 'password', kind: 'password' }],
};
const state = (extra = {}) => ({
  available: true,
  loggedIn: false,
  connected: false,
  csrf: 'proof',
  ...extra,
});
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status });
function fixture(replies) {
  const requests = [];
  const controller = createLocalLoginController({
    fetch: async (url, options) => {
      requests.push({ url, ...options });
      const next = replies.shift();
      if (next instanceof Error) throw next;
      assert.ok(next, 'unexpected network call');
      return next;
    },
  });
  return { controller, requests };
}
test('begin keeps CSRF/cookies on local origin; complete requires ready status', async () => {
  const { controller, requests } = fixture([
    json(state()),
    json({ step: form }),
    json({
      step: { type: 'complete', redirectTo: 'https://evil.example.com' },
    }),
    json(state({ loggedIn: true, connected: false })),
  ]);
  assert.deepEqual(
    await controller.start({ signal: new AbortController().signal }),
    form,
  );
  assert.equal(requests[1].headers['X-Auriga-CSRF'], 'proof');
  assert.equal(requests[1].credentials, 'same-origin');
  assert.equal(requests[1].redirect, 'error');
  await assert.rejects(
    controller.submit(
      { type: 'form', formId: 'password', values: {} },
      { signal: new AbortController().signal },
    ),
    (error) => error.reason === 'network',
  );
});
test('resume expiry has a controlled recovery; explicit restart preserves device backing and never globally logs out', async () => {
  const { controller, requests } = fixture([
    json(state({ resumeAvailable: true })),
    json({ code: 'common.unauthenticated' }, 401),
    json(state()),
    json(state()),
    json(state({ loggedIn: false })),
    json(state()),
    json({ step: form }),
  ]);
  await assert.rejects(
    controller.start({ signal: new AbortController().signal }),
    (error) => error.reason === 'expired',
  );
  assert.deepEqual(
    await controller.restart({ signal: new AbortController().signal }),
    form,
  );
  const logout = requests.filter((r) => r.url.endsWith('/logout'));
  assert.equal(logout.length, 1);
  assert.deepEqual(JSON.parse(logout[0].body), {});
});
test('retry from authenticated disconnected status never posts logout', async () => {
  const { controller, requests } = fixture([
    json(state({ loggedIn: true, connected: false })),
    json(state({ loggedIn: true, connected: true })),
  ]);
  await assert.rejects(
    controller.restart({ signal: new AbortController().signal }),
    (error) => error.reason === 'network',
  );
  assert.deepEqual(
    await controller.restart({ signal: new AbortController().signal }),
    { type: 'complete', redirectTo: '/' },
  );
  assert.equal(
    requests.some((r) => r.url.endsWith('/logout')),
    false,
  );
});
test('network failure, absent configuration, and expired credentials remain distinct', async () => {
  for (const [replies, reason] of [
    [[new TypeError('SECRET transport')], 'network'],
    [[json({}, 404), json({ status: 'unconfigured' })], 'unconfigured'],
    [[json({ code: 'iam.login_tx_expired' }, 400)], 'expired'],
  ]) {
    const { controller } = fixture(replies);
    await assert.rejects(
      controller.start({ signal: new AbortController().signal }),
      (error) => error.reason === reason && !error.message.includes('SECRET'),
    );
  }
});
test('restart refuses to cancel an identity that appeared between status and anonymous cancellation', async () => {
  const { controller, requests } = fixture([
    json(state()),
    json(state({ loggedIn: true })),
  ]);
  await assert.rejects(
    controller.restart({ signal: new AbortController().signal }),
    (error) => error.reason === 'expired',
  );
  assert.equal(
    requests.some((r) => r.url.endsWith('/logout')),
    false,
  );
});
test('non-form inputs cannot bypass the local FormInput contract', async () => {
  const { controller, requests } = fixture([]);
  for (const input of [
    { type: 'poll' },
    { type: 'webauthn', credential: {} },
    { type: 'event', name: 'qr_confirmed' },
    { type: 'callback', params: {} },
  ]) {
    await assert.rejects(
      controller.submit(input, { signal: new AbortController().signal }),
      (error) => error.reason === 'unavailable',
    );
  }
  assert.equal(requests.length, 0);
});
test('server unsupported step errors become unavailable rather than generic network errors', async () => {
  const { controller } = fixture([
    json(state()),
    json({ code: 'common.unavailable' }, 501),
  ]);
  await assert.rejects(
    controller.start({ signal: new AbortController().signal }),
    (error) => error.reason === 'unavailable',
  );
});
