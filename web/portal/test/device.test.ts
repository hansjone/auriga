import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import DeviceView from '../src/views/DeviceView.vue';

const code = 'BCDFGHJK',
  providerId = globalThis.crypto.randomUUID();
const me = {
  account: {
    id: 'alice',
    tenantId: 'tenant',
    displayName: 'Alice',
    status: 'active',
  },
  session: { id: 'session', providerId, current: true, audience: 'portal' },
};
const device = {
  userCode: 'BCDF-GHJK',
  clientId: 'desktop',
  clientName: { en: 'Desktop' },
  scopes: ['auriga.connect'],
  device: { ip: '192.0.2.10', name: 'Synthetic desktop' },
  expiresAt: new Date(Date.now() + 120000).toISOString(),
};
const provider = {
  id: providerId,
  connectorType: 'uac',
  displayName: { en: 'Company' },
  order: 0,
  primaryStep: 'qr',
};
const json = (
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
const fetcher = vi.fn<typeof fetch>();
let wrapper: ReturnType<typeof mount> | undefined;
async function render(path = '/device/' + code) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/device/:userCode?', component: DeviceView }],
  });
  await router.push(path);
  vi.stubGlobal('fetch', fetcher);
  wrapper = mount(DeviceView, {
    global: {
      plugins: [router, createAurigaI18n('zh-CN')],
      stubs: { DesktopDownload: true },
    },
  });
  await flushPromises();
  return wrapper;
}
function action(text: string) {
  const button = wrapper!
    .findAll('button')
    .find((value) => value.text() === text);
  expect(button).toBeDefined();
  return button!;
}
afterEach(() => {
  document.querySelector('meta[name=auriga-uac-provider]')?.remove();
  wrapper?.unmount();
  wrapper = undefined;
  fetcher.mockReset();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  document.body.replaceChildren();
});
function ordinary() {
  fetcher.mockImplementation(async (input, init) => {
    const path = String(input);
    if (path === '/api/iam/v1/me') return json(me);
    if (path === '/api/iam/v1/device/' + code) return json(device);
    if (init?.method === 'POST') return new Response(null, { status: 204 });
    throw new Error('Unexpected request');
  });
}
describe('normal portal device confirmation', () => {
  it('drops pagehide results and reloads a restored document instead of reviving its old attempt', async () => {
    let resolve!: (value: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    await render();
    const reload = vi.fn();
    vi.stubGlobal('location', {
      href: globalThis.location.href,
      origin: globalThis.location.origin,
      reload,
    });
    globalThis.dispatchEvent(new Event('pagehide'));
    resolve(json(me));
    await flushPromises();
    expect(fetcher).toHaveBeenCalledOnce();
    const event = new Event('pageshow');
    Object.defineProperty(event, 'persisted', { value: true });
    globalThis.dispatchEvent(event);
    expect(reload).toHaveBeenCalledOnce();
  });
  it('requires matching manual input, shows the IP, and never creates a cloud instance', async () => {
    ordinary();
    await render();
    expect(wrapper!.text()).toContain('BCDF-GHJK');
    expect(wrapper!.text()).toContain('192.0.2.10');
    expect((action('确认批准设备').element as HTMLButtonElement).disabled).toBe(
      true,
    );
    await wrapper!.get('[data-testid=verify-user-code]').setValue('BCDF-WXZZ');
    expect((action('确认批准设备').element as HTMLButtonElement).disabled).toBe(
      true,
    );
    await wrapper!.get('[data-testid=verify-user-code]').setValue('bcdf-ghjk');
    await action('确认批准设备').trigger('click');
    await flushPromises();
    expect(wrapper!.text()).toContain('设备已批准');
    const writes = fetcher.mock.calls.filter(
      ([, init]) => init?.method === 'POST',
    );
    expect(writes).toHaveLength(1);
    expect(writes[0]?.[0]).toBe('/api/iam/v1/device/' + code + '/approve');
    expect(JSON.parse(String(writes[0]?.[1]?.body))).toEqual({});
    expect(
      fetcher.mock.calls.some(([path]) => String(path).includes('workspace')),
    ).toBe(false);
  });
  it('keeps anonymous device SSO explicit, then returns to manual confirmation with the header reference', async () => {
    const reference = globalThis.crypto.randomUUID();
    document.head.insertAdjacentHTML(
      'beforeend',
      `<meta name="auriga-uac-provider" content="${providerId}">`,
    );
    let signedIn = false;
    const context = document.createElement('script');
    context.type = 'application/json';
    context.id = 'auriga-uac-navigation';
    context.textContent = JSON.stringify({
      purpose: 'device',
      autoEligible: false,
      autoSuppressed: false,
      navigationProof: btoa(
        String.fromCharCode(
          ...globalThis.crypto.getRandomValues(new Uint8Array(32)),
        ),
      )
        .replaceAll('+', '-')
        .replaceAll('/', '_')
        .replaceAll('=', ''),
      transactionId: globalThis.crypto.randomUUID(),
      expiresAt: new Date(Date.now() + 120000).toISOString(),
    });
    document.body.append(context);
    fetcher.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/api/iam/v1/me')
        return signedIn
          ? json(me)
          : json(
              {
                status: 401,
                code: 'common.unauthenticated',
                title: 'Anonymous',
              },
              401,
            );
      if (path.endsWith('/providers')) return json({ items: [provider] });
      if (path.endsWith('/sso')) {
        signedIn = true;
        return json(
          {
            state: 'authenticated',
            transactionId: globalThis.crypto.randomUUID(),
          },
          200,
          { 'X-Auriga-Correlation-Id': reference },
        );
      }
      if (path === '/api/iam/v1/device/' + code) return json(device);
      throw new Error('Unexpected request');
    });
    await render();
    expect(
      fetcher.mock.calls.some(([path]) => String(path).endsWith('/sso')),
    ).toBe(false);
    await action('继续企业登录').trigger('click');
    await flushPromises();
    expect(wrapper!.text()).toContain('重新输入用户码');
    expect(wrapper!.get('[data-testid=device-correlation]').text()).toBe(
      reference,
    );
    expect((action('确认批准设备').element as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(
      fetcher.mock.calls.some(
        ([path]) =>
          String(path).includes('workspace') ||
          String(path).endsWith('/approve'),
      ),
    ).toBe(false);
  });
  it.each([false, true])(
    'checks the same session after step-up before the sole approval retry (changed=%s)',
    async (changed) => {
      let approvals = 0,
        verified = false;
      const tx = {
        id: globalThis.crypto.randomUUID(),
        purpose: 'stepup',
        status: 'pending',
        provider: { id: providerId },
        expiresAt: device.expiresAt,
        step: {
          type: 'form',
          formId: 'totp',
          fields: [
            {
              name: 'code',
              kind: 'otp',
              label: { en: 'TOTP' },
              required: true,
            },
          ],
        },
      };
      fetcher.mockImplementation(async (input) => {
        const path = String(input);
        if (path === '/api/iam/v1/me')
          return json(
            changed && verified
              ? { ...me, session: { ...me.session, id: 'other' } }
              : me,
          );
        if (path === '/api/iam/v1/device/' + code) return json(device);
        if (path.endsWith('/approve')) {
          approvals++;
          return approvals === 1
            ? json(
                { status: 403, code: 'iam.step_up_required', title: 'Verify' },
                403,
              )
            : new Response(null, { status: 204 });
        }
        if (path.endsWith('/transactions')) return json(tx);
        if (path.endsWith('/advance')) {
          verified = true;
          return json({
            ...tx,
            status: 'completed',
            step: { type: 'complete', redirectTo: '/device' },
          });
        }
        throw new Error('Unexpected request');
      });
      await render();
      await wrapper!.get('[data-testid=verify-user-code]').setValue(code);
      await action('确认批准设备').trigger('click');
      await flushPromises();
      expect(wrapper!.text()).toContain('额外验证');
      expect(approvals).toBe(1);
      await wrapper!.get('input[name=code]').setValue('123456');
      await wrapper!.get('fieldset form').trigger('submit');
      await flushPromises();
      expect(approvals).toBe(changed ? 1 : 2);
      expect(wrapper!.text()).toContain(
        changed ? '登录身份已变化' : '设备已批准',
      );
    },
  );
  it('rejects mismatched authoritative user codes and expires without an approval write', async () => {
    fetcher.mockImplementation(async (input) =>
      String(input).endsWith('/me')
        ? json(me)
        : json({ ...device, userCode: 'WXZZ-BCDF' }),
    );
    await render();
    expect(wrapper!.text()).toContain('已过期或不可用');
    expect(fetcher.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(
      false,
    );
  });
  it('expires a displayed request and disables confirmation', async () => {
    vi.useFakeTimers();
    ordinary();
    await render();
    await vi.advanceTimersByTimeAsync(120001);
    expect(wrapper!.text()).toContain('已过期或不可用');
    expect(wrapper!.find('[data-testid=verify-user-code]').exists()).toBe(
      false,
    );
  });
  it('supports explicit denial and discards a late approval response after unmount', async () => {
    ordinary();
    await render();
    await action('拒绝设备').trigger('click');
    await flushPromises();
    expect(wrapper!.text()).toContain('已拒绝设备请求');
    expect(
      fetcher.mock.calls.some(([path]) => String(path).endsWith('/deny')),
    ).toBe(true);
    wrapper!.unmount();
    wrapper = undefined;
    let resolve!: (response: Response) => void;
    ordinary();
    fetcher.mockImplementation(async (input) =>
      String(input).endsWith('/me')
        ? json(me)
        : String(input).endsWith('/approve')
          ? new Promise<Response>((done) => {
              resolve = done;
            })
          : json(device),
    );
    await render();
    await wrapper!.get('[data-testid=verify-user-code]').setValue(code);
    await action('确认批准设备').trigger('click');
    await flushPromises();
    wrapper!.unmount();
    wrapper = undefined;
    resolve(new Response(null, { status: 204 }));
    await flushPromises();
    expect(document.body.textContent).not.toContain('设备已批准');
  });
});
