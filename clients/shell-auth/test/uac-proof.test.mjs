import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { fixture, command, OWNER, OTHER } from './fixtures.mjs';
import { redact } from '../diagnostics.mjs';

function setup() {
  const f = fixture();
  const requests = [];
  let fail,
    mfa = false,
    exchange = 0;
  const transactionId = randomUUID(),
    id = randomUUID();
  f.platform.config.nativeClientId = 'auriga-dsh-native';
  f.platform.loginSession =
    () =>
    async (path, options = {}) => {
      requests.push({ path, ...options });
      if (fail) throw fail;
      if (options.method === 'DELETE') return {};
      if (path.endsWith('/providers'))
        return {
          items: [
            {
              id: f.identity.providerId,
              connectorType: 'uac',
              displayName: { en: 'UAC' },
            },
          ],
        };
      if (path.endsWith('/challenges'))
        return {
          id,
          transactionId,
          state: 'verified',
          expiresAt: new Date(Date.now() + 120000).toISOString(),
          nativeDevice: {
            deviceCode: 'synthetic-code',
            expiresAt: new Date(Date.now() + 300000).toISOString(),
          },
        };
      if (path.endsWith('/complete'))
        return {
          transactionId,
          state: mfa ? 'additional_step_required' : 'authenticated',
        };
      if (path.endsWith('/advance')) mfa = false;
      return {
        id: transactionId,
        status: mfa ? 'pending' : 'completed',
        step: mfa
          ? {
              type: 'form',
              formId: 'platform_totp',
              fields: [{ name: 'otp', kind: 'otp', required: true }],
            }
          : { type: 'complete', redirectTo: '/' },
      };
    };
  f.platform.nativePoll = async () => {
    exchange++;
    return { ...f.tokens, client_id: 'auriga-dsh-native' };
  };
  const credential = {
    generation: randomUUID(),
    empNo: '001Ab',
    token: 'synthetic-uac-secret',
  };
  const publish = (value = credential) => f.broker.localUac(value, OWNER);
  return {
    ...f,
    requests,
    credential,
    publish,
    fail(value) {
      fail = value;
    },
    mfa() {
      mfa = true;
    },
    exchanges: () => exchange,
  };
}

test('local proof silently establishes one PKCE device and repeated checks reuse refresh', async () => {
  const f = setup();
  await f.publish();
  await f.broker.check();
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.equal(f.exchanges(), 1);
  const creation = f.requests.find((r) => r.path.endsWith('/challenges'));
  assert.equal(creation.body.uacProof.empNo, '001Ab');
  assert.equal(creation.body.uacProof.token, f.credential.token);
  assert.match(creation.body.uacProof.nonce, /^[\w-]{43}$/);
  assert.match(creation.body.nativeConnect.codeChallenge, /^[\w-]{43}$/);
  for (let i = 0; i < 5; i++) {
    await f.publish();
    await f.broker.check();
  }
  assert.equal(f.exchanges(), 1);
  for (const value of [f.saved, f.logs, f.broker.snapshot(), f.opened])
    assert.ok(!JSON.stringify(value).includes(f.credential.token));
  await f.broker.close();
});

test('MFA remains an explicit shared inline step and background checks cannot bypass it', async () => {
  const f = setup();
  f.mfa();
  await f.publish();
  await f.broker.check();
  assert.equal(
    f.broker.snapshot(OWNER).inlineLogin.step.formId,
    'platform_totp',
  );
  assert.equal(f.broker.snapshot(OWNER).localUacProof, true);
  for (let i = 0; i < 4; i++) {
    await f.publish();
    await f.broker.check();
  }
  assert.equal(f.exchanges(), 0);
  assert.equal(
    f.requests.filter((r) => r.path.endsWith('/challenges')).length,
    1,
  );
  await f.broker.command(
    {
      ...command(f.broker, 'inline_advance'),
      input: {
        operation: 'submit',
        formId: 'platform_totp',
        values: { otp: '123456' },
      },
    },
    OWNER,
  );
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.equal(f.exchanges(), 1);
  await f.broker.close();
});

