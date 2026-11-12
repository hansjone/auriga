import { afterEach, expect, it, vi } from 'vitest';
import { apiClient } from '../src/api';
import { opsRuntimeApi } from '../src/admin/opsRuntimeApi';
vi.mock('../src/api', () => ({ apiClient: { post: vi.fn() } }));
afterEach(() => {
  vi.restoreAllMocks();
  document.body.replaceChildren();
});
it('streams using a native form without a ticket URL, custom header or full-buffer fetch', async () => {
  vi.mocked(apiClient.post).mockResolvedValue({
    token: 'single-use-secret',
    csrf: 'session-csrf',
  });
  const fetch = vi.spyOn(globalThis, 'fetch');
  const submit = vi
    .spyOn(HTMLFormElement.prototype, 'submit')
    .mockImplementation(function (this: HTMLFormElement) {
      expect(this.method).toBe('post');
      expect(this.action).toContain('/backups/example-id/download');
      expect(this.action).not.toContain('secret');
      expect(new FormData(this).get('ticket')).toBe('single-use-secret');
      expect(new FormData(this).get('csrf')).toBe('session-csrf');
    });
  await opsRuntimeApi.download('example-id', new AbortController().signal);
  expect(submit).toHaveBeenCalledTimes(1);
  expect(fetch).not.toHaveBeenCalled();
  expect(document.querySelector('form')).toBeNull();
});

it.each([403, 404, 503])(
  'reports a native iframe error response %s without echoing its body',
  async (status) => {
    vi.mocked(apiClient.post).mockResolvedValue({
      token: 'secret',
      csrf: 'csrf',
    });
    vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {});
    const failed = vi.fn();
    await opsRuntimeApi.download(
      'backup',
      new AbortController().signal,
      failed,
    );
    const frame = document.querySelector('iframe')!;
    Object.defineProperty(frame, 'contentDocument', {
      value: {
        URL: 'http://localhost/api/error',
        body: {
          textContent: JSON.stringify({ status, detail: 'must-not-echo' }),
        },
      },
    });
    frame.dispatchEvent(new Event('load'));
    expect(failed).toHaveBeenCalledOnce();
    expect(failed.mock.calls[0]).toEqual([]);
  },
);

it('ignores late iframe failures after the page aborts', async () => {
  vi.mocked(apiClient.post).mockResolvedValue({
    token: 'secret',
    csrf: 'csrf',
  });
  vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {});
  const controller = new AbortController(),
    failed = vi.fn();
  await opsRuntimeApi.download('backup', controller.signal, failed);
  controller.abort();
  document.querySelector('iframe')!.dispatchEvent(new Event('error'));
  expect(failed).not.toHaveBeenCalled();
});
