import { afterEach, describe, expect, it, vi } from 'vitest';
import { createUacLoginView } from '../src/native-login/uac';
import { createIamApi } from '../src/api/iam';
import { createApiClient } from '../src/api/client';

const challenge = {
  id: '11111111-1111-4111-8111-111111111111',
  transactionId: '22222222-2222-4222-8222-222222222222',
  qrContent: 'synthetic-public-qr',
  expiresAt: new Date(Date.now() + 120000).toISOString(),
  pollIntervalSeconds: 2,
};
const tick = async () => {
  for (let n = 0; n < 20; n++) await Promise.resolve();
};
function setup() {
  const calls: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    void _init;
    const path = String(input);
    calls.push(path);
    const body = path.endsWith('/complete')
      ? { state: 'authenticated', transactionId: challenge.transactionId }
      : path.endsWith(challenge.id)
        ? { ...challenge, state: 'waiting' }
        : challenge;
    return new Response(JSON.stringify(body), {
      headers: { 'Content-Type': 'application/json' },
    });
  });
  const root = document.createElement('div');
  document.body.append(root);
  const api = createIamApi(createApiClient({ fetch }));
  const onComplete = vi.fn();
  return { root, api, onComplete, calls, fetch };
}
afterEach(() => {
  vi.useRealTimers();
  document.body.replaceChildren();
});
describe('UAC native login', () => {
  it('renders a real QR SVG and reads status without completing waiting attempts', async () => {
    const ctx = setup();
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    expect(
      ctx.root.querySelector('svg path')?.getAttribute('d')?.length,
    ).toBeGreaterThan(100);
    expect(ctx.calls.filter((p) => p.endsWith('/complete'))).toHaveLength(0);
    expect(ctx.onComplete).not.toHaveBeenCalled();
    view.dispose();
  });
  it('cancels a challenge arriving after disposal and never completes it', async () => {
    const ctx = setup();
    let resolve!: (r: Response) => void;
    ctx.fetch.mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        }),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    view.dispose();
    resolve(
      new Response(JSON.stringify(challenge), {
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await tick();
    expect(
      ctx.fetch.mock.calls.some(([, init]) => init?.method === 'DELETE'),
    ).toBe(true);
    expect(ctx.onComplete).not.toHaveBeenCalled();
    expect(ctx.root.children).toHaveLength(0);
  });
  it('completes only after verified and clears the QR before callback', async () => {
    const ctx = setup();
    ctx.fetch.mockImplementation(
      async (input) =>
        new Response(
          JSON.stringify(
            String(input).endsWith('/complete')
              ? {
                  state: 'authenticated',
                  transactionId: challenge.transactionId,
                }
              : String(input).endsWith(challenge.id)
                ? { ...challenge, state: 'verified' }
                : challenge,
          ),
          { headers: { 'Content-Type': 'application/json' } },
        ),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    await vi.waitFor(() => expect(ctx.onComplete).toHaveBeenCalledOnce());
    expect(ctx.root.querySelector('svg')).toBeNull();
    view.dispose();
  });
  it.each([
    { purpose: 'device' as const, autoSuppressed: false },
    { purpose: 'portal' as const, autoSuppressed: true },
  ])('does not automatically invoke SSO for %j', async (binding) => {
    const ctx = setup();
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
      navigation: {
        ...binding,
        autoEligible: true,
        navigationProof: 'p'.repeat(43),
        transactionId: challenge.transactionId,
        expiresAt: challenge.expiresAt,
      },
    });
    await tick();
    expect(ctx.calls.some((p) => p.endsWith('/sso'))).toBe(false);
    view.dispose();
  });
  it('clears QR on expiry and never polls or completes afterwards', async () => {
    vi.useFakeTimers();
    const ctx = setup();
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    await vi.advanceTimersByTimeAsync(120001);
    expect(ctx.root.querySelector('svg')).toBeNull();
    expect(ctx.onComplete).not.toHaveBeenCalled();
    view.dispose();
  });
});