test('offline and rate-limited attempts back off, single-flight and recover without losing local identity', async () => {
  for (const status of [undefined, 429]) {
    const f = setup();
    let elapsed = 0;
    f.broker.mono = () => elapsed;
    f.fail(
      Object.assign(new Error('synthetic failure'), {
        status,
        code: status ? 'common.rate_limited' : 'common.unavailable',
      }),
    );
    await f.publish();
    await f.broker.check();
    assert.equal(
      f.broker.snapshot().reason,
      status ? 'rate_limited' : 'offline',
    );
    const before = f.requests.length;
    await Promise.all(Array.from({ length: 8 }, () => f.broker.check()));
    assert.equal(f.requests.length, before);
    assert.equal(f.broker.localSource.credential.empNo, '001Ab');
    f.fail(undefined);
    elapsed = 10000;
    await f.publish();
    await f.broker.check();
    assert.equal(f.broker.snapshot().state, 'authenticated');
    assert.equal(f.exchanges(), 1);
    await f.broker.close();
  }
});

test('lost create response reuses nonce, PKCE verifier and the exact credential proof', async () => {
  const f = setup();
  let elapsed = 0,
    lost = true;
  f.broker.mono = () => elapsed;
  const loginSession = f.platform.loginSession;
  f.platform.loginSession = (...args) => {
    const request = loginSession(...args);
    return async (path, options) => {
      const result = await request(path, options);
      if (path.endsWith('/challenges') && lost) {
        lost = false;
        throw new Error('lost synthetic response');
      }
      return result;
    };
  };
  await f.publish();
  await f.broker.check();
  assert.equal(f.exchanges(), 0);
  elapsed = 10000;
  await f.publish();
  await f.broker.check();
  const creations = f.requests.filter((r) => r.path.endsWith('/challenges'));
  assert.equal(creations.length, 2);
  assert.deepEqual(creations[0].body, creations[1].body);
  assert.equal(f.exchanges(), 1);
  await f.broker.close();
});

test('permanent proof rejection and disabled provider require user action without retry storms', async () => {
  for (const code of [
    'iam.uac_proof_rejected',
    'iam.uac_proof_subject_required',
    'iam.provider_disabled',
  ]) {
    const f = setup();
    f.fail(Object.assign(new Error('denied'), { status: 403, code }));
    await f.publish();
    await f.broker.check();
    assert.equal(f.broker.snapshot().state, 'reauth_required');
    assert.equal(f.broker.snapshot().loginError.code, code);
    assert.equal(f.broker.record.manualLoginRequired, true);
    const before = f.requests.length;
    f.fail(undefined);
    for (let i = 0; i < 3; i++) {
      await f.publish();
      await f.broker.check();
    }
    assert.equal(f.requests.length, before);
    assert.equal(f.exchanges(), 0);
    await f.broker.close();
  }
});

test('a newly authenticated local generation recovers from an earlier proof rejection', async () => {
  const f = setup();
  const correlationId = randomUUID();
  const code = 'iam.uac_proof_subject_required';
  f.fail(
    Object.assign(new Error('synthetic private detail'), {
      status: 403,
      code,
      correlationId,
    }),
  );
  await f.publish();
  await f.broker.check();
  assert.equal(f.saved.at(-1).proofFailureGeneration, f.credential.generation);
  assert.deepEqual(f.broker.snapshot().loginError, { code, correlationId });
  const entry = f.logs.map(redact).find((event) => event.code === code);
  assert.equal(entry.httpStatus, 403);
  assert.equal(entry.correlationId, correlationId);
  assert.doesNotMatch(JSON.stringify(f.logs), /synthetic private detail/);
  f.fail(undefined);
  await f.publish({ ...f.credential, generation: randomUUID() });
  await f.broker.check();
  assert.equal(f.exchanges(), 1);
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.equal(f.broker.snapshot().loginError, undefined);
  assert.equal(f.saved.at(-1).proofFailureGeneration, undefined);
  await f.broker.close();
});

