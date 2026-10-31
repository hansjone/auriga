import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { mkdir, mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { parseArgs, runPhase1, totp, nextTotp } from './run.mjs';

test('第⑤项调用固定模型原生云桌入口，单项通过不声称完整八项通过', async () => {
  let calls = 0;
  const report = await runPhase1(
    {
      baseUrl: 'https://portal.example.com:8080',
      only: [5],
      output: 'unused.json',
    },
    {
      cloudDesk: {
        testOnly: true,
        connect: { platformOrigin: 'https://portal.example.com:8080' },
        output: 'native-evidence',
      },
    },
    {
      nativeConversation: async () => {
        calls++;
        return {
          status: 'passed',
          runId: 'test-run',
          fixture: { receipts: [1, 2, 3, 4] },
          cleanup: [],
        };
      },
    },
  );
  assert.equal(calls, 1);
  assert.equal(report.items[4].status, 'PASS');
  assert.equal(report.evidenceType, 'injected-test-adapter');
  assert.equal(report.complete, false);
  assert.equal(report.items[4].nativeConversation.receivedMessages, 4);
});

test('第⑤项原生报告失败沿用真实失败trace，忽略不相关面板502', async () => {
  const report = await runPhase1(
    {
      baseUrl: 'https://portal.example.com:8080',
      only: [5],
      output: 'unused.json',
    },
    {
      cloudDesk: {
        testOnly: true,
        connect: { platformOrigin: 'https://portal.example.com:8080' },
        output: 'native-evidence',
      },
    },
    {
      nativeConversation: async () => ({
        status: 'failed',
        stage: 'cloud-native-network-join',
        failure: {
          stage: 'cloud-native-network-join',
          traceId: 'a'.repeat(32),
        },
        httpFailures: [
          {
            stage: 'cloud-node-plugin-health',
            status: 502,
            traceId: 'b'.repeat(32),
          },
        ],
        cleanup: [],
      }),
    },
  );
  assert.equal(report.items[4].status, 'FAIL');
  assert.equal(report.items[4].traceId, 'a'.repeat(32));
  assert.equal(report.items[4].traceAcknowledged, true);
});

test('第⑤项拒绝非固定模型配置及与报告目标不同的平台', async () => {
  for (const cloudDesk of [
    {
      testOnly: false,
      connect: { platformOrigin: 'https://portal.example.com:8080' },
    },
    {
      testOnly: true,
      connect: { platformOrigin: 'https://other.example.com' },
    },
  ]) {
    let calls = 0;
    const report = await runPhase1(
      {
        baseUrl: 'https://portal.example.com:8080',
        only: [5],
        output: 'unused.json',
      },
      { cloudDesk },
      {
        nativeConversation: async () => {
          calls++;
          return { status: 'passed' };
        },
      },
    );
    assert.equal(report.items[4].status, 'FAIL');
    assert.equal(calls, 0);
  }
});

async function fixture(t, handler) {
  const server = http.createServer(handler);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  return `http://127.0.0.1:${server.address().port}`;
}

test('CLI落盘八项JSON与部分验收退出码2', async (t) => {
  const baseUrl = await fixture(t, (req, res) => {
    res.setHeader('content-type', 'application/json');
    res.end('{"items":[]}');
  });
  const parent = fileURLToPath(new URL('./.local/', import.meta.url));
  await mkdir(parent, { recursive: true });
  const directory = await mkdtemp(join(parent, 'cli-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const credentials = join(directory, 'credentials.json');
  const output = join(directory, 'phase1-report.json');
  await writeFile(credentials, '{}');
  const child = spawn(
    process.execPath,
    [
      fileURLToPath(new URL('./run.mjs', import.meta.url)),
      '--base-url',
      baseUrl,
      '--admin-credentials',
      credentials,
      '--only',
      '1',
      '--output',
      output,
    ],
    { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true },
  );
  let stdout = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk;
  });
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', resolve);
  });
  assert.equal(code, 2);
  const report = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(report.items.length, 8);
  assert.equal(report.complete, false);
  assert.match(stdout, /PASS=0 FAIL=0 SKIP=8/);
});

