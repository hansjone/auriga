import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import {
  ApiError,
  createApiClient,
  createAurigaI18n,
  type LoginTransaction,
  type Me,
} from '@auriga/shared';
import StepUpDialog from '../src/admin/StepUpDialog.vue';
import { requestStepUp, withStepUp } from '../src/admin/stepUp';
import { iamApi } from '../src/api';

const me: Me = {
  account: {
    id: 'account',
    tenantId: 'tenant',
    displayName: 'Alice',
    status: 'active',
    createdAt: '2026-10-27T00:00:00Z',
  },
  session: {
    id: 'session',
    providerId: 'provider',
    audience: 'admin',
    current: true,
    mfa: true,
    createdAt: '2026-10-27T00:00:00Z',
    expiresAt: '2026-10-28T00:00:00Z',
  },
  identities: [],
  mfa: { totp: true, passkeys: 0, required: true },
};
const tx: LoginTransaction = {
  id: 'tx',
  status: 'pending',
  purpose: 'stepup',
  provider: {
    id: 'provider',
    connectorType: 'team-account',
    displayName: { en: 'Team' },
  },
  step: {
    type: 'form',
    formId: 'totp',
    fields: [{ name: 'code', kind: 'otp', required: true }],
    submitLabel: { en: 'Verify' },
  },
  expiresAt: '2026-10-28T00:00:00Z',
};
let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
});
async function setup() {
  vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
  vi.spyOn(iamApi, 'createLoginTransaction').mockResolvedValue(tx);
  vi.spyOn(iamApi, 'cancelLoginTransaction').mockResolvedValue();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/models/providers', component: { template: '<div />' } },
      { path: '/overview', component: { template: '<div />' } },
    ],
  });
  await router.push('/models/providers');
  wrapper = mount(StepUpDialog, {
    props: { me },
    global: {
      plugins: [router, createAurigaI18n('zh-CN')],
      stubs: {
        ElDialog: {
          props: ['modelValue'],
          template:
            '<div v-if="modelValue" role="dialog"><slot /><slot name="footer" /></div>',
        },
      },
    },
  });
  const raw = createApiClient();
  const request = vi
    .spyOn(raw, 'request')
    .mockRejectedValueOnce(
      new ApiError({
        status: 403,
        title: 'Step up',
        code: 'iam.step_up_required',
      }),
    )
    .mockResolvedValue('saved');
  const result = withStepUp(raw, requestStepUp)
    .post('/api/test', { value: 1 })
    .then(
      (value) => value,
      (error: unknown) => error,
    );
  await flushPromises();
  return { router, request, result };
}
describe('in-place step-up dialog', () => {
  it('submits real step input and resumes only after IAM reports completed', async () => {
    vi.spyOn(iamApi, 'advanceLoginTransaction').mockResolvedValue({
      ...tx,
      status: 'completed',
      step: { type: 'complete', redirectTo: '/ignored' },
    });
    const { router, request, result } = await setup();
    expect(iamApi.createLoginTransaction).toHaveBeenCalledWith({
      providerId: 'provider',
      purpose: 'stepup',
      returnTo: '/models/providers',
    });
    await wrapper!.get('input[name="code"]').setValue('123456');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(iamApi.advanceLoginTransaction).toHaveBeenCalledWith('tx', {
      type: 'form',
      formId: 'totp',
      values: { code: '123456' },
    });
    expect(await result).toBe('saved');
    expect(request).toHaveBeenCalledTimes(2);
    expect(router.currentRoute.value.path).toBe('/models/providers');
  });
  it('cancels without replaying and cancels the IAM transaction', async () => {
    const { request, result } = await setup();
    await wrapper!
      .findAll('button')
      .find((button) => button.text() === '取消操作')!
      .trigger('click');
    await flushPromises();
    expect(await result).toBeInstanceOf(Error);
    expect(request).toHaveBeenCalledTimes(1);
    expect(iamApi.cancelLoginTransaction).toHaveBeenCalledWith('tx');
  });
  it('cancels on navigation without replaying', async () => {
    const { router, request, result } = await setup();
    await router.push('/overview');
    await flushPromises();
    expect(await result).toBeInstanceOf(Error);
    expect(request).toHaveBeenCalledTimes(1);
  });
  it('rejects a changed account after successful verification', async () => {
    vi.spyOn(iamApi, 'advanceLoginTransaction').mockResolvedValue({
      ...tx,
      status: 'completed',
      step: { type: 'complete', redirectTo: '/' },
    });
    const { request, result } = await setup();
    vi.mocked(iamApi.getMe).mockResolvedValue({
      ...me,
      account: { ...me.account, id: 'other' },
    });
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(wrapper!.text()).toContain('会话已改变');
    wrapper!.unmount();
    wrapper = undefined;
    expect(await result).toBeInstanceOf(Error);
    expect(request).toHaveBeenCalledTimes(1);
  });
});
