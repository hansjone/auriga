import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import ElementPlus from 'element-plus';
import { createAurigaI18n } from '@auriga/shared';
import UacSettings from '../src/admin/UacSettings.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import { apiClient } from '../src/api';
import { bindStepUp } from '../src/admin/stepUp';
const path = '/api/iam/v1/admin/providers/example/uac';
let wrapper: VueWrapper | undefined;
let permissions: string[];
let calls: { path: string; init: RequestInit }[];
let fail: number;
let enabled: boolean;
let draft: boolean;
let fallbackStatus: number;
let fallbackTag: string;
let failureCode: string | undefined;
let bundleType: string;
const json = (data: unknown, etag = '"provider-3"', status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json', ETag: etag },
  });
beforeEach(() => {
  permissions = [
    'iam.providers.read',
    'iam.providers.write',
    'audit.events.read',
    'ops.alerts.read',
  ];
  calls = [];
  fail = 0;
  enabled = true;
  draft = false;
  fallbackStatus = 200;
  fallbackTag = '"draft-1"';
  failureCode = undefined;
  bundleType = 'application/zip';
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=example-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), location.href).pathname;
      calls.push({ path: url, init });
      if (url === '/api/org/v1/me') return json({ permissions });
      if (init.method === 'PUT' && fail) {
        const status = fail;
        fail = 0;
        return json(
          {
            status,
            code:
              failureCode ??
              (status === 403
                ? 'iam.step_up_required'
                : 'common.precondition_failed'),
            title: 'Error',
          },
          '',
          status,
        );
      }
      if (url.endsWith('/diagnostics'))
        return json({ enabled: false, revision: 8 }, '"diagnostic-8"');
      if (url.endsWith('/diagnostic-bundles'))
        return new Response('example-zip', {
          headers: {
            'Content-Type': bundleType,
            'X-Auriga-Diagnostic-Partial': 'true',
          },
        });
      if (url === path.replace(/\/uac$/, ''))
        return json(
          { id: 'example', connectorType: 'uac', enabled: false, config: {} },
          fallbackTag,
          fallbackStatus,
        );
      if (url === path && init.method === 'GET' && draft)
        return json({ status: 404, code: 'common.not_found' }, '', 404);
      if (init.method === 'PUT') {
        enabled = JSON.parse(String(init.body)).enabled;
        draft = false;
      }
      return json({
        enabled,
        baseUrl: 'https://identity.example.com',
        systemCode: 'example',
      });
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.unstubAllGlobals();
});
async function render() {
  wrapper = mount(UacSettings, {
    props: { providerId: 'example' },
    global: {
      plugins: [ElementPlus, createAurigaI18n('zh-CN')],
      stubs: { teleport: true, transition: false },
    },
  });
  await flushPromises();
}
async function click(id: string) {
  await wrapper!.get(`[data-testid=${id}]`).trigger('click');
  await flushPromises();
}
async function confirm() {
  wrapper!.getComponent(ActionConfirm).vm.$emit('confirm');
  await flushPromises();
}
const puts = () => calls.filter((c) => c.init.method === 'PUT');
async function fillDraft() {
  await wrapper!
    .get('[data-testid=uac-base-url]')
    .setValue('https://identity.example.com');
  await wrapper!.get('[data-testid=uac-system-code]').setValue('example');
}
it('saves only the generated config fields with the actual whole-row ETag and CSRF', async () => {
  await render();
  await click('uac-save');
  await confirm();
  expect(puts()).toHaveLength(1);
  expect(JSON.parse(String(puts()[0].init.body))).toEqual({
    enabled: true,
    baseUrl: 'https://identity.example.com',
    systemCode: 'example',
  });
  expect(new Headers(puts()[0].init.headers).get('If-Match')).toBe(
    '"provider-3"',
  );
  expect(new Headers(puts()[0].init.headers).get('X-Auriga-CSRF')).toBe(
    'example-csrf',
  );
});
it('reloads on 412 without replaying the write', async () => {
  await render();
  fail = 412;
  await click('uac-save');
  await confirm();
  expect(wrapper!.text()).toContain('配置已被他人修改');
  expect(puts()).toHaveLength(1);
  expect(
    calls.filter((c) => c.path === path && c.init.method === 'GET'),
  ).toHaveLength(2);
});
it('initializes a 404 draft from the generic provider ETag and enables through the first PUT', async () => {
  draft = true;
  await render();
  expect(
    (wrapper!.get('[data-testid=uac-enabled]').element as HTMLInputElement)
      .checked,
  ).toBe(false);
  expect(
    (wrapper!.get('[data-testid=uac-base-url]').element as HTMLInputElement)
      .value,
  ).toBe('');
  expect(
    (wrapper!.get('[data-testid=uac-system-code]').element as HTMLInputElement)
      .value,
  ).toBe('');
  await wrapper!
    .get('[data-testid=uac-base-url]')
    .setValue('https://identity.example.com');
  await wrapper!.get('[data-testid=uac-system-code]').setValue('example');
  await wrapper!.get('[data-testid=uac-enabled]').setValue(true);
  await click('uac-save');
  await confirm();
  expect(puts()).toHaveLength(1);
  expect(new Headers(puts()[0].init.headers).get('If-Match')).toBe('"draft-1"');
  expect(JSON.parse(String(puts()[0].init.body))).toEqual({
    enabled: true,
    baseUrl: 'https://identity.example.com',
    systemCode: 'example',
  });
  expect(wrapper!.text()).toContain('UAC 配置已保存');
});
it.each([403, 404, 503])(
  'does not allow saving when generic draft read fails with %s',
  async (status) => {
    draft = true;
    fallbackStatus = status;
    await render();
    expect(wrapper!.find('[data-testid=uac-save]').exists()).toBe(false);
    expect(puts()).toHaveLength(0);
  },
);
it('rereads a 404 draft on 412 and uses the fresh whole-row ETag only after confirmation', async () => {
  draft = true;
  await render();
  await fillDraft();
  fail = 412;
  fallbackTag = '"draft-2"';
  await click('uac-save');
  await confirm();
  expect(wrapper!.text()).toContain('配置已被他人修改');
  expect(puts()).toHaveLength(1);
  expect(new Headers(puts()[0].init.headers).get('If-Match')).toBe('"draft-1"');
  expect(
    calls.filter(
      (c) => c.path === path.replace(/\/uac$/, '') && c.init.method === 'GET',
    ),
  ).toHaveLength(2);
  await fillDraft();
  await click('uac-save');
  await confirm();
  expect(puts()).toHaveLength(2);
  expect(new Headers(puts()[1].init.headers).get('If-Match')).toBe('"draft-2"');
});
it('blocks another save if the generic draft reread after 412 fails', async () => {
  draft = true;
  await render();
  await fillDraft();
  fail = 412;
  fallbackStatus = 503;
  await click('uac-save');
  await confirm();
  expect(wrapper!.text()).toContain('重新加载失败');
  const save = wrapper!.find('[data-testid=uac-save]');
  expect(!save.exists() || save.attributes('disabled') !== undefined).toBe(
    true,
  );
  expect(puts()).toHaveLength(1);
});
it('hides writes and download when permissions are absent', async () => {
  permissions = ['iam.providers.read'];
  await render();
  expect(wrapper!.find('[data-testid=uac-save]').exists()).toBe(false);
  expect(wrapper!.find('[data-testid=uac-download]').exists()).toBe(false);
});
it('continues the same config and ETag through step-up', async () => {
  const verify = vi.fn(async () => {});
  const unbind = bindStepUp(verify);
  try {
    await render();
    fail = 403;
    await click('uac-save');
    await confirm();
    expect(verify).toHaveBeenCalledOnce();
    expect(puts()).toHaveLength(2);
    expect(puts()[0].init.body).toBe(puts()[1].init.body);
    expect(new Headers(puts()[1].init.headers).get('If-Match')).toBe(
      '"provider-3"',
    );
  } finally {
    unbind();
  }
});
it('requires explicit confirmation before disabling sessions', async () => {
  await render();
  await wrapper!.get('[data-testid=uac-enabled]').setValue(false);
  await click('uac-save');
  expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
    '会撤销该身份源的已有会话',
  );
  expect(puts()).toHaveLength(0);
  await confirm();
  expect(puts()).toHaveLength(1);
});
it('uses an independent diagnostic ETag and 900 second TTL', async () => {
  await render();
  await click('uac-diagnostics-on');
  await confirm();
  expect(puts()[0].path).toBe(`${path}/diagnostics`);
  expect(new Headers(puts()[0].init.headers).get('If-Match')).toBe(
    '"diagnostic-8"',
  );
  expect(JSON.parse(String(puts()[0].init.body))).toEqual({
    enabled: true,
    ttlSeconds: 900,
  });
});
it('rereads diagnostic settings after 412 without replaying the write', async () => {
  await render();
  fail = 412;
  await click('uac-diagnostics-on');
  await confirm();
  expect(wrapper!.text()).toContain('诊断配置已被他人修改');
  expect(puts()).toHaveLength(1);
  expect(
    calls.filter(
      (c) => c.path === `${path}/diagnostics` && c.init.method === 'GET',
    ),
  ).toHaveLength(2);
});
it.each([401, 403])(
  'clears diagnostic access after an explicit %s denial',
  async (status) => {
    await render();
    fail = status;
    failureCode =
      status === 401 ? 'common.unauthenticated' : 'common.forbidden';
    await click('uac-diagnostics-on');
    await confirm();
    expect(puts()).toHaveLength(1);
    expect(wrapper!.find('[data-testid=uac-save]').exists()).toBe(false);
    expect(wrapper!.find('[data-testid=uac-download]').exists()).toBe(false);
  },
);
it('rejects a successful non-ZIP response without starting a download', async () => {
  const create = vi.fn();
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = create;
    },
  );
  bundleType = 'text/html';
  await render();
  await click('uac-download');
  await confirm();
  expect(create).not.toHaveBeenCalled();
  expect(wrapper!.text()).not.toContain('诊断包已下载');
  expect(wrapper!.find('[role=alert]').exists()).toBe(true);
});
it('downloads the ZIP and displays partial source warning', async () => {
  const create = vi.fn(() => 'blob:example');
  const revoke = vi.fn();
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = create;
      static revokeObjectURL = revoke;
    },
  );
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  await render();
  await click('uac-download');
  await confirm();
  expect(create).toHaveBeenCalledOnce();
  await new Promise((resolve) => globalThis.setTimeout(resolve, 1100));
  expect(revoke).toHaveBeenCalledOnce();
  expect(wrapper!.text()).toContain('诊断包缺少部分来源');
  expect(
    calls.find((c) => c.path.endsWith('/diagnostic-bundles'))?.init.body,
  ).toBe(JSON.stringify({ providerId: 'example', sinceSeconds: 7200 }));
});
