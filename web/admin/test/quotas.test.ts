import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n, type Member } from '@auriga/shared';
import ElementPlus from 'element-plus';
import QuotasView from '../src/views/QuotasView.vue';
import { apiClient } from '../src/api';
import { type Quota } from '../src/admin/quotaApi';
import {
  fromQuota,
  toQuota,
  validateDraft,
  clearedFields,
  preserveUnknownQuota,
  unsafeQuotaFields,
} from '../src/admin/quotaForm';

const memberId = '01900000-0000-7000-8000-000000000001';
const groupId = '01900000-0000-7000-8000-000000000002';
const timestamp = '2026-10-24T00:00:00Z';
let permissions: string[];
let defaults: Quota;
let override: Quota | undefined;
let effective: Quota;
let exists: boolean;
let calls: { path: string; method: string; init: RequestInit }[];
let intercept:
  | ((path: string, init: RequestInit) => Promise<Response> | undefined)
  | undefined;
let wrapper: VueWrapper | undefined;
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const missing = () =>
  json({ status: 404, code: 'org.not_found', title: 'Not found' }, 404);
const member: Member = {
  id: memberId,
  displayName: 'Alice',
  email: 'alice@example.com',
  memberType: 'member',
  status: 'active',
  groupIds: [groupId],
  roleKeys: ['member'],
  createdAt: timestamp,
};
const writes = () => calls.filter((call) => call.method !== 'GET');
beforeEach(() => {
  permissions = [
    'org.quotas.read',
    'org.quotas.write',
    'org.groups.read',
    'org.members.read',
  ];
  defaults = {
    instance: { cpuMillis: 200, maxInstances: 2, alwaysOnAllowed: true },
  };
  override = undefined;
  effective = {
    instance: { maxInstances: 17, alwaysOnAllowed: false },
  };
  exists = true;
  calls = [];
  intercept = undefined;
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=test-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), location.href);
      const path = url.pathname + url.search;
      const method = init.method || 'GET';
      calls.push({ path, method, init });
      const custom = intercept?.(path, init);
      if (custom) return custom;
      if (path === '/api/org/v1/me')
        return json({
          member,
          permissions,
          tenant: {
            id: groupId,
            slug: 'example',
            displayName: 'Example',
            locale: 'zh-CN',
            hosts: [],
            createdAt: timestamp,
          },
        });
      if (path.startsWith('/api/org/v1/admin/members?'))
        return json({ items: [member] });
      if (path === '/api/org/v1/admin/groups')
        return json({
          items: [
            {
              id: groupId,
              name: 'Example group',
              source: 'manual',
              memberCount: 1,
              createdAt: timestamp,
            },
          ],
        });
      if (path === `/api/org/v1/admin/members/${memberId}`)
        return exists ? json(member) : missing();
      if (path === `/api/org/v1/admin/groups/${groupId}`)
        return exists
          ? json({
              id: groupId,
              name: 'Example group',
              source: 'manual',
              memberCount: 1,
              createdAt: timestamp,
            })
          : missing();
      if (path.endsWith('/quota/effective'))
        return exists ? json(effective) : missing();
      if (path.endsWith('/quotas/default')) {
        if (method === 'PUT') defaults = JSON.parse(String(init.body));
        return json(defaults);
      }
      if (path.endsWith('/quota')) {
        if (method === 'PUT') {
          override = JSON.parse(String(init.body));
          return json(override);
        }
        if (method === 'DELETE') {
          override = undefined;
          effective = { instance: { maxInstances: 42 } };
          return new Response(null, { status: 204 });
        }
        return override === undefined ? missing() : json(override);
      }
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
async function render() {
  wrapper = mount(QuotasView, {
    attachTo: document.body,
    global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
  });
  await flushPromises();
}
async function click(label: string) {
  const button = wrapper!
    .findAll('button')
    .find((item) => item.text() === label);
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
async function choose(label: string, value: string) {
  const select = wrapper!
    .findAllComponents({ name: 'ElSelect' })
    .find((item) => item.props('ariaLabel') === label);
  expect(select, label).toBeDefined();
  select!.vm.$emit('update:modelValue', value);
  await flushPromises();
}
async function confirm() {
  await wrapper!.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
}
async function submit() {
  await wrapper!.findAll('form').at(-1)!.trigger('submit');
  await flushPromises();
}
async function target(scope: 'member' | 'group') {
  await choose('配置层', scope);
  await choose('选择目标', scope === 'member' ? memberId : groupId);
}

describe('quota form semantics', () => {
  it('retains unknown fields and unedited known values while clearing only explicitly unset fields', () => {
    const original = {
      instance: { cpuMillis: 200, futureCapacity: { count: 7 } },
      futureSection: { enabled: true },
    };
    const draft = fromQuota(original);
    draft.cpuMillis.mode = 'unset';
    expect(preserveUnknownQuota(original, toQuota(draft))).toEqual({
      instance: { futureCapacity: { count: 7 } },
      futureSection: { enabled: true },
    });
    expect(
      unsafeQuotaFields({ instance: { futureLimit: 9007199254740992 } }),
    ).toEqual(['instance.futureLimit']);
  });
  it('round-trips omitted, null, zero and false independently', () => {
    const quota: Quota = {
      instance: {
        cpuMillis: 100,
        memoryMb: 256,
        diskGb: 1,
        maxInstances: 0,
        alwaysOnAllowed: false,
      },
    };
    expect(toQuota(fromQuota(quota))).toEqual(quota);
    expect(toQuota(fromQuota({}))).toEqual({});
  });
  it.each(['cpuMillis', 'memoryMb', 'diskGb'] as const)(
    'does not accept unlimited for %s',
    (key) => {
      const draft = fromQuota({});
      draft[key].mode = 'unlimited';
      expect(validateDraft(draft)).toBe(key);
    },
  );
  it.each(['-1', '1.5', '1e3', '9007199254740992', ''] as const)(
    'rejects invalid or imprecise integer %s',
    (value) => {
      const draft = fromQuota({});
      draft.maxInstances = { mode: 'value', value };
      expect(validateDraft(draft)).toBe('maxInstances');
    },
  );
  it('validates instance resource minima', () => {
    const draft = fromQuota({ instance: { cpuMillis: 99 } });
    expect(validateDraft(draft)).toBe('cpuMillis');
    draft.cpuMillis.mode = 'unset';
    draft.memoryMb = { mode: 'value', value: '255' };
    expect(validateDraft(draft)).toBe('memoryMb');
  });
  it('identifies every removed current-layer field for replacement confirmation', () => {
    expect(
      clearedFields(defaults, { instance: { maxInstances: null } }),
    ).toEqual(['cpuMillis', 'alwaysOnAllowed']);
  });
});
describe('quota page workflows', () => {
  it('keeps model budgets out of instance quota settings', async () => {
    await render();
    expect(wrapper!.text()).not.toContain('模型预算');
    expect(wrapper!.text()).not.toContain('token 上限');
    expect(wrapper!.find('[aria-label="模型预算币种"]').exists()).toBe(false);
  });
  it('keeps a candidate 403 visible after clearing selection data and disabling writes', async () => {
    intercept = (path) =>
      path.startsWith('/api/org/v1/admin/members?')
        ? Promise.resolve(
            json(
              { status: 403, code: 'common.forbidden', title: 'Denied' },
              403,
            ),
          )
        : undefined;
    await render();
    await choose('配置层', 'member');
    expect(wrapper!.text()).toContain('common.forbidden');
    expect(wrapper!.find('.details').exists()).toBe(false);
    expect(writes()).toHaveLength(0);
  });
  it('blocks writes for unsafe returned values and explains the precision restriction', async () => {
    defaults = { instance: { maxInstances: 9007199254740992 } };
    await render();
    expect(wrapper!.text()).toContain('超出可安全编辑的整数精度');
    await submit();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.find('[data-testid="confirm-action"]').exists()).toBe(
      false,
    );
  });
  it('preserves unknown fields through an actual PUT request', async () => {
    defaults = {
      instance: { cpuMillis: 200, futureCapacity: 7 },
      futureSection: { enabled: true },
    } as Quota;
    await render();
    await choose('CPU 配置层', 'unset');
    await submit();
    await confirm();
    expect(defaults).toEqual({
      instance: { futureCapacity: 7 },
      futureSection: { enabled: true },
    });
  });
  it('sets only the edited instance limit on an empty layer', async () => {
    defaults = {};
    await render();
    await choose('实例数量上限 配置层', 'value');
    await wrapper!.get('input[aria-label="实例数量上限"]').setValue('0');
    await submit();
    await confirm();
    expect(defaults).toEqual({ instance: { maxInstances: 0 } });
  });
  it('confirms full replacement including cleared fields and preserves null/zero/deny', async () => {
    await render();
    await choose('CPU 配置层', 'unset');
    await choose('实例数量上限 配置层', 'unlimited');
    await choose('允许常驻', 'deny');
    await submit();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('本次将清除：CPU');
    await confirm();
    expect(defaults).toEqual({
      instance: { maxInstances: null, alwaysOnAllowed: false },
    });
    expect(writes()[0].method).toBe('PUT');
    expect(new Headers(writes()[0].init.headers).get('X-Auriga-CSRF')).toBe(
      'test-csrf',
    );
    expect(wrapper!.text()).toContain('已重新读取配置');
    expect(wrapper!.text()).not.toContain('删除本层覆盖');
  });
  it('allows cancelling a replacement without writing', async () => {
    await render();
    await submit();
    await wrapper!
      .getComponent({ name: 'ActionConfirm' })
      .findAll('button')[0]
      .trigger('click');
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('exposes unlimited only for schema-nullable fields', async () => {
    await render();
    const cpu = wrapper!
      .findAllComponents({ name: 'ElSelect' })
      .find((item) => item.props('ariaLabel') === 'CPU 配置层')!;
    expect(
      cpu.props('options').map((item: { value: string }) => item.value),
    ).toEqual(['unset', 'value']);
  });
  it('shows absent member override only after revalidating target and uses official effective values', async () => {
    await render();
    await target('member');
    expect(wrapper!.text()).toContain('本层未配置');
    expect(wrapper!.text()).toContain('17');
    expect(wrapper!.text()).toContain('不限');
    expect(
      calls.filter(
        (call) => call.path === `/api/org/v1/admin/members/${memberId}`,
      ),
    ).toHaveLength(2);
    expect(calls.some((call) => call.path.endsWith('/quota/effective'))).toBe(
      true,
    );
    expect(wrapper!.text()).toContain('接口不提供具体来源组');
  });
  it('deletes a member override then rereads the effective quota instead of computing it locally', async () => {
    override = { instance: { maxInstances: 0 } };
    await render();
    await target('member');
    await click('删除本层覆盖');
    await confirm();
    expect(writes()[0].method).toBe('DELETE');
    expect(wrapper!.text()).toContain('本层未配置');
    expect(wrapper!.text()).toContain('42');
    expect(
      calls.filter((call) => call.path.endsWith('/quota/effective')),
    ).toHaveLength(2);
  });
  it('reads and saves a group override with an explicit zero', async () => {
    await render();
    await target('group');
    await choose('实例数量上限 配置层', 'value');
    await wrapper!.get('input[aria-label="实例数量上限"]').setValue('0');
    await submit();
    await confirm();
    expect(override).toEqual({ instance: { maxInstances: 0 } });
  });
  it('deletes a group override and reloads the unconfigured state', async () => {
    override = { instance: { maxInstances: 0 } };
    await render();
    await target('group');
    await click('删除本层覆盖');
    await confirm();
    expect(override).toBeUndefined();
    expect(wrapper!.text()).toContain('本层未配置');
  });
  it('does not turn a missing target into an unconfigured layer', async () => {
    await render();
    await choose('配置层', 'member');
    exists = false;
    await choose('选择目标', memberId);
    expect(wrapper!.text()).toContain('org.not_found');
    expect(wrapper!.text()).not.toContain('本层未配置');
    expect(wrapper!.find('.details').exists()).toBe(false);
  });
  it('does not swallow an unrelated common 404', async () => {
    intercept = (path) =>
      path.endsWith('/quota')
        ? Promise.resolve(
            json(
              {
                status: 404,
                code: 'common.not_found',
                title: 'Route not found',
              },
              404,
            ),
          )
        : undefined;
    await render();
    await target('group');
    expect(wrapper!.text()).toContain('common.not_found');
    expect(wrapper!.text()).not.toContain('本层未配置');
  });
  it('revalidates a target disappearing after quota 404', async () => {
    intercept = (path) => {
      if (path.endsWith('/quota')) {
        exists = false;
        return Promise.resolve(missing());
      }
    };
    await render();
    await target('member');
    expect(wrapper!.text()).toContain('org.not_found');
    expect(wrapper!.text()).not.toContain('本层未配置');
  });
  it('does not read group or member candidates with quota permission alone', async () => {
    permissions = ['org.quotas.read', 'org.quotas.write'];
    await render();
    await choose('配置层', 'group');
    expect(wrapper!.text()).toContain('需要相应的分组或成员查看权限');
    await choose('配置层', 'member');
    expect(
      calls.some(
        (call) =>
          call.path === '/api/org/v1/admin/groups' ||
          call.path.includes('/admin/members?'),
      ),
    ).toBe(false);
  });
  it('does not read quotas without quota-read permission', async () => {
    permissions = [];
    await render();
    expect(calls.map((call) => call.path)).toEqual(['/api/org/v1/me']);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('rechecks write permission and clears quota data after revocation', async () => {
    await render();
    await submit();
    permissions = [];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
    expect(wrapper!.find('.details').exists()).toBe(false);
  });
  it('handles server-side 403 by clearing loaded quotas and forbidding subsequent writes', async () => {
    intercept = (path, init) =>
      init.method === 'PUT'
        ? Promise.resolve(
            json(
              { status: 403, code: 'common.forbidden', title: 'Denied' },
              403,
            ),
          )
        : undefined;
    await render();
    await submit();
    await confirm();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.find('.details').exists()).toBe(false);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('does not replay a save when the following read fails', async () => {
    let written = false;
    intercept = (path, init) => {
      if (init.method === 'PUT') {
        written = true;
        return Promise.resolve(json({}));
      }
      if (written && path.endsWith('/quotas/default'))
        return Promise.resolve(
          json(
            { status: 503, code: 'common.unavailable', title: 'Unavailable' },
            503,
          ),
        );
    };
    await render();
    await submit();
    await confirm();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).toContain('不要重复写入');
    expect(wrapper!.text()).toContain('common.unavailable');
  });
  it('does not submit twice while a write response is pending', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path, init) =>
      init.method === 'PUT'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    await submit();
    await confirm();
    await confirm();
    expect(writes()).toHaveLength(1);
    resolve(json(defaults));
    await flushPromises();
  });
  it('ignores a stale default-quota response after selecting a new scope', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path.endsWith('/quotas/default')
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    await choose('配置层', 'member');
    await choose('选择目标', memberId);
    resolve(json({ instance: { cpuMillis: 999 } }));
    await flushPromises();
    expect(wrapper!.text()).toContain('成员有效配额');
    expect(wrapper!.find('input[aria-label="CPU"]').exists()).toBe(false);
  });
  it('paginates authorized member candidates with opaque cursors', async () => {
    intercept = (path) =>
      path === '/api/org/v1/admin/members?limit=50'
        ? Promise.resolve(
            json({ items: [member], nextCursor: 'opaque+/cursor' }),
          )
        : path.includes('cursor=opaque%2B%2Fcursor')
          ? Promise.resolve(
              json({ items: [{ ...member, displayName: 'Bob' }] }),
            )
          : undefined;
    await render();
    await choose('配置层', 'member');
    await click('下一页');
    expect(
      calls.some((call) => call.path.includes('cursor=opaque%2B%2Fcursor')),
    ).toBe(true);
    await click('上一页');
    expect(
      calls.filter(
        (call) => call.path === '/api/org/v1/admin/members?limit=50',
      ),
    ).toHaveLength(2);
  });
});
