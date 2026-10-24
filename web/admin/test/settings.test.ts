import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ElementPlus from 'element-plus';
import SettingsView from '../src/views/SettingsView.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import { apiClient } from '../src/api';
import { policyEtag, type Policy, type Tenant } from '../src/admin/settingsApi';
import {
  workspaceDraft,
  workspaceValue,
  unsafeWorkspace,
  validWorkspaceDraft,
} from '../src/admin/settingsForm';

const teamPath = '/api/org/v1/tenant';
const policyPath = '/api/org/v1/admin/policies/workspace';
const timestamp = '2026-10-24T00:00:00Z';
let permissions: string[];
let tenant: Tenant;
let policy: Policy;
let etag: string | undefined;
let calls: { path: string; method: string; init: RequestInit }[];
let intercept:
  | ((path: string, init: RequestInit) => Promise<Response> | undefined)
  | undefined;
let wrapper: VueWrapper | undefined;
const json = (value: unknown, status = 200, tag?: string) =>
  new Response(JSON.stringify(value), {
    status,
    headers: {
      'Content-Type': 'application/json',
      ...(tag === undefined ? {} : { ETag: tag }),
    },
  });
const problem = (status: number, code = 'common.forbidden') =>
  json({ status, code, title: 'Error' }, status);
const writes = () => calls.filter((call) => call.method !== 'GET');
beforeEach(() => {
  permissions = ['org.tenant.write', 'org.policies.read', 'org.policies.write'];
  tenant = {
    id: '01900000-0000-7000-8000-000000000001',
    slug: 'example',
    displayName: 'Example',
    locale: 'zh-CN',
    hosts: ['admin.example.com'],
    createdAt: timestamp,
  };
  policy = {
    key: 'workspace',
    version: 1,
    value: { idleMinutes: 30, wakeOnSchedule: true } as Policy['value'],
    updatedAt: timestamp,
  };
  etag = '"1"';
  calls = [];
  intercept = undefined;
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=test-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = new URL(String(input), location.href).pathname;
      const method = init.method || 'GET';
      calls.push({ path, method, init });
      const custom = intercept?.(path, init);
      if (custom) return custom;
      if (path === '/api/org/v1/me')
        return json({ permissions, tenant, member: {} });
      if (path === teamPath) return json(tenant);
      if (path === '/api/org/v1/admin/tenant') {
        tenant = { ...tenant, ...JSON.parse(String(init.body)) };
        return json(tenant);
      }
      if (path === policyPath) {
        if (method === 'PUT') {
          policy = {
            ...policy,
            version: policy.version + 1,
            value: JSON.parse(String(init.body)).value,
          };
          etag = `"${policy.version}"`;
        }
        return json(policy, 200, etag);
      }
      throw new Error(`Unexpected ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = '';
  document.head.innerHTML = '';
  document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
});
async function render() {
  wrapper = mount(SettingsView, {
    attachTo: document.body,
    global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
  });
  await flushPromises();
  return wrapper;
}
async function input(testId: string, value: string) {
  await wrapper!.get(`[data-testid="${testId}"]`).setValue(value);
}
async function click(testId: string) {
  await wrapper!.get(`[data-testid="${testId}"]`).trigger('click');
  await flushPromises();
}
async function confirm() {
  wrapper!.getComponent(ActionConfirm).vm.$emit('confirm');
  await flushPromises();
}
describe('settings profile and policy', () => {
  it('reads tenant without a tenant read permission and independently withholds policies', async () => {
    permissions = ['org.tenant.write'];
    await render();
    expect(wrapper!.text()).toContain('admin.example.com');
    expect(calls.some((call) => call.path === policyPath)).toBe(false);
    await input('team-name', 'New team');
    await click('save-team');
    await confirm();
    expect(writes()).toHaveLength(1);
  });
  it('permits policy-only editors and leaves tenant read only', async () => {
    permissions = ['org.policies.read', 'org.policies.write'];
    await render();
    expect(
      wrapper!.get('[data-testid="save-team"]').attributes('disabled'),
    ).toBeDefined();
    await input('field-idleMinutes', '40');
    await click('save-workspace');
    await confirm();
    expect(writes()[0]?.method).toBe('PUT');
  });
  it('does not imply read from policy write and makes no unchanged writes', async () => {
    permissions = ['org.policies.write', 'org.tenant.write'];
    await render();
    await click('save-team');
    expect(
      wrapper!.getComponent(ActionConfirm).props('message'),
    ).toBeUndefined();
    expect(calls.some((call) => call.path === policyPath)).toBe(false);
    expect(writes()).toEqual([]);
  });
  it('sends only changed profile keys, merge patch content type and literal empty strings', async () => {
    await render();
    await input('team-name', '');
    await click('save-team');
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      'Example',
    );
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      '多个管理员',
    );
    await confirm();
    expect(JSON.parse(String(writes()[0]!.init.body))).toEqual({
      displayName: '',
    });
    expect(new Headers(writes()[0]!.init.headers).get('Content-Type')).toBe(
      'application/merge-patch+json',
    );
    expect(calls.filter((call) => call.path === '/api/org/v1/me')).toHaveLength(
      2,
    );
    expect(calls.filter((call) => call.path === teamPath)).toHaveLength(2);
  });
  it('cancels confirmation without writes', async () => {
    await render();
    await input('team-locale', 'en');
    await click('save-team');
    wrapper!.getComponent(ActionConfirm).vm.$emit('cancel');
    await flushPromises();
    await confirm();
    expect(writes()).toEqual([]);
  });
  it('preserves unknown values, omitted optional fields and exact original ETag', async () => {
    policy.value = {
      idleMinutes: 30,
      future: { enabled: true, limits: [7] },
    } as Policy['value'];
    await render();
    etag = '"8"'; // A later server revision must not replace the draft's original tag.
    await input('field-idleMinutes', '45');
    await click('save-workspace');
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      '30 → 45',
    );
    await confirm();
    expect(JSON.parse(String(writes()[0]!.init.body))).toEqual({
      value: { idleMinutes: 45, future: { enabled: true, limits: [7] } },
    });
    expect(new Headers(writes()[0]!.init.headers).get('If-Match')).toBe('"1"');
    expect(new Headers(writes()[0]!.init.headers).get('X-Auriga-CSRF')).toBe(
      'test-csrf',
    );
  });
  it('keeps 412 draft and blocks resubmission until explicit reload and confirmation', async () => {
    await render();
    intercept = (path, init) =>
      path === policyPath && init.method === 'PUT'
        ? Promise.resolve(problem(412, 'org.precondition_failed'))
        : undefined;
    await input('field-idleMinutes', '45');
    await click('save-workspace');
    await confirm();
    expect(wrapper!.text()).toContain('草稿已保留');
    expect(
      (
        wrapper!.get('[data-testid="field-idleMinutes"]')
          .element as HTMLInputElement
      ).value,
    ).toBe('45');
    await click('save-workspace');
    expect(writes()).toHaveLength(1);
    policy.value = { idleMinutes: 50 } as Policy['value'];
    etag = '"2"';
    await click('reload-workspace');
    expect(
      (
        wrapper!.get('[data-testid="field-idleMinutes"]')
          .element as HTMLInputElement
      ).value,
    ).toBe('50');
    expect(
      wrapper!.getComponent(ActionConfirm).props('message'),
    ).toBeUndefined();
  });
  it.each([undefined, 'W/"1"', '1', '"opaque"', '"9223372036854775808"'])(
    'blocks missing or unusable ETag %s',
    async (value) => {
      etag = value;
      await render();
      expect(wrapper!.text()).toContain('非法强 ETag');
      expect(
        wrapper!.get('[data-testid="save-workspace"]').attributes('disabled'),
      ).toBeDefined();
    },
  );
  it.each([
    { idleMinutes: 4 },
    { wakeOnSchedule: 'yes' },
    { maxConcurrentStarts: null },
    { future: { n: 9007199254740992 } },
    { future: [0.5] },
  ])('blocks malformed or unsafe server value %j', async (value) => {
    policy.value = value as Policy['value'];
    await render();
    expect(wrapper!.text()).toContain('无法安全往返');
    expect(
      wrapper!.get('[data-testid="save-workspace"]').attributes('disabled'),
    ).toBeDefined();
    expect(writes()).toEqual([]);
  });
  it.each(['4', '3.5', '9007199254740992', ''])(
    'rejects invalid draft idle minutes %s',
    async (value) => {
      await render();
      await input('field-idleMinutes', value);
      await click('save-workspace');
      expect(wrapper!.text()).toContain('请填写有效值');
      expect(
        wrapper!.getComponent(ActionConfirm).props('message'),
      ).toBeUndefined();
    },
  );
  it('represents empty optional policy without inserting defaults and permits setting all six fields', async () => {
    policy.value = {};
    await render();
    await click('save-workspace');
    expect(writes()).toEqual([]);
    for (const [key, value] of Object.entries({
      idleMinutes: '5',
      wakeOnSchedule: 'false',
      alwaysOnRequiresApproval: 'true',
      maxConcurrentStarts: '1',
      defaultDshVersion: '',
      allowPersonalPlugins: 'false',
    })) {
      await wrapper!.get(`[data-testid="present-${key}"] input`).setValue(true);
      if (
        ['idleMinutes', 'maxConcurrentStarts', 'defaultDshVersion'].includes(
          key,
        )
      )
        await input(`field-${key}`, value);
      else await wrapper!.get(`[data-testid="field-${key}"]`).setValue(value);
    }
    await click('save-workspace');
    await confirm();
    expect(JSON.parse(String(writes()[0]!.init.body))).toEqual({
      value: {
        idleMinutes: 5,
        wakeOnSchedule: false,
        alwaysOnRequiresApproval: true,
        maxConcurrentStarts: 1,
        defaultDshVersion: '',
        allowPersonalPlugins: false,
      },
    });
  });
  it('explicitly omits an existing optional value and lists its removal', async () => {
    await render();
    await wrapper!
      .get('[data-testid="present-wakeOnSchedule"] input')
      .setValue(false);
    await click('save-workspace');
    expect(wrapper!.getComponent(ActionConfirm).props('message')).toContain(
      '省略',
    );
    await confirm();
    expect(JSON.parse(String(writes()[0]!.init.body))).toEqual({
      value: { idleMinutes: 30 },
    });
  });
  it('blocks removed write permission at final confirmation', async () => {
    await render();
    await input('team-name', 'New team');
    await click('save-team');
    permissions = ['org.policies.read'];
    await confirm();
    expect(writes()).toEqual([]);
    expect(
      (wrapper!.get('[data-testid="team-name"]').element as HTMLInputElement)
        .value,
    ).toBe('New team');
    expect(wrapper!.text()).toContain('没有此操作所需权限');
  });
  it('clears policy draft and ETag on revoked policy read while preserving profile access', async () => {
    await render();
    await input('field-idleMinutes', '40');
    await click('save-workspace');
    permissions = ['org.tenant.write', 'org.policies.write'];
    await confirm();
    expect(writes()).toEqual([]);
    expect(wrapper!.find('[data-testid="field-idleMinutes"]').exists()).toBe(
      false,
    );
    expect(
      wrapper!.get('[data-testid="team-name"]').attributes('disabled'),
    ).toBeUndefined();
  });
  it('rechecks policy write independently while retaining read-only policy data', async () => {
    await render();
    await input('field-idleMinutes', '45');
    await click('save-workspace');
    permissions = ['org.tenant.write', 'org.policies.read'];
    await confirm();
    expect(writes()).toEqual([]);
    expect(wrapper!.find('[data-testid="field-idleMinutes"]').exists()).toBe(
      true,
    );
    expect(
      wrapper!.get('[data-testid="save-workspace"]').attributes('disabled'),
    ).toBeDefined();
    expect(
      wrapper!.get('[data-testid="save-team"]').attributes('disabled'),
    ).toBeUndefined();
  });
  it.each([401, 403])(
    'cleans denied policy writes (%s) without reporting success',
    async (status) => {
      await render();
      intercept = (path, init) =>
        path === policyPath && init.method === 'PUT'
          ? Promise.resolve(problem(status))
          : undefined;
      await input('field-idleMinutes', '45');
      await click('save-workspace');
      await confirm();
      expect(writes()).toHaveLength(1);
      expect(wrapper!.find('[data-testid="field-idleMinutes"]').exists()).toBe(
        false,
      );
      expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(
        status === 403,
      );
      expect(wrapper!.text()).not.toContain('已保存服务端配置');
    },
  );
  it('recovers an initial membership failure by reading only and keeps section failures independent', async () => {
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? Promise.resolve(problem(503, 'common.unavailable'))
        : undefined;
    await render();
    expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(false);
    intercept = (path) =>
      path === policyPath
        ? Promise.resolve(problem(503, 'common.unavailable'))
        : undefined;
    await click('reload-team');
    await click('reload-workspace');
    expect(
      wrapper!.get('[data-testid="save-team"]').attributes('disabled'),
    ).toBeUndefined();
    expect(wrapper!.get('[data-testid="workspace-section"]').text()).toContain(
      '服务暂时不可用',
    );
    expect(writes()).toEqual([]);
  });
  it('displays loading until the official tenant read settles', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === teamPath
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    expect(wrapper!.get('[data-testid="team-section"]').text()).toContain(
      '加载中',
    );
    expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(false);
    resolve(json(tenant));
    await flushPromises();
    expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(true);
    expect(wrapper!.get('[data-testid="team-section"]').text()).not.toContain(
      '加载中',
    );
  });
  it.each(['team', 'workspace'] as const)(
    'only retries official reading after %s write success and read failure',
    async (section) => {
      await render();
      await input(
        section === 'team' ? 'team-name' : 'field-idleMinutes',
        section === 'team' ? 'New team' : '45',
      );
      let failed = false;
      const pathToFail = section === 'team' ? teamPath : policyPath;
      intercept = (path, init) => {
        if (
          path === pathToFail &&
          (!init.method || init.method === 'GET') &&
          writes().length &&
          !failed
        ) {
          failed = true;
          return Promise.resolve(problem(503, 'common.unavailable'));
        }
        return undefined;
      };
      await click(`save-${section}`);
      await confirm();
      expect(wrapper!.text()).toContain('写入已提交');
      expect(
        wrapper!.get(`[data-testid="save-${section}"]`).attributes('disabled'),
      ).toBeDefined();
      await click(`reload-${section}`);
      expect(writes()).toHaveLength(1);
      expect(
        wrapper!.get(`[data-testid="save-${section}"]`).attributes('disabled'),
      ).toBeUndefined();
    },
  );
  it('renders missing policy and isolated failure while keeping team editable', async () => {
    intercept = (path) =>
      path === policyPath
        ? Promise.resolve(problem(404, 'org.not_found'))
        : undefined;
    await render();
    expect(wrapper!.text()).toContain('尚无 workspace 策略');
    expect(wrapper!.find('[data-testid="save-workspace"]').exists()).toBe(
      false,
    );
    expect(
      wrapper!.get('[data-testid="save-team"]').attributes('disabled'),
    ).toBeUndefined();
  });
  it('clears only policy on its 403 and clears both sections on 401', async () => {
    await render();
    intercept = (path) =>
      path === policyPath ? Promise.resolve(problem(403)) : undefined;
    await click('reload-workspace');
    expect(wrapper!.find('[data-testid="field-idleMinutes"]').exists()).toBe(
      false,
    );
    expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(true);
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? Promise.resolve(problem(401, 'common.unauthenticated'))
        : undefined;
    await click('reload-team');
    expect(wrapper!.find('[data-testid="team-name"]').exists()).toBe(false);
  });
  it('ignores a late policy read after revocation from another section', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === policyPath
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    permissions = ['org.tenant.write'];
    await click('reload-team');
    resolve(json(policy, 200, '"1"'));
    await flushPromises();
    expect(wrapper!.find('[data-testid="field-idleMinutes"]').exists()).toBe(
      false,
    );
  });
  it('does not write after unmount during final authorization and prevents duplicate confirmation', async () => {
    await render();
    await input('team-name', 'New team');
    await click('save-team');
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : undefined;
    await confirm();
    await confirm();
    wrapper!.unmount();
    wrapper = undefined;
    resolve(json({ permissions }));
    await flushPromises();
    expect(writes()).toEqual([]);
    expect(calls.filter((call) => call.path === '/api/org/v1/me')).toHaveLength(
      2,
    );
  });
});
describe('workspace form boundary', () => {
  it('preserves unknown nested data and distinguishes omitted from empty string', () => {
    const original = { defaultDshVersion: '', future: [true, { n: 42 }] };
    const draft = workspaceDraft(original);
    expect(validWorkspaceDraft(draft)).toBe(true);
    expect(unsafeWorkspace(original)).toBe(false);
    expect(workspaceValue(original, draft)).toEqual(original);
    expect(draft.idleMinutes.enabled).toBe(false);
    expect(draft.defaultDshVersion.enabled).toBe(true);
  });
  it('accepts quoted long ETags and rejects arbitrary, weak and invalid tags', () => {
    expect(policyEtag('"9223372036854775807"')).toBe(true);
    for (const tag of [null, '', '*', '"x"', 'W/"1"', '"1", "2"', '"-1"'])
      expect(policyEtag(tag)).toBe(false);
  });
});
