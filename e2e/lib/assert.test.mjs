import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  assertRevokeSuccess,
  containerIsRunning,
  edgeInstanceOk,
  instanceReadyRunning,
  phaseReached,
  safeSummary,
  assertInstanceUnauthorized,
  assertStoppedEvidence,
  assertRevocationClose,
  assertAlarmWake,
} from './assert.mjs';

describe('phaseReached (strict actual phase)', () => {
  it('rejects desired-only stop (old waitPhase false positive)', () => {
    assert.equal(
      phaseReached({ phase: 'running', desired: 'stopped' }, 'stopped'),
      false,
    );
    assert.equal(
      phaseReached({ phase: 'stopping', desired: 'stopped' }, 'stopped'),
      false,
    );
  });

  it('accepts only actual phase match', () => {
    assert.equal(
      phaseReached({ phase: 'stopped', desired: 'stopped' }, 'stopped'),
      true,
    );
    assert.equal(
      phaseReached({ phase: 'running', desired: 'running' }, 'running'),
      true,
    );
  });
});

describe('instanceReadyRunning', () => {
  it('requires ready + running phase', () => {
    assert.equal(instanceReadyRunning({ phase: 'running', ready: true }), true);
    assert.equal(
      instanceReadyRunning({ phase: 'running', ready: false }),
      false,
    );
    assert.equal(
      instanceReadyRunning({ phase: 'starting', ready: true }),
      false,
    );
  });
});

describe('assertRevokeSuccess', () => {
  it('rejects 4xx soft-pass (old revokeSmoke false positive)', () => {
    assert.throws(() => assertRevokeSuccess(404, { title: 'not found' }), {
      code: 'revoke_not_ok',
    });
    assert.throws(() => assertRevokeSuccess(400, {}), {
      code: 'revoke_not_ok',
    });
    assert.throws(() => assertRevokeSuccess(500, {}), {
      code: 'revoke_not_ok',
    });
  });

  it('accepts contract success statuses', () => {
    assert.doesNotThrow(() => assertRevokeSuccess(204));
    assert.doesNotThrow(() => assertRevokeSuccess(200, { revoked: 1 }));
  });
});

describe('containerIsRunning', () => {
  it('parses docker inspect Running field', () => {
    assert.equal(containerIsRunning('true'), true);
    assert.equal(containerIsRunning('false'), false);
    assert.equal(containerIsRunning('exited'), false);
  });
});

describe('edgeInstanceOk', () => {
  it('requires 200 and fake-dsh body', () => {
    assert.equal(edgeInstanceOk({ status: 200, text: 'fake-dsh ok\n' }), true);
    assert.equal(edgeInstanceOk({ status: 502, text: 'fake-dsh ok\n' }), false);
    assert.equal(edgeInstanceOk({ status: 200, text: 'starting…' }), false);
  });
});

describe('safeSummary', () => {
  it('redacts cookie and token-like material', () => {
    const s = safeSummary({
      cookie: 'auriga_session=supersecret',
      Authorization: 'Bearer abc.def',
    });
    assert.doesNotMatch(s, /supersecret/);
    assert.doesNotMatch(s, /abc\.def/);
    assert.match(s, /redacted/i);
  });
});

for (const input of [
  {
    token: 'sentinel-token',
    values: { password: 'sentinel-password', otp: '654321' },
    items: [{ secret: 'sentinel-secret' }],
  },
  JSON.stringify({
    ticket: 'sentinel-ticket',
    nonce: 'sentinel-nonce',
    cookie: 'sentinel-cookie',
    nested: { totpSecret: 'sentinel-totp' },
  }),
]) {
  it('redacts nested JSON credential keys before truncation', () => {
    const summary = safeSummary(input, 2000);
    assert.doesNotMatch(summary, /sentinel-|654321/);
    assert.match(summary, /redacted/);
  });
}
it('redacts standalone JWT-shaped values in error text', () => {
  const jwt = [
    Buffer.from(JSON.stringify({ alg: 'HS256' })).toString('base64url'),
    Buffer.from(JSON.stringify({ sub: 'test' })).toString('base64url'),
    Buffer.from('fake-signature').toString('base64url'),
  ].join('.');
  assert.doesNotMatch(safeSummary(`failure ${jwt}`), /eyJ/);
});

describe('strict lifecycle and revoke evidence', () => {
  it('only accepts the exact edge authentication rejection', () => {
    assert.doesNotThrow(() =>
      assertInstanceUnauthorized({
        status: 401,
        body: { code: 'edge.unauthorized' },
      }),
    );
    for (const status of [200, 302, 403, 404, 500, 502, 503, 504])
      assert.throws(() =>
        assertInstanceUnauthorized({
          status,
          body: { code: 'edge.unauthorized' },
        }),
      );
    assert.throws(() =>
      assertInstanceUnauthorized({
        status: 401,
        body: { code: 'common.unauthenticated' },
      }),
    );
    assert.throws(() =>
      assertInstanceUnauthorized({
        status: 401,
        body: { code: 'edge.unauthorized' },
        text: 'fake-dsh',
      }),
    );
  });
  it('requires an existing stopped container and stopped-route code', () => {
    const page = { status: 503, body: { code: 'edge.instance_not_running' } };
    assert.doesNotThrow(() =>
      assertStoppedEvidence({ ok: true, field: 'false' }, page),
    );
    for (const inspect of [
      { ok: true, field: 'missing' },
      { ok: false, field: 'false' },
      { ok: true, field: 'true' },
    ])
      assert.throws(() => assertStoppedEvidence(inspect, page));
    for (const status of [200, 401, 403, 404, 500, 502, 504])
      assert.throws(() =>
        assertStoppedEvidence({ ok: true, field: 'false' }, { status }),
      );
    assert.throws(() =>
      assertStoppedEvidence(
        { ok: true, field: 'false' },
        { status: 503, body: { code: 'common.unavailable' } },
      ),
    );
  });
  it('rejects pre-DELETE, equal-time, missing and application Close evidence', () => {
    assert.doesNotThrow(() => assertRevocationClose({ at: 1001 }, 1000, null));
    for (const info of [null, { at: 999 }, { at: 1000 }])
      assert.throws(() => assertRevocationClose(info, 1000, null));
    assert.throws(() =>
      assertRevocationClose({ at: 1001 }, 1000, { code: 1001 }),
    );
  });
  it('requires a new alarm pre-warm start and consumed/advanced schedule', () => {
    const wakeAt = Date.parse('2026-01-01T00:10:00Z');
    const stoppedAt = wakeAt - 240_000;
    const inspect = {
      ok: true,
      running: true,
      startedAt: new Date(wakeAt - 120_000).toISOString(),
    };
    assert.doesNotThrow(() => assertAlarmWake(inspect, wakeAt, stoppedAt, {}));
    assert.doesNotThrow(() =>
      assertAlarmWake(inspect, wakeAt, stoppedAt, {
        nextWakeAt: new Date(wakeAt + 60_000).toISOString(),
      }),
    );
    for (const startedAt of [
      'invalid',
      new Date(stoppedAt).toISOString(),
      new Date(wakeAt - 126_000).toISOString(),
    ])
      assert.throws(() =>
        assertAlarmWake({ ...inspect, startedAt }, wakeAt, stoppedAt, {}),
      );
    assert.throws(() =>
      assertAlarmWake(inspect, wakeAt, stoppedAt, {
        nextWakeAt: new Date(wakeAt).toISOString(),
      }),
    );
  });
});
