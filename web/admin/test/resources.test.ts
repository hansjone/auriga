import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createRouter, createMemoryHistory } from 'vue-router';
import ElementPlus, { ElDialog, ElDrawer } from 'element-plus';
import { createAurigaI18n, type OrgPaths } from '@auriga/shared';
import { apiClient } from '../src/api';
import {
  resourceApi,
  type ResourcePackage,
  type ResourceVersion,
} from '../src/admin/resourceApi';
import { navigation } from '../src/admin/navigation';
import ResourcesView from '../src/views/ResourcesView.vue';
import WorkflowUnavailableView from '../src/views/WorkflowUnavailableView.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import { router as realRouter } from '../src/router';

const base = '/api/resource/v1/packages';
const packageId = '01900000-0000-7000-8000-000000000001';
const accountId = '01900000-0000-7000-8000-000000000002';
const versionId = '01900000-0000-7000-8000-000000000003';
const groupId = '01900000-0000-7000-8000-000000000004';
const time = '2026-10-24T00:00:00Z';
const read = 'resource.packages.read';
const upload = 'resource.packages.upload';
type Membership =
  OrgPaths['/me']['get']['responses']['200']['content']['application/json'];
let permissions: string[];
let item: ResourcePackage;
let fixed: ResourceVersion;
let etag: string | undefined;
let calls: { path: string; method: string; init: RequestInit }[];
let wrapper: VueWrapper | undefined;
let intercept:
  | ((
      path: string,
      init: RequestInit,
    ) => Promise<Response> | Response | undefined)
  | undefined;
const json = (body: unknown, status = 200, tag?: string) =>
  new Response(JSON.stringify(body), {
    status,
    headers: {
      'Content-Type': 'application/json',
      ...(tag === undefined ? {} : { ETag: tag }),
    },
  });
const problem = (status: number, code: string) =>
  json({ status, code, title: 'Error' }, status);
