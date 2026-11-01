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
  {
    method = 'GET',
    body,
    idempotencyKey,
    expected = [200],
    timeout = 15000,
    discardBody = false,
  } = {},
) {
  const base = httpUrl(adapter.config.relay.baseUrl);
  const url = new URL(target);
  const origin = httpUrl(adapter.config.admin?.url).origin;
  requireThat(
    base.origin === origin &&
      base.pathname === '/api/ai/v1/admin/relay/' &&
      url.origin === origin &&
      !url.username &&
      !url.password &&
      !url.hash &&
      /^\/api\/ai\/v1\/admin\/relay\/(providers|pools|virtual-keys|logs)(\/[A-Za-z0-9-]+(\/keys)?)?$/.test(
        url.pathname,
      ),
    '管理请求必须留在平台同源中转站入口',
  );
  adapter.state = state;
  if (!adapter.relayAdminPage) {
    const admin = adapter.config.admin;
    requireThat(
      admin?.url && admin.accountId && admin.username && admin.password,
      '中转站管理缺少正常管理员凭据',
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
    requireThat(teams.length === 1, '中转站管理要求唯一普通团队身份源');
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
      requireThat(result.status() === 200, '中转站管理员登录响应失败');
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
    requireThat(outcome.status === 'completed', '中转站管理前管理员登录未完成');
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
    requireThat(accountId === admin.accountId, '中转站管理管理员身份不匹配');
    await page.goto(origin + '/models/relay');
    adapter.relayAdminPage = page;
    adapter.relayAdminEvidence = { authentication: 'platform-admin-session' };
  }

  const response = await adapter.relayAdminPage.evaluate(
    async ({
      url,
      method,
      body,
      idempotencyKey,
      traceparent,
      timeout,
      discardBody,
      ipMode,
    }) => {
      let csrf;
      if (!['GET', 'HEAD'].includes(method)) {
        if (ipMode) {
          const bytes = globalThis.crypto.getRandomValues(new Uint8Array(32));
          csrf = btoa(String.fromCharCode(...bytes))
            .replaceAll('+', '-')
            .replaceAll('/', '_')
            .replaceAll('=', '');
          const registered = await globalThis.fetch(
            '/api/iam/v1/admin/session/csrf-tokens',
            {
              method: 'POST',
              credentials: 'same-origin',
              redirect: 'error',
              headers: {
                'Content-Type': 'application/json',
                'X-Auriga-CSRF-Bootstrap': 'admin-session-v1',
              },
              body: JSON.stringify({ token: csrf }),
            },
          );
          if (registered.status !== 200)
            return { status: registered.status, headers: {}, body: null };
        } else {
          csrf = document.cookie
            .split(';')
            .map((x) => x.trim())
            .find((x) => /^(?:__Host-)?auriga_csrf=/.test(x))
            ?.split('=')
            .slice(1)
            .join('=');
          csrf = csrf ? decodeURIComponent(csrf) : '';
        }
      }
      const result = await globalThis.fetch(url, {
        method,
        credentials: 'same-origin',
        redirect: 'error',
        signal: globalThis.AbortSignal.timeout(timeout),
        headers: {
          accept: 'application/json',
          traceparent,
          ...(csrf ? { 'X-Auriga-CSRF': csrf } : {}),
          ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}),
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (discardBody) await result.body?.cancel();
      return {
        status: result.status,
        headers: { 'x-trace-id': result.headers.get('x-trace-id') },
        body: discardBody || result.status === 204 ? null : await result.json(),
      };
    },
    {
      url: url.href,
      method,
      body,
      idempotencyKey,
      ipMode: adapter.access?.mode === 'ip',
      traceparent: traceHeader(state),
      timeout,
      discardBody,
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
