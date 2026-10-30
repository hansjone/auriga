import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter, RouterView } from 'vue-router';
import { createAurigaI18n, type LoginTransaction } from '@auriga/shared';
import InviteView from '../src/views/InviteView.vue';
import LoginView from '../src/views/LoginView.vue';
import { bindApiRouter } from '../src/api';

const provider = {
  id: 'team',
  connectorType: 'team-account',
  displayName: { 'zh-CN': '团队账号', en: 'Team account' },
};
const register: LoginTransaction['step'] = {
  type: 'form',
  formId: 'register',
  fields: [
    { name: 'username', kind: 'text', required: true },
    { name: 'displayName', kind: 'text', required: false },
    { name: 'password', kind: 'password', required: true },
  ],
};
const firstTotp: LoginTransaction['step'] = {
  type: 'form',
  formId: 'first_totp',
  fields: [
    {
      name: 'enrollment',
      kind: 'info',
      required: false,
      label: {
        en: 'Add this secret to your authenticator: TEST-ENROLLMENT',
        'zh-CN': '请将此密钥添加到验证器：TEST-ENROLLMENT',
      },
    },
    { name: 'otp', kind: 'otp', required: true },
  ],
};
function transaction(step: LoginTransaction['step']): LoginTransaction {
  return {
    id: 'invite-tx',
    status: step.type === 'complete' ? 'completed' : 'pending',
    purpose: 'login',
    provider,
    step,
    expiresAt: '2099-01-01T00:00:00Z',
  };
}
function json(body: unknown) {
  return new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json' },
  });
}
let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.unstubAllGlobals();
});
async function openInvite(code = 'invite-code') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/invite/:code', component: InviteView },
      { path: '/login', component: LoginView },
      { path: '/', component: { template: '<div>Home</div>' } },
    ],
  });
  bindApiRouter(router);
  await router.push(`/invite/${encodeURIComponent(code)}`);
  wrapper = mount(RouterView, {
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  return router;
}

describe('portal invitation flow', () => {
  it('carries the invitation through preview, registration and first TOTP to the portal', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        json({ valid: true, tenantName: 'Example', inviterName: 'Admin' }),
      )
      .mockResolvedValueOnce(json({ items: [provider] }))
      .mockResolvedValueOnce(json(transaction(register)))
      .mockResolvedValueOnce(json(transaction(firstTotp)))
      .mockResolvedValueOnce(
        json(transaction({ type: 'complete', redirectTo: '/' })),
      );
    vi.stubGlobal('fetch', fetch);
    const router = await openInvite('invite+code');
    expect(fetch.mock.calls[0]?.[0]).toBe(
      '/api/org/v1/invitations/preview/invite%2Bcode',
    );
    expect(wrapper!.text()).toContain('Example');
    await wrapper!.get('button').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({
      invitation: 'invite+code',
      returnTo: '/',
    });
    await wrapper!.get('[data-provider-id="team"]').trigger('click');
    await flushPromises();
    expect(JSON.parse(fetch.mock.calls[2]?.[1].body)).toEqual({
      providerId: 'team',
      purpose: 'login',
      returnTo: '/',
      invitationCode: 'invite+code',
    });
    await wrapper!.get('input[name="username"]').setValue('alice');
    await wrapper!.get('input[name="displayName"]').setValue('Alice');
    await wrapper!.get('input[name="password"]').setValue('test-password');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(fetch.mock.calls[3]?.[0]).toBe(
      '/api/iam/v1/login/transactions/invite-tx/advance',
    );
    expect(JSON.parse(fetch.mock.calls[3]?.[1].body)).toEqual({
      type: 'form',
      formId: 'register',
      values: {
        username: 'alice',
        displayName: 'Alice',
        password: 'test-password',
      },
    });
    expect(wrapper!.text()).toContain('TEST-ENROLLMENT');
    expect(router.currentRoute.value.path).toBe('/login');
    await wrapper!.get('input[name="otp"]').setValue('123456');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(JSON.parse(fetch.mock.calls[4]?.[1].body)).toEqual({
      type: 'form',
      formId: 'first_totp',
      values: { otp: '123456' },
    });
    expect(router.currentRoute.value.path).toBe('/');
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it.each(['expired', 'used', 'revoked', 'not_found'])(
    'does not start registration for a %s invitation',
    async (reason) => {
      const fetch = vi.fn().mockResolvedValue(json({ valid: false, reason }));
      vi.stubGlobal('fetch', fetch);
      const router = await openInvite();
      expect(wrapper!.text()).toContain('邀请无效或已失效');
      expect(wrapper!.find('button').exists()).toBe(false);
      expect(router.currentRoute.value.path).toBe('/invite/invite-code');
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );
});
