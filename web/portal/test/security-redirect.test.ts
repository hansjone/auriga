import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ApiError,
  createAurigaI18n,
  type LoginTransaction,
  type Me,
} from '@auriga/shared';
import SecurityView from '../src/views/account/SecurityView.vue';
import { iamApi } from '../src/api';

const me: Me = {
  account: {
    id: 'account',
    tenantId: 'tenant',
    displayName: 'Alice',
    status: 'active',
    createdAt: '2026-10-28T00:00:00Z',
  },
  session: {
    id: 'session',
    providerId: 'oidc',
    audience: 'portal',
    current: true,
    mfa: true,
    createdAt: '2026-10-28T00:00:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
  },
  identities: [],
  mfa: { totp: false, passkeys: 0, required: false },
};
const tx: LoginTransaction = {
  id: 'tx',
  purpose: 'stepup',
  status: 'pending',
  provider: { id: 'oidc', connectorType: 'oidc', displayName: { en: 'OIDC' } },
  step: {
    type: 'redirect',
    method: 'GET',
    url: 'https://idp.example.com/authorize?state=opaque',
  },
  expiresAt: '2099-01-01T00:00:00Z',
};
const complete: LoginTransaction = {
  ...tx,
  status: 'completed',
  step: { type: 'complete', redirectTo: '/account/security' },
};
const required = () =>
  new ApiError({ status: 403, code: 'iam.step_up_required', title: 'Step up' });
let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
  vi.useRealTimers();
});
async function setup() {
  vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
  vi.spyOn(iamApi, 'listMySessions').mockResolvedValue({ items: [me.session] });
  const begin = vi.spyOn(iamApi, 'beginTotpEnrollment').mockResolvedValue({
    enrollmentId: 'enrollment',
    secret: 'ONLY-IN-PARENT',
    otpauthUri: 'otpauth://totp/Auriga',
    expiresAt: '2099-01-01T00:00:00Z',
  });
  const confirm = vi
    .spyOn(iamApi, 'confirmTotpEnrollment')
    .mockRejectedValueOnce(required())
    .mockResolvedValue();
  vi.spyOn(iamApi, 'createLoginTransaction').mockResolvedValue(tx);
  vi.spyOn(iamApi, 'cancelLoginTransaction').mockResolvedValue();
  const read = vi
    .spyOn(iamApi, 'getLoginTransaction')
    .mockResolvedValue(complete);
  const child = {
    closed: false,
    close: vi.fn(),
    opener: null,
    location: { href: 'about:blank', replace: vi.fn() },
  };
  const open = vi
    .spyOn(window, 'open')
    .mockReturnValue(child as unknown as Window);
  wrapper = mount(SecurityView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  await wrapper.get('[data-action="enroll"]').trigger('click');
  await flushPromises();
  await wrapper.get('input[autocomplete="one-time-code"]').setValue('123456');
  await wrapper.get('[data-form="totp"]').trigger('submit');
  await flushPromises();
  return { begin, confirm, read, child, open };
}
describe('portal redirect step-up continuation', () => {
  it('preserves enrollment and code, then retries the original confirmation once', async () => {
    const { begin, confirm, child, open } = await setup();
    expect(wrapper!.find('[data-step-type="redirect"]').exists()).toBe(false);
    expect(open).not.toHaveBeenCalled();
    expect(wrapper!.text()).toContain('ONLY-IN-PARENT');
    expect(
      (wrapper!.get('[data-form="totp"] input').element as HTMLInputElement)
        .value,
    ).toBe('123456');
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(begin).toHaveBeenCalledTimes(1);
    expect(confirm.mock.calls).toEqual([
      ['enrollment', { code: '123456' }],
      ['enrollment', { code: '123456' }],
    ]);
    expect(child.close).toHaveBeenCalledOnce();
    expect(wrapper!.text()).not.toContain('ONLY-IN-PARENT');
    expect(wrapper!.text()).toContain('TOTP 已确认');
  });
  it('keeps the draft when the popup is blocked, then permits reopening', async () => {
    const { confirm, open, read, child } = await setup();
    open.mockReturnValueOnce(null);
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(wrapper!.text()).toContain('浏览器拦截了验证窗口');
    expect(wrapper!.text()).toContain('ONLY-IN-PARENT');
    expect(read).not.toHaveBeenCalled();
    expect(confirm).toHaveBeenCalledTimes(1);
    open.mockReturnValue(child as unknown as Window);
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(confirm).toHaveBeenCalledTimes(2);
  });
  it.each(['account', 'session'])(
    'does not retry after the original %s changed',
    async (changed) => {
      const { confirm } = await setup();
      vi.mocked(iamApi.getMe).mockResolvedValue(
        changed === 'account'
          ? { ...me, account: { ...me.account, id: 'other' } }
          : { ...me, session: { ...me.session, id: 'other' } },
      );
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      expect(confirm).toHaveBeenCalledTimes(1);
      expect(wrapper!.text()).toContain('会话已改变');
    },
  );
  it('rejects a different transaction without losing the enrollment', async () => {
    const { confirm, read } = await setup();
    read.mockResolvedValue({ ...complete, id: 'other-transaction' });
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(wrapper!.text()).toContain('未能核验原验证事务');
    expect(wrapper!.text()).toContain('ONLY-IN-PARENT');
  });
  it('preserves the enrollment after popup timeout without retrying', async () => {
    const { confirm, read, child } = await setup();
    read.mockImplementation(() => new Promise(() => {}));
    vi.useFakeTimers();
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await vi.advanceTimersByTimeAsync(300_001);
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(child.close).toHaveBeenCalledOnce();
    expect(wrapper!.text()).toContain('验证已超时');
    expect(wrapper!.text()).toContain('ONLY-IN-PARENT');
  });
  it.each(['cancel', 'unmount'])(
    'ignores late popup completion after %s',
    async (action) => {
      const { confirm, read, child } = await setup();
      let finish!: (value: LoginTransaction) => void;
      read.mockImplementation(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );
      await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
      await flushPromises();
      if (action === 'cancel')
        await wrapper!
          .get('.verification > button:last-child')
          .trigger('click');
      else {
        wrapper!.unmount();
        wrapper = undefined;
      }
      finish(complete);
      await flushPromises();
      expect(confirm).toHaveBeenCalledTimes(1);
      expect(child.close).toHaveBeenCalledOnce();
    },
  );
  it('uses the private return marker and the original session after IAM clears its cookie', async () => {
    const { confirm, read, child } = await setup();
    const returnTo = vi.mocked(iamApi.createLoginTransaction).mock.calls[0]![0]
      .returnTo!;
    expect(returnTo).toMatch(/^\/account\/security\?aurigaStepUp=[a-f\d]{64}$/);
    read.mockRejectedValue(
      new ApiError({
        status: 404,
        code: 'common.not_found',
        title: 'Not found',
      }),
    );
    child.location.href = `${location.origin}${returnTo}`;
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(confirm).toHaveBeenCalledTimes(2);
  });
  it('surfaces a second step-up denial without creating another transaction or retry loop', async () => {
    const { confirm } = await setup();
    confirm.mockRejectedValue(required());
    await wrapper!.get('[data-action="redirect-step-up"]').trigger('click');
    await flushPromises();
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(iamApi.createLoginTransaction).toHaveBeenCalledTimes(1);
    expect(wrapper!.find('[role="alert"]').exists()).toBe(true);
    expect(wrapper!.text()).toContain('仍需近期身份验证');
    expect(wrapper!.text()).toContain('ONLY-IN-PARENT');
  });
});