test('explicit retry repairs an old vault only for the live local credential owner', async () => {
  const f = setup();
  await f.vault.save({ manualLoginRequired: true, revocations: [] });
  await f.broker.initialize();
  await assert.rejects(
    f.broker.command(command(f.broker, 'retry_local_uac'), OWNER),
    /common.conflict/,
  );
  await f.publish();
  await f.broker.check();
  assert.equal(f.requests.length, 0);
  await assert.rejects(
    f.broker.command(command(f.broker, 'retry_local_uac'), OTHER),
    /common.conflict/,
  );
  const retry = command(f.broker, 'retry_local_uac');
  await assert.rejects(
    f.broker.pageCommand(retry, OWNER),
    /common.validation_failed/,
  );
  const response = await f.broker.command(retry, OWNER);
  assert.equal(response.snapshot.state, 'authenticating');
  await Promise.all(f.broker.inflight);
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.equal(f.exchanges(), 1);
  assert.deepEqual(await f.broker.command(retry, OWNER), response);
  assert.equal(f.exchanges(), 1);
  await f.broker.close();
});

test('transient proof retries stop after five attempts for the same local generation', async () => {
  const f = setup();
  let elapsed = 0;
  f.broker.mono = () => elapsed;
  f.fail(
    Object.assign(new Error('limited'), {
      status: 429,
      code: 'iam.rate_limited',
    }),
  );
  for (let i = 0; i < 24; i++) {
    await f.publish();
    await f.broker.check();
    elapsed += 5000;
  }
  assert.equal(f.requests.length, 5);
  assert.equal(f.broker.snapshot().state, 'reauth_required');
  assert.equal(f.broker.snapshot().reason, 'rate_limited');
  assert.equal(f.exchanges(), 0);
  await f.broker.close();
});

test('lost single-use exchange response persists a recovery gate across broker restarts', async () => {
  const f = setup();
  f.platform.nativePoll = async () => {
    assert.equal(f.saved.at(-1).manualLoginRequired, true);
    throw new Error('lost synthetic token response');
  };
  await f.publish();
  await f.broker.check();
  assert.equal(f.broker.record.manualLoginRequired, true);
  assert.equal(f.broker.snapshot().state, 'reauth_required');
  const { Broker } = await import('../broker.mjs');
  const next = await new Broker({
    platform: f.platform,
    vault: f.vault,
    openBrowser: async () => {},
  }).initialize();
  const before = f.requests.length;
  await next.localUac(f.credential, OWNER);
  await next.check();
  assert.equal(f.requests.length, before);
  assert.equal(next.record.tokens, undefined);
  await f.broker.close();
  await next.close();
});

test('explicit logout and remote revocation cannot silently recreate a device', async () => {
  for (const revoke of [false, true]) {
    const f = setup();
    await f.publish();
    await f.broker.check();
    if (revoke) {
      f.platform.profile = async () => {
        throw Object.assign(new Error('revoked'), {
          status: 401,
          code: 'iam.invalid_token',
        });
      };
      await f.broker.check();
      assert.equal(f.broker.snapshot().state, 'revoked');
    } else await f.broker.logout();
    for (let i = 0; i < 4; i++) {
      await f.publish({ ...f.credential, generation: randomUUID() });
      await f.broker.check();
    }
    assert.equal(f.exchanges(), 1);
    assert.equal(f.broker.record.manualLoginRequired, true);
    assert.equal(f.broker.record.tokens, undefined);
    await f.broker.close();
  }
});

test('local lease loss, logout and account switch fence a late token response', async () => {
  for (const change of ['lease', 'logout', 'account']) {
    const f = setup();
    let resolve, entered;
    const started = new Promise((r) => {
      entered = r;
    });
    const response = new Promise((r) => {
      resolve = r;
    });
    f.platform.nativePoll = async () => {
      entered();
      return response;
    };
    await f.publish();
    const running = f.broker.check();
    await started;
    if (change === 'logout') await f.broker.logout();
    else
      await f.publish(
        change === 'account'
          ? { ...f.credential, generation: randomUUID(), empNo: '002Cd' }
          : null,
      );
    resolve(f.tokens);
    await running;
    assert.equal(f.broker.record.tokens, undefined);
    assert.notEqual(f.broker.snapshot().state, 'authenticated');
    assert.ok(f.revoked.includes(f.tokens.refresh_token));
    await f.broker.close();
  }
});

