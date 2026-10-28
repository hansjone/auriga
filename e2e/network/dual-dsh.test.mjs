import test from 'node:test';
import { Buffer } from 'node:buffer';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { natsTap, persistenceMarker } from './support/dual-proxy.mjs';
import { replaceOnce } from './support/dual-prepare.mjs';
import { mutate, sourcePath, verdict } from './support/dual-verdict.mjs';
import { authenticatorCode, readyConnectProfile } from './support/dual-cli.mjs';
import { dualEdgeConfig } from './support/dual-edge.mjs';
import { join } from 'node:path';

test('普通登录的验证器按 RFC 6238 SHA1 当前真实计数生成六位码', () => {
  assert.equal(
    authenticatorCode('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', 59000),
    '287082',
  );
  assert.throws(() => authenticatorCode('invalid'));
});

test('CLI 启动地址不代表连接配置生效，须等公开 activeOrigin 后只检查一次 status', async () => {
  const connect = {
    platformOrigin: 'https://example.com',
    deviceName: 'Example A',
  };
  const calls = [];
  let pending = true;
  const ready = await readyConnectProfile(
    async (path) => {
      calls.push(path);
      if (path === '/auriga/settings/state') {
        const activeOrigin = pending ? '' : connect.platformOrigin;
        pending = false;
        return { ...connect, activeOrigin };
      }
      assert.equal(pending, false);
      return { available: true, loggedIn: false };
    },
    connect,
    'A',
  );
  assert.equal(ready.settings.activeOrigin, connect.platformOrigin);
  assert.deepEqual(calls, [
    '/auriga/settings/state',
    '/auriga/settings/state',
    '/auriga/local-login/status',
  ]);
});

test('连接配置错误、状态不可用或请求失败立即失败，不重试状态或重新登录', async () => {
  const connect = {
    platformOrigin: 'https://example.com',
    deviceName: 'Example A',
  };
  for (const bad of [
    { platformOrigin: 'https://other.example.com', activeOrigin: '' },
    { ...connect, activeOrigin: 'https://other.example.com' },
    { ...connect, activeOrigin: connect.platformOrigin, deviceName: 'wrong' },
  ])
    await assert.rejects(readyConnectProfile(async () => bad, connect, 'A'));
  for (const status of [
    { available: false, loggedIn: false },
    { available: true, loggedIn: true },
  ]) {
    await assert.rejects(
      readyConnectProfile(
        async (path) =>
          path === '/auriga/settings/state'
            ? { ...connect, activeOrigin: connect.platformOrigin }
            : status,
        connect,
        'A',
      ),
    );
  }
  let calls = 0;
  await assert.rejects(
    readyConnectProfile(
      async () => {
        calls++;
        throw new Error('503');
      },
      connect,
      'A',
    ),
    /503/,
  );
  assert.equal(calls, 1);
  const paths = [];
  await assert.rejects(
    readyConnectProfile(
      async (path) => {
        paths.push(path);
        if (path === '/auriga/settings/state')
          return { ...connect, activeOrigin: connect.platformOrigin };
        throw new Error('status: 503');
      },
      connect,
      'A',
    ),
    /status: 503/,
  );
  assert.deepEqual(paths, [
    '/auriga/settings/state',
    '/auriga/local-login/status',
  ]);
});

test('双 DSH edge 独立配置固定 network HTTPS 上游和私有信任材料', () => {
  const input = {
    accessFile: 'access.json',
    backend: 'http://127.0.0.1:30000',
    gateway: {
      https: 'https://127.0.0.1:30001',
      ca: join('private', 'broker-cert.pem'),
    },
    directory: 'private',
  };
  const cfg = dualEdgeConfig(input);
  assert.equal(cfg.upstreams['network-submit'], input.gateway.https);
  assert.equal(cfg.upstreams.iam, input.backend);
  assert.equal(cfg.upstreams['ai-device'], undefined);
  assert.deepEqual(cfg.networkSubmitHop, {
    certFile: join('private', 'broker-cert.pem'),
    keyFile: join('private', 'broker-key.pem'),
    caFile: input.gateway.ca,
    serverName: 'localhost',
  });
  for (const https of [
    'http://127.0.0.1:30001',
    'https://example.com:30001',
    'https://127.0.0.1:30001/path',
  ])
    assert.throws(() =>
      dualEdgeConfig({ ...input, gateway: { ...input.gateway, https } }),
    );
});

