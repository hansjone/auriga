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
