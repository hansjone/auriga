#!/usr/bin/env node
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomBytes } from 'node:crypto';
import { totp, totpNotBefore } from './checks.mjs';
export { totp } from './checks.mjs';
import { setTimeout as delay } from 'node:timers/promises';

export class StepError extends Error {}
export class Skip extends StepError {}
export function requireThat(condition, message) {
  if (!condition) throw new StepError(message);
}
export function httpUrl(value) {
  const url = new URL(value);
  requireThat(
    ['http:', 'https:'].includes(url.protocol) &&
      !url.username &&
      !url.password &&
      !url.hash &&
      !url.search,
    'URL必须为不含凭据、query和fragment的HTTP(S)地址',
  );
  return url;
}

export function parseArgs(args) {
  const values = {};
  for (let i = 0; i < args.length; i++) {
    const key = args[i];
    if (key === '--help') return { help: true };
    requireThat(
      [
        '--base-url',
        '--admin-credentials',
        '--desktop-dsh',
        '--only',
        '--output',
      ].includes(key),
      '未知参数',
    );
    requireThat(
      !values[key] && args[i + 1] && !args[i + 1].startsWith('--'),
      '参数缺值或重复',
    );
    values[key] = args[++i];
  }
  requireThat(
    values['--base-url'] && values['--admin-credentials'],
    '必须提供--base-url和--admin-credentials',
  );
  httpUrl(values['--base-url']);
  const only = values['--only']
    ? values['--only'].split(',').map(Number)
    : [1, 2, 3, 4, 5, 6, 7, 8];
  requireThat(
    only.length &&
      only.every((id) => Number.isInteger(id) && id >= 1 && id <= 8),
    '--only必须为1到8的逗号分隔编号',
  );
  return {
    baseUrl: values['--base-url'],
    credentialsFile: values['--admin-credentials'],
    desktopDsh: values['--desktop-dsh'],
    only: [...new Set(only)],
    output: resolve(values['--output'] || 'phase1-report.json'),
  };
}

export async function nextTotp(
  credentials,
  { now = Date.now, sleep = delay } = {},
) {
  requireThat(credentials.totpSecret, '登录要求TOTP，凭据文件未配置totpSecret');
  let time = now();
  const notBefore = totpNotBefore(time, credentials.otpCounter);
  if (notBefore > time) {
    await sleep(notBefore - time);
    time = now();
  }
  credentials.otpCounter = Math.floor(time / 30000);
  return totp(credentials.totpSecret, time);
}

export function traceState() {
  return {
    traceId: randomBytes(16).toString('hex'),
    acknowledged: false,
    requests: [],
  };
}
export function traceHeader(state) {
  return `00-${state.traceId}-${randomBytes(8).toString('hex')}-01`;
}
export function observeTrace(state, headers, status) {
  const parent = headers.traceparent?.match(
    /^[\da-f]{2}-([\da-f]{32})-[\da-f]{16}-[\da-f]{2}$/i,
  )?.[1];
  const id = headers['x-trace-id'] || parent;
  state.acknowledged = /^[\da-f]{32}$/i.test(id || '') && !/^0+$/.test(id);
  if (state.acknowledged) {
    state.traceId = id.toLowerCase();
    state.acknowledged = true;
  }
  state.requests.push({
    status,
    traceId: state.traceId,
    acknowledged: state.acknowledged,
  });
}

export async function request(
  url,
  state,
  { method = 'GET', body, key, expected = [200], timeout = 15000 } = {},
) {
  const headers = {
    traceparent: traceHeader(state),
    accept: 'application/json',
  };
  if (body !== undefined) headers['content-type'] = 'application/json';
  if (key) headers.authorization = `Bearer ${key}`;
  let response;
  try {
    response = await globalThis.fetch(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'error',
      signal: globalThis.AbortSignal.timeout(timeout),
    });
  } catch {
    state.acknowledged = false;
    throw new StepError(
      'HTTP请求无法完成（连接、TLS、重定向或超时）；按trace收集日志',
    );
  }
  observeTrace(state, Object.fromEntries(response.headers), response.status);
  requireThat(
    expected.includes(response.status),
    `HTTP状态${response.status}，预期${expected.join('/')}`,
  );
  if (response.status === 204) return null;
  try {
    return await response.json();
  } catch {
    throw new StepError('接口未返回JSON');
  }
}

