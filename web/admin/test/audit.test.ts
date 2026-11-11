import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import AuditView from '../src/views/AuditView.vue';
import { auditApi } from '../src/admin/auditApi';
import { adminApi } from '../src/admin/api';
const row = {
  id: 'event',
  tenantId: 'tenant',
  time: '2026-11-01T00:00:00Z',
  actor: { id: 'alice', ip: '192.0.2.10' },
  action: 'org.member.update',
  result: 'success' as const,
  source: 'auriga/org',
  traceId: 'trace',
};
const mountView = () =>
  mount(AuditView, { global: { plugins: [createAurigaI18n('zh-CN')] } });
afterEach(() => vi.restoreAllMocks());
it('lists, filters, pages and opens structured details', async () => {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions: ['audit.events.read'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  const list = vi
    .spyOn(auditApi, 'list')
    .mockResolvedValue({ items: [row], nextCursor: 'next' });
  vi.spyOn(auditApi, 'detail').mockResolvedValue(row);
  const w = mountView();
  await flushPromises();
  expect(w.text()).toContain('alice');
  await w.get('[data-next]').trigger('click');
  await flushPromises();
  expect(list.mock.calls.at(-1)?.[1]).toBe('next');
  await w.get('[data-detail]').trigger('click');
  await flushPromises();
  expect(w.get('[role="dialog"]').text()).toContain('trace');
  await w.get('[name="actor"]').setValue('bob');
  expect(w.find('[role="dialog"]').exists()).toBe(false);
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(list.mock.calls.at(-1)?.[0].actor).toBe('bob');
  w.unmount();
});
it('denies without permission and never queries events', async () => {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions: [],
  } as unknown as Awaited<ReturnType<typeof adminApi.membership>>);
  const list = vi.spyOn(auditApi, 'list');
  const w = mountView();
  await flushPromises();
  expect(w.text()).toContain('无权查看审计日志');
  expect(list).not.toHaveBeenCalled();
  w.unmount();
});
it('exports the applied filters and offers a CSV download', async () => {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions: ['audit.events.read'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.spyOn(auditApi, 'list').mockResolvedValue({ items: [row] });
  const exp = vi
    .spyOn(auditApi, 'export')
    .mockResolvedValue({ csv: 'time,actor\r\nnow,alice' });
  const create = vi.fn().mockReturnValue('blob:test');
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = create;
      static revokeObjectURL = vi.fn();
    },
  );
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(() => {});
  const w = mountView();
  await flushPromises();
  await w.get('[data-export]').trigger('click');
  await flushPromises();
  expect(exp).toHaveBeenCalled();
  expect(create).toHaveBeenCalled();
  expect(click).toHaveBeenCalled();
  w.unmount();
  vi.unstubAllGlobals();
});
