import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import ElementPlus from 'element-plus';
import { createAurigaI18n } from '@auriga/shared';
import CredentialsView from '../src/views/CredentialsView.vue';
import { credentialsApi } from '../src/admin/credentialsApi';
import { adminApi } from '../src/admin/api';
const row = {
  id: 'device-1',
  instanceId: 'device-1',
  type: 'managed' as const,
  status: 'active' as const,
  version: 2,
  expiringSoon: true,
};
function setup(permissions = ['iam.keys.write']) {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({ permissions } as Awaited<
    ReturnType<typeof adminApi.membership>
  >);
  vi.spyOn(credentialsApi, 'list').mockResolvedValue({
    items: [row],
    serviceMetadataAvailable: false,
  });
  vi.spyOn(credentialsApi, 'revoke').mockResolvedValue({
    ...row,
    status: 'revoked',
  });
  return mount(CredentialsView, {
    global: {
      plugins: [createAurigaI18n('zh-CN'), ElementPlus],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        ActionConfirm: {
          props: ['message'],
          template:
            '<button v-if="message" data-testid="confirm" @click="$emit(\'confirm\')">Confirm</button>',
        },
      },
    },
  });
}
afterEach(() => vi.restoreAllMocks());
it('denies missing permission before loading metadata', async () => {
  const w = setup([]);
  await flushPromises();
  expect(credentialsApi.list).not.toHaveBeenCalled();
  expect(w.text()).toContain('??');
  w.unmount();
});
it('shows metadata and honest unavailable service state; forwards filters', async () => {
  const w = setup();
  await flushPromises();
  expect(w.text()).toContain('device-1');
  expect(w.text()).toContain('????');
  await w.get('[name="credentialType"]').setValue('managed');
  await w.get('[name="expiringSoon"]').setValue(true);
  await w.get('[data-testid="refresh"]').trigger('click');
  await flushPromises();
  expect(credentialsApi.list).toHaveBeenLastCalledWith('managed', '', true);
  w.unmount();
});
it('requires explicit confirmation and sends the displayed version', async () => {
  const w = setup();
  await flushPromises();
  await w.get('[data-testid="revoke"]').trigger('click');
  expect(credentialsApi.revoke).not.toHaveBeenCalled();
  await w.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(credentialsApi.revoke).toHaveBeenCalledWith('device-1', 2);
  w.unmount();
});
