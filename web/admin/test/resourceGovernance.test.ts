import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import { adminApi } from '../src/admin/api';
import { resourceApi } from '../src/admin/resourceApi';
import ResourceGovernanceView from '../src/views/resources/ResourceGovernanceView.vue';
import { router } from '../src/router';

const membership = (permissions: string[]) =>
  vi
    .spyOn(adminApi, 'membership')
    .mockResolvedValue({ permissions } as Awaited<
      ReturnType<typeof adminApi.membership>
    >);
function open(phase: 'review' | 'published' | 'distribution') {
  return mount(ResourceGovernanceView, {
    props: { phase },
    global: {
      plugins: [createAurigaI18n('zh-CN')],
      stubs: { RouterLink: true },
    },
  });
}
afterEach(() => vi.restoreAllMocks());
describe('resource governance', () => {
  it('routes all three entries to the real view', () => {
    for (const phase of ['review', 'published', 'distribution'])
      expect(
        router.resolve('/resources/' + phase).matched.at(-1)?.components
          ?.default,
      ).toBe(ResourceGovernanceView);
  });
  it('does not read a review queue without explicit permission', async () => {
    membership(['resource.packages.read']);
    const read = vi.spyOn(resourceApi, 'governance');
    const view = open('review');
    await flushPromises();
    expect(read).not.toHaveBeenCalled();
    expect(view.find('[role="alert"]').exists()).toBe(true);
    view.unmount();
  });
  it('reads real version metadata and paginates with the server cursor', async () => {
    membership(['resource.versions.review']);
    const read = vi
      .spyOn(resourceApi, 'governance')
      .mockResolvedValueOnce({ items: [], nextCursor: 'signed' })
      .mockResolvedValueOnce({ items: [] });
    const view = open('review');
    await flushPromises();
    await view.get('[data-testid="next"]').trigger('click');
    await flushPromises();
    expect(read).toHaveBeenLastCalledWith('review', 'signed');
    view.unmount();
  });
  it('clears old data and reports queue failures', async () => {
    membership(['resource.versions.publish']);
    vi.spyOn(resourceApi, 'governance').mockRejectedValue(new Error('offline'));
    const view = open('published');
    await flushPromises();
    expect(view.find('[role="alert"]').exists()).toBe(true);
    expect(view.findAll('tbody tr')).toHaveLength(0);
    view.unmount();
  });
  it('loads the real distribution queue and offers intent registration', async () => {
    membership(['resource.distributions.manage']);
    const read = vi.spyOn(resourceApi, 'governance');
    const reports = vi
      .spyOn(resourceApi, 'distributions')
      .mockResolvedValue({ items: [] });
    const view = open('distribution');
    await flushPromises();
    expect(read).not.toHaveBeenCalled();
    expect(reports).toHaveBeenCalledWith(undefined);
    expect(view.find('form').exists()).toBe(true);
    expect(view.findAll('tbody tr')).toHaveLength(0);
    view.unmount();
  });
});
