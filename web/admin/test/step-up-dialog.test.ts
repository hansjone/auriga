import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
beforeEach(() => {
  // Keep the fixture's transaction valid regardless of the host's calendar date.
  // Leave setImmediate real so flushPromises can drain Vue/API work explicitly.
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] });
  vi.setSystemTime(new Date('2026-10-27T23:59:00Z'));
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
async function setup(transaction = tx) {
  vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
  vi.spyOn(iamApi, 'createLoginTransaction').mockResolvedValue(transaction);
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
    // Plain HTTP IP deployments can expose getRandomValues without randomUUID.
    vi.stubGlobal('crypto', {
      getRandomValues: globalThis.crypto.getRandomValues.bind(
        globalThis.crypto,
      ),
    });
    vi.spyOn(iamApi, 'advanceLoginTransaction').mockResolvedValue({
      ...tx,
      status: 'completed',
      step: { type: 'complete', redirectTo: '/ignored' },
    });
    const { router, request, result } = await setup();
    expect(iamApi.createLoginTransaction).toHaveBeenCalledWith({
      providerId: 'provider',
      purpose: 'stepup',
      returnTo: expect.stringMatching(/^\/overview\?aurigaStepUp=[a-f\d-]+$/),
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
  it.each([60_000, 1])(
    'keeps the original write and parent route through a redirect popup (%i ms before expiry)',
    async (remaining) => {
      vi.setSystemTime(new Date(Date.parse(tx.expiresAt) - remaining));
      const child = {
        closed: false,
        opener: null,
        close: vi.fn(),
        location: { href: 'about:blank', replace: vi.fn() },
      };
      vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window);
      vi.spyOn(iamApi, 'getLoginTransaction').mockResolvedValue({
        ...tx,
        status: 'completed',
        step: { type: 'complete', redirectTo: '/overview' },
      });
      const { request, result, router } = await setup({
        ...tx,
        step: {
          type: 'redirect',
          method: 'GET',
          url: 'https://idp.example.com/authorize',
        },
      });
      expect(wrapper!.find('[data-step-type="redirect"]').exists()).toBe(false);
      expect(window.open).not.toHaveBeenCalled();
      expect(request).toHaveBeenCalledTimes(1);
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      expect(await result).toBe('saved');
      expect(request).toHaveBeenCalledTimes(2);
      expect(request.mock.calls[0]).toEqual(request.mock.calls[1]);
      expect(router.currentRoute.value.path).toBe('/models/providers');
      expect(child.close).toHaveBeenCalledOnce();
    },
  );
  it.each([0, 1])(
    'does not open or replay an expired transaction (%i ms after expiry)',
    async (elapsed) => {
      vi.setSystemTime(new Date(Date.parse(tx.expiresAt) + elapsed));
      const open = vi.spyOn(window, 'open').mockReturnValue(null);
      const read = vi.spyOn(iamApi, 'getLoginTransaction');
      const { request, result, router } = await setup({
        ...tx,
        step: {
          type: 'redirect',
          method: 'GET',
          url: 'https://idp.example.com/authorize',
        },
      });
      const settled = vi.fn();
      void result.then(settled);
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      expect(wrapper!.text()).toContain('验证已超时');
      expect(wrapper!.text()).toContain('草稿已保留');
      expect(open).not.toHaveBeenCalled();
      expect(read).not.toHaveBeenCalled();
      expect(request).toHaveBeenCalledTimes(1);
      expect(settled).not.toHaveBeenCalled();
      await router.push('/overview');
      expect(await result).toBeInstanceOf(Error);
      expect(request).toHaveBeenCalledTimes(1);
    },
  );
  it.each([
    { lifetime: 1000, deadline: 1000 },
    { lifetime: 600_000, deadline: 300_000 },
  ])(
    'expires at $deadline ms without replaying a late completion (transaction lifetime=$lifetime ms)',
    async ({ lifetime, deadline }) => {
      const child = {
        closed: false,
        opener: null,
        close: vi.fn(),
        location: { href: 'about:blank', replace: vi.fn() },
      };
      vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window);
      let finish!: (value: LoginTransaction) => void;
      const read = vi.spyOn(iamApi, 'getLoginTransaction').mockImplementation(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );
      const { request, result, router } = await setup({
        ...tx,
        expiresAt: new Date(Date.now() + lifetime).toISOString(),
        step: {
          type: 'redirect',
          method: 'GET',
          url: 'https://idp.example.com/authorize',
        },
      });
      const settled = vi.fn();
      void result.then(settled);
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      expect(read).toHaveBeenCalledExactlyOnceWith('tx');
      await vi.advanceTimersByTimeAsync(deadline - 1);
      expect(wrapper!.find('[role="alert"]').exists()).toBe(false);
      expect(child.close).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      await flushPromises();
      expect(wrapper!.text()).toContain('验证已超时');
      expect(wrapper!.text()).toContain('草稿已保留');
      expect(child.close).toHaveBeenCalledOnce();
      finish({
        ...tx,
        status: 'completed',
        step: { type: 'complete', redirectTo: '/overview' },
      });
      await flushPromises();
      expect(settled).not.toHaveBeenCalled();
      expect(request).toHaveBeenCalledTimes(1);
      expect(router.currentRoute.value.path).toBe('/models/providers');
      await router.push('/overview');
      expect(await result).toBeInstanceOf(Error);
      expect(request).toHaveBeenCalledTimes(1);
      expect(child.close).toHaveBeenCalledOnce();
    },
  );
  it('keeps the operation pending when a popup is blocked and permits a second click', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const read = vi.spyOn(iamApi, 'getLoginTransaction').mockResolvedValue({
      ...tx,
      status: 'completed',
      step: { type: 'complete', redirectTo: '/overview' },
    });
    const { request, result } = await setup({
      ...tx,
      step: {
        type: 'redirect',
        method: 'GET',
        url: 'https://idp.example.com/authorize',
      },
    });
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(wrapper!.text()).toContain('浏览器拦截了验证窗口');
    expect(wrapper!.text()).toContain('草稿已保留');
    expect(request).toHaveBeenCalledTimes(1);
    expect(read).not.toHaveBeenCalled();
    open.mockReturnValue({
      closed: false,
      opener: null,
      close: vi.fn(),
      location: { href: 'about:blank', replace: vi.fn() },
    } as unknown as Window);
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(await result).toBe('saved');
    expect(request).toHaveBeenCalledTimes(2);
  });
  it.each([false, true])(
    'checks the original session after the callback cleared the transaction cookie (changed=%s)',
    async (changed) => {
      const child = {
        closed: false,
        opener: null,
        close: vi.fn(),
        location: { href: 'about:blank', replace: vi.fn() },
      };
      vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window);
      vi.spyOn(iamApi, 'getLoginTransaction').mockRejectedValue(
        new ApiError({
          status: 404,
          code: 'common.not_found',
          title: 'Not found',
        }),
      );
      const { request, result } = await setup({
        ...tx,
        step: {
          type: 'redirect',
          method: 'GET',
          url: 'https://idp.example.com/authorize',
        },
      });
      const returnTo = vi.mocked(iamApi.createLoginTransaction).mock
        .calls[0]![0].returnTo!;
      child.location.href = `${location.origin}${returnTo}`;
      if (changed)
        vi.mocked(iamApi.getMe).mockResolvedValue({
          ...me,
          session: { ...me.session, id: 'other-session' },
        });
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      if (changed) {
        expect(request).toHaveBeenCalledTimes(1);
        expect(wrapper!.text()).toContain('会话已改变');
        wrapper!.unmount();
        wrapper = undefined;
        expect(await result).toBeInstanceOf(Error);
      } else {
        expect(await result).toBe('saved');
        expect(request).toHaveBeenCalledTimes(2);
      }
    },
  );
  it('closes an active popup on navigation and never replays a late completion', async () => {
    const child = {
      closed: false,
      opener: null,
      close: vi.fn(),
      location: { href: 'about:blank', replace: vi.fn() },
    };
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window);
    let finish!: (value: LoginTransaction) => void;
    vi.spyOn(iamApi, 'getLoginTransaction').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const { request, result, router } = await setup({
      ...tx,
      step: {
        type: 'redirect',
        method: 'GET',
        url: 'https://idp.example.com/authorize',
      },
    });
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    await router.push('/overview');
    finish({
      ...tx,
      status: 'completed',
      step: { type: 'complete', redirectTo: '/overview' },
    });
    await flushPromises();
    expect(await result).toBeInstanceOf(Error);
    expect(request).toHaveBeenCalledTimes(1);
    expect(child.close).toHaveBeenCalledOnce();
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
