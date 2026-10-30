import { randomBytes, createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { requireThat, httpUrl, StepError } from './run.mjs';
import { nativeClient, diagnoseNativeSession } from './cloud-desk.native.mjs';
import { verifyPoolFailover } from './relay-failover.mjs';

export async function configureRelayProvider(
  client,
  settings,
  provider,
  key,
  model,
  cleanup,
) {
  requireThat(
    /^auriga-relay-[a-f0-9]{12}$/.test(provider),
    '模型验收提供方名称无效',
  );
  const url = httpUrl(settings.baseUrl);
  requireThat(url.pathname === '/ai/v1', '原生模型提供方须使用中转站/ai/v1');
  requireThat(
    Number.isSafeInteger(settings.contextWindow) &&
      settings.contextWindow > 0 &&
      Number.isSafeInteger(settings.maxTokens) &&
      settings.maxTokens > 0 &&
      settings.maxTokens < settings.contextWindow,
    '须显式声明管理员核准的模型上下文与输出上限',
  );
  const ref = 'AURIGA_RELAY_' + provider.slice(-12).toUpperCase();
  const view = async () => {
    const described = await client.rpc('settings/describe');
    requireThat(described.writable === true, 'DSH原生模型设置不可写');
    const namespace = described.namespaces.find(
      (row) => row.ns === 'llm-pi-ai',
    );
    requireThat(namespace, 'DSH未提供公开llm-pi-ai设置');
    return namespace;
  };
  const original = await view();
  requireThat(
    original.value?.providers?.[provider] === undefined,
    '只允许创建全新验收提供方',
  );
  await client.rpc('credentials/set', { ref, value: key });
  cleanup.push(() => client.rpc('credentials/unset', { ref }));
  await client.rpc('settings/mutate', {
    ns: original.ns,
    expectedRevision: original.revision,
    ops: [
      {
        op: 'set',
        path: ['providers', provider],
        value: {
          displayName: 'Auriga relay acceptance',
          api: 'openai-completions',
          compat: { supportsStore: false },
          apiKeyEnv: ref,
          baseURL: url.href,
          models: [
            {
              id: model,
              contextWindow: settings.contextWindow,
              maxTokens: settings.maxTokens,
            },
          ],
          retryPolicy: { mode: 'normal', maxRetries: 0 },
          ...(settings.hostHeader
            ? { headers: { Host: settings.hostHeader } }
            : {}),
        },
      },
    ],
  });
  cleanup.push(async () => {
    const current = await view();
    await client.rpc('settings/mutate', {
      ns: current.ns,
      expectedRevision: current.revision,
      ops: [{ op: 'unset', path: ['providers', provider] }],
    });
  });
}

export async function waitAssistant(
  client,
  sessionId,
  marker,
  timeout = 90000,
) {
  const deadline = Date.now() + timeout;
  do {
    const cut = await client.rpc('session/projections', {
      request: { sessionId },
    });
    requireThat(Number.isInteger(cut.asOfSeq), '原生会话投影无序号');
    const page = await client.rpc('session/page', {
      request: {
        address: { kind: 'session', sessionId },
        throughSeq: cut.asOfSeq,
        maxMessages: 40,
      },
    });
    for (const row of page.records ?? []) {
      const event = row.event;
      if (event?.type !== 'assistant/message' || event.data?.interrupted)
        continue;
      const text = (event.data?.message?.content ?? [])
        .filter((part) => part.type === 'text')
        .map((part) => part.text)
        .join('');
      const usage = event.data?.usage;
      if (
        text.includes(marker) &&
        usage &&
        usage.inputTokens + usage.outputTokens > 0
      )
        return {
          markerObserved: true,
          inputTokens: usage.inputTokens,
          outputTokens: usage.outputTokens,
        };
    }
    requireThat(
      !(page.records ?? []).some((row) => row.event?.type === 'turn/end'),
      'DSH原生对话已结束，但缺少当前标记的assistant回复与用量',
    );
    await delay(250);
  } while (Date.now() < deadline);
  throw new StepError('DSH原生会话未返回当前标记的真实assistant回复与用量');
}

function pageRequest(page) {
  const entry = page.url();
  return async (path, body) => {
    const url = new URL(path.replace(/^\//, ''), entry);
    const result = await page.evaluate(
      async ({ url, body }) => {
        const response = await globalThis.fetch(url, {
          method: body === undefined ? 'GET' : 'POST',
          credentials: 'same-origin',
          redirect: 'error',
          signal: globalThis.AbortSignal.timeout(15000),
          headers:
            body === undefined ? {} : { 'content-type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
        return { status: response.status, value: await response.json() };
      },
      { url: url.href, body },
    );
    requireThat(
      result.status === 200,
      `原生模型会话RPC失败：HTTP ${result.status}`,
    );
    return result.value;
  };
}

async function spendForKey(api, key) {
  const hash = createHash('sha256').update(key).digest('hex');
  const deadline = Date.now() + 15000;
  do {
    const rows = await api('spend/logs?api_key=' + hash + '&summarize=false');
    requireThat(Array.isArray(rows), 'LiteLLM用量日志不是数组');
    const row = rows.find(
      (row) => row.api_key === hash && row.total_tokens > 0,
    );
    if (row) {
      requireThat(
        /^[A-Za-z0-9._:-]{1,128}$/.test(row.request_id ?? ''),
        'LiteLLM用量缺少合法请求标识',
      );
      return {
        totalTokens: row.total_tokens,
        requestId: row.request_id,
        ...(/^[A-Za-z0-9_-]{1,128}$/.test(row.model_id ?? '')
          ? { deploymentId: row.model_id }
          : {}),
      };
    }
    await delay(500);
  } while (Date.now() < deadline);
  throw new StepError('LiteLLM未查到该成员测试Key的非零token用量');
}

export async function nativeModels(adapter, state, item) {
  const config = adapter.config.modelNative;
  requireThat(
    adapter.config.relay.upstreamMode === 'live',
    '原生模型验收只用于显式启用的真实中转站',
  );
  requireThat(
    config.pool && config.cloud && config.desktop,
    '缺少原生云桌模型配置或池名称',
  );
  const members = {
    cloud: adapter.member.accountId,
    desktop: adapter.desktopEvidence.accountId,
  };
  const tenant = adapter.access.tenantId;
  requireThat(
    /^[a-f0-9-]{36}$/.test(tenant) &&
      Object.values(members).every((id) => /^[a-f0-9-]{36}$/.test(id)),
    '原生模型验收缺少已登录成员身份',
  );
  const api = (path, options = {}) =>
    adapter.relayAdminRequest(
      new URL(path, adapter.config.relay.baseUrl),
      state,
      { key: adapter.config.relay.adminKey, ...options },
    );
  const publicUrl = httpUrl(adapter.config.relay.publicBaseUrl).href.replace(
    /\/$/,
    '',
  );
  requireThat(
    httpUrl(config.desktop.baseUrl).href.replace(/\/$/, '') === publicUrl,
    '桌面原生模型须使用当前平台的公开中转站',
  );
  const cloudUrl = httpUrl(config.cloud.baseUrl);
  if (cloudUrl.href.replace(/\/$/, '') !== publicUrl) {
    const portal = adapter.access.portalOrigin;
    const authority =
      portal.host + ([80, 443].includes(portal.port) ? '' : ':' + portal.port);
    requireThat(
      adapter.access.mode === 'ip' &&
        cloudUrl.protocol === 'http:' &&
        cloudUrl.hostname === adapter.access.instanceControl.dnsAlias &&
        Number(cloudUrl.port || 80) === portal.port &&
        config.cloud.hostHeader === authority,
      '云端内部模型入口须使用本部署可信edge别名与门户authority',
    );
  }
  item.nativeModels = { conversations: [] };
  const converse = async (side, model, phase = 'normal') => {
    const settings = config[side];
    const client = nativeClient(
      pageRequest(side === 'desktop' ? adapter.desktopPage : adapter.cloudPage),
    );
    const cleanup = [];
    let failure;
    let sessionId;
    let proof;
    try {
      const created = await api('key/generate', {
        method: 'POST',
        body: {
          user_id: tenant + ':' + members[side],
          models: [model],
          duration: '10m',
          max_budget: 0.05,
          key_alias: 'phase1-native-' + randomBytes(8).toString('hex'),
        },
      });
      requireThat(
        typeof created.key === 'string' && created.key.startsWith('sk-'),
        '未签发成员测试Key',
      );
      cleanup.push(() =>
        api('key/delete', { method: 'POST', body: { keys: [created.key] } }),
      );
      const bound = await api(
        'key/info?key=' + encodeURIComponent(created.key),
      );
      requireThat(
        bound.info?.user_id === tenant + ':' + members[side],
        '成员模型测试Key归属未绑定已登录成员',
      );
      const provider = 'auriga-relay-' + randomBytes(6).toString('hex');
      await configureRelayProvider(
        client,
        settings,
        provider,
        created.key,
        model,
        cleanup,
      );
      cleanup.push(() => client.cancel());
      sessionId = await client.createSession(settings.cwd, provider, model);
      const marker = 'PHASE1_NATIVE_' + randomBytes(12).toString('hex');
      await client.prompt(
        sessionId,
        'Reply with exactly ' + marker + '. Do not use tools.',
      );
      const response = await waitAssistant(client, sessionId, marker);
      const spend = await spendForKey(api, created.key);
      proof = spend;
      item.nativeModels.conversations.push({
        phase,
        side,
        model,
        memberId: members[side],
        sessionId,
        ...response,
        ...spend,
      });
    } catch (error) {
      failure = error;
      if (sessionId) {
        item.nativeModels.failedConversation = {
          side,
          model,
          sessionId,
          diagnostic: await diagnoseNativeSession(client, sessionId).catch(
            () => ({ available: false }),
          ),
        };
      }
    }
    for (const action of cleanup.reverse()) {
      try {
        await action();
      } catch {
        failure = new StepError('原生模型测试的会话、设置、凭据或Key清理失败');
      }
    }
    if (failure) throw failure;
    return proof;
  };
  for (const side of ['desktop', 'cloud']) {
    for (const model of [
      ...adapter.config.relay.models.map((row) => row.name),
      config.pool,
    ]) {
      await converse(side, model);
    }
  }
  item.nativeModels.failover = {};
  await verifyPoolFailover(
    config,
    adapter.config.relay.models,
    api,
    (side, model) => converse(side, model, 'one-deployment-blocked'),
    item.nativeModels.failover,
  );
  item.nativeModels.failoverVerified = item.nativeModels.failover.verified;
  return item.nativeModels;
}
