import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import ElementPlus, { ElDialog, ElDrawer } from 'element-plus';
import { createAurigaI18n } from '@auriga/shared';
import { apiClient } from '../src/api';
import IdentityProvidersView from '../src/views/IdentityProvidersView.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import {
  configForm,
  configChanges,
  configError,
  displayDraft,
  providerBody,
} from '../src/admin/providersForm';
import type { Provider, ConnectorType } from '../src/admin/providersApi';
const platformFetch = globalThis.fetch;
const base = '/api/iam/v1/admin/providers';
const id = '01900000-0000-7000-8000-000000000001';
const timestamp = '2026-10-24T00:00:00Z';
const secret = 'example-new-secret-fixture';
const read = 'iam.providers.read';
const write = 'iam.providers.write';
let permissions: string[];
let provider: Provider;
let connector: ConnectorType;
let rows: Provider[];
let calls: { path: string; method: string; init: RequestInit }[];
let wrapper: VueWrapper | undefined;
let intercept:
  | ((
      path: string,
      init: RequestInit,
    ) => Response | Promise<Response> | undefined)
  | undefined;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const problem = (status: number, code: string) =>
  json({ status, code, title: 'Error', detail: secret }, status);
const writes = () =>
  calls.filter((call) => call.path.startsWith(base) && call.method !== 'GET');
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
beforeEach(() => {
  permissions = [read, write];
  calls = [];
  intercept = undefined;
  connector = {
    source: 'builtin',
    descriptor: {
      type: 'oidc',
      spiVersion: '1',
      displayName: { en: 'OIDC', 'zh-CN': 'OIDC' },
      steps: ['redirect'],
      multipleInstances: true,
      configSchema: {
        type: 'object',
        required: ['issuer', 'clientId', 'clientSecret'],
        additionalProperties: false,
        properties: {
          issuer: { type: 'string', format: 'uri', minLength: 1 },
          clientId: { type: 'string', minLength: 1 },
          clientSecret: { type: 'string', minLength: 1, 'x-secret': true },
          scopes: {
            type: 'array',
            minItems: 1,
            items: { type: 'string', minLength: 1 },
            default: ['openid'],
          },
          claimMapping: {
            type: 'object',
            properties: {
              subject: { type: 'string' },
              email: { type: 'string' },
            },
          },
        },
      } as unknown as ConnectorType['descriptor']['configSchema'],
    },
  };
  provider = {
    id,
    connectorType: 'oidc',
    builtin: false,
    enabled: true,
    display: {
      name: { en: 'Example SSO', 'zh-CN': '示例身份源', fr: 'Exemple' },
      order: 100,
      buttonText: { en: 'Sign in', fr: 'Connexion' },
    },
    config: {
      issuer: 'https://login.example.com',
      clientId: 'example-client',
      clientSecret: { 'x-secret-set': true },
      claimMapping: { subject: 'sub', email: 'email' },
    } as unknown as Provider['config'],
    admission: {
      mode: 'invite_only',
      mfa: 'new_device',
      trustExternalMfa: false,
      defaultMemberType: 'member',
      sessionTtlSeconds: 3600,
      idleTimeoutSeconds: 600,
      externalSessionCheckSeconds: 0,
    },
    callbackUrl: 'https://portal.example.com/api/iam/v1/login/callback/' + id,
    createdAt: timestamp,
    updatedAt: timestamp,
  };
  rows = [provider];
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=example-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = new URL(String(input), location.href).pathname;
      const method = init.method ?? 'GET';
      calls.push({ path, method, init });
      const result = intercept?.(path, init);
      if (result) return result;
      if (init.signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (path === '/api/org/v1/me') return json({ permissions });
      if (path === '/api/iam/v1/admin/connectors')
        return json({ items: [connector] });
      if (path === base && method === 'GET') return json({ items: rows });
      if (path === base && method === 'POST') {
        const body = JSON.parse(String(init.body));
        provider = {
          ...provider,
          ...body,
          config: {
            ...body.config,
            ...(body.config.clientSecret
              ? { clientSecret: { 'x-secret-set': true } }
              : {}),
          },
        };
        rows = [provider];
        return json(provider, 201);
      }
      if (path === `${base}/${id}` && method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        provider = {
          ...provider,
          ...body,
          config: {
            ...provider.config,
            ...body.config,
            ...(body.config?.clientSecret
              ? { clientSecret: { 'x-secret-set': true } }
              : {}),
          },
        };
        rows = [provider];
        return json(provider);
      }
      if (path === `${base}/${id}` && method === 'DELETE') {
        provider = { ...provider, enabled: false };
        rows = [provider];
        return new Response(null, { status: 204 });
      }
      if (path === `${base}/${id}`) return json(provider);
      throw new Error(`Unexpected ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
  document.head.innerHTML = '';
  document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
});
async function render(locale: 'zh-CN' | 'en' = 'zh-CN') {
  wrapper = mount(IdentityProvidersView, {
    attachTo: document.body,
    global: {
      plugins: [ElementPlus, createAurigaI18n(locale)],
      stubs: { teleport: true },
    },
  });
  await flushPromises();
}
async function click(testId: string) {
  await wrapper!.get(`[data-testid="${testId}"]`).trigger('click');
  await flushPromises();
}
async function input(testId: string, value: string | boolean) {
  await wrapper!.get(`[data-testid="${testId}"]`).setValue(value);
  await flushPromises();
}
async function detail() {
  await click('provider-row');
}
async function edit() {
  await detail();
  await click('edit-provider');
}
async function creation() {
  await click('new-provider');
  await input('display-nameEn', 'New SSO');
  await input('config-issuer', 'https://id.example.com');
  await input('config-clientId', 'new-client');
  await input('config-clientSecret', secret);
  await click('save-provider');
}
async function confirm() {
  await wrapper!
    .getComponent(ActionConfirm)
    .get('[data-testid=confirm-action]')
    .trigger('click');
  await flushPromises();
}

describe('provider schema and secret preservation', () => {
  it('keeps arrays containing embedded newlines read-only instead of changing their item boundaries', () => {
    const form = configForm(connector, { scopes: ['one\ntwo'] }, false);
    expect(
      form.fields.find((field) => field.path[0] === 'scopes')!.readonly,
    ).toBe(true);
    expect(configChanges(form, {})).toEqual({});
  });
  it('blocks scalar root schemas that cannot be represented by the provider config object', () => {
    connector.descriptor.configSchema = {
      type: 'string',
    } as unknown as ConnectorType['descriptor']['configSchema'];
    expect(configForm(connector, {}, true).createBlocked).toBe(true);
    expect(configForm(connector, {}, false).fields).toHaveLength(0);
  });
  it('omits all untouched settings including secret masks and schema defaults', () => {
    const form = configForm(connector, provider.config, false);
    expect(form.fields.find((f) => f.secret)).toMatchObject({
      configured: true,
      text: '',
      set: false,
    });
    expect(configChanges(form, provider.config)).toEqual({});
    expect(form.fields.find((f) => f.path[0] === 'scopes')!.set).toBe(false);
  });
  it('rebuilds a nested top-level object while retaining its known siblings', () => {
    const form = configForm(connector, provider.config, false);
    const field = form.fields.find(
      (f) => f.path.join('.') === 'claimMapping.subject',
    )!;
    field.set = true;
    field.text = 'user_id';
    expect(configChanges(form, provider.config)).toEqual({
      claimMapping: { subject: 'user_id', email: 'email' },
    });
  });
  it('makes nested objects with hidden unknown siblings read-only rather than forwarding masks', () => {
    provider.config = {
      claimMapping: { subject: 'sub', future: { 'x-secret-set': true } },
    } as unknown as Provider['config'];
    const form = configForm(connector, provider.config, false);
    expect(
      form.fields.find((f) => f.path.join('.') === 'claimMapping.subject')!
        .readonly,
    ).toBe(true);
    expect(form.unsupported).toContain('claimMapping.future');
    expect(configChanges(form, provider.config)).toEqual({});
  });
  it('blocks composed config creation but allows metadata-only edits without dropping opaque config', () => {
    connector.descriptor.configSchema = {
      oneOf: [{ type: 'object' }],
    } as unknown as ConnectorType['descriptor']['configSchema'];
    expect(configForm(connector, {}, true).createBlocked).toBe(true);
    const draft = displayDraft(provider);
    draft.nameEn = 'Changed';
    const body = providerBody(
      configForm(connector, { 'x-secret-set': true }, false),
      draft,
      true,
      'oidc',
      provider,
    );
    expect(body).not.toHaveProperty('config');
    expect(body).not.toHaveProperty('admission');
  });
  it('preserves localized labels and admission instead of silently replacing them', () => {
    const draft = displayDraft(provider);
    draft.nameEn = 'Changed';
    const body = providerBody(
      configForm(connector, provider.config, false),
      draft,
      true,
      'oidc',
      provider,
    );
    expect(body.display?.name.fr).toBe('Exemple');
    expect(body.display?.buttonText?.fr).toBe('Connexion');
    expect(body).not.toHaveProperty('admission');
  });
  it('validates required secret replacements and string arrays', () => {
    const form = configForm(connector, {}, true);
    form.fields
      .filter((f) => f.set)
      .forEach((f) => {
        f.text = f.path[0] === 'issuer' ? 'https://example.com' : 'value';
      });
    const secretField = form.fields.find((f) => f.secret)!;
    secretField.text = '';
    expect(configError(form)).toBe('clientSecret');
    secretField.text = 'value';
    const scopes = form.fields.find((f) => f.path[0] === 'scopes')!;
    scopes.set = true;
    scopes.text = 'openid\nprofile';
    expect(configError(form)).toBeUndefined();
    expect(configChanges(form, {})).toHaveProperty('scopes', [
      'openid',
      'profile',
    ]);
  });
  it('does not truncate unsafe numbers or enable unsupported object arrays', () => {
    connector.descriptor.configSchema = {
      type: 'object',
      properties: {
        n: { type: 'integer' },
        rules: { type: 'array', items: { type: 'object' } },
      },
    } as unknown as ConnectorType['descriptor']['configSchema'];
    const form = configForm(
      connector,
      { n: Number.MAX_SAFE_INTEGER + 1 },
      false,
    );
    expect(form.fields[0].readonly).toBe(true);
    expect(form.unsupported).toContain('rules');
    expect(configChanges(form, {})).toEqual({});
  });
  it('blocks prototype-sensitive paths', () => {
    connector.descriptor.configSchema = JSON.parse(
      '{"type":"object","required":["__proto__"],"properties":{"__proto__":{"type":"string"}}}',
    );
    const form = configForm(connector, {}, true);
    expect(form.createBlocked).toBe(true);
    expect(form.fields).toHaveLength(0);
    expect({}).not.toHaveProperty('polluted');
  });
});

describe('real provider management workflows', () => {
  it('keeps metadata editable if connector discovery fails without overwriting unknown config', async () => {
    intercept = (path) =>
      path === '/api/iam/v1/admin/connectors'
        ? problem(503, 'common.unavailable')
        : undefined;
    await render();
    await edit();
    await input('display-nameEn', 'Changed');
    await click('save-provider');
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).not.toHaveProperty(
      'config',
    );
  });
  it('clears all visible provider data on a server 401 during refresh', async () => {
    await render();
    await detail();
    intercept = (path) =>
      path === base ? problem(401, 'common.unauthenticated') : undefined;
    await click('refresh-providers');
    expect(wrapper!.findAll('[data-testid=provider-row]')).toHaveLength(0);
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
  });
  it('reads the real lists and drawer with masked configuration only', async () => {
    await render();
    await detail();
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(true);
    expect(wrapper!.text()).toContain('已设置（服务端标记）');
    expect(wrapper!.text()).not.toContain('x-secret-set');
    expect(calls.map((c) => c.path)).toEqual(
      expect.arrayContaining([
        '/api/iam/v1/admin/connectors',
        base,
        `${base}/${id}`,
      ]),
    );
    expect(writes()).toHaveLength(0);
  });
  it('creates with JSON, a fresh permission check, CSRF and the page AbortSignal; confirmation hides secrets', async () => {
    await render();
    await creation();
    expect(wrapper!.getComponent(ActionConfirm).props('message')).not.toContain(
      secret,
    );
    expect(writes()).toHaveLength(0);
    const count = calls.length;
    await confirm();
    expect(calls[count].path).toBe('/api/org/v1/me');
    const call = writes()[0];
    expect(call.method).toBe('POST');
    expect(new Headers(call.init.headers).get('Content-Type')).toBe(
      'application/json',
    );
    expect(new Headers(call.init.headers).get('X-Auriga-CSRF')).toBe(
      'example-csrf',
    );
    expect(call.init.signal).toBeDefined();
    expect(call.init.signal!.aborted).toBe(false);
    expect(JSON.parse(String(call.init.body))).toMatchObject({
      connectorType: 'oidc',
      display: { name: { en: 'New SSO' } },
      config: { clientSecret: secret },
    });
    expect(wrapper!.text()).toContain('身份源配置已保存');
    expect(wrapper!.text()).not.toContain(secret);
  });
  it('patches display without echoing untouched config or changing admission', async () => {
    await render();
    await edit();
    await input('display-nameEn', 'Updated');
    await click('save-provider');
    await confirm();
    const call = writes()[0];
    expect(new Headers(call.init.headers).get('Content-Type')).toBe(
      'application/merge-patch+json',
    );
    const body = JSON.parse(String(call.init.body));
    expect(body).not.toHaveProperty('config');
    expect(body).not.toHaveProperty('admission');
    expect(body.display.name.fr).toBe('Exemple');
    expect(call.init.signal).toBeDefined();
  });
  it('sends only explicitly replaced secret text, then clears it on server rejection without exposing detail', async () => {
    await render();
    await edit();
    await input('set-clientSecret', true);
    await input('config-clientSecret', secret);
    await click('save-provider');
    intercept = (path, init) =>
      path === `${base}/${id}` && init.method === 'PATCH'
        ? problem(400, 'iam.invalid_config')
        : undefined;
    expect(wrapper!.getComponent(ActionConfirm).props('message')).not.toContain(
      secret,
    );
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body)).config).toEqual({
      clientSecret: secret,
    });
    expect(
      (
        wrapper!.get('[data-testid=config-clientSecret]')
          .element as HTMLInputElement
      ).value,
    ).toBe('');
    expect(wrapper!.text()).not.toContain(secret);
    expect(wrapper!.text()).toContain('iam.invalid_config');
  });
  it('disables by DELETE while retaining the row and accurately describing the result', async () => {
    await render();
    await detail();
    await click('toggle-provider');
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      '不是物理删除',
    );
    await confirm();
    expect(writes()[0].method).toBe('DELETE');
    expect(writes()[0].init.signal).toBeDefined();
    expect(rows).toHaveLength(1);
    expect(wrapper!.text()).toContain('身份源已停用');
    expect(wrapper!.get('[data-testid=toggle-provider]').text()).toBe('启用');
  });
  it('enables a disabled provider through PATCH', async () => {
    provider.enabled = false;
    await render();
    await detail();
    await click('toggle-provider');
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      enabled: true,
    });
  });
  it.each([true, false])(
    'locks built-in and emergency instances with builtin=%s',
    async (flag) => {
      provider.builtin = flag;
      provider.connectorType = 'emergency';
      connector.descriptor.type = 'emergency';
      await render();
      await detail();
      expect(wrapper!.find('[data-testid=edit-provider]').exists()).toBe(false);
      expect(wrapper!.find('[data-testid=toggle-provider]').exists()).toBe(
        false,
      );
      expect(
        wrapper!.get('[data-testid=new-provider]').attributes('disabled'),
      ).toBeDefined();
    },
  );
  it('keeps an ordinary built-in team account fully read-only', async () => {
    provider.builtin = true;
    provider.connectorType = 'team-account';
    await render();
    await detail();
    expect(wrapper!.find('[data-testid=edit-provider]').exists()).toBe(false);
  });
  it('does not create a second single-instance connector', async () => {
    connector.descriptor.multipleInstances = false;
    await render();
    expect(
      wrapper!.get('[data-testid=new-provider]').attributes('disabled'),
    ).toBeDefined();
  });
  it.each([{ perms: [read] }, { perms: [write] }, { perms: [] }])(
    'separates read and write permissions: $perms',
    async ({ perms }) => {
      permissions = perms;
      await render();
      expect(wrapper!.find('[data-testid=new-provider]').exists()).toBe(false);
      expect(wrapper!.findAll('[data-testid=provider-row]')).toHaveLength(
        perms.includes(read) ? 1 : 0,
      );
    },
  );
  it('rechecks revoked write permission before the request and clears new secrets', async () => {
    await render();
    await creation();
    permissions = [read];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.getComponent(ElDialog).props('modelValue')).toBe(false);
    expect(wrapper!.text()).not.toContain(secret);
  });
  it('clears rows, drawer and draft after read revocation', async () => {
    await render();
    await edit();
    await input('set-clientSecret', true);
    await input('config-clientSecret', secret);
    await click('save-provider');
    permissions = [];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.findAll('[data-testid=provider-row]')).toHaveLength(0);
  });
  it('handles server 403 as access loss without fake success', async () => {
    await render();
    await edit();
    await input('display-nameEn', 'Changed');
    await click('save-provider');
    intercept = (path, init) =>
      init.method === 'PATCH' ? problem(403, 'common.forbidden') : undefined;
    await confirm();
    expect(wrapper!.findAll('[data-testid=provider-row]')).toHaveLength(0);
    expect(wrapper!.text()).not.toContain('身份源配置已保存');
  });
  it('does not replay a successful write when readback fails', async () => {
    await render();
    await edit();
    await input('display-nameEn', 'Changed');
    await click('save-provider');
    intercept = (path, init) =>
      writes().length && init.method === 'GET' && path === base
        ? problem(503, 'common.unavailable')
        : undefined;
    await confirm();
    expect(wrapper!.text()).toContain('写请求已成功');
    expect(writes()).toHaveLength(1);
    intercept = undefined;
    const button = wrapper!
      .getComponent(ElDrawer)
      .findAll('button')
      .find((b) => b.text() === '重试读取')!;
    await button.trigger('click');
    await flushPromises();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).not.toContain('写请求已成功');
  });
  it('cancels confirmation without writing and clears secret input', async () => {
    await render();
    await creation();
    wrapper!.getComponent(ActionConfirm).vm.$emit('cancel');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    expect(
      (
        wrapper!.get('[data-testid=config-clientSecret]')
          .element as HTMLInputElement
      ).value,
    ).toBe('');
  });
  it('ignores a late provider detail after closing', async () => {
    const wait = deferred<Response>();
    intercept = (path) => (path === `${base}/${id}` ? wait.promise : undefined);
    await render();
    await click('provider-row');
    wrapper!.getComponent(ElDrawer).vm.$emit('close');
    const button = wrapper!
      .getComponent(ElDrawer)
      .findAll('button')
      .find((b) => b.text() === '关闭详情')!;
    await button.trigger('click');
    await flushPromises();
    wait.resolve(json(provider));
    await flushPromises();
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.find('[data-testid=edit-provider]').exists()).toBe(false);
  });
  it('does not send a write after unmount during final authorization', async () => {
    await render();
    await creation();
    const wait = deferred<Response>();
    intercept = (path) =>
      path === '/api/org/v1/me' ? wait.promise : undefined;
    await confirm();
    wrapper!.unmount();
    wrapper = undefined;
    wait.resolve(json({ permissions }));
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('renders an honest empty state and supports read error recovery', async () => {
    rows = [];
    await render();
    expect(wrapper!.text()).toContain('暂无数据');
    intercept = (path) =>
      path === base ? problem(503, 'common.unavailable') : undefined;
    await click('refresh-providers');
    expect(wrapper!.text()).toContain('服务暂时不可用');
    intercept = undefined;
    rows = [provider];
    await click('refresh-providers');
    expect(wrapper!.findAll('[data-testid=provider-row]')).toHaveLength(1);
  });
  it('uses English workflow text', async () => {
    await render('en');
    expect(wrapper!.text()).toContain('Identity providers');
    await detail();
    expect(wrapper!.text()).toContain('Configured (server marker)');
    expect(wrapper!.get('[data-testid=toggle-provider]').text()).toContain(
      'Disable',
    );
  });
});

describe('IP CSRF setup and actual HTTP write delivery', () => {
  it.each([
    { method: 'POST', unmount: false },
    { method: 'PATCH', unmount: false },
    { method: 'DELETE', unmount: false },
    { method: 'POST', unmount: true },
    { method: 'PATCH', unmount: true },
    { method: 'DELETE', unmount: true },
  ])('$method with unmount=$unmount', async ({ method, unmount }) => {
    const delivered: {
      method: string | undefined;
      csrf: string | undefined;
    }[] = [];
    const server = createServer(async (request, response) => {
      delivered.push({
        method: request.method,
        csrf: request.headers['x-auriga-csrf'] as string,
      });
      for await (const chunk of request) void chunk;
      if (method === 'DELETE') {
        provider = { ...provider, enabled: false };
        rows = [provider];
        response.writeHead(204);
        response.end();
      } else {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify(provider));
      }
    });
    await new Promise<void>((resolve) =>
      server.listen(0, '127.0.0.1', resolve),
    );
    try {
      const port = (server.address() as AddressInfo).port;
      const fixtureFetch = globalThis.fetch;
      const attempts: (AbortSignal | null | undefined)[] = [];
      let nativeResult: Promise<Response> | undefined;
      vi.stubGlobal(
        'fetch',
        vi.fn((input: RequestInfo | URL, init: RequestInit = {}) => {
          const path = new URL(String(input), location.href).pathname;
          if (path.startsWith(base) && init.method === method) {
            attempts.push(init.signal);
            nativeResult = platformFetch(
              `http://127.0.0.1:${port}${path}`,
              init,
            );
            return nativeResult;
          }
          return fixtureFetch(input, init);
        }),
      );
      await render();
      if (method === 'POST') await creation();
      else if (method === 'PATCH') {
        await edit();
        await input('display-nameEn', 'Changed');
        await click('save-provider');
      } else {
        await detail();
        await click('toggle-provider');
      }
      document.head.innerHTML = '<meta name="auriga-access-mode" content="ip">';
      const csrf = deferred<Response>();
      let token = '';
      intercept = (path, init) => {
        if (path !== '/api/iam/v1/admin/session/csrf-tokens') return undefined;
        token = JSON.parse(String(init.body)).token;
        return csrf.promise;
      };
      await confirm();
      await confirm();
      expect(token).not.toBe('');
      expect(attempts).toHaveLength(0);
      expect(delivered).toHaveLength(0);
      if (unmount) {
        wrapper!.unmount();
        wrapper = undefined;
      }
      csrf.resolve(
        json({ token, expiresAt: new Date(Date.now() + 60000).toISOString() }),
      );
      await flushPromises();
      expect(attempts).toHaveLength(1);
      expect(attempts[0]!.aborted).toBe(unmount);
      if (unmount) {
        await expect(nativeResult).rejects.toMatchObject({
          name: 'AbortError',
        });
        expect(delivered).toHaveLength(0);
      } else {
        await nativeResult;
        await flushPromises();
        expect(delivered).toEqual([{ method, csrf: token }]);
        wrapper!.unmount();
        wrapper = undefined;
        expect(delivered).toHaveLength(1);
      }
      expect(
        calls.filter(
          (call) => call.path === '/api/iam/v1/admin/session/csrf-tokens',
        ),
      ).toHaveLength(1);
    } finally {
      await new Promise<void>((resolve, reject) =>
        server.close((e) => (e ? reject(e) : resolve())),
      );
    }
  });
});