test('CLI实际加载浏览器模块不被反向导入与顶层await锁死', async (t) => {
  const parent = fileURLToPath(new URL('./.local/', import.meta.url));
  await mkdir(parent, { recursive: true });
  const directory = await mkdtemp(join(parent, 'cli-browser-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const credentials = join(directory, 'credentials.json');
  const output = join(directory, 'report.json');
  await writeFile(credentials, '{}');
  const child = spawn(
    process.execPath,
    [
      fileURLToPath(new URL('./run.mjs', import.meta.url)),
      '--base-url',
      'http://127.0.0.1:1',
      '--admin-credentials',
      credentials,
      '--only',
      '2',
      '--output',
      output,
    ],
    { stdio: 'ignore', windowsHide: true },
  );
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', resolve);
  });
  assert.equal(
    code,
    1,
    '缺部署材料应正常落盘FAIL，不能以未完成顶层await的13退出',
  );
  const report = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(report.items[1].status, 'FAIL');
  assert.equal(report.cloudJourney.failedStep, 'preflight');
});

test('集中Loki查询覆盖本次失败trace而不是无关成功日志', async (t) => {
  let failedTrace;
  const baseUrl = await fixture(t, (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    res.setHeader('content-type', 'application/json');
    if (url.pathname === '/health') {
      failedTrace = req.headers.traceparent.split('-')[1];
      res.writeHead(503);
      res.end('{}');
      return;
    }
    assert.equal(url.pathname, '/loki/api/v1/query_range');
    assert.match(url.searchParams.get('query'), new RegExp(failedTrace));
    res.end(
      JSON.stringify({
        status: 'success',
        data: {
          result: [
            {
              values: ['edge', 'iam'].map((service) => [
                '1',
                JSON.stringify({ service, trace_id: failedTrace }),
              ]),
            },
          ],
        },
      }),
    );
  });
  const report = await runPhase1(
    { baseUrl, only: [1, 8] },
    {
      healthChecks: [{ name: 'platform', url: '/health', kind: 'spring' }],
      observability: {
        lokiUrl: baseUrl,
        streamSelector: '{service=~".+"}',
        expectedServices: ['edge', 'iam'],
      },
    },
  );
  assert.equal(report.items[0].status, 'FAIL');
  assert.equal(report.items[7].status, 'PASS');
  assert.equal(report.items[7].verifiedTraceCount, 1);
});

test('remote Loki query transport proves the same cross-service trace coverage', async (t) => {
  let probe;
  const baseUrl = await fixture(t, (req, res) => {
    probe = { method: req.method, path: req.url };
    res.writeHead(400, { 'content-type': 'application/json' });
    res.end('{}');
  });
  const report = await runPhase1(
    { baseUrl, only: [8] },
    {
      observability: {
        lokiUrl: 'http://127.0.0.1:3100',
        streamSelector: '{service=~".+"}',
        expectedServices: ['edge', 'iam'],
        queryCommand: [
          process.execPath,
          '-e',
          `
          let input = '';
          process.stdin.on('data', chunk => input += chunk);
          process.stdin.on('end', () => {
            const url = new URL(input.trim());
            if (BigInt(url.searchParams.get('end')) - BigInt(url.searchParams.get('start')) < 10000000000n) process.exit(5);
            const query = url.searchParams.get('query');
            const id = query.match(/trace_id="([0-9a-f]{32})"/)[1];
            process.stdout.write(JSON.stringify({status:'success',data:{result:[{
              values: ['edge','iam'].map(service => ['1',JSON.stringify({service,trace_id:id})])
            }]}}));
          });
        `,
        ],
      },
    },
  );
  assert.equal(report.items[7].status, 'PASS');
  assert.equal(report.items[7].verifiedTraceCount, 1);
  assert.equal(probe.method, 'POST');
  assert.match(
    probe.path,
    /^\/api\/iam\/v1\/login\/transactions\/[a-f0-9-]{36}\/advance$/,
  );
});

