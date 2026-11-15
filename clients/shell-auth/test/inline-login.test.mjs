import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture, command, OWNER, OTHER } from './fixtures.mjs';
import { createPlatform, trustedConfig } from '../platform.mjs';

function setup() {
  const providerId = randomUUID(),
    transactionId = randomUUID(),
    challengeId = randomUUID();
  const f = fixture(),
    calls = [];
  let state = 'waiting',
    mode = 'uac';
  const form = {
    type: 'form',
    formId: 'password',
    fields: [{ name: 'password', kind: 'password' }],
  };
  const nativeDevice = {
    deviceCode: randomUUID(),
    expiresAt: new Date(Date.now() + 120000).toISOString(),
    interval: 5,
  };
  const platform = createPlatform(
    trustedConfig({ platformOrigin: 'https://example.com' }),
    async (url, options) => {
      const path = new URL(url).pathname;
      calls.push({ path, ...options });
      if (path.endsWith('/providers'))
        return Response.json({
          items: [
            {
              id: providerId,
              connectorType: mode,
              displayName: { en: 'Team' },
            },
          ],
        });
      if (path.endsWith('/challenges') || path.endsWith('/transactions')) {
        const body = JSON.parse(options.body);
        assert.equal(body.nativeConnect.codeChallengeMethod, 'S256');
        assert.match(body.nativeConnect.codeChallenge, /^[\w-]{43}$/);
        return Response.json(
          mode === 'uac'
            ? {
                id: challengeId,
                transactionId,
                qrContent: 'https://example.com/qr',
                expiresAt: nativeDevice.expiresAt,
                nativeDevice,
              }
            : {
                id: transactionId,
                status: 'pending',
                step: form,
                nativeDevice,
              },
          {
            headers: {
              'set-cookie':
                '__Host-auriga_portal_login_tx=bound; Secure; HttpOnly; Path=/',
            },
          },
        );
      }
      if (path.endsWith('/token')) {
        const body = new URLSearchParams(options.body);
        assert.equal(body.get('device_code'), nativeDevice.deviceCode);
        assert.match(body.get('code_verifier'), /^[\w-]{43}$/);
        assert.equal(options.headers.Cookie, undefined);
        return Response.json(f.tokens);
      }
      assert.equal(
        options.headers.Cookie,
        '__Host-auriga_portal_login_tx=bound',
      );
      assert.equal(options.headers.Origin, 'https://example.com');
      if (options.method === 'DELETE')
        return new Response(null, { status: 204 });
      if (path.endsWith('/' + challengeId))
        return Response.json({ id: challengeId, state });
      if (path.endsWith('/complete'))
        return Response.json({ transactionId, state: 'authenticated' });
      return Response.json({
        id: transactionId,
        status: 'completed',
        step: { type: 'complete', redirectTo: '/' },
      });
    },
  );
  f.platform.loginSession = platform.loginSession;
  f.platform.nativePoll = platform.nativePoll;
  f.platform.config = platform.config;
  f.broker.openBrowser = () => {
    throw new Error('must not open browser');
  };
  const run = async (action, input, owner = OWNER) =>
    f.broker.command(
      { ...command(f.broker, action), ...(input ? { input } : {}) },
      owner,
    );
  return {
    f,
    calls,
    run,
    state: (value) => (state = value),
    mode: (value) => (mode = value),
  };
}

