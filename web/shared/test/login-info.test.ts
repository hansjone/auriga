import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Me } from '../src/api/iam';
import { ApiError } from '../src/api/problem';
import { loginInfoText, watchLoginInfo } from '../src/native-login/login-info';

const me: Me = {
  account: {
    id: 'member',
    tenantId: 'tenant',
    displayName: 'Alice',
    status: 'active',
    createdAt: '',
  },
  session: {
    id: 'session',
    providerId: 'provider',
    audience: 'portal',
    current: true,
    mfa: false,
    createdAt: '',
    expiresAt: '2099-01-01T00:00:00Z',
  },
  identities: [],
  mfa: { totp: false, passkeys: 0, required: false },
  uacLoginInfo: { purpose: 'login_info', status: 'pending' },
};
afterEach(() => vi.useRealTimers());
function callbacks() {
  return { onInfo: vi.fn(), onExpired: vi.fn(), onIdentityChanged: vi.fn() };
}
describe('current session login information', () => {
  it('refreshes pending information once, displays a safe department string and stops', async () => {
    vi.useFakeTimers();
    const events = callbacks();
    const info = {
      purpose: 'login_info' as const,
      status: 'available' as const,
      department: 'Synthetic department',
      updatedAt: '2026-11-14T01:00:00Z',
    };
    const read = vi.fn().mockResolvedValue({ ...me, uacLoginInfo: info });
    const monitor = watchLoginInfo(me, read, events);
    expect(read).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(events.onInfo).toHaveBeenLastCalledWith(info);
    expect(loginInfoText(info, 'zh-CN')).toContain(
      '所属部门：Synthetic department',
    );
    await vi.advanceTimersByTimeAsync(120000);
    expect(read).toHaveBeenCalledOnce();
    monitor.dispose();
  });
  it.each(['pending', 'unavailable'] as const)(
    'does not display a stale department in %s',
    (status) => {
      const info = {
        purpose: 'login_info' as const,
        status,
        department: 'STALE-DEPARTMENT',
      };
      expect(loginInfoText(info, 'zh-CN')).toBe('部门信息暂不可用');
      expect(loginInfoText(info, 'en')).not.toContain('STALE-DEPARTMENT');
    },
  );
  it('degrades a read failure to unavailable without revoking or logging out', async () => {
    vi.useFakeTimers();
    const events = callbacks();
    const monitor = watchLoginInfo(
      me,
      vi.fn().mockRejectedValue(new Error('SECRET-UPSTREAM')),
      events,
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(events.onInfo).toHaveBeenLastCalledWith({
      purpose: 'login_info',
      status: 'unavailable',
    });
    expect(events.onExpired).not.toHaveBeenCalled();
    expect(events.onIdentityChanged).not.toHaveBeenCalled();
    monitor.dispose();
  });
  it('drops an old in-flight response after disposal', async () => {
    vi.useFakeTimers();
    const events = callbacks();
    let resolve!: (value: Me) => void;
    const read = vi.fn(
      () =>
        new Promise<Me>((done) => {
          resolve = done;
        }),
    );
    const monitor = watchLoginInfo(me, read, events);
    await vi.advanceTimersByTimeAsync(2000);
    monitor.dispose();
    resolve({
      ...me,
      uacLoginInfo: {
        purpose: 'login_info',
        status: 'available',
        department: 'OLD',
      },
    });
    await Promise.resolve();
    expect(events.onInfo).toHaveBeenCalledTimes(1);
  });
  it.each(['account', 'session', 'audience', 'generation'] as const)(
    'clears information if %s changes',
    async (changed) => {
      vi.useFakeTimers();
      const events = callbacks();
      const updated: Me = {
        ...me,
        account: { ...me.account },
        session: { ...me.session },
        uacLoginInfo: {
          purpose: 'login_info',
          status: 'available',
          department: 'OTHER',
        },
      };
      if (changed === 'account') updated.account.id = 'other';
      if (changed === 'session') updated.session.id = 'other';
      if (changed === 'audience') updated.session.audience = 'admin';
      if (changed === 'generation')
        Object.assign(updated.session, {
          uacSessionBinding: {
            sourceSessionId: 'source',
            providerId: 'provider',
            authGeneration: 2,
          },
        });
      const monitor = watchLoginInfo(
        me,
        vi.fn().mockResolvedValue(updated),
        events,
      );
      await vi.advanceTimersByTimeAsync(2000);
      expect(events.onInfo).toHaveBeenLastCalledWith(undefined);
      expect(events.onIdentityChanged).toHaveBeenCalledOnce();
      monitor.dispose();
    },
  );
  it('expires on 401 and bounds repeated pending reads', async () => {
    vi.useFakeTimers();
    const events = callbacks();
    const monitor = watchLoginInfo(
      me,
      vi.fn().mockRejectedValue(
        new ApiError({
          title: 'Expired',
          status: 401,
          code: 'common.unauthenticated',
        }),
      ),
      events,
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(events.onExpired).toHaveBeenCalledOnce();
    monitor.dispose();
    const pending = callbacks(),
      read = vi.fn().mockResolvedValue(me);
    const bounded = watchLoginInfo(me, read, pending);
    await vi.advanceTimersByTimeAsync(120000);
    expect(read).toHaveBeenCalledTimes(30);
    expect(pending.onInfo).toHaveBeenLastCalledWith({
      purpose: 'login_info',
      status: 'unavailable',
    });
    bounded.dispose();
  });
});
