import { ApiError } from '@auriga/shared';
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
    sourceGaps: [{ source: 'identity-header', reason: 'not_configured' }],
  });
  vi.spyOn(credentialsApi, 'revoke').mockResolvedValue({
    ...row,
    status: 'revoked',
    replacementVersion: 3,
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
  expect(w.text()).toContain('\u6743\u9650');
  w.unmount();
});
it('shows metadata and honest unavailable service state; forwards filters', async () => {
  const w = setup();
  await flushPromises();
  expect(w.text()).toContain('device-1');
  expect(w.text()).toContain('\u672a\u914d\u7f6e');
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
  expect(w.text()).toContain('3');
  w.unmount();
});

it('renders source metadata and the authoritative pending version with real Chinese text', async () => {
  const w = setup();
  vi.mocked(credentialsApi.list).mockResolvedValue({
    items: [
      { ...row, replacementVersion: 3 },
      {
        id: 'service:edge',
        type: 'service',
        source: 'iam-service-issuance',
        versionLabel: 'issued-1',
        status: 'active',
        issuedAt: '2026-01-01T00:00:00Z',
        expiresAt: '2026-01-02T00:00:00Z',
        expiringSoon: true,
      },
      {
        id: 'node:example',
        type: 'node',
        source: 'workspace-node-certificates',
        versionLabel: 'serial-2',
        status: 'active',
        expiringSoon: false,
      },
      {
        id: 'network-keyset',
        type: 'keyset',
        source: 'network-keyset',
        versionLabel: '42',
        status: 'active',
        expiringSoon: false,
      },
    ],
    serviceMetadataAvailable: true,
    sourceGaps: [],
  });
  await flushPromises();
  expect(w.text()).toContain('\u5e73\u53f0\u51ed\u636e');
  expect(w.get('[data-testid="replacement-version"]').text()).toContain('3');
  expect(w.text()).toContain('issued-1');
  expect(w.text()).toContain('serial-2');
  expect(w.text()).toContain('42');
  expect(w.find('[data-testid="source-gaps"]').exists()).toBe(false);
  expect(w.text()).not.toContain('credentialSources.');
  w.unmount();
});

it('allows completing reissuance after revocation committed but workspace was unavailable', async () => {
  const w = setup();
  await flushPromises();
  vi.mocked(credentialsApi.revoke).mockRejectedValueOnce(
    new Error('unavailable'),
  );
  await w.get('[data-testid="revoke"]').trigger('click');
  await w.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  vi.mocked(credentialsApi.list).mockResolvedValue({
    items: [{ ...row, status: 'revoked' }],
    serviceMetadataAvailable: false,
    sourceGaps: [],
  });
  await w.get('[data-testid="refresh"]').trigger('click');
  await flushPromises();
  expect(
    w.get('[data-testid="revoke"]').attributes('disabled'),
  ).toBeUndefined();
  await w.get('[data-testid="revoke"]').trigger('click');
  await w.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(credentialsApi.revoke).toHaveBeenCalledTimes(2);
  expect(w.text()).toContain('3');
  w.unmount();
});

it('labels deleted instances and never offers reissuance', async () => {
  const w = setup();
  vi.mocked(credentialsApi.list).mockResolvedValue({
    items: [{ ...row, status: 'instance_deleted' }],
    serviceMetadataAvailable: false,
    sourceGaps: [],
  });
  await flushPromises();
  expect(w.text()).toContain('\u5b9e\u4f8b\u5df2\u5220\u9664');
  expect(w.get('[data-testid="revoke"]').attributes('disabled')).toBeDefined();
  expect(credentialsApi.revoke).not.toHaveBeenCalled();
  w.unmount();
});

it('keeps rows when recent verification is incomplete instead of reporting lost permission', async () => {
  const w = setup();
  await flushPromises();
  vi.mocked(credentialsApi.revoke).mockRejectedValue(
    new ApiError({
      title: 'step up',
      status: 403,
      code: 'iam.step_up_required',
    }),
  );
  await w.get('[data-testid="revoke"]').trigger('click');
  await w.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('device-1');
  expect(w.text()).toContain('\u8eab\u4efd\u9a8c\u8bc1\u672a\u5b8c\u6210');
  w.unmount();
});