test('inline UAC waiting to scanned to success uses PKCE and isolated cookies without browser or user code', async () => {
  const { f, calls, run, state } = setup();
  await run('inline_login');
  assert.equal(f.broker.snapshot(OWNER).inlineLogin.step.status, 'waiting');
  assert.equal(f.broker.snapshot(OTHER).inlineLogin, undefined);
  await assert.rejects(run('inline_advance', { operation: 'poll' }, OTHER), {
    code: 'common.conflict',
  });
  state('scanned');
  await run('inline_advance', { operation: 'poll' });
  assert.equal(f.broker.snapshot(OWNER).inlineLogin.step.status, 'scanned');
  state('verified');
  await run('inline_advance', { operation: 'poll' });
  assert.equal(f.broker.state, 'authenticated');
  assert.equal(f.broker.snapshot(OWNER).inlineLogin, undefined);
  assert.ok(!JSON.stringify(f.logs).includes('code_verifier'));
  assert.ok(calls.every((c) => !c.path.includes('device_authorization')));
  await f.broker.close();
});
test('inline team and emergency submit their transaction form without returning credentials', async () => {
  for (const type of ['team-account', 'emergency']) {
    const { f, run, mode } = setup();
    mode(type);
    await run('inline_login');
    assert.equal(f.broker.snapshot(OWNER).inlineLogin.step.type, 'form');
    await run('inline_advance', {
      operation: 'submit',
      formId: 'password',
      values: { password: randomUUID() },
    });
    assert.equal(f.broker.state, 'authenticated');
    await f.broker.close();
  }
});
test('inline QR expiry and denial close the attempt, and cancel removes projections', async () => {
  for (const terminal of ['expired', 'denied']) {
    const { f, run, state } = setup();
    await run('inline_login');
    state(terminal);
    await run('inline_advance', { operation: 'poll' });
    assert.equal(
      f.broker.state,
      terminal === 'expired' ? 'reauth_required' : 'signed_out',
    );
    assert.equal(f.broker.snapshot(OWNER).inlineLogin, undefined);
    await f.broker.close();
  }
  const { f, run } = setup();
  await run('inline_login');
  await run('cancel_login');
  assert.equal(f.broker.state, 'signed_out');
  assert.equal(f.broker.snapshot(OWNER).inlineLogin, undefined);
  await f.broker.close();
});

test('cancel during native token exchange revokes late credentials instead of restoring login', async () => {
  const { f, run, mode } = setup();
  mode('team-account');
  let release, entered;
  const waiting = new Promise((resolve) => {
    entered = resolve;
  });
  f.platform.nativePoll = () => {
    entered();
    return new Promise((resolve) => {
      release = resolve;
    });
  };
  await run('inline_login');
  const pending = run('inline_advance', {
    operation: 'submit',
    formId: 'password',
    values: { password: 'test' },
  });
  await waiting;
  await run('cancel_login');
  release(f.tokens);
  await pending;
  assert.equal(f.broker.state, 'signed_out');
  assert.equal(f.broker.record.tokens, undefined);
  assert.deepEqual(
    new Set(f.revoked),
    new Set([f.tokens.access_token, f.tokens.refresh_token]),
  );
  await f.broker.close();
});

test('inline invalid_client preserves platform reason and correlation without opening browser', async () => {
  const { f, run } = setup();
  const correlationId = randomUUID();
  f.platform.loginSession = () => async () => {
    throw Object.assign(new Error('rejected'), {
      status: 401,
      code: 'invalid_client',
      correlationId,
    });
  };
  await run('inline_login');
  assert.equal(f.broker.state, 'signed_out');
  assert.equal(f.broker.snapshot().reason, 'platform_rejected');
  assert.deepEqual(f.broker.snapshot().loginError, {
    code: 'invalid_client',
    correlationId,
  });
  assert.ok(
    f.logs.some(
      (e) => e.code === 'invalid_client' && e.correlationId === correlationId,
    ),
  );
  await f.broker.close();
});

test('an invalid password remains in the same native form and can be retried', async () => {
  const { f, run, mode } = setup();
  mode('team-account');
  const session = f.platform.loginSession;
  let rejected = false;
  f.platform.loginSession = (signal) => {
    const request = session(signal);
    return (path, options) => {
      if (path.endsWith('/advance') && !rejected) {
        rejected = true;
        throw Object.assign(new Error('invalid credentials'), {
          status: 401,
          code: 'iam.invalid_credentials',
        });
      }
      return request(path, options);
    };
  };
  await run('inline_login');
  const input = {
    operation: 'submit',
    formId: 'password',
    values: { password: 'test' },
  };
  await run('inline_advance', input);
  assert.equal(
    f.broker.snapshot(OWNER).inlineLogin.step.error.code,
    'iam.invalid_credentials',
  );
  await run('inline_advance', input);
  assert.equal(f.broker.state, 'authenticated');
  await f.broker.close();
});