test('参数拒绝未知条目、URL凭据和缺少管理员文件', () => {
  assert.throws(() => parseArgs(['--base-url', 'http://localhost']));
  assert.throws(() =>
    parseArgs([
      '--base-url',
      'http://u:p@localhost',
      '--admin-credentials',
      'a',
    ]),
  );
  assert.throws(() =>
    parseArgs([
      '--base-url',
      'http://localhost',
      '--admin-credentials',
      'a',
      '--only',
      '9',
    ]),
  );
  assert.deepEqual(
    parseArgs([
      '--base-url',
      'http://localhost',
      '--admin-credentials',
      'a',
      '--only',
      '1,2',
    ]).only,
    [1, 2],
  );
});

test('RFC6238 TOTP 向量', () => {
  assert.equal(totp('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', 59000), '287082');
});

test('连续注册/登录等待下一个TOTP窗口，避免真实IAM重放拒绝', async () => {
  let time = 60000;
  const credentials = {
    totpSecret: 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', // gitleaks:allow RFC 6238 public test vector
    otpCounter: 2,
  };
  const otp = await nextTotp(credentials, {
    now: () => time,
    sleep: async (ms) => {
      time += ms;
    },
  });
  assert.equal(credentials.otpCounter, 3);
  assert.equal(otp, totp(credentials.totpSecret, time));
});

test('健康核心通过，未部署LiteLLM是SKIP而不是PASS', async (t) => {
  const baseUrl = await fixture(t, (req, res) => {
    assert.match(req.headers.traceparent, /^00-[a-f0-9]{32}-[a-f0-9]{16}-01$/);
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ status: 'UP' }));
  });
  const report = await runPhase1(
    { baseUrl, only: [1] },
    { healthChecks: [{ name: 'platform', url: '/health', kind: 'spring' }] },
  );
  assert.equal(report.items[0].status, 'SKIP');
  assert.equal(report.items[0].checks[0].status, 'PASS');
  assert.equal(report.items[1].status, 'SKIP');
});

test('HTTP健康失败保留响应trace与收集命令且不泄漏响应秘密', async (t) => {
  const trace = 'a'.repeat(32);
  const baseUrl = await fixture(t, (req, res) => {
    res.writeHead(503, { 'x-trace-id': trace });
    res.end('password=never-print-this');
  });
  const report = await runPhase1(
    { baseUrl, only: [1] },
    { healthChecks: [{ name: 'platform', url: '/health', kind: 'spring' }] },
  );
  assert.equal(report.items[0].status, 'FAIL');
  assert.equal(report.items[0].traceId, trace);
  assert.match(
    report.items[0].collectCommand,
    /scripts\/logs\/collect --trace-id a{32}/,
  );
  assert.equal(JSON.stringify(report).includes('never-print-this'), false);
});

test('邀请失败不阻断其他条目，缺少桌面和中转站均给SKIP原因', async () => {
  const adapter = {
    invite: async () => {
      throw new Error('password=must-not-leak');
    },
    close: async () => {},
  };
  const report = await runPhase1(
    { baseUrl: 'http://localhost', only: [2, 4, 6, 7, 8] },
    {},
    { adapter },
  );
  assert.equal(report.items[1].status, 'FAIL');
  assert.equal(report.items[3].status, 'SKIP');
  assert.equal(report.items[5].status, 'SKIP');
  assert.equal(report.items[6].status, 'SKIP');
  assert.equal(report.items[7].status, 'SKIP');
  assert.equal(JSON.stringify(report).includes('must-not-leak'), false);
  assert.ok(
    report.items.every((item) => item.reason || item.status === 'PASS'),
  );
});

test('--only 3 自动准备邀请身份但不声称第2项已验收', async () => {
  let invitations = 0;
  const adapter = {
    invite: async () => {
      invitations++;
      return { memberVerified: true };
    },
    cloud: async () => ({ ready: true, pluginLoggedIn: true }),
    close: async () => {},
  };
  const report = await runPhase1(
    { baseUrl: 'http://localhost', only: [3] },
    { cloud: { loggedInSelector: '#logged' } },
    { adapter },
  );
  assert.equal(invitations, 1);
  assert.equal(report.items[1].status, 'SKIP');
  assert.equal(report.items[2].status, 'PASS');
});

