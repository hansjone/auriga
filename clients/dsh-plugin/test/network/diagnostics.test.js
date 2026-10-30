import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createNetworkLifecycle } from '../../src/network/lifecycle.js';

async function until(check) {
  for (let i = 0; i < 1000; i++) {
    if (check()) return;
    await setImmediate();
  }
  assert.fail('condition not reached');
}
function fixture(ctx, prepare, options = {}) {
  return createNetworkLifecycle({
    directory: 'unused',
    ctx,
    getAccess: async () => 'SECRET-token',
    authorizeLocal: async () => false,
    defaultProvider: { kind: 'device-connection', prepare },
    backoff: { baseMs: 1, maxMs: 1 },
    ...options,
  });
}

test('安全日志生命周期首失保留受控诊断与统一日志字段，重复重试有界且未知对象不泄漏', async () => {
  const lines = [];
  let count = 0;
  const error = Object.assign(
    new Error('SECRET-marker', { cause: new Error('SECRET-cause') }),
    {
      code: 'SECRET-code',
      metadata: { tenant: 'SECRET-tenant' },
      status: 'SECRET-status',
    },
  );
  const lifecycle = fixture(
    {
      logger: {
        warn: (...args) => {
          assert.equal(args.length, 1);
          lines.push(args[0]);
        },
      },
    },
    async () => {
      count++;
      throw error;
    },
  );
  lifecycle.login();
  // Real lifecycle retries, with a bounded wall-time guard rather than a mocked logger counter.
  for (let i = 0; i < 100 && count < 5; i++)
    await new Promise((r) => setTimeout(r, 2));
  await lifecycle.close();
  assert.ok(count >= 5);
  assert.equal(lines.length, 1);
  assert.ok(Buffer.byteLength(lines[0]) <= 2048);
  assert.equal(lines[0].includes('SECRET'), false);
  const event = JSON.parse(lines[0].slice('auriga_network_diagnostic '.length));
  assert.deepEqual(
    Object.keys(event).sort(),
    [
      'v',
      'event',
      'phase',
      'code',
      'http_status',
      'errno',
      'elapsed_ms',
      'budget_ms',
      'attempt',
      'cancelled',
      'created',
      'generation',
      'service',
      'level',
      'trace_id',
    ].sort(),
  );
  assert.equal(event.event, 'failure');
  assert.equal(event.phase, 'identity_prepare');
  assert.equal(event.code, 'unknown');
  assert.equal(event.attempt, 0);
  assert.equal(event.created, null);
  assert.ok(event.elapsed_ms >= 0);
});

test('安全日志一个generation最多保留24种首失，不随重试刷屏', async () => {
  const lines = [];
  let count = 0;
  const lifecycle = fixture(
    { logger: { warn: (line) => lines.push(line) } },
    async () => {
      throw { status: 450 + (count++ % 40) };
    },
  );
  lifecycle.login();
  for (let i = 0; i < 200 && count < 35; i++)
    await new Promise((r) => setTimeout(r, 2));
  await lifecycle.close();
  assert.ok(count >= 35);
  assert.equal(lines.length, 24);
  assert.equal(
    JSON.parse(lines[0].slice('auriga_network_diagnostic '.length)).http_status,
    450,
  );
});

test('安全日志旧generation迟到失败与注销关闭后失败不会覆盖新首失', async () => {
  const events = [];
  let rejectOld,
    calls = 0;
  const lifecycle = fixture(
    {
      logger: {
        warn: (line) =>
          events.push(
            JSON.parse(line.slice('auriga_network_diagnostic '.length)),
          ),
      },
    },
    async () => {
      if (++calls === 1)
        return new Promise((_resolve, reject) => {
          rejectOld = reject;
        });
      throw { status: 403, message: 'SECRET-new' };
    },
    { backoff: { baseMs: 30000, maxMs: 30000 } },
  );
  lifecycle.login();
  await until(() => rejectOld);
  lifecycle.logout();
  lifecycle.login();
  await until(() => events.length === 1);
  const first = JSON.stringify(events);
  rejectOld({ code: 'network.revoked', message: 'SECRET-old' });
  await setImmediate();
  assert.equal(JSON.stringify(events), first);
  assert.equal(events[0].http_status, 403);
  await lifecycle.close();
  assert.equal(JSON.stringify(events), first);
});

for (const ctx of [
  {},
  {
    logger: {
      warn() {
        throw new Error('logger unavailable');
      },
    },
  },
  new Proxy(
    {},
    {
      get() {
        throw new Error('logger getter unavailable');
      },
    },
  ),
])
  test('安全日志缺失或抛错不改变原有403拒绝与关闭', async () => {
    const lifecycle = fixture(ctx, async () => {
      throw { status: 403 };
    });
    lifecycle.login();
    await until(() => lifecycle.status.state === 'unavailable');
    assert.equal(lifecycle.status.reason, 'forbidden');
    await lifecycle.close();
    assert.equal(lifecycle.status.state, 'stopped');
  });

test('安全日志logger晚挂载后下次重试仍可记录，缺失期间不消耗去重额度', async () => {
  const ctx = {};
  const lines = [];
  let count = 0;
  const lifecycle = fixture(ctx, async () => {
    count++;
    throw { status: 503 };
  });
  lifecycle.login();
  await until(() => count > 0);
  ctx.logger = { warn: (line) => lines.push(line) };
  for (let i = 0; i < 100 && !lines.length; i++)
    await new Promise((r) => setTimeout(r, 2));
  await lifecycle.close();
  assert.equal(lines.length, 1);
});

test('安全日志不读取异常metadata或cause属性', async () => {
  const lines = [];
  const error = { status: 403 };
  Object.defineProperty(error, 'metadata', {
    get() {
      throw new Error('SECRET-metadata');
    },
  });
  Object.defineProperty(error, 'cause', {
    get() {
      throw new Error('SECRET-cause');
    },
  });
  const lifecycle = fixture(
    { logger: { warn: (line) => lines.push(line) } },
    async () => {
      throw error;
    },
  );
  lifecycle.login();
  await until(() => lines.length);
  await lifecycle.close();
  assert.equal(lines[0].includes('SECRET'), false);
});