const writes = () => calls.filter((c) => ['POST', 'PATCH'].includes(c.method));
const membership = (): Membership => ({
  member: {
    id: accountId,
    displayName: 'Alice',
    memberType: 'member',
    status: 'active',
    groupIds: [groupId],
    roleKeys: [],
    createdAt: time,
  },
  tenant: {
    id: packageId,
    slug: 'example',
    displayName: 'Example',
    locale: 'en',
    hosts: ['example.com'],
    createdAt: time,
  },
  permissions: [...permissions],
});
beforeEach(() => {
  calls = [];
  permissions = [read, upload];
  etag = '"server-original"';
  intercept = undefined;
  item = {
    id: packageId,
    tenantId: packageId,
    name: 'Document navigation',
    kind: 'plugin',
    ownerId: accountId,
    level: 'l3',
    visibility: 'tenant',
    groupIds: [],
    revision: 1,
    createdAt: time,
    enabled: true,
    defaultVersionId: null,
  };
  fixed = {
    id: versionId,
    packageId,
    version: '1.2.0',
    digest: `sha256:${'a'.repeat(64)}`,
    sizeBytes: 128,
    mediaType: 'application/gzip',
    state: 'pending_review',
    revision: 2,
    createdAt: time,
  };
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=example-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), location.href);
      const path = url.pathname + url.search;
      const method = init.method ?? 'GET';
      calls.push({ path, method, init });
      const custom = intercept?.(path, init);
      if (custom) return custom;
      if (path === '/api/org/v1/me') return json(membership());
      if (path === '/api/org/v1/admin/groups')
        return json({
          items: [
            {
              id: groupId,
              name: 'Engineering',
              memberCount: 1,
              source: 'manual',
              createdAt: time,
            },
          ],
        });
      if (path.startsWith(`${base}?`)) return json({ items: [item] });
      if (path === base && method === 'POST') {
        item = { ...item, ...JSON.parse(String(init.body)) };
        return json(item, 201, '"created"');
      }
      if (path === `${base}/${packageId}` && method === 'PATCH') {
        item = { ...item, ...JSON.parse(String(init.body)), revision: 2 };
        return json(item, 200, '"updated"');
      }
      if (path === `${base}/${packageId}`) return json(item, 200, etag);
      if (path.startsWith(`${base}/${packageId}/versions?`))
        return json({ items: [fixed] });
      if (path === `${base}/${packageId}/versions/${versionId}`)
        return json(fixed, 200, '"version"');
      throw new Error(`Unexpected ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});
async function page(locale: 'zh-CN' | 'en' = 'zh-CN') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/resources', component: ResourcesView },
      { path: '/:pathMatch(.*)*', component: { template: '<div/>' } },
    ],
  });
  await router.push('/resources');
  wrapper = mount(ResourcesView, {
    attachTo: document.body,
    global: {
      plugins: [router, createAurigaI18n(locale), ElementPlus],
      stubs: { teleport: true },
    },
  });
  await flushPromises();
  return wrapper;
}
async function click(
  label: string,
  root: Pick<VueWrapper, 'findAll'> = wrapper!,
) {
  const button = root
    .findAll('button')
    .find((b) => b.text() === label && !b.attributes('disabled'));
  expect(button, `button ${label}`).toBeTruthy();
  await button!.trigger('click');
  await flushPromises();
}
async function openDetail() {
  await wrapper!.get('.resource-name').trigger('click');
  await flushPromises();
}
async function create(name = 'New package') {
  await wrapper!.get('[data-testid=new-package]').trigger('click');
  await flushPromises();
  await wrapper!.get('input[data-testid=package-name]').setValue(name);
  await wrapper!.get('[data-testid=save-package]').trigger('click');
  await flushPromises();
}
async function confirm() {
  await wrapper!
    .getComponent(ActionConfirm)
    .get('[data-testid=confirm-action]')
    .trigger('click');
  await flushPromises();
}
async function edit(name = 'Renamed package') {
  await openDetail();
  await wrapper!.get('[data-testid=edit-package]').trigger('click');
  await flushPromises();
  await wrapper!.get('input[data-testid=package-name]').setValue(name);
  await wrapper!.get('[data-testid=save-package]').trigger('click');
  await flushPromises();
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('real resource reads and fixed versions', () => {
  it('uses four production GETs with list, drawer and a fixed version; never guesses download or review endpoints', async () => {
    await page();
    expect(wrapper!.text()).toContain(item.name);
    await openDetail();
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(true);
    await wrapper!.get('.resource-version-list button').trigger('click');
    await flushPromises();
    expect(wrapper!.get('[data-testid=version-detail]').text()).toContain(
      fixed.digest,
    );
    expect(wrapper!.get('[data-testid=version-detail] dt').text()).toBe(
      '资源版本',
    );
    expect(wrapper!.text()).toContain('待审核');
    expect(wrapper!.text()).toContain('未指定默认版本');
    expect(
      calls.some(
        (c) => c.path === `${base}/${packageId}/versions/${versionId}`,
      ),
    ).toBe(true);
    expect(writes()).toHaveLength(0);
    expect(calls.filter((c) => c.path.startsWith(base))).toHaveLength(4);
  });
  it('searches only the current page without sending unsupported filters', async () => {
    await page();
    await wrapper!.get('.resource-toolbar input').setValue('no match');
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
    expect(wrapper!.text()).toContain('筛选仅作用于当前页');
    expect(calls.filter((c) => c.path.startsWith(`${base}?`))).toHaveLength(1);
    expect(calls.find((c) => c.path.startsWith(`${base}?`))!.path).toBe(
      `${base}?limit=50`,
    );
  });
  it('passes opaque package and version cursors without parsing them', async () => {
    const cursor = 'opaque+/=next';
    intercept = (path) =>
      path === `${base}?limit=50`
        ? json({ items: [item], nextCursor: cursor })
        : path === `${base}/${packageId}/versions?limit=50`
          ? json({ items: [fixed], nextCursor: cursor })
          : undefined;
    await page();
    await click('下一页');
    expect(
      calls.some(
        (c) => c.path === `${base}?limit=50&cursor=opaque%2B%2F%3Dnext`,
      ),
    ).toBe(true);
    await openDetail();
    await click('下一页', wrapper!.getComponent(ElDrawer));
    expect(
      calls.some(
        (c) =>
          c.path ===
          `${base}/${packageId}/versions?limit=50&cursor=opaque%2B%2F%3Dnext`,
      ),
    ).toBe(true);
  });
  it('shows empty versions without claiming upload or publication', async () => {
    intercept = (path) =>
      path.includes('/versions?') ? json({ items: [] }) : undefined;
    await page();
    await openDetail();
    expect(wrapper!.text()).toContain('暂无可读取的版本');
    expect(wrapper!.findAll('.resource-version-list button')).toHaveLength(0);
  });
  it('shows an empty catalog honestly', async () => {
    intercept = (path) =>
      path.startsWith(`${base}?`) ? json({ items: [] }) : undefined;
    await page();
    expect(wrapper!.text()).toContain('暂无数据');
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
  });
  it('treats ACL and tenant 404 as unavailable details without exposing the old object', async () => {
    intercept = (path) =>
      path === `${base}/${packageId}`
        ? problem(404, 'common.not_found')
        : undefined;
    await page();
    await openDetail();
    expect(wrapper!.text()).toContain('资源不存在或当前账号不可见');
    expect(wrapper!.find('[data-testid=edit-package]').exists()).toBe(false);
  });
  it('allows recovery after an initial membership read failure', async () => {
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? problem(503, 'common.unavailable')
        : undefined;
    await page();
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
    intercept = undefined;
    await click('刷新');
    expect(wrapper!.text()).toContain(item.name);
  });
  it('reports a disabled or unavailable service and can retry only reads', async () => {
    intercept = (path) =>
      path.startsWith(`${base}?`)
        ? problem(503, 'common.unavailable')
        : undefined;
    await page();
    expect(wrapper!.text()).toContain('资源服务尚未启用');
    intercept = undefined;
    await click('重试');
    expect(wrapper!.text()).toContain(item.name);
    expect(writes()).toHaveLength(0);
  });
});

describe('metadata writes and permission boundaries', () => {
  it('creates only supported metadata using JSON, CSRF and one intent key after confirmation and a fresh me read', async () => {
    await page();
    await create();
    expect(writes()).toHaveLength(0);
    const count = calls.length;
    await confirm();
    expect(calls[count].path).toBe('/api/org/v1/me');
    const call = writes()[0];
    expect(call.path).toBe(base);
    expect(JSON.parse(String(call.init.body))).toEqual({
      name: 'New package',
      kind: 'plugin',
      visibility: 'private',
      groupIds: [],
    });
    const headers = new Headers(call.init.headers);
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(headers.get('X-Auriga-CSRF')).toBe('example-csrf');
    expect(headers.get('Idempotency-Key')).toMatch(/^[\da-f-]{36}$/);
    expect(wrapper!.text()).toContain('包元数据已保存');
    expect(calls.filter((c) => c.path.startsWith(`${base}?`)).length).toBe(2);
  });
  it('patches changed fields only with the exact GET ETag and JSON content type', async () => {
    await page();
    await edit();
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      'Document navigation → Renamed package',
    );
    await confirm();
    const call = writes()[0];
    expect(call.method).toBe('PATCH');
    expect(JSON.parse(String(call.init.body))).toEqual({
      name: 'Renamed package',
    });
    expect(new Headers(call.init.headers).get('If-Match')).toBe(
      '"server-original"',
    );
    expect(new Headers(call.init.headers).get('Content-Type')).toBe(
      'application/json',
    );
  });
  it.each([undefined, 'W/"1"', '1'])(
    'blocks editing when GET ETag is missing or invalid: %s',
    async (tag) => {
      etag = tag;
      await page();
      await openDetail();
      expect(
        wrapper!.get('[data-testid=edit-package]').attributes('disabled'),
      ).toBeDefined();
      expect(wrapper!.text()).toContain('有效强 ETag');
    },
  );
  it('retains draft on 412 and requires an explicit discard/reload and another confirmation', async () => {
    intercept = (path, init) =>
      path === `${base}/${packageId}` && init.method === 'PATCH'
        ? problem(412, 'common.precondition_failed')
        : undefined;
    await page();
    await edit();
    await confirm();
    expect(wrapper!.text()).toContain('草稿已保留');
    expect(
      (
        wrapper!.get('input[data-testid=package-name]')
          .element as HTMLInputElement
      ).value,
    ).toBe('Renamed package');
    expect(
      wrapper!.get('[data-testid=save-package]').attributes('disabled'),
    ).toBeDefined();
    await click('重新加载并放弃草稿');
    expect(writes()).toHaveLength(1);
    await confirm();
    expect(wrapper!.getComponent(ElDialog).props('modelValue')).toBe(false);
    expect(writes()).toHaveLength(1);
    intercept = undefined;
    etag = '"fresh"';
    await openDetail();
    await wrapper!.get('[data-testid=edit-package]').trigger('click');
    await flushPromises();
    await wrapper!
      .get('input[data-testid=package-name]')
      .setValue('Second draft');
    await wrapper!.get('[data-testid=save-package]').trigger('click');
    await confirm();
    expect(new Headers(writes()[1].init.headers).get('If-Match')).toBe(
      '"fresh"',
    );
  });
  it('does not replay successful POST when list readback fails', async () => {
    intercept = (path) =>
      path.startsWith(`${base}?`) && writes().length
        ? problem(503, 'common.unavailable')
        : undefined;
    await page();
    await create();
    await confirm();
    expect(wrapper!.text()).toContain('写请求已成功');
    expect(writes()).toHaveLength(1);
    intercept = undefined;
    await click('重试读取');
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).not.toContain('写请求已成功');
  });
  it('does not replay successful PATCH when detail readback fails', async () => {
    intercept = (path, init) =>
      path === `${base}/${packageId}` &&
      init.method === 'GET' &&
      writes().length
        ? problem(503, 'common.unavailable')
        : undefined;
    await page();
    await edit();
    await confirm();
    expect(wrapper!.text()).toContain('写请求已成功');
    intercept = undefined;
    await click('重试读取');
    expect(writes()).toHaveLength(1);
  });
  it('reuses the exact creation body and key after an uncertain network response', async () => {
    intercept = (path, init) =>
      path === base && init.method === 'POST'
        ? Promise.reject(new Error('network failed'))
        : undefined;
    await page();
    await create('Original intent');
    await confirm();
    expect(wrapper!.text()).toContain('创建结果尚未确认');
    expect(
      wrapper!.get('input[data-testid=package-name]').attributes('disabled'),
    ).toBeDefined();
    await wrapper!.get('[data-testid=save-package]').trigger('click');
    await flushPromises();
    intercept = undefined;
    await confirm();
    expect(writes()).toHaveLength(2);
    expect(writes()[1].init.body).toBe(writes()[0].init.body);
    expect(new Headers(writes()[1].init.headers).get('Idempotency-Key')).toBe(
      new Headers(writes()[0].init.headers).get('Idempotency-Key'),
    );
  });
  it('shows the step-up requirement without clearing read access or bypassing the write filter', async () => {
    intercept = (path, init) =>
      path === base && init.method === 'POST'
        ? problem(403, 'iam.step_up_required')
        : undefined;
    await page();
    await create();
    await confirm();
    expect(wrapper!.text()).toContain('需要近期二次验证');
    expect(wrapper!.text()).toContain(item.name);
    expect(wrapper!.find('[data-testid=new-package]').exists()).toBe(true);
    expect(writes()).toHaveLength(1);
  });
  it.each([{ perms: [read] }, { perms: [upload] }, { perms: [] }])(
    'keeps read and upload permissions separate: %j',
    async ({ perms }) => {
      permissions = perms;
      await page();
      expect(wrapper!.find('[data-testid=new-package]').exists()).toBe(false);
      expect(wrapper!.findAll('tbody tr')).toHaveLength(
        perms.includes(read) ? 1 : 0,
      );
      expect(calls.some((c) => c.path.startsWith(base))).toBe(
        perms.includes(read),
      );
    },
  );
  it('rechecks upload permission before sending a confirmed write', async () => {
    await page();
    await create();
    permissions = [read];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('没有此操作所需权限');
  });
  it('clears list, drawer and draft when read permission is revoked before confirmation', async () => {
    await page();
    await edit();
    permissions = [upload];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.getComponent(ElDialog).props('modelValue')).toBe(false);
  });
  it('clears sensitive reads on a server 403 without claiming success', async () => {
    intercept = (path, init) =>
      path === `${base}/${packageId}` && init.method === 'PATCH'
        ? problem(403, 'common.forbidden')
        : undefined;
    await page();
    await edit();
    await confirm();
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.text()).not.toContain('包元数据已保存');
  });
  it.each(['other-owner', 'l1'])(
    'does not offer metadata edits for %s',
    async (reason) => {
      if (reason === 'other-owner') item.ownerId = groupId;
      else item.level = 'l1';
      await page();
      await openDetail();
      expect(wrapper!.find('[data-testid=edit-package]').exists()).toBe(false);
    },
  );
  it('does not submit after cancellation', async () => {
    await page();
    await create();
    await click('取消', wrapper!.getComponent(ActionConfirm));
    expect(writes()).toHaveLength(0);
  });
  it('rejects invalid names instead of relying on truncating inputs', async () => {
    await page();
    await create('x'.repeat(215));
    expect(wrapper!.text()).toContain('1–214');
    expect(
      wrapper!.getComponent(ActionConfirm).props('message'),
    ).toBeUndefined();
    expect(writes()).toHaveLength(0);
  });
  it('never requests group candidates without the real group-read permission', async () => {
    await page();
    await create();
    expect(calls.some((c) => c.path === '/api/org/v1/admin/groups')).toBe(
      false,
    );
  });
  it('uses official group options and lets the backend enforce membership', async () => {
    permissions.push('org.groups.read');
    await page();
    await create();
    await click('取消', wrapper!.getComponent(ActionConfirm));
    await wrapper!.get('[data-testid=package-visibility]').setValue('groups');
    await flushPromises();
    await wrapper!.get('[data-testid=package-groups]').setValue([groupId]);
    await flushPromises();
    await wrapper!.get('[data-testid=save-package]').trigger('click');
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body)).groupIds).toEqual([
      groupId,
    ]);
    expect(calls.some((c) => c.path === '/api/org/v1/admin/groups')).toBe(true);
  });
  it('preserves an existing group scope on a name-only edit without group-read permission', async () => {
    item.visibility = 'groups';
    item.groupIds = [groupId];
    await page();
    await edit();
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      name: 'Renamed package',
    });
  });
});

describe('resource async boundaries and navigation', () => {
  it('keeps concurrent detail response ETags isolated', async () => {
    const first = deferred<Response>();
    intercept = (path) =>
      path === `${base}/${packageId}`
        ? first.promise
        : path === `${base}/${groupId}`
          ? json({ ...item, id: groupId }, 200, '"second"')
          : undefined;
    const pendingFirst = resourceApi.get(packageId);
    const second = await resourceApi.get(groupId);
    first.resolve(json(item, 200, '"first"'));
    expect(second.etag).toBe('"second"');
    expect((await pendingFirst).etag).toBe('"first"');
  });
  it('clears old results on HTTP 401 during a detail refresh', async () => {
    await page();
    await openDetail();
    intercept = (path) =>
      path === `${base}/${packageId}`
        ? problem(401, 'common.unauthenticated')
        : undefined;
    await click('刷新');
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
  });
  it('refuses to replay a creation intent after the 24-hour window', async () => {
    intercept = (path, init) =>
      path === base && init.method === 'POST'
        ? problem(503, 'common.unavailable')
        : undefined;
    await page();
    await create();
    await confirm();
    const later = Date.now() + 86_400_001;
    const spy = vi.spyOn(Date, 'now').mockReturnValue(later);
    try {
      await wrapper!.get('[data-testid=save-package]').trigger('click');
      await flushPromises();
      await confirm();
      expect(wrapper!.text()).toContain('超过 24 小时');
      expect(writes()).toHaveLength(1);
    } finally {
      spy.mockRestore();
    }
  });
  it('rejects a changed account or tenant before submitting an old intent', async () => {
    await page();
    await create();
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? json({
            ...membership(),
            member: { ...membership().member, id: groupId },
          })
        : undefined;
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.findAll('tbody tr')).toHaveLength(0);
  });
  it('ignores a late detail after closing the drawer', async () => {
    const wait = deferred<Response>();
    intercept = (path) =>
      path === `${base}/${packageId}` ? wait.promise : undefined;
    await page();
    await wrapper!.get('.resource-name').trigger('click');
    await flushPromises();
    await click('关闭详情', wrapper!.getComponent(ElDrawer));
    wait.resolve(json(item, 200, etag));
    await flushPromises();
    expect(wrapper!.getComponent(ElDrawer).props('modelValue')).toBe(false);
    expect(wrapper!.find('[data-testid=edit-package]').exists()).toBe(false);
  });
  it('ignores a late version after switching to another package detail', async () => {
    const wait = deferred<Response>();
    intercept = (path) =>
      path.endsWith(`/versions/${versionId}`) ? wait.promise : undefined;
    await page();
    await openDetail();
    await wrapper!.get('.resource-version-list button').trigger('click');
    await flushPromises();
    await click('关闭详情', wrapper!.getComponent(ElDrawer));
    await openDetail();
    wait.resolve(json(fixed));
    await flushPromises();
    expect(wrapper!.find('[data-testid=version-detail]').exists()).toBe(false);
  });
  it('prevents duplicate confirmation while a write is pending and ignores its result after unmount', async () => {
    const wait = deferred<Response>();
    intercept = (path, init) =>
      path === base && init.method === 'POST' ? wait.promise : undefined;
    await page();
    await create();
    await confirm();
    await wrapper!.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(writes()).toHaveLength(1);
    wrapper!.unmount();
    wrapper = undefined;
    const before = calls.length;
    wait.resolve(json(item, 201));
    await flushPromises();
    expect(calls).toHaveLength(before);
  });
  it('renders English resource labels and operation scopes', async () => {
    await page('en');
    expect(wrapper!.text()).toContain('Resource governance');
    await openDetail();
    await wrapper!.get('.resource-version-list button').trigger('click');
    await flushPromises();
    expect(wrapper!.get('[data-testid=version-detail] dt').text()).toBe(
      'Resource version',
    );
    await click('Close details', wrapper!.getComponent(ElDrawer));
    await create();
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      'does not upload, review, publish',
    );
  });
  it('keeps all eight primary groups and every existing deep link', () => {
    expect(navigation).toHaveLength(8);
    for (const path of [
      '/members',
      '/nodes',
      '/instances',
      '/groups',
      '/invitations',
      '/quotas',
      '/roles',
      '/providers',
      '/settings',
    ])
      expect(
        realRouter.resolve(path).matched.at(-1)?.components?.default,
      ).toBeTruthy();
    expect(
      realRouter.resolve('/resources').matched.at(-1)?.components?.default,
    ).toBe(ResourcesView);
  });
  it('preserves headquarters and devices as unavailable and links relay to LiteLLM', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/runtime/headquarters', component: WorkflowUnavailableView },
      ],
    });
    await router.push('/runtime/headquarters');
    wrapper = mount(WorkflowUnavailableView, {
      global: { plugins: [router, createAurigaI18n()] },
    });
    expect(wrapper.text()).toContain('独立系统 DSH');
    expect(wrapper.text()).toContain('尚未开放');
    expect(navigation.flatMap((n) => n.children.map((c) => c[0]))).toEqual(
      expect.arrayContaining(['/runtime/devices']),
    );
    expect(navigation.some((item) => item.path === '/models/relay')).toBe(true);
    expect(calls).toHaveLength(0);
  });
});