test('统一②③仅邀请一次，无自定义登录selector也必须执行托管插件验证', async () => {
  const calls = [];
  const evidence = {
    status: 'passed',
    steps: [{ name: 'plugin-login', mode: 'managed', anonymousStatus: 401 }],
  };
  const report = await runPhase1(
    { baseUrl: 'http://localhost', only: [2, 3] },
    {},
    {
      adapter: {
        invite: async () => {
          calls.push('invite');
          return { memberVerified: true };
        },
        cloud: async () => {
          calls.push('cloud');
          return { ready: true, pluginLoggedIn: true };
        },
        close: async () => {
          calls.push('close');
        },
        evidence: () => evidence,
      },
    },
  );
  assert.deepEqual(calls, ['invite', 'cloud', 'close']);
  assert.equal(report.items[1].status, 'PASS');
  assert.equal(report.items[2].status, 'PASS');
  assert.deepEqual(report.cloudJourney, evidence);
});

test('实例READY但插件尚未登录时第③项失败，不被准备成功掩盖', async () => {
  const report = await runPhase1(
    { baseUrl: 'http://localhost', only: [2, 3] },
    {},
    {
      adapter: {
        invite: async () => ({ memberVerified: true }),
        cloud: async () => ({ ready: true, pluginLoggedIn: false }),
        close: async () => {},
      },
    },
  );
  assert.equal(report.items[1].status, 'PASS');
  assert.equal(report.items[2].status, 'FAIL');
  assert.equal(report.complete, false);
});

test('邀请首失后依赖条目沿用首失trace，不重放创建邀请', async () => {
  let attempts = 0;
  const report = await runPhase1(
    { baseUrl: 'http://localhost', only: [2, 3] },
    { cloud: { loggedInSelector: '#logged' } },
    {
      adapter: {
        invite: async () => {
          attempts++;
          throw new Error('fixture failure');
        },
        close: async () => {},
      },
    },
  );
  assert.equal(attempts, 1);
  assert.equal(report.items[1].traceId, report.items[2].traceId);
});

test('已配置的LiteLLM 503不能冒未部署SKIP', async (t) => {
  const baseUrl = await fixture(t, (req, res) => {
    res.writeHead(503);
    res.end('{}');
  });
  const report = await runPhase1(
    { baseUrl, only: [1] },
    { healthChecks: [], relay: { baseUrl } },
  );
  assert.equal(report.items[0].status, 'FAIL');
});

for (const mismatch of [false, true]) {
  test(`真实提供商模式允许同提供商与同地址的两个模型，实际池不匹配则拒绝：${mismatch}`, async (t) => {
    const models = ['model-a', 'model-b'].map((name) => ({
      name,
      provider: 'provider-a',
      apiBase: 'https://models.example.com/v1',
      upstreamModel: 'openai/' + name,
    }));
    const baseUrl = await fixture(t, (req, res) => {
      assert.equal(req.url, '/model/info');
      res.setHeader('content-type', 'application/json');
      res.end(
        JSON.stringify({
          data: models.map((model, index) => ({
            model_name: model.name,
            litellm_params: {
              api_base:
                mismatch && index === 1
                  ? 'https://other.example.com/v1'
                  : model.apiBase,
              model: model.upstreamModel,
            },
          })),
        }),
      );
    });
    let calls = 0;
    const report = await runPhase1(
      { baseUrl, only: [6], desktopDsh: 'isolated-test-descriptor.json' },
      {
        relay: { baseUrl, upstreamMode: 'live', allowModelCalls: true, models },
        desktop: { loggedInSelector: '#signed-in' },
        modelUi: {},
      },
      {
        adapter: {
          invite: async () => ({ memberVerified: true }),
          cloud: async () => ({ ready: true, pluginLoggedIn: true }),
          desktop: async () => ({ installed: true, pluginLoggedIn: true }),
          models: async () => {
            calls++;
          },
          close: async () => {},
        },
      },
    );
    assert.equal(report.items[5].status, mismatch ? 'FAIL' : 'PASS');
    assert.equal(calls, mismatch ? 0 : 1);
    assert.equal(report.evidenceType, 'injected-test-adapter');
  });
}

