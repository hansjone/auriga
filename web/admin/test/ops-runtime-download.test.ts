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