const navigation = {
  purpose: 'portal' as const,
  autoSuppressed: false,
  autoEligible: true,
  navigationProof: 'p'.repeat(43),
  transactionId: challenge.transactionId,
  expiresAt: challenge.expiresAt,
};
function action(root: HTMLElement, name: string) {
  const button = [...root.querySelectorAll('button')].find(
    (value) => value.textContent === name,
  );
  expect(button).toBeDefined();
  return button!;
}
describe('SSO and terminal page states', () => {
  it.each([
    'expired',
    'denied',
    'unavailable',
    'cancelled',
    'failed',
    'scanned',
  ] as const)(
    'displays %s without exposing a raw upstream message',
    async (state) => {
      const ctx = setup();
      ctx.fetch.mockImplementation(
        async (input) =>
          new Response(
            JSON.stringify(
              String(input).endsWith(challenge.id)
                ? { ...challenge, state, message: 'SECRET-UPSTREAM' }
                : challenge,
            ),
            { headers: { 'Content-Type': 'application/json' } },
          ),
      );
      const view = createUacLoginView(ctx.root, {
        ...ctx,
        providerId: 'provider',
      });
      await tick();
      await vi.waitFor(() => expect(ctx.root.dataset.uacState).toBe(state));
      expect(ctx.root.textContent).not.toContain('SECRET-UPSTREAM');
      if (state !== 'scanned') expect(ctx.root.querySelector('svg')).toBeNull();
      await view.dispose();
    },
  );
  it.each(['authenticated', 'additional_step_required'] as const)(
    'finishes eligible SSO as %s without creating QR challenges',
    async (state) => {
      const ctx = setup();
      ctx.fetch.mockImplementation(
        async () =>
          new Response(
            JSON.stringify({ state, transactionId: challenge.transactionId }),
            { headers: { 'Content-Type': 'application/json' } },
          ),
      );
      const view = createUacLoginView(ctx.root, {
        ...ctx,
        providerId: 'provider',
        navigation,
      });
      await vi.waitFor(() => expect(ctx.onComplete).toHaveBeenCalledOnce());
      expect(ctx.fetch.mock.calls.map(([path]) => path)).toEqual([
        '/api/iam/v1/login/uac/sso',
      ]);
      expect(ctx.root.dataset.uacState).toBe(state);
      expect(action(ctx.root, '重新获取二维码').hidden).toBe(true);
      await view.dispose();
    },
  );
  it('falls back from SSO to QR and emits only allowlisted page events', async () => {
    const ctx = setup(),
      events = vi.fn();
    ctx.fetch.mockImplementationOnce(
      async () =>
        new Response(
          JSON.stringify({
            state: 'scan_required',
            transactionId: challenge.transactionId,
          }),
          { headers: { 'Content-Type': 'application/json' } },
        ),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
      navigation,
      onEvent: events,
      resolveCorrelationId: () => 'synthetic-correlation',
    });
    await vi.waitFor(() => expect(ctx.root.dataset.uacState).toBe('waiting'));
    expect(ctx.fetch.mock.calls[0]?.[0]).toBe('/api/iam/v1/login/uac/sso');
    expect(
      ctx.root.querySelector('[data-testid=uac-reference]')?.textContent,
    ).toContain('synthetic-correlation');
    expect(events).toHaveBeenCalledWith({
      stage: 'sso',
      phase: 'end',
      result: 'scan_required',
      correlationId: 'synthetic-correlation',
    });
    const serialized = JSON.stringify(events.mock.calls);
    expect(serialized).not.toContain(navigation.navigationProof);
    expect(serialized).not.toContain(challenge.qrContent);
    await view.dispose();
  });
  it('does not consume a suppressed or device proof before an explicit continue click', async () => {
    for (const purpose of ['portal', 'device'] as const) {
      const ctx = setup();
      ctx.fetch.mockImplementation(
        async () =>
          new Response(
            JSON.stringify({
              state: 'authenticated',
              transactionId: challenge.transactionId,
            }),
            { headers: { 'Content-Type': 'application/json' } },
          ),
      );
      const view = createUacLoginView(ctx.root, {
        ...ctx,
        providerId: 'provider',
        navigation: { ...navigation, purpose, autoSuppressed: true },
      });
      await tick();
      expect(ctx.fetch).not.toHaveBeenCalled();
      expect(ctx.root.dataset.uacState).toBe('continue_required');
      action(ctx.root, '继续企业登录').click();
      await vi.waitFor(() => expect(ctx.onComplete).toHaveBeenCalledOnce());
      expect(JSON.parse(String(ctx.fetch.mock.calls[0]?.[1]?.body)).mode).toBe(
        'continue',
      );
      await view.dispose();
    }
  });
  it('does not retry a security rejection and never displays error detail', async () => {
    const ctx = setup();
    ctx.fetch.mockResolvedValue(
      new Response(
        JSON.stringify({
          status: 403,
          code: 'common.forbidden',
          title: 'SECRET-UPSTREAM',
          detail: 'SECRET-UPSTREAM',
        }),
        {
          status: 403,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
      navigation,
    });
    await vi.waitFor(() =>
      expect(ctx.root.dataset.uacState).toBe('invalid_request'),
    );
    expect(ctx.fetch).toHaveBeenCalledOnce();
    expect(ctx.root.textContent).not.toContain('SECRET-UPSTREAM');
    await view.dispose();
  });
  it('keeps a cancelled in-flight SSO result uncertain and does not complete or create a replacement', async () => {
    const ctx = setup();
    let resolve!: (value: Response) => void;
    ctx.fetch.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
      navigation,
    });
    await tick();
    expect(ctx.root.dataset.uacState).toBe('sso');
    action(ctx.root, '取消').click();
    resolve(
      new Response(
        JSON.stringify({
          state: 'authenticated',
          transactionId: challenge.transactionId,
        }),
        { headers: { 'Content-Type': 'application/json' } },
      ),
    );
    await tick();
    expect(ctx.root.dataset.uacState).toBe('uncertain');
    expect(ctx.onComplete).not.toHaveBeenCalled();
    action(ctx.root, '重新获取二维码').click();
    await tick();
    expect(ctx.fetch).toHaveBeenCalledOnce();
    await view.dispose();
  });
  it('holds replacement QR creation until late challenge cancellation has settled', async () => {
    const ctx = setup();
    let create!: (value: Response) => void, remove!: (value: Response) => void;
    ctx.fetch.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          create = done;
        }),
    );
    ctx.fetch.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          remove = done;
        }),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    action(ctx.root, '取消').click();
    expect(action(ctx.root, '重新获取二维码').disabled).toBe(true);
    create(
      new Response(JSON.stringify(challenge), {
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await tick();
    expect(action(ctx.root, '重新获取二维码').disabled).toBe(true);
    remove(new Response(null, { status: 204 }));
    await tick();
    expect(action(ctx.root, '重新获取二维码').disabled).toBe(false);
    await view.dispose();
  });
  it('honors Retry-After without automatically resubmitting a write', async () => {
    vi.useFakeTimers();
    const ctx = setup();
    ctx.fetch.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          status: 429,
          title: 'Limited',
          code: 'common.rate_limited',
        }),
        {
          status: 429,
          headers: {
            'Content-Type': 'application/problem+json',
            'Retry-After': '5',
          },
        },
      ),
    );
    const view = createUacLoginView(ctx.root, {
      ...ctx,
      providerId: 'provider',
    });
    await tick();
    expect(ctx.root.dataset.uacState).toBe('limited');
    expect(action(ctx.root, '重新获取二维码').disabled).toBe(true);
    await vi.advanceTimersByTimeAsync(5000);
    expect(action(ctx.root, '重新获取二维码').disabled).toBe(false);
    expect(ctx.fetch).toHaveBeenCalledOnce();
    await view.dispose();
  });
});
