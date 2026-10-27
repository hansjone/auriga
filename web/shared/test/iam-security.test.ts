import { describe, expect, it, vi } from 'vitest';
import { createApiClient } from '../src/api/client';
import { createIamApi } from '../src/api/iam';

describe('IAM account security API', () => {
  it('uses the existing enrollment, password and session endpoints with CSRF', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response('{}', {
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    const api = createIamApi(
      createApiClient({ fetch, getCookie: () => 'auriga_csrf=test' }),
    );
    await api.beginTotpEnrollment();
    await api.confirmTotpEnrollment('enrollment/id', { code: '123456' });
    await api.changePassword({ currentPassword: 'old', newPassword: 'new' });
    await api.listMySessions();
    await api.revokeOtherSessions();
    expect(
      fetch.mock.calls.map(([path, init]) => [path, init?.method]),
    ).toEqual([
      ['/api/iam/v1/me/mfa/totp/enrollment', 'POST'],
      ['/api/iam/v1/me/mfa/totp/enrollment/enrollment%2Fid/confirm', 'POST'],
      ['/api/iam/v1/me/password', 'PUT'],
      ['/api/iam/v1/me/sessions', 'GET'],
      ['/api/iam/v1/me/sessions/revoke-others', 'POST'],
    ]);
    expect(
      new Headers(fetch.mock.calls[0]?.[1]?.headers).get('X-Auriga-CSRF'),
    ).toBe('test');
  });
});
