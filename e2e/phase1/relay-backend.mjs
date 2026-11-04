import { randomUUID } from 'node:crypto';
import { requireThat, StepError, request, modelCallMode } from './run.mjs';

export async function allPages(api, path) {
  const items = [],
    seen = new Set();
  let cursor;
  do {
    const query = new URLSearchParams({ limit: '200' });
    if (cursor) query.set('cursor', cursor);
    const page = await api(path + (path.includes('?') ? '&' : '?') + query);
    requireThat(Array.isArray(page?.items), '中转站列表响应不合法');
    items.push(...page.items);
    cursor = page.nextCursor;
    requireThat(!cursor || !seen.has(cursor), '中转站分页游标重复');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return items;
}

export async function verifyRelayCatalog(relay, api) {
  const pools = await allPages(api, 'pools');
  const providers = await allPages(api, 'providers');
  requireThat(
    relay.models.every((model) =>
      pools.some(
        (pool) =>
          pool.id === model.name &&
          pool.enabled &&
          pool.routes.some(
            (route) =>
              route.model === model.upstreamModel &&
              providers.some(
                (provider) =>
                  provider.id === route.providerId &&
                  provider.hasCredential &&
                  provider.configuration.enabled &&
                  provider.configuration.baseUrl === model.apiBase,
              ),
          ),
      ),
    ),
    '中转站模型池与已授权上游不匹配',
  );
}

export async function verifyVirtualKeys(relay, state, adminRequest) {
  requireThat(
    relay?.baseUrl && relay.publicBaseUrl,
    '缺少中转站管理或公开入口',
  );
  const mode = modelCallMode(relay);
  const base = new URL(
    relay.baseUrl.endsWith('/') ? relay.baseUrl : relay.baseUrl + '/',
  );
  const publicBase = new URL(
    relay.publicBaseUrl.endsWith('/')
      ? relay.publicBaseUrl
      : relay.publicBaseUrl + '/',
  );
  requireThat(
    !publicBase.username &&
      !publicBase.password &&
      !publicBase.search &&
      !publicBase.hash &&
      ['https:', 'http:'].includes(publicBase.protocol),
    '公开入口不合法',
  );
  const api = (path, options) =>
    adminRequest(new URL(path, base), state, options);
  const keys = [];
  const issue = async (budget, rpm) => {
    const input = {
      name: 'phase1-' + randomUUID(),
      active: true,
      monthlyBudgetMicros: budget,
      requestsPerMinute: rpm,
      maxConcurrency: 4,
      maxInFlightTokens: 67108864,
      models: [relay.models[0].name],
    };
    const issued = await api('virtual-keys', {
      method: 'POST',
      body: input,
      idempotencyKey: randomUUID(),
    });
    requireThat(
      issued?.key?.id && typeof issued.secret === 'string',
      '中转站未签发虚拟 Key',
    );
    keys.push({ ...issued, input });
    return keys.at(-1);
  };
  const call = (key, expected = [200]) =>
    request(new URL('chat/completions', publicBase), state, {
      method: 'POST',
      key: key.secret,
      expected,
      body: {
        model: relay.models[0].name,
        messages: [{ role: 'user', content: 'Reply OK.' }],
        max_tokens: 8,
      },
      timeout: 170000,
    });
  let failure;
  try {
    const key = await issue(1000000, 1);
    const response = await call(key);
    requireThat(
      response.choices?.length && response.usage?.total_tokens > 0,
      '模型响应缺少真实用量',
    );
    const limited = await call(key, [429]);
    requireThat(limited.code === 'common.rate_limited', '限流拒绝原因不符');
    const exhausted = await issue(0, 60);
    const denied = await call(exhausted, [429]);
    requireThat(denied.code === 'ai.budget_exceeded', '预算拒绝原因不符');
    const usage = await allPages(
      api,
      'logs?keyId=' + encodeURIComponent(key.key.id),
    );
    requireThat(
      usage.some(
        (row) =>
          row.inputTokens + row.outputTokens > 0 && row.frozenMicros === 0,
      ),
      '外部 Key 用量不可查',
    );
    await api('virtual-keys/' + encodeURIComponent(key.key.id), {
      method: 'PUT',
      body: { ...key.input, active: false },
    });
    key.revoked = true;
    await call(key, [401, 403]);
  } catch (error) {
    failure = error;
  }
  let cleanupFailed = false;
  for (const key of keys) {
    if (key.revoked) continue;
    try {
      await api('virtual-keys/' + encodeURIComponent(key.key.id), {
        method: 'PUT',
        body: { ...key.input, active: false },
      });
    } catch {
      cleanupFailed = true;
    }
  }
  if (cleanupFailed)
    throw new StepError(
      '虚拟Key清理失败' +
        (failure instanceof StepError && /^HTTP状态/.test(failure.message)
          ? '；首失：' + failure.message
          : '') +
        '；请在管理台吊销 phase1 测试 Key',
    );
  if (failure) throw failure;
  return mode;
}