const names = [
  '部署与健康（含LiteLLM）',
  '邀请、接受、设置登录并重新登录门户',
  '云端DSH READY且插件已登录',
  '隔离桌面DSH安装插件包并登录',
  '云端与桌面双向收发及回复',
  '两提供商模型池、双端调用与用量',
  '外部虚拟Key调用、预算、限流、用量、吊销',
  '失败trace与集中跨服务日志',
];

export async function runPhase1(options, config, dependencies = {}) {
  const report = {
    schemaVersion: 1,
    startedAt: new Date().toISOString(),
    target: httpUrl(options.baseUrl).origin,
    evidenceType:
      dependencies.adapter || dependencies.nativeConversation
        ? 'injected-test-adapter'
        : 'deployed-target',
    items: [],
    cleanupErrors: [],
  };
  let adapter = dependencies.adapter;
  let identityReady = false;
  let cloudReady = false;
  let desktopReady = false;
  const firstFailures = new Map();
  const once = async (name, state, operation) => {
    if (firstFailures.has(name)) {
      const first = firstFailures.get(name);
      state.traceId = first.traceId;
      state.acknowledged = first.acknowledged;
      throw first.error;
    }
    try {
      return await operation();
    } catch (error) {
      firstFailures.set(name, {
        error,
        traceId: state.traceId,
        acknowledged: state.acknowledged,
      });
      throw error;
    }
  };
  const getAdapter = async () => {
    if (!adapter) {
      const { BrowserSteps } = await import('./browser.mjs');
      adapter = new BrowserSteps(options, config);
    }
    return adapter;
  };
  const identity = async (state) => {
    if (!identityReady) {
      await once('identity', state, async () => {
        const result = await (await getAdapter()).invite(state);
        requireThat(result?.memberVerified, '邀请后身份未核实');
      });
      identityReady = true;
    }
  };
  const cloud = async (state) => {
    if (!cloudReady) {
      await identity(state);
      await once('cloud', state, async () => {
        const result = await (await getAdapter()).cloud(state);
        requireThat(
          result?.ready && result.pluginLoggedIn,
          '实例READY或插件登录证据缺失',
        );
      });
      cloudReady = true;
    }
  };
  const desktop = async (state) => {
    if (!options.desktopDsh)
      throw new Skip(
        '未提供--desktop-dsh隔离profile描述文件；不操作用户现有DSH',
      );
    if (!config.desktop?.loggedInSelector)
      throw new Skip('PLUGIN-DIST尚未接线：缺少桌面安装/登录UI配置');
    if (!desktopReady) {
      await identity(state);
      await once('desktop', state, async () => {
        const result = await (await getAdapter()).desktop(state);
        requireThat(
          result?.installed && result.pluginLoggedIn,
          '插件包安装或桌面登录证据缺失',
        );
      });
      desktopReady = true;
    }
  };
  const steps = [
    async (state, item) => {
      item.checks = [];
      const health = config.healthChecks || [
        { name: 'iam', url: '/api/iam/v1/login/providers', kind: 'providers' },
      ];
      for (const check of health) {
        requireThat(
          ['spring', 'providers', 'http'].includes(check.kind),
          'healthChecks.kind必须为spring/providers/http',
        );
        const data = await request(new URL(check.url, options.baseUrl), state);
        if (check.kind === 'spring')
          requireThat(data?.status === 'UP', '服务健康状态不是UP');
        if (check.kind === 'providers')
          requireThat(Array.isArray(data?.items), 'IAM身份源健康响应不合法');
        item.checks.push({ name: check.name, status: 'PASS' });
      }
      if (config.relay?.baseUrl) {
        const data = await request(
          new URL('health/readiness', trailing(config.relay.baseUrl)),
          state,
          { key: config.relay.adminKey },
        );
        requireThat(
          data?.status === 'healthy' || data?.status === 'ok',
          'LiteLLM readiness未就绪',
        );
        item.checks.push({ name: 'LiteLLM', status: 'PASS' });
      } else
        item.checks.push({
          name: 'LiteLLM',
          status: 'SKIP',
          reason: '未配置relay.baseUrl；LiteLLM未部署或未接线',
        });
      if (!config.healthChecks?.length || !config.healthInventoryComplete)
        throw new Skip(
          '已完成列出的健康探测；缺少完整healthChecks清单及healthInventoryComplete，不能声称全部服务healthy',
        );
      if (!config.relay?.baseUrl)
        throw new Skip('基础服务健康检查通过；LiteLLM未部署或未配置');
    },
    async (state) => {
      await identity(state);
    },
    async (state) => {
      await cloud(state);
    },
    async (state) => {
      await desktop(state);
    },
    async (state, item) => {
      if (config.cloudDesk) {
        requireThat(
          config.cloudDesk.testOnly === true,
          '原生云桌入口仅允许本地固定模型testOnly配置',
        );
        requireThat(
          httpUrl(config.cloudDesk.connect?.platformOrigin).origin ===
            report.target,
          '原生云桌平台必须与统一验收目标一致',
        );
        const nativeRun =
          dependencies.nativeConversation ??
          (await import('./cloud-desk.mjs')).run;
        const result = await nativeRun(
          config.cloudDesk,
          resolve(
            config.cloudDesk.output ?? dirname(options.output),
            config.cloudDesk.output
              ? ''
              : 'cloud-desk-' + randomBytes(6).toString('hex'),
          ),
        );
        item.nativeConversation = {
          status: result.status,
          runId: result.runId,
          stage: result.stage,
          receivedMessages: result.fixture?.receipts?.length ?? 0,
        };
        if (result.status !== 'passed') {
          const trace = result.failure?.traceId;
          if (/^[a-f0-9]{32}$/.test(trace ?? '')) {
            state.traceId = trace;
            state.acknowledged = true;
          }
          throw new StepError(
            '原生云桌互发失败：' +
              (result.failure?.stage ?? result.stage ?? 'unknown'),
          );
        }
        return;
      }
      if (!config.messaging)
        throw new Skip('CLOUD-DESK双向通信UI尚未接线：缺少messaging配置');
      if (config.messaging.testOnly !== true)
        throw new Skip(
          '双端回复可能触发模型；需messaging.testOnly确认只连接免费假上游',
        );
      await cloud(state);
      await desktop(state);
      await (await getAdapter()).messages(state);
    },
    async (state, item) => {
      if (!config.relay?.baseUrl)
        throw new Skip('RELAY-LITELLM尚未部署或未配置');
      const mode = modelCallMode(config.relay);
      item.upstreamMode = mode;
      requireThat(
        new Set(config.relay.models.map((model) => model.name)).size >= 2,
        '需至少两个不同的模型名称',
      );
      if (mode === 'fixture')
        requireThat(
          new Set(config.relay.models.map((model) => model.provider)).size >=
            2 && config.relay.models.every((model) => model.provider),
          '需至少两个独立假提供商',
        );
      else
        requireThat(
          new Set(config.relay.models.map((model) => model.upstreamModel))
            .size >= 2,
          '真实提供商模式需至少两个不同的上游模型',
        );
      if (!config.modelUi)
        throw new Skip(
          '双DSH模型调用UI尚未接线：缺少modelUi；直接代理请求不能代替DSH调用',
        );
      const pool = await request(
        new URL('model/info', trailing(config.relay.baseUrl)),
        state,
        { key: config.relay.adminKey },
      );
      requireThat(
        config.relay.models.every(
          (model) =>
            model.apiBase &&
            pool?.data?.some(
              (entry) =>
                entry.model_name === model.name &&
                entry.litellm_params?.api_base === model.apiBase &&
                (mode !== 'live' ||
                  entry.litellm_params?.model === model.upstreamModel),
            ),
        ),
        'LiteLLM模型池与配置的上游模型或地址不匹配',
      );
      if (mode === 'fixture')
        requireThat(
          new Set(config.relay.models.map((model) => model.apiBase)).size >= 2,
          '至少需要两个独立假上游地址',
        );
      await cloud(state);
      await desktop(state);
      await (await getAdapter()).models(state);
    },
    async (state, item) => {
      item.upstreamMode = await virtualKeys(config.relay, state);
    },
    async (state, item) => {
      if (
        !config.observability?.lokiUrl ||
        !config.observability.streamSelector ||
        !config.observability.expectedServices?.length
      )
        throw new Skip(
          'LOG-1尚未接线：失败已附trace与scripts/logs/collect命令，但缺少Loki地址、streamSelector和expectedServices',
        );
      const traces = report.items
        .filter((entry) => entry.status === 'FAIL')
        .map((entry) => entry.traceId);
      if (!traces.length) {
        // Harmless failing request; no account mutation or expensive model call.
        await request(
          new URL('/api/iam/v1/phase1-not-found', options.baseUrl),
          state,
          { expected: [404] },
        );
        traces.push(state.traceId);
      }
      for (const id of new Set(traces))
        await verifyLogs(config.observability, id, report.startedAt);
      item.verifiedTraceCount = new Set(traces).size;
      item.services = config.observability.expectedServices;
    },
  ];
  for (let index = 0; index < steps.length; index++) {
    const id = index + 1;
    const item = {
      id,
      name: names[index],
      status: 'SKIP',
      reason: '未被--only选中',
    };
    report.items.push(item);
    if (!(options.only || [1, 2, 3, 4, 5, 6, 7, 8]).includes(id)) continue;
    const state = traceState();
    const started = Date.now();
    try {
      await steps[index](state, item);
      item.status = 'PASS';
      delete item.reason;
    } catch (error) {
      item.status = error instanceof Skip ? 'SKIP' : 'FAIL';
      item.reason =
        error instanceof StepError
          ? error.message
          : '验收步骤执行失败（原始异常可能包含秘密，未写入报告）';
      if (item.status === 'FAIL') {
        item.traceId = state.traceId;
        item.traceAcknowledged = state.acknowledged;
        item.collectCommand = `scripts/logs/collect --trace-id ${state.traceId}`;
      }
    }
    item.durationMs = Date.now() - started;
    item.requests = state.requests;
  }
  try {
    await adapter?.close();
  } catch {
    report.cleanupErrors.push(
      '浏览器或桌面测试子进程清理失败，请核测试profile',
    );
  }
  const journey = adapter?.evidence?.();
  if (journey) report.cloudJourney = journey;
  report.finishedAt = new Date().toISOString();
  report.summary = Object.fromEntries(
    ['PASS', 'FAIL', 'SKIP'].map((status) => [
      status,
      report.items.filter((item) => item.status === status).length,
    ]),
  );
  report.complete = report.summary.PASS === 8 && !report.cleanupErrors.length;
  return report;
}