test('真实提供商模式须显式开启且不接受伪标testOnly或重复模型', async () => {
  const model = {
    name: 'model-a',
    provider: 'provider-a',
    apiBase: 'https://models.example.com/v1',
    upstreamModel: 'openai/model-a',
  };
  for (const relay of [
    {
      upstreamMode: 'unknown',
      allowModelCalls: true,
      models: [model, { ...model, name: 'model-b' }],
    },
    {
      upstreamMode: 'live',
      allowModelCalls: true,
      models: [
        { ...model, testOnly: true },
        { ...model, name: 'model-b' },
      ],
    },
    { upstreamMode: 'live', allowModelCalls: true, models: [model, model] },
  ]) {
    const report = await runPhase1(
      { baseUrl: 'http://127.0.0.1', only: [6] },
      { relay: { baseUrl: 'http://127.0.0.1', ...relay } },
    );
    assert.equal(report.items[5].status, 'FAIL');
  }
  const skipped = await runPhase1(
    { baseUrl: 'http://127.0.0.1', only: [6, 7] },
    {
      relay: {
        baseUrl: 'http://127.0.0.1',
        upstreamMode: 'live',
        models: [model],
      },
    },
  );
  assert.equal(skipped.items[5].status, 'SKIP');
  assert.equal(skipped.items[6].status, 'SKIP');
});

test('真实外部Key验收拒绝缺失公开入口或包含凭据的地址，且不签发Key', async (t) => {
  let requests = 0;
  const baseUrl = await fixture(t, (_req, res) => {
    requests++;
    res.writeHead(500).end('{}');
  });
  const model = {
    name: 'model-a',
    provider: 'provider-a',
    apiBase: 'https://models.example.com/v1',
    upstreamModel: 'openai/model-a',
  };
  for (const overrides of [
    {},
    { publicBaseUrl: 'https://username:password@example.com/ai/v1' },
    {
      publicBaseUrl: 'https://portal.example.com/ai/v1',
      models: [{ ...model, testOnly: 'true' }],
    },
  ]) {
    const report = await runPhase1(
      { baseUrl, only: [7] },
      {
        relay: {
          baseUrl,
          adminKey: 'test-admin',
          upstreamMode: 'live',
          allowModelCalls: true,
          models: [model],
          ...overrides,
        },
      },
    );
    assert.equal(report.items[6].status, 'FAIL');
    assert.equal(requests, 0);
    assert.equal(JSON.stringify(report).includes('username:password'), false);
  }
});

for (const upstreamMode of ['fixture', 'live']) {
  for (const { broken, budgetStatus } of [false, true].flatMap((broken) =>
    [401, 422].map((budgetStatus) => ({ broken, budgetStatus })),
  )) {
    test(`虚拟Key真实HTTP流程与清理（${upstreamMode}，${budgetStatus}）：${broken ? '错误鉴权不能冒预算生效' : '成功调用/限流/预算/用量/吊销'}`, async (t) => {
      const keys = new Map();
      const requestId = randomBytes(8).toString('hex');
      const adminKey = randomBytes(24).toString('hex');
      const handler = async (req, res) => {
        const url = new URL(req.url, 'http://localhost');
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : {};
        const respond = (data, status = 200) => {
          res.writeHead(status, { 'content-type': 'application/json' });
          res.end(JSON.stringify(data));
        };
        const key = req.headers.authorization?.slice(7);
        const completion = [
          '/v1/chat/completions',
          '/ai/v1/chat/completions',
        ].includes(url.pathname);
        if (!completion) assert.equal(key, adminKey);
        else assert.notEqual(key, adminKey);
        if (url.pathname === '/key/generate') {
          const created = randomBytes(20).toString('hex');
          keys.set(created, { ...body, calls: 0 });
          return respond({ key: created });
        }
        if (url.pathname === '/key/delete') {
          for (const key of body.keys) keys.delete(key);
          return respond({});
        }
        if (url.pathname === '/key/info')
          return respond({ info: { spend: 0 } });
        if (url.pathname === '/spend/logs') {
          assert.equal(url.searchParams.get('request_id'), requestId);
          return respond([{ request_id: requestId, total_tokens: 2 }]);
        }
        if (completion) {
          if (!keys.has(key))
            return respond({ error: { type: 'authentication_error' } }, 401);
          const record = keys.get(key);
          if (record.max_budget === 0)
            return respond(
              {
                error: {
                  type: broken ? 'authentication_error' : 'budget_exceeded',
                },
              },
              budgetStatus,
            );
          if (record.calls++)
            return respond({ error: { type: 'rate_limit_exceeded' } }, 429);
          return respond({
            id: requestId,
            choices: [{}],
            usage: { total_tokens: 2 },
          });
        }
        respond({}, 404);
      };
      const baseUrl = await fixture(t, (req, res) => {
        if (upstreamMode === 'live')
          assert.ok(!req.url.includes('chat/completions'));
        void handler(req, res);
      });
      const publicUrl =
        upstreamMode === 'live'
          ? await fixture(t, (req, res) => {
              assert.equal(req.url, '/ai/v1/chat/completions');
              void handler(req, res);
            })
          : undefined;
      const report = await runPhase1(
        { baseUrl, only: [7] },
        {
          relay: {
            baseUrl,
            upstreamMode,
            publicBaseUrl: publicUrl && publicUrl + '/ai/v1',
            adminKey,
            allowModelCalls: true,
            models:
              upstreamMode === 'live'
                ? [
                    {
                      name: 'model-a',
                      provider: 'provider-a',
                      apiBase: 'https://models.example.com/v1',
                      upstreamModel: 'openai/model-a',
                    },
                  ]
                : [{ name: 'fake', provider: 'fake-a', testOnly: true }],
          },
        },
      );
      assert.equal(report.items[6].status, broken ? 'FAIL' : 'PASS');
      assert.equal(keys.size, 0);
      assert.equal(JSON.stringify(report).includes(adminKey), false);
    });
  }
}

