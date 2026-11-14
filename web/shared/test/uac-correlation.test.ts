import { describe, expect, it, vi } from 'vitest';
import { createApiClient } from '../src/api/client';
import { createIamApi } from '../src/api/iam';
import { createUacLoginView } from '../src/native-login/uac';
import { navigationCorrelationId } from '../src/api/correlation';

describe('K2 UAC correlation header', () => {
  it('uses the response header instead of an unrelated body field or client header', async () => {
    const id = globalThis.crypto.randomUUID(),
      forged = globalThis.crypto.randomUUID();
    const fetcher = vi.fn<typeof fetch>(
      async () =>
        new Response(
          JSON.stringify({
            state: 'scan_required',
            transactionId: globalThis.crypto.randomUUID(),
            correlationId: forged,
          }),
          {
            headers: {
              'Content-Type': 'application/json',
              'X-Auriga-Correlation-Id': id,
            },
          },
        ),
    );
    const client = createApiClient({ fetch: fetcher });
    const response = await client.post(
      '/api/iam/v1/login/uac/sso',
      {},
      { headers: { 'X-Auriga-Correlation-Id': forged } },
    );
    expect(client.getCorrelationId?.(response)).toBe(id);
    expect(
      new Headers(fetcher.mock.calls[0]?.[1]?.headers).has(
        'X-Auriga-Correlation-Id',
      ),
    ).toBe(false);
    expect(fetcher.mock.calls[0]?.[1]?.redirect).toBe('error');
  });
  it('retains a safe header ID for a failed SSO response and displays it without error secrets', async () => {
    const id = globalThis.crypto.randomUUID(),
      events = vi.fn();
    const api = createIamApi(
      createApiClient({
        fetch: async () =>
          new Response(
            JSON.stringify({
              status: 403,
              title: 'SECRET',
              detail: 'SECRET',
              code: 'common.forbidden',
            }),
            {
              status: 403,
              headers: {
                'Content-Type': 'application/problem+json',
                'X-Auriga-Correlation-Id': id,
              },
            },
          ),
      }),
    );
    const root = document.createElement('div');
    const view = createUacLoginView(root, {
      api,
      providerId: globalThis.crypto.randomUUID(),
      navigation: {
        purpose: 'portal',
        autoEligible: true,
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
      },
      onComplete: vi.fn(),
      onEvent: events,
    });
    await vi.waitFor(() =>
      expect(root.dataset.uacState).toBe('invalid_request'),
    );
    expect(root.textContent).toContain(id);
    expect(root.textContent).not.toContain('SECRET');
    expect(events).toHaveBeenCalledWith({
      stage: 'sso',
      phase: 'end',
      result: 'failure',
      correlationId: id,
    });
    await view.dispose();
  });
  it('rejects malformed or multi-value IDs and reads the initial navigation ID separately', async () => {
    const id = globalThis.crypto.randomUUID();
    document.head.insertAdjacentHTML(
      'beforeend',
      `<meta name="auriga-uac-correlation" content="${id}">`,
    );
    expect(navigationCorrelationId(document)).toBe(id);
    document.querySelector('meta[name=auriga-uac-correlation]')?.remove();
    const client = createApiClient({
      fetch: async () =>
        new Response('{}', {
          headers: {
            'Content-Type': 'application/json',
            'X-Auriga-Correlation-Id':
              id + ', ' + globalThis.crypto.randomUUID(),
          },
        }),
    });
    expect(
      client.getCorrelationId?.(
        await client.get('/api/iam/v1/login/uac/challenges'),
      ),
    ).toBeUndefined();
  });
});