function trailing(url) {
  return httpUrl(url).href.replace(/\/?$/, '/');
}

function modelCallMode(relay) {
  if (relay.allowModelCalls !== true || !relay.models?.length)
    throw new Skip('模型调用未启用；需显式配置models和allowModelCalls');
  const mode = relay.upstreamMode ?? 'fixture';
  requireThat(['fixture', 'live'].includes(mode), '未知的模型上游模式');
  if (mode === 'fixture') {
    requireThat(
      relay.models.every((model) => model.testOnly === true),
      '假上游模式拒绝未标testOnly的模型配置',
    );
    return mode;
  }
  requireThat(
    relay.models.every(
      (model) =>
        (model.testOnly === undefined || model.testOnly === false) &&
        typeof model.name === 'string' &&
        model.name &&
        typeof model.provider === 'string' &&
        model.provider &&
        typeof model.upstreamModel === 'string' &&
        model.upstreamModel &&
        typeof model.apiBase === 'string' &&
        model.apiBase,
    ),
    '真实提供商须声明模型、提供商和上游地址，且不能伪标testOnly',
  );
  requireThat(
    new Set(relay.models.map((model) => model.name)).size ===
      relay.models.length,
    '真实模型名称不能重复',
  );
  for (const model of relay.models) httpUrl(model.apiBase);
  return mode;
}

