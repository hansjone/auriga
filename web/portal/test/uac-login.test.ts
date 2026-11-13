import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import LoginView from '../src/views/LoginView.vue';
import { bindApiRouter } from '../src/api';

const id = '11111111-1111-4111-8111-111111111111';
const provider = {
  id,
  connectorType: 'uac',
  displayName: { en: 'Company', 'zh-CN': '企业登录' },
  order: 0,
  primaryStep: 'qr',
};
const challenge = {
  id,
  transactionId: id,
  qrContent: 'synthetic-public-qr',
  expiresAt: '2099-01-01T00:00:00Z',
  pollIntervalSeconds: 2,
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
async function render() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/login', component: LoginView }],
  });
  await router.push('/login');
  const navigate = vi.fn();
  bindApiRouter(router, navigate);
  return mount(LoginView, {
    global: {
      plugins: [router, createAurigaI18n('zh-CN')],
      stubs: { DesktopDownload: true },
    },
  });
}
function context() {
  const node = document.createElement('script');
  node.type = 'application/json';
  node.id = 'auriga-uac-navigation';
  node.textContent = JSON.stringify({
    purpose: 'portal',
    navigationProof: 'p'.repeat(43),
    transactionId: id,
    expiresAt: '2099-01-01T00:00:00Z',
    autoEligible: true,
    autoSuppressed: false,
  });
  document.body.append(node);
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});
describe('portal UAC normal entry', () => {
  it('does not expose unknown problem fields through the page or translation diagnostics', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        json(
          {
            status: 503,
            code: 'SECRET-UPSTREAM',
            title: 'SECRET-UPSTREAM',
            detail: 'SECRET-UPSTREAM',
          },
          503,
        ),
      ),
    );
    const wrapper = await render();
    await flushPromises();
    expect(wrapper.text()).not.toContain('SECRET-UPSTREAM');
    expect(JSON.stringify(warn.mock.calls)).not.toContain('SECRET-UPSTREAM');
    wrapper.unmount();
  });
  it('uses server navigation context for SSO, falls back to QR, and cancels before returning to providers', async () => {
    context();
    const calls: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const path = String(input);
        calls.push(path);
        if (path.endsWith('/providers')) return json({ items: [provider] });
        if (path.endsWith('/sso'))
          return json({ state: 'scan_required', transactionId: id });
        if (init?.method === 'DELETE')
          return new Response(null, { status: 204 });
        if (path.endsWith('/challenges')) return json(challenge);
        if (path.endsWith(id)) return json({ ...challenge, state: 'waiting' });
        throw new Error('Unexpected endpoint');
      }),
    );
    const wrapper = await render();
    await flushPromises();
    expect(wrapper.get('[data-uac-state]').attributes('data-uac-state')).toBe(
      'waiting',
    );
    expect(wrapper.find('svg').exists()).toBe(true);
    expect(document.getElementById('auriga-uac-navigation')).toBeNull();
    expect(calls.slice(0, 3)).toEqual([
      '/api/iam/v1/login/providers',
      '/api/iam/v1/login/uac/sso',
      '/api/iam/v1/login/uac/challenges',
    ]);
    expect(wrapper.emitted('authEvent')?.length).toBeGreaterThan(0);
    await wrapper.get('button.auriga-login-back').trigger('click');
    await flushPromises();
    expect(calls.at(-1)).toBe('/api/iam/v1/login/uac/challenges/' + id);
    expect(wrapper.find('[data-testid=provider-select]').exists()).toBe(true);
    wrapper.unmount();
  });
  it('continues additional verification and then opens the fixed workspace entry', async () => {
    context();
    const assign = vi.fn();
    vi.stubGlobal('location', { href: 'http://localhost/login', assign });
    const tx = {
      id,
      purpose: 'login',
      status: 'pending',
      provider,
      expiresAt: '2099-01-01T00:00:00Z',
      step: {
        type: 'form',
        formId: 'totp',
        fields: [
          { name: 'code', kind: 'otp', required: true, label: { en: 'TOTP' } },
        ],
      },
    };
    const calls: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const path = String(input);
        calls.push(path);
        if (path.endsWith('/providers')) return json({ items: [provider] });
        if (path.endsWith('/sso'))
          return json({ state: 'additional_step_required', transactionId: id });
        if (path.endsWith('/advance'))
          return json({
            ...tx,
            status: 'completed',
            step: {
              type: 'complete',
              redirectTo: 'https://untrusted.example.com/',
            },
          });
        if (path.endsWith('/transactions/' + id)) return json(tx);
        throw new Error('Unexpected endpoint');
      }),
    );
    const wrapper = await render();
    await flushPromises();
    expect(assign).not.toHaveBeenCalled();
    expect(calls.some((path) => path.includes('/workspace/'))).toBe(false);
    await wrapper.get('input[name=code]').setValue('123456');
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(assign).toHaveBeenCalledExactlyOnceWith('/instance-login.html');
    wrapper.unmount();
  });
  it('does not revive a login page when a provider list arrives after unmount', async () => {
    let resolve!: (response: Response) => void;
    const fetcher = vi.fn<typeof fetch>(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    vi.stubGlobal('fetch', fetcher);
    const wrapper = await render();
    wrapper.unmount();
    resolve(json({ items: [provider] }));
    await flushPromises();
    expect(fetcher).toHaveBeenCalledOnce();
    expect(document.querySelector('svg')).toBeNull();
  });
});
