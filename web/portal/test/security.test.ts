import { mount, flushPromises } from '@vue/test-utils';
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
    createdAt: '2026-10-27T00:00:00Z',
  },
  session: {
    id: 'session',
    providerId: 'provider',
    audience: 'portal',
    current: true,
    mfa: false,
    createdAt: '2026-10-27T00:00:00Z',
    expiresAt: '2026-10-28T00:00:00Z',
  },
  identities: [],
  mfa: { totp: false, passkeys: 0, required: false },
};
afterEach(() => vi.restoreAllMocks());
describe('account security', () => {
  it('shows enrollment only in memory and confirms through IAM before reporting enabled', async () => {
    vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
    vi.spyOn(iamApi, 'listMySessions').mockResolvedValue({
      items: [me.session],
    });
    vi.spyOn(iamApi, 'beginTotpEnrollment').mockResolvedValue({
      enrollmentId: 'enrollment',
      secret: 'ONE-TIME-SECRET',
      otpauthUri: 'otpauth://totp/Auriga',
      expiresAt: '2026-10-28T00:00:00Z',
    });
    const confirm = vi
      .spyOn(iamApi, 'confirmTotpEnrollment')
      .mockResolvedValue();
    const wrapper = mount(SecurityView, {
      global: { plugins: [createAurigaI18n('zh-CN')] },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('未配置');
    await wrapper.get('[data-action="enroll"]').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('ONE-TIME-SECRET');
    await wrapper.get('input[autocomplete="one-time-code"]').setValue('123456');
    await wrapper.get('[data-form="totp"]').trigger('submit');
    await flushPromises();
    expect(confirm).toHaveBeenCalledWith('enrollment', { code: '123456' });
    expect(wrapper.text()).not.toContain('ONE-TIME-SECRET');
    expect(wrapper.text()).toContain('当前会话');
    wrapper.unmount();
  });
  it('keeps the secret after an invalid code and clears it on cancellation', async () => {
    vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
    vi.spyOn(iamApi, 'listMySessions').mockResolvedValue({ items: [] });
    vi.spyOn(iamApi, 'beginTotpEnrollment').mockResolvedValue({
      enrollmentId: 'enrollment',
      secret: 'ONE-TIME-SECRET',
      otpauthUri: 'otpauth://totp/Auriga',
      expiresAt: '2026-10-28T00:00:00Z',
    });
    vi.spyOn(iamApi, 'confirmTotpEnrollment').mockRejectedValue(
      new ApiError({
        status: 400,
        title: 'Invalid code',
        code: 'iam.invalid_credentials',
      }),
    );
    const wrapper = mount(SecurityView, {
      global: { plugins: [createAurigaI18n('zh-CN')] },
    });
    await flushPromises();
    await wrapper.get('[data-action="enroll"]').trigger('click');
    await flushPromises();
    await wrapper.get('input[autocomplete="one-time-code"]').setValue('000000');
    await wrapper.get('[data-form="totp"]').trigger('submit');
    await flushPromises();
    expect(wrapper.find('[role="alert"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('ONE-TIME-SECRET');
    expect(wrapper.text()).not.toContain('TOTP 已确认');
    await wrapper
      .get('[data-form="totp"] button[type="button"]')
      .trigger('click');
    expect(wrapper.text()).not.toContain('ONE-TIME-SECRET');
    wrapper.unmount();
  });
  it('does not display an empty session list after a failed read', async () => {
    vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
    vi.spyOn(iamApi, 'listMySessions').mockRejectedValue(
      new ApiError({
        status: 403,
        title: 'Forbidden',
        code: 'common.forbidden',
      }),
    );
    const wrapper = mount(SecurityView, {
      global: { plugins: [createAurigaI18n('zh-CN')] },
    });
    await flushPromises();
    expect(wrapper.find('[role="alert"]').exists()).toBe(true);
    expect(wrapper.text()).not.toContain('暂无会话');
    wrapper.unmount();
  });
  it.each([false, true])(
    'verifies an expired enrollment request without duplicating enrollment completed in IAM (%s)',
    async (enrolledInIam) => {
      vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
      vi.spyOn(iamApi, 'listMySessions').mockResolvedValue({ items: [] });
      const enroll = vi
        .spyOn(iamApi, 'beginTotpEnrollment')
        .mockRejectedValueOnce(
          new ApiError({
            status: 403,
            title: 'Step up',
            code: 'iam.step_up_required',
          }),
        )
        .mockResolvedValue({
          enrollmentId: 'enrollment',
          secret: 'ONE-TIME-SECRET',
          otpauthUri: 'otpauth://totp/Auriga',
          expiresAt: '2026-10-28T00:00:00Z',
        });
      const tx: LoginTransaction = {
        id: 'tx',
        purpose: 'stepup',
        status: 'pending',
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
      vi.spyOn(iamApi, 'createLoginTransaction').mockResolvedValue(tx);
      vi.spyOn(iamApi, 'advanceLoginTransaction').mockResolvedValue({
        ...tx,
        status: 'completed',
        step: { type: 'complete', redirectTo: '/' },
      });
      const wrapper = mount(SecurityView, {
        global: { plugins: [createAurigaI18n('zh-CN')] },
      });
      await flushPromises();
      await wrapper.get('[data-action="enroll"]').trigger('click');
      await flushPromises();
      expect(enroll).toHaveBeenCalledTimes(1);
      vi.mocked(iamApi.getMe).mockResolvedValue({
        ...me,
        mfa: { ...me.mfa, totp: enrolledInIam },
      });
      await wrapper.get('.verification input[name="code"]').setValue('123456');
      await wrapper.get('.verification form').trigger('submit');
      await flushPromises();
      expect(enroll).toHaveBeenCalledTimes(enrolledInIam ? 1 : 2);
      if (enrolledInIam) {
        expect(wrapper.text()).toContain('已配置');
        expect(wrapper.text()).not.toContain('ONE-TIME-SECRET');
      } else expect(wrapper.text()).toContain('ONE-TIME-SECRET');
      wrapper.unmount();
    },
  );
  it('changes the password, clears the inputs and refreshes sessions', async () => {
    vi.spyOn(iamApi, 'getMe').mockResolvedValue(me);
    const list = vi
      .spyOn(iamApi, 'listMySessions')
      .mockResolvedValue({ items: [me.session] });
    const change = vi.spyOn(iamApi, 'changePassword').mockResolvedValue();
    const revoke = vi
      .spyOn(iamApi, 'revokeOtherSessions')
      .mockResolvedValue({ revoked: 1 });
    const wrapper = mount(SecurityView, {
      global: { plugins: [createAurigaI18n('zh-CN')] },
    });
    await flushPromises();
    await wrapper
      .get('input[autocomplete="current-password"]')
      .setValue('old-password');
    await wrapper
      .get('input[autocomplete="new-password"]')
      .setValue('new-password');
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(change).toHaveBeenCalledWith({
      currentPassword: 'old-password',
      newPassword: 'new-password',
    });
    expect(
      (
        wrapper.get('input[autocomplete="current-password"]')
          .element as HTMLInputElement
      ).value,
    ).toBe('');
    await wrapper.get('[data-action="revoke"]').trigger('click');
    await flushPromises();
    expect(revoke).toHaveBeenCalledTimes(1);
    expect(list).toHaveBeenCalledTimes(3);
    wrapper.unmount();
  });
});