async function virtualKeys(relay, state) {
  if (!relay?.baseUrl) throw new Skip('RELAY-LITELLM尚未部署或未配置');
  const mode = modelCallMode(relay);
  if (mode === 'live')
    requireThat(
      relay.publicBaseUrl,
      '真实外部Key验收须声明publicBaseUrl公开OpenAI入口',
    );
  requireThat(relay.adminKey, '虚拟Key管理缺少relay.adminKey');
  const base = trailing(relay.baseUrl);
  const completionUrl = relay.publicBaseUrl
    ? new URL('chat/completions', trailing(relay.publicBaseUrl))
    : new URL('v1/chat/completions', base);
  const keys = [];
  const api = (path, options = {}) =>
    request(new URL(path, base), state, { key: relay.adminKey, ...options });
  const generate = async (settings) => {
    const result = await api('key/generate', {
      method: 'POST',
      body: {
        models: relay.models.map((model) => model.name),
        duration: '10m',
        key_alias: `phase1-${randomBytes(8).toString('hex')}`,
        ...settings,
      },
    });
    requireThat(
      typeof result?.key === 'string' && result.key.length > 0,
      'LiteLLM未签发虚拟Key',
    );
    keys.push(result.key);
    return result.key;
  };
  const call = (key, expected = [200]) =>
    request(completionUrl, state, {
      method: 'POST',
      key,
      body: {
        model: relay.models[0].name,
        messages: [{ role: 'user', content: `phase1-${mode}-upstream-probe` }],
        max_tokens: 8,
        stream: false,
      },
      expected,
    });
  let failure;
  try {
    const key = await generate({ rpm_limit: 1, max_budget: 1 });
    const completion = await call(key);
    requireThat(
      completion?.choices?.length &&
        Number.isFinite(completion?.usage?.total_tokens),
      '标准OpenAI响应缺少choices或usage',
    );
    const limited = await call(key, [429]);
    requireThat(
      /rate.?limit|rpm|requests per minute/i.test(
        JSON.stringify(limited?.error),
      ),
      '限流响应不是明确的rate limit错误',
    );
    // A zero budget tests enforcement without consuming a paid budget.
    const exhausted = await generate({ max_budget: 0 });
    const denied = await call(exhausted, [400, 401, 402, 403, 429]);
    requireThat(
      /budget|exceed.*spend/i.test(JSON.stringify(denied?.error)),
      '预算请求被拒绝但没有明确budget错误，不能视为预算生效',
    );
    const infoUrl = new URL('key/info', base);
    infoUrl.searchParams.set('key', key);
    const info = await request(infoUrl, state, { key: relay.adminKey });
    requireThat(Number.isFinite(info?.info?.spend), '虚拟Key用量不可查');
    await waitSpend(relay, completion.id, state);
    await api('key/delete', { method: 'POST', body: { keys: [key] } });
    keys.splice(keys.indexOf(key), 1);
    await call(key, [401, 403]);
  } catch (error) {
    failure = error;
  }
  for (const key of keys) {
    try {
      await api('key/delete', { method: 'POST', body: { keys: [key] } });
    } catch {
      failure = new StepError(
        '虚拟Key清理失败；10分钟到期前请在LiteLLM管理界面检查phase1测试Key',
      );
    }
  }
  if (failure) throw failure;
  return mode;
}

