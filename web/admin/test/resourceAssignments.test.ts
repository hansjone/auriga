import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import {
  resourceCatalogApi as api,
  type CatalogAssignment,
} from '../src/admin/resourceCatalogApi';
import Assignments from '../src/views/resources/ResourceAssignments.vue';
const identity = {
  tenantId: 'tenant',
  kind: 'plugin' as const,
  packageId: 'p',
  versionId: 'v',
  version: '1.0.0',
  manifestDigest: 'sha256:' + 'a'.repeat(64),
};
const rule: CatalogAssignment = {
  id: 'rule',
  identity,
  scope: 'group',
  targetId: 'group',
  installationMode: 'required',
  revision: 4,
  active: true,
};
const views: ReturnType<typeof mount>[] = [];
afterEach(() => {
  views.splice(0).forEach((v) => v.unmount());
  vi.restoreAllMocks();
});
function setup(disabled = false) {
  vi.spyOn(api, 'assignments').mockResolvedValue({ items: [rule] });
  const view = mount(Assignments, {
    props: {
      disabled,
      kind: 'plugin',
      member: {
        member: { id: 'publisher' },
        tenant: { id: 'tenant' },
        permissions: ['resource.distributions.manage'],
      } as never,
    },
    global: {
      plugins: [createAurigaI18n('zh-CN')],
      stubs: {
        ActionConfirm: {
          props: ['message'],
          template:
            '<button v-if="message" data-testid="confirm" @click="$emit(\'confirm\')">Confirm</button>',
        },
      },
    },
  });
  views.push(view);
  return view;
}
it('creates exact scoped required/default/optional rules and keeps registration separate from application', async () => {
  const view = setup();
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { id: 'p', tenantId: 'tenant', kind: 'plugin' } as never,
    etag: '"1"',
  });
  vi.spyOn(api, 'version').mockResolvedValue({
    data: {
      id: 'v',
      packageId: 'p',
      kind: 'plugin',
      state: 'published',
      version: '1.0.0',
      digest: identity.manifestDigest,
    } as never,
    etag: '"1"',
  });
  const assign = vi.spyOn(api, 'assign').mockResolvedValue(rule);
  await flushPromises();
  await view.get('[data-testid="assignment-package"]').setValue('p');
  await view.get('[data-testid="assignment-version"]').setValue('v');
  await view.get('[data-testid="assignment-scope"]').setValue('tenant');
  for (const mode of ['required', 'default', 'optional']) {
    await view.get('[data-testid="assignment-mode"]').setValue(mode);
    await view.get('form').trigger('submit');
    await flushPromises();
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(assign).toHaveBeenLastCalledWith(
      { identity, scope: 'tenant', targetId: 'tenant', installationMode: mode },
      expect.any(String),
    );
    expect(view.text()).toContain('已登记');
  }
});
it('loads rules after the parent finishes loading the main distribution page', async () => {
  const view = setup(true);
  await flushPromises();
  expect(api.assignments).not.toHaveBeenCalled();
  await view.setProps({ disabled: false });
  await flushPromises();
  expect(api.assignments).toHaveBeenCalledTimes(1);
  expect(view.get('[data-testid="assignment-detail"]').text()).toBe('p');
});
it('uses the exact rule revision to remove, preserves retry fingerprint, then requires refresh on conflict', async () => {
  const view = setup();
  const remove = vi
    .spyOn(api, 'removeAssignment')
    .mockRejectedValueOnce(new Error('Network'))
    .mockRejectedValueOnce(
      new ApiError({
        title: 'Conflict',
        status: 412,
        code: 'common.precondition_failed',
      }),
    );
  await flushPromises();
  await view.get('[data-testid="assignment-detail"]').trigger('click');
  expect(view.text()).toContain('group');
  await view.get('[data-testid="assignment-remove"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="assignment-retry"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(remove.mock.calls[0]!.slice(0, 2)).toEqual(['rule', '"4"']);
  expect(remove.mock.calls[0]).toEqual(remove.mock.calls[1]);
  expect(view.text()).toContain('数据已变化');
});
