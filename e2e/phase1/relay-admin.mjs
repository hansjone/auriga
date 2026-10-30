import {
  httpUrl,
  requireThat,
  nextTotp,
  observeTrace,
  traceHeader,
} from './run.mjs';

export async function relayAdminRequest(
  adapter,
  target,
  state,
  { method = 'GET', body, key, expected = [200], timeout = 15000 } = {},
) {
  const base = httpUrl(adapter.config.relay.baseUrl);
  const url = new URL(target);
  const paths = [
    'health/readiness',
    'model/info',
    'key/generate',
    'key/info',
    'key/delete',
    'spend/logs',
  ];
  requireThat(
    base.pathname === '/ai/' &&
      url.origin === base.origin &&
      !url.username &&
      !url.password &&
      !url.hash &&
      paths.some((path) => url.pathname === base.pathname + path),
    '管理请求必须留在已配置的LiteLLM管理入口',
  );
  adapter.state = state;
  if (!adapter.relayAdminPage) {
    const admin = adapter.config.admin;
    requireThat(
      admin?.url && admin.accountId && admin.username && admin.password,
      'LiteLLM交接缺少正常管理员凭据',
    );
    const origin = httpUrl(admin.url).origin;
    const context = await adapter.context();
    const page = await context.newPage();
    await page.goto(origin + '/login');
    const teams = await page.evaluate(async () => {
      const response = await globalThis.fetch('/api/iam/v1/login/providers', {
        credentials: 'same-origin',
      });
      const data = await response.json();
      return data.items
        .filter((row) => row.connectorType === 'team-account')
        .map((row) => row.id);
    });
    requireThat(teams.length === 1, 'LiteLLM交接要求唯一普通团队身份源');
    await page.locator(`[data-provider-id="${teams[0]}"]`).click();
    await page.locator('input[name="username"]').fill(admin.username);
    await page.locator('input[name="password"]').fill(admin.password);
    const submit = async () => {
      const response = page.waitForResponse(
        (r) =>
          r.request().method() === 'POST' &&
          /\/login\/transactions\/[^/]+\/advance$/.test(
            new URL(r.url()).pathname,
          ),
      );
      await page.locator('button[type="submit"]').click();
      const result = await response;
      requireThat(result.status() === 200, 'LiteLLM管理员登录响应失败');
      return adapter.retainedLogin(new URL(result.url()).pathname);
    };
    let outcome = await submit();
    if (
      outcome.status === 'pending' &&
      ['totp', 'platform_totp'].includes(outcome.step?.formId)
    ) {
      const fields = outcome.step.fields.filter(
        (field) => field.kind === 'otp',
      );
      requireThat(
        fields.length === 1 && ['otp', 'code'].includes(fields[0].name),
        '管理员验证码字段不合法',
      );
      await page
        .locator(`input[name="${fields[0].name}"]`)
        .fill(await nextTotp(admin));
      outcome = await submit();
    }
    requireThat(
      outcome.status === 'completed',
      'LiteLLM交接前管理员登录未完成',
    );
    await page.waitForURL(
      (current) => current.origin === origin && current.pathname !== '/login',
    );
    const accountId = await page.evaluate(
      async () =>
        (
          await (
            await globalThis.fetch('/api/iam/v1/me', {
              credentials: 'same-origin',
            })
          ).json()
        ).account?.id,
    );
    requireThat(accountId === admin.accountId, 'LiteLLM交接管理员身份不匹配');
    const handoff = page.waitForResponse(
      (r) =>
        r.url() === base.origin + '/ai/session' &&
        r.request().method() === 'POST',
    );
    const landed = page.waitForResponse(
      (r) =>
        r.url() === base.origin + '/ai/ui/' &&
        r.request().isNavigationRequest(),
    );
    const [gate, final] = await Promise.all([
      handoff,
      landed,
      page.locator('a[href="/ai/ui/"]').click(),
    ]);
    requireThat(
      (await gate.request().allHeaders()).origin === origin &&
        gate.status() === 303 &&
        final.status() === 200,
      'LiteLLM真实浏览器交接失败',
    );
    await page.waitForURL(base.origin + '/ai/ui/');
    // The UI may navigate to its own sign-in route after loading. A separate
    // same-origin JSON document keeps management fetches in a stable browser
    // page while retaining the genuine handoff cookie in this context.
    const apiPage = await context.newPage();
    const readiness = await apiPage.goto(base.href + 'health/readiness');
    requireThat(readiness.status() === 200, 'LiteLLM管理API浏览器入口未就绪');
    adapter.relayAdminPage = apiPage;
    adapter.relayAdminEvidence = {
      authentication: 'public-admin-browser-handoff',
      handoffStatus: 303,
      uiStatus: 200,
    };
  }
  const response = await adapter.relayAdminPage.evaluate(
    async ({ url, method, body, key, traceparent, timeout }) => {
      const result = await globalThis.fetch(url, {
        method,
        credentials: 'same-origin',
        redirect: 'error',
        signal: globalThis.AbortSignal.timeout(timeout),
        headers: {
          accept: 'application/json',
          traceparent,
          ...(key ? { authorization: 'Bearer ' + key } : {}),
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      return {
        status: result.status,
        headers: { 'x-trace-id': result.headers.get('x-trace-id') },
        body: result.status === 204 ? null : await result.json(),
      };
    },
    {
      url: url.href,
      method,
      body,
      key,
      traceparent: traceHeader(state),
      timeout,
    },
  );
  observeTrace(state, response.headers, response.status);
  adapter.relayAdminEvidence.requests ??= [];
  adapter.relayAdminEvidence.requests.push({
    method,
    path: url.pathname,
    status: response.status,
  });
  requireThat(
    expected.includes(response.status),
    `HTTP状态${response.status}，预期${expected.join('/')}`,
  );
  return response.body;
}