export async function waitSpend(relay, requestId, state) {
  requireThat(
    typeof requestId === 'string' && requestId.length > 0,
    '模型响应缺少可关联的request id',
  );
  const url = new URL('spend/logs', trailing(relay.baseUrl));
  url.searchParams.set('request_id', requestId);
  url.searchParams.set('summarize', 'false');
  const deadline = Date.now() + 10000;
  do {
    const rows = await request(url, state, { key: relay.adminKey });
    requireThat(Array.isArray(rows), 'LiteLLM spend/logs返回格式不合法');
    if (
      rows.some(
        (row) =>
          row.request_id === requestId &&
          (row.total_tokens > 0 ||
            row.prompt_tokens + row.completion_tokens > 0),
      )
    )
      return;
    await delay(250);
  } while (Date.now() < deadline);
  throw new StepError('未查到当前请求的非零token用量');
}

async function verifyLogs(config, id, startedAt) {
  const url = new URL('loki/api/v1/query_range', trailing(config.lokiUrl));
  url.searchParams.set(
    'query',
    `${config.streamSelector} | json | trace_id="${id}"`,
  );
  url.searchParams.set(
    'start',
    String(BigInt(new Date(startedAt).getTime()) * 1000000n),
  );
  url.searchParams.set('limit', '1000');
  const deadline = Date.now() + 10000;
  do {
    url.searchParams.set('end', String(BigInt(Date.now()) * 1000000n));
    const data = await request(url, traceState());
    requireThat(
      data?.status === 'success' && Array.isArray(data?.data?.result),
      'Loki query_range返回格式不合法',
    );
    const services = new Set();
    for (const stream of data.data.result) {
      for (const [, line] of stream.values || []) {
        try {
          const entry = JSON.parse(line);
          if (entry.trace_id === id) services.add(entry.service);
        } catch {
          /* Non-JSON log lines cannot prove structured trace coverage. */
        }
      }
    }
    if (config.expectedServices.every((service) => services.has(service)))
      return;
    await delay(250);
  } while (Date.now() < deadline);
  throw new StepError('同一失败trace在集中日志中未覆盖预期跨服务链路');
}