test('a local account switch after association clears the former platform identity', async () => {
  const f = setup();
  await f.publish();
  await f.broker.check();
  await f.publish({
    ...f.credential,
    generation: randomUUID(),
    empNo: '002Cd',
  });
  await f.broker.check();
  assert.equal(f.broker.snapshot().state, 'reauth_required');
  assert.equal(f.broker.snapshot().reason, 'account_changed');
  assert.equal(f.broker.snapshot().identity, undefined);
  assert.equal(f.exchanges(), 1);
  await f.broker.close();
});

test('three offline recovery cycles preserve one device and use the existing refresh lineage', async () => {
  const f = setup();
  await f.publish();
  await f.broker.check();
  const profile = f.platform.profile;
  let refreshes = 0;
  f.platform.refresh = async () => {
    refreshes++;
    return { ...f.tokens, client_id: 'auriga-dsh-native' };
  };
  for (let i = 0; i < 3; i++) {
    f.platform.profile = async () => {
      throw Object.assign(new Error('offline'), { status: 503 });
    };
    await f.broker.check();
    assert.equal(f.broker.snapshot().state, 'unreachable');
    f.platform.profile = profile;
    await f.broker.check();
    assert.equal(f.broker.snapshot().state, 'authenticated');
  }
  assert.equal(refreshes, 3);
  assert.equal(f.exchanges(), 1);
  assert.equal(
    f.requests.filter((r) => r.path.endsWith('/challenges')).length,
    1,
  );
  await f.broker.close();
});

test('proof IPC requires a live OS backend peer, current epoch and increasing sequence', async () => {
  const { HostChannel } = await import('../host.mjs');
  const { valid } = await import('../schema.mjs');
  const f = setup();
  f.mfa();
  const host = new HostChannel(f.broker);
  const peer = host.connect({
    verified: true,
    connectionId: randomUUID(),
    sid: 'synthetic-sid',
    processStart: '1',
    role: 'plugin-backend',
  });
  const message = {
    protocolVersion: 1,
    requestId: randomUUID(),
    shellEpoch: f.broker.epoch,
    sequence: 1,
    expiresAt: new Date(Date.now() + 25000).toISOString(),
    credential: f.credential,
  };
  assert.equal(valid('ShellAuthCommand', message), false);
  assert.equal(valid('ShellAuthPageCommand', message), false);
  await assert.rejects(host.localUac(message, { ...peer }), /peer_not_allowed/);
  await assert.rejects(
    host.localUac({ ...message, shellEpoch: randomUUID() }, peer),
    /stale_identity/,
  );
  await host.localUac(message, peer);
  await f.broker.check();
  await assert.rejects(host.localUac(message, peer), /stale_identity/);
  host.disconnect(peer);
  await f.broker.check();
  assert.equal(f.broker.localSource, undefined);
  assert.equal(f.broker.attempt, undefined);
  await assert.rejects(
    host.localUac({ ...message, sequence: 2 }, peer),
    /peer_not_allowed/,
  );
  host.close();
  await f.broker.close();
});

test('hosts without local proof capability reject publication and release peers normally', async () => {
  const { HostChannel } = await import('../host.mjs');
  const authority = new EventEmitter();
  const host = new HostChannel(authority);
  const connect = () =>
    host.connect({
      verified: true,
      connectionId: randomUUID(),
      sid: 'synthetic-managed-sid',
      processStart: '1',
      role: 'plugin-backend',
    });
  const peer = connect();
  await assert.rejects(host.localUac({}, peer), /peer_not_allowed/);
  assert.doesNotThrow(() => host.disconnect(peer));
  connect();
  assert.doesNotThrow(() => host.close());
  assert.equal(authority.listenerCount('changed'), 0);
});
