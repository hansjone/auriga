import { describe, expect, it, vi } from 'vitest';
import { ApiError, createApiClient } from '@auriga/shared';
import { requestStepUp, withStepUp } from '../src/admin/stepUp';

const required = () =>
  new ApiError({ status: 403, code: 'iam.step_up_required', title: 'Step up' });
describe('administrative write continuation', () => {
  it('preserves the original denial when no verification UI is mounted', async () => {
    const raw = createApiClient();
    const denial = required();
    vi.spyOn(raw, 'request').mockRejectedValue(denial);
    await expect(withStepUp(raw, requestStepUp).post('/api/test')).rejects.toBe(
      denial,
    );
  });
  it('verifies once and retries the identical write once', async () => {
    const raw = createApiClient();
    const request = vi
      .spyOn(raw, 'request')
      .mockRejectedValueOnce(required())
      .mockResolvedValue({ ok: true });
    const verify = vi.fn(async () => {});
    const client = withStepUp(raw, verify);
    await expect(
      client.patch(
        '/api/test',
        { value: 1 },
        { headers: { 'If-Match': '"7"' } },
      ),
    ).resolves.toEqual({ ok: true });
    expect(verify).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledTimes(2);
    expect(request.mock.calls[1]).toEqual(request.mock.calls[0]);
  });
  it.each([403, 429, 503])(
    'does not replay ordinary HTTP %i',
    async (status) => {
      const raw = createApiClient();
      const request = vi
        .spyOn(raw, 'request')
        .mockRejectedValue(
          new ApiError({ status, code: 'common.forbidden', title: 'Error' }),
        );
      const verify = vi.fn();
      await expect(
        withStepUp(raw, verify).post('/api/test', {}),
      ).rejects.toBeInstanceOf(ApiError);
      expect(verify).not.toHaveBeenCalled();
      expect(request).toHaveBeenCalledTimes(1);
    },
  );
  it('does not loop when the retry still requires verification', async () => {
    const raw = createApiClient();
    const request = vi.spyOn(raw, 'request').mockRejectedValue(required());
    const verify = vi.fn(async () => {});
    await expect(
      withStepUp(raw, verify).delete('/api/test'),
    ).rejects.toBeInstanceOf(ApiError);
    expect(request).toHaveBeenCalledTimes(2);
    expect(verify).toHaveBeenCalledTimes(1);
  });
  it('does not replay cancelled verification or aborted operations', async () => {
    const raw = createApiClient();
    const request = vi.spyOn(raw, 'request').mockRejectedValue(required());
    await expect(
      withStepUp(raw, async () => {
        throw new Error('cancelled');
      }).post('/api/test'),
    ).rejects.toThrow('cancelled');
    expect(request).toHaveBeenCalledTimes(1);
    const controller = new AbortController();
    const client = withStepUp(raw, async () => {
      controller.abort();
    });
    await expect(
      client.post('/api/test', {}, { signal: controller.signal }),
    ).rejects.toThrow();
    expect(request).toHaveBeenCalledTimes(2);
  });
  it('does not retry reads even if their error requests step-up', async () => {
    const raw = createApiClient();
    const request = vi.spyOn(raw, 'request').mockRejectedValue(required());
    const verify = vi.fn();
    await expect(
      withStepUp(raw, verify).get('/api/test'),
    ).rejects.toBeInstanceOf(ApiError);
    expect(verify).not.toHaveBeenCalled();
    expect(request).toHaveBeenCalledTimes(1);
  });
  it('invalidates a second client when the session changes during verification', async () => {
    const raw = createApiClient();
    const request = vi.spyOn(raw, 'request').mockRejectedValue(required());
    const main = withStepUp(createApiClient(), async () => {});
    await expect(
      withStepUp(raw, async () => {
        main.invalidatePendingRequests();
      }).post('/api/test'),
    ).rejects.toBeInstanceOf(ApiError);
    expect(request).toHaveBeenCalledTimes(1);
  });
  it('shares one verification for concurrent denials', async () => {
    const raw = createApiClient();
    vi.spyOn(raw, 'request')
      .mockRejectedValueOnce(required())
      .mockRejectedValueOnce(required())
      .mockResolvedValue('done');
    let finish!: () => void;
    const verify = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const client = withStepUp(raw, verify);
    const first = client.post('/api/first'),
      second = client.post('/api/second');
    await Promise.resolve();
    expect(verify).toHaveBeenCalledTimes(1);
    finish();
    await expect(Promise.all([first, second])).resolves.toEqual([
      'done',
      'done',
    ]);
  });
});
