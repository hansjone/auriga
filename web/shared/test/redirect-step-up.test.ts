import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/problem';
import type { LoginTransaction } from '../src/api/iam';
import { startRedirectStepUp } from '../src/steps/redirectStepUp';

const tx: LoginTransaction = {
  id: 'tx',
  purpose: 'stepup',
  status: 'pending',
  provider: {
    id: 'provider',
    connectorType: 'oidc',
    displayName: { en: 'OIDC' },
  },
  step: {
    type: 'redirect',
    method: 'GET',
    url: 'https://idp.example.com/authorize?state=server-state',
  },
  expiresAt: '2099-01-01T00:00:00Z',
};
const completed: LoginTransaction = {
  ...tx,
  status: 'completed',
  step: { type: 'complete', redirectTo: '/overview?aurigaStepUp=nonce' },
};
function popup() {
  const child = {
    closed: false,
    close: vi.fn(),
    opener: globalThis,
    location: { href: 'about:blank', replace: vi.fn() },
    document: document.implementation.createHTMLDocument(),
  };
  const open = vi
    .spyOn(window, 'open')
    .mockReturnValue(child as unknown as Window);
  return { child, open };
}
afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});
describe('redirect step-up popup', () => {
  it('opens GET in an isolated popup and verifies the exact transaction', async () => {
    const { child, open } = popup();
    const read = vi.fn(async () => completed);
    const run = startRedirectStepUp(tx, '/overview?aurigaStepUp=nonce', read);
    await expect(run.done).resolves.toEqual(completed);
    expect(open).toHaveBeenCalledWith(
      'about:blank',
      '_blank',
      expect.any(String),
    );
    expect(child.opener).toBeNull();
    expect(child.location.replace).toHaveBeenCalledWith(
      'https://idp.example.com/authorize?state=server-state',
    );
    expect(read).toHaveBeenCalledWith('tx');
    expect(child.close).toHaveBeenCalledOnce();
  });
  it('submits POST redirect parameters only in the popup document', async () => {
    const { child } = popup();
    const submit = vi
      .spyOn(HTMLFormElement.prototype, 'submit')
      .mockImplementation(() => {});
    const run = startRedirectStepUp(
      {
        ...tx,
        step: {
          type: 'redirect',
          method: 'POST',
          url: 'https://idp.example.com/authorize',
          params: { SAMLRequest: 'opaque', RelayState: 'state' },
        },
      },
      '/overview?aurigaStepUp=nonce',
      async () => completed,
    );
    await run.done;
    expect(submit).toHaveBeenCalledOnce();
    const form = child.document.querySelector('form')!;
    expect(form.method).toBe('post');
    expect(form.action).toBe('https://idp.example.com/authorize');
    expect(
      form.querySelector('input[name="RelayState"]')?.getAttribute('value'),
    ).toBe('state');
    expect(document.querySelector('form')).toBeNull();
  });
  it('reports blocked popups without starting a poll', () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    const read = vi.fn();
    expect(() =>
      startRedirectStepUp(tx, '/overview?aurigaStepUp=nonce', read),
    ).toThrow('popupBlocked');
    expect(read).not.toHaveBeenCalled();
  });
  it.each([
    { ...completed, id: 'other' },
    { ...completed, purpose: 'login' as const },
    { ...completed, status: 'expired' as const },
  ])('rejects mismatched or expired transactions', async (value) => {
    popup();
    await expect(
      startRedirectStepUp(tx, '/overview?aurigaStepUp=nonce', async () => value)
        .done,
    ).rejects.toThrow('popupFailed');
  });
  it('requires the exact private return marker when IAM cleared its transaction cookie', async () => {
    vi.useFakeTimers();
    const { child } = popup();
    child.location.href = `${location.origin}/overview?aurigaStepUp=wrong`;
    const read = vi.fn(async () => {
      throw new ApiError({
        status: 404,
        code: 'common.not_found',
        title: 'Not found',
      });
    });
    const run = startRedirectStepUp(tx, '/overview?aurigaStepUp=nonce', read);
    const resolved = vi.fn();
    void run.done.then(resolved);
    await vi.advanceTimersByTimeAsync(1000);
    expect(resolved).not.toHaveBeenCalled();
    child.location.href = `${location.origin}/overview?aurigaStepUp=nonce`;
    await vi.advanceTimersByTimeAsync(1000);
    await expect(run.done).resolves.toBeNull();
  });
  it('cancels and ignores an in-flight late result', async () => {
    const { child } = popup();
    let finish!: (value: LoginTransaction) => void;
    const run = startRedirectStepUp(
      tx,
      '/overview?aurigaStepUp=nonce',
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const outcome = run.done.catch((error: unknown) => error);
    run.close();
    finish(completed);
    expect(await outcome).toMatchObject({ message: 'popupClosed' });
    expect(child.close).toHaveBeenCalledOnce();
  });
  it('does not accept a late result after the user closed the popup during a read', async () => {
    const { child } = popup();
    let finish!: (value: LoginTransaction) => void;
    const run = startRedirectStepUp(
      tx,
      '/overview?aurigaStepUp=nonce',
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const outcome = run.done.then(
      (value) => value,
      (error: unknown) => error,
    );
    child.closed = true;
    finish(completed);
    expect(await outcome).toMatchObject({ message: 'popupClosed' });
  });
  it('expires even if the last read never settles', async () => {
    vi.useFakeTimers();
    const { child } = popup();
    const run = startRedirectStepUp(
      { ...tx, expiresAt: new Date(Date.now() + 1000).toISOString() },
      '/overview?aurigaStepUp=nonce',
      () => new Promise(() => {}),
    );
    const outcome = run.done.catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(1001);
    expect(await outcome).toMatchObject({ message: 'popupExpired' });
    expect(child.close).toHaveBeenCalledOnce();
  });
});