test('virtual key cleanup failure retains the first HTTP status without private response data', async (t) => {
  const privateKey = randomBytes(24).toString('hex');
  const baseUrl = await fixture(t, (req, res) => {
    res.setHeader('content-type', 'application/json');
    if (req.url === '/key/generate')
      return res.end(JSON.stringify({ key: privateKey }));
    res.writeHead(req.url === '/key/delete' ? 500 : 403);
    res.end(JSON.stringify({ error: { detail: privateKey } }));
  });
  const report = await runPhase1(
    { baseUrl, only: [7] },
    {
      relay: {
        baseUrl,
        adminKey: 'test-master',
        allowModelCalls: true,
        models: [{ name: 'fake', provider: 'fake-a', testOnly: true }],
      },
    },
  );
  assert.equal(report.items[6].status, 'FAIL');
  assert.match(
    report.items[6].reason,
    /虚拟Key清理失败；首失：HTTP状态403，预期200/,
  );
  assert.equal(JSON.stringify(report).includes(privateKey), false);
});

test('multiple virtual key cleanup failures retain the original HTTP failure', async (t) => {
  const privateKey = randomBytes(24).toString('hex');
  let created = 0,
    calls = 0,
    deleted = 0;
  const baseUrl = await fixture(t, (req, res) => {
    res.setHeader('content-type', 'application/json');
    if (req.url === '/key/generate')
      return res.end(JSON.stringify({ key: privateKey + ++created }));
    if (req.url === '/key/delete') {
      deleted++;
      res.writeHead(500);
      return res.end('{}');
    }
    if (++calls === 1)
      return res.end(
        '{"id":"test-request","choices":[{}],"usage":{"total_tokens":2}}',
      );
    res.writeHead(calls === 2 ? 429 : 500);
    res.end(
      JSON.stringify({
        error: { type: calls === 2 ? 'rate_limit_exceeded' : privateKey },
      }),
    );
  });
  const report = await runPhase1(
    { baseUrl, only: [7] },
    {
      relay: {
        baseUrl,
        adminKey: 'test-master',
        allowModelCalls: true,
        models: [{ name: 'fake', provider: 'fake-a', testOnly: true }],
      },
    },
  );
  assert.equal(created, 2);
  assert.equal(deleted, 2);
  assert.match(
    report.items[6].reason,
    /首失：HTTP状态500，预期400\/401\/402\/403\/422\/429/,
  );
  assert.equal(JSON.stringify(report).includes(privateKey), false);
});