export async function main(args) {
  let options;
  try {
    options = parseArgs(args);
    if (options.help) {
      console.log(
        'node e2e/phase1/run.mjs --base-url <门户URL> --admin-credentials <JSON文件> [--desktop-dsh <隔离profile JSON>] [--only 1,2] [--output phase1-report.json]',
      );
      return 0;
    }
    const config = JSON.parse(
      (await readFile(options.credentialsFile, 'utf8')).replace(/^\uFEFF/, ''),
    );
    const report = await runPhase1(options, config);
    await mkdir(dirname(options.output), { recursive: true });
    const serialized = `${JSON.stringify(report, null, 2)}\n`;
    const snapshot = resolve(
      dirname(options.output),
      `phase1-${Date.now()}-${randomBytes(4).toString('hex')}.json`,
    );
    await writeFile(snapshot, serialized, { flag: 'wx', mode: 0o600 });
    await writeFile(options.output, serialized, {
      mode: 0o600,
    });
    for (const item of report.items) {
      console.log(
        `${item.id}. ${item.status} ${item.name}${item.reason ? `：${item.reason}` : ''}`,
      );
      if (item.status === 'FAIL')
        console.log(
          `   trace_id=${item.traceId}（${item.traceAcknowledged ? '响应确认' : '仅客户端生成，服务端未确认'}）\n   ${item.collectCommand}`,
        );
    }
    console.log(
      `PASS=${report.summary.PASS} FAIL=${report.summary.FAIL} SKIP=${report.summary.SKIP}；完整验收=${report.complete ? '是' : '否'}；报告：${options.output}`,
    );
    return report.summary.FAIL || report.cleanupErrors.length
      ? 1
      : report.complete
        ? 0
        : 2;
  } catch (error) {
    console.error(
      error instanceof StepError
        ? error.message
        : '启动失败：请检查参数、凭据JSON和报告输出目录（不打印原始异常/秘密）',
    );
    return 1;
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
)
  main(process.argv.slice(2)).then((code) => {
    process.exitCode = code;
  });