test('双 DSH 中继跨 WebSocket 分片保持 NATS 原字节且从不观察 CONNECT 凭据', () => {
  const frame = JSON.stringify({ type: 'receipt', operation_id: 'operation' });
  const original = Buffer.from(
    'CONNECT {"auth_token":"private"}\r\nPING\r\nPUB up ' +
      Buffer.byteLength(frame) +
      '\r\n' +
      frame +
      '\r\n',
  );
  const forwarded = [],
    observed = [];
  const tap = natsTap(
    (bytes) => forwarded.push(bytes),
    (value) => observed.push(value),
  );
  for (const byte of original) tap(Buffer.from([byte]));
  assert.deepEqual(Buffer.concat(forwarded), original);
  assert.deepEqual(observed, [JSON.parse(frame)]);
});
test('双 DSH 中继只丢指定应用帧，不生成业务响应且继续转发后续命令', () => {
  const frame = JSON.stringify({ type: 'receipt' }),
    forwarded = [];
  const tap = natsTap(
    (bytes) => forwarded.push(bytes),
    () => 'drop',
  );
  tap(Buffer.from('PUB up ' + frame.length + '\r\n' + frame + '\r\nPING\r\n'));
  assert.equal(Buffer.concat(forwarded).toString(), 'PING\r\n');
});
test('测试装配变换必须唯一匹配，不能弱化未知或重复的测试桥', () => {
  assert.equal(replaceOnce('before', 'before', 'after'), 'after');
  assert.throws(() => replaceOnce('', 'before', 'after'));
  assert.throws(() => replaceOnce('beforebefore', 'before', 'after'));
});
test('SEND-4 单生产反证仍只去掉原 outbox 耐久 await', async () => {
  const source = await readFile(
    new URL('../../' + sourcePath, import.meta.url),
    'utf8',
  );
  const changed = mutate(source);
  assert.notEqual(source, changed);
  assert.ok(!changed.includes('draft.durable.submissions[key] = saved;'));
  assert.ok(changed.includes('const bytes = await submit('));
  assert.throws(() => mutate(source + source));
});
const failure = () => ({
  head: 'a'.repeat(40),
  host: { version: '0.2.1-rc.3' },
  completed: false,
  stages: [
    {
      stage: 'gateway-ready',
      https: 'https://127.0.0.1:30001',
      gatewayPid: 50,
    },
    {
      stage: 'edge-ready',
      pid: 100,
      https: 'https://127.0.0.1:30002',
      submitUpstream: 'https://127.0.0.1:30001',
      serverName: 'localhost',
    },
    ...['A', 'B'].map((name, i) => ({
      stage: 'dsh-ready',
      name,
      pid: i + 1,
      profile: name,
      dshHome: name,
      cwd: name,
    })),
    ...['A', 'B'].map((name) => ({
      stage: 'dsh-closed',
      name,
      exitCode: 0,
      signal: null,
    })),
    ...['edge-closed', 'gateway-closed', 'java-closed'].map((stage) => ({
      stage,
      exitCode: 0,
      signal: null,
    })),
  ],
  checks: ['SEND-4: independent full CLI'],
  businessFailure: {
    name: 'AssertionError',
    code: 'ERR_ASSERTION',
    message: persistenceMarker,
  },
  failures: [{ message: persistenceMarker }],
});
test('SEND-4 反证拒绝其他错误、同进程、遗漏独立 profile 或清理失败', () => {
  verdict(failure(), 'SkipSubmissionCommit', 1);
  for (const change of [
    (m) => (m.stages[3].pid = m.stages[2].pid),
    (m) => (m.stages[3].profile = m.stages[2].profile),
    (m) => (m.businessFailure.message = 'timeout'),
    (m) => (m.stages.at(-1).exitCode = 1),
    (m) => m.failures.push({ message: 'PG survived' }),
    (m) => (m.checks = []),
  ]) {
    const m = failure();
    change(m);
    assert.throws(() => verdict(m, 'SkipSubmissionCommit', 1));
  }
  assert.throws(() => verdict(failure(), 'SkipSubmissionCommit', 0));
});

test('SEND-4 验收必须经过独立 edge、固定 HTTPS submit 上游并干净退出', () => {
  for (const change of [
    (m) => (m.stages = m.stages.filter((v) => v.stage !== 'edge-ready')),
    (m) => (m.stages[1].submitUpstream = 'https://127.0.0.1:30003'),
    (m) => (m.stages[1].https = m.stages[0].https),
    (m) => (m.stages[1].pid = m.stages[2].pid),
    (m) => (m.stages[1].pid = m.stages[0].gatewayPid),
    (m) => (m.stages[1].serverName = ''),
    (m) => (m.stages.find((v) => v.stage === 'edge-closed').exitCode = 1),
  ]) {
    const m = failure();
    change(m);
    assert.throws(() => verdict(m, 'SkipSubmissionCommit', 1));
  }
});
