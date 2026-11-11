import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import { adminApi } from '../src/admin/api';
import { resourceApi, type ResourceVersion } from '../src/admin/resourceApi';
import ResourceGovernanceView from '../src/views/resources/ResourceGovernanceView.vue';

const version: ResourceVersion = {
  id: 'v1',
  packageId: 'p1',
  version: '1.0.0',
  digest: 'sha256:' + 'a'.repeat(64),
  sizeBytes: 100,
  mediaType: 'application/gzip',
  state: 'pending_review',
  revision: 2,
  createdAt: '2026-11-11T00:00:00Z',
};
const detail = {
  resourcePackage: {
    id: 'p1',
    tenantId: 't1',
    ownerId: 'author',
    name: 'example',
    kind: 'plugin' as const,
    level: 'l3' as const,
    visibility: 'tenant' as const,
    groupIds: [],
    revision: 2,
    enabled: false,
    defaultVersionId: null,
    createdAt: version.createdAt,
  },
  version,
  authorId: 'author',
  metadata: {
    version: '1.0.0',
    expectedDigest: version.digest,
    manifest: {
      name: 'example',
      dshVersionRange: '*',
      os: 'any' as const,
      arch: 'any' as const,
    },
    permissions: ['files.read'],
    changeNotes: 'Fix archive',
  },
  files: [{ path: 'package/index.js', digest: version.digest, sizeBytes: 20 }],
  reviews: [
    {
      review: {
        id: 'r1',
        packageId: 'p1',
        versionId: 'v1',
        digest: version.digest,
        versionRevision: 2,
        state: 'pending' as const,
        revision: 1,
      },
      reason: 'Please review',
    },
  ],
};
const open = (phase: 'review' | 'published' | 'distribution' = 'review') =>
  mount(ResourceGovernanceView, {
    props: { phase },
    global: {
      plugins: [createAurigaI18n('zh-CN')],
      stubs: {
        RouterLink: true,
        ActionConfirm: {
          props: ['message', 'busy'],
          template:
            '<div v-if="message"><button data-testid="confirm-action" @click="$emit(\'confirm\')">Confirm</button></div>',
        },
      },
    },
  });
function membership(id = 'reviewer') {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    member: { id },
    permissions: [
      'resource.versions.review',
      'resource.versions.publish',
      'resource.distributions.manage',
    ],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
}
afterEach(() => vi.restoreAllMocks());

it('loads archive evidence and binds approval to exact digest, version and review ETag', async () => {
  membership();
  vi.spyOn(resourceApi, 'governance').mockResolvedValue({ items: [version] });
  vi.spyOn(resourceApi, 'governanceDetail').mockResolvedValue(detail);
  const decide = vi.spyOn(resourceApi, 'decide').mockResolvedValue({
    ...detail.reviews[0]!.review,
    state: 'approved',
    revision: 2,
  });
  const view = open();
  await flushPromises();
  await view.get('[data-testid="detail"]').trigger('click');
  await flushPromises();
  expect(view.text()).toContain('package/index.js');
  expect(view.text()).toContain('files.read');
  expect(
    view.get('[data-testid="approve"]').attributes('disabled'),
  ).toBeDefined();
  await view.get('[data-testid="reason"]').setValue('Checked files');
  await view.get('[data-testid="approve"]').trigger('click');
  expect(decide).not.toHaveBeenCalled();
  await view.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(decide).toHaveBeenCalledWith(
    'r1',
    {
      decision: 'approve',
      reason: 'Checked files',
      versionRevision: 2,
      digest: version.digest,
    },
    '"1"',
    expect.any(String),
  );
  view.unmount();
});
it('prevents authors from preparing their own review', async () => {
  membership('author');
  vi.spyOn(resourceApi, 'governance').mockResolvedValue({ items: [version] });
  vi.spyOn(resourceApi, 'governanceDetail').mockResolvedValue(detail);
  const view = open();
  await flushPromises();
  await view.get('[data-testid="detail"]').trigger('click');
  await flushPromises();
  expect(view.find('[data-testid="approve"]').exists()).toBe(false);
  view.unmount();
});
it('hides withdrawal actions after the version has been withdrawn', async () => {
  membership();
  const withdrawn = { ...version, state: 'withdrawn' as const, revision: 5 };
  vi.spyOn(resourceApi, 'governance').mockResolvedValue({ items: [withdrawn] });
  vi.spyOn(resourceApi, 'governanceDetail').mockResolvedValue({
    ...detail,
    version: withdrawn,
  });
  const view = open('published');
  await flushPromises();
  await view.get('[data-testid="detail"]').trigger('click');
  await flushPromises();
  expect(view.find('.market-danger').exists()).toBe(false);
  view.unmount();
});

it('shows actual pending generations without inventing a successful application', async () => {
  membership();
  vi.spyOn(resourceApi, 'distributions').mockResolvedValue({
    items: [
      {
        id: 'd1',
        instanceId: 'i1',
        ownerId: 'o1',
        selectionRevision: 7,
        state: 'applying',
        revision: 2,
        generation: 5,
        observedGeneration: 4,
        ready: false,
      },
    ],
  });
  const view = open('distribution');
  await flushPromises();
  expect(view.text()).toContain('应用中');
  expect(view.text()).toContain('5 / 4');
  expect(view.get('tbody').text()).not.toContain('已生效');
  view.unmount();
});

it('recovers a rejected draft after refresh and confirms reset with its exact revision', async () => {
  membership('author');
  vi.mocked(adminApi.membership).mockResolvedValue({
    member: { id: 'author' },
    permissions: [
      'resource.versions.review',
      'resource.packages.upload',
      'resource.versions.submit',
    ],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.spyOn(resourceApi, 'governance').mockResolvedValue({ items: [] });
  vi.spyOn(resourceApi, 'get').mockResolvedValue({
    data: detail.resourcePackage,
    etag: '"2"',
  });
  vi.spyOn(resourceApi, 'version').mockResolvedValue({
    ...version,
    state: 'rejected',
    revision: 3,
  });
  const reset = vi
    .spyOn(resourceApi, 'draft')
    .mockResolvedValue({ ...version, state: 'draft', revision: 4 });
  const view = open();
  await flushPromises();
  const form = view.findAll('form')[1]!;
  const fields = form.findAll('input');
  await fields[0]!.setValue('p1');
  await fields[1]!.setValue('v1');
  await form.trigger('submit');
  await flushPromises();
  await view.get('div.market-form textarea').setValue('Corrected notes');
  const button = view
    .findAll('button')
    .find((b) => b.text() === '退回草稿后重新送审')!;
  await button.trigger('click');
  expect(reset).not.toHaveBeenCalled();
  await view.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(reset).toHaveBeenCalledWith(
    'p1',
    'v1',
    'Corrected notes',
    '"3"',
    expect.any(String),
  );
  view.unmount();
});

it('labels a recovered private personal draft as unreviewed before confirming its signature', async () => {
  membership('author');
  vi.mocked(adminApi.membership).mockResolvedValue({
    member: { id: 'author' },
    permissions: ['resource.versions.review', 'resource.packages.upload'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.spyOn(resourceApi, 'governance').mockResolvedValue({ items: [] });
  vi.spyOn(resourceApi, 'get').mockResolvedValue({
    data: { ...detail.resourcePackage, level: 'l4', visibility: 'private' },
    etag: '"2"',
  });
  vi.spyOn(resourceApi, 'version').mockResolvedValue({
    ...version,
    state: 'draft',
    revision: 1,
  });
  const sign = vi
    .spyOn(resourceApi, 'personalSign')
    .mockResolvedValue({ ...version, state: 'published', revision: 2 });
  const view = open();
  await flushPromises();
  const form = view.findAll('form')[1]!;
  const fields = form.findAll('input');
  await fields[0]!.setValue('p1');
  await fields[1]!.setValue('v1');
  await form.trigger('submit');
  await flushPromises();
  expect(view.text()).toContain('未审核');
  await view.get('div.market-form textarea').setValue('Personal use');
  await view
    .findAll('button')
    .find((b) => b.text() === '签名发布个人版本')!
    .trigger('click');
  expect(sign).not.toHaveBeenCalled();
  await view.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(sign).toHaveBeenCalledWith(
    'p1',
    'v1',
    'Personal use',
    '"1"',
    expect.any(String),
  );
  view.unmount();
});

it('keeps distinct historical versions visible with their reported distribution outcomes', async () => {
  membership();
  vi.spyOn(resourceApi, 'distributions').mockResolvedValue({
    items: [
      {
        id: 'old',
        instanceId: 'instance',
        ownerId: 'owner',
        selectionRevision: 1,
        revision: 2,
        state: 'applied',
        generation: 2,
        observedGeneration: 2,
        ready: true,
        selections: [
          {
            packageId: 'plugin-package',
            versionId: 'old-version',
            action: 'install',
          },
        ],
      },
      {
        id: 'new',
        instanceId: 'instance',
        ownerId: 'owner',
        selectionRevision: 2,
        revision: 1,
        state: 'applying',
        generation: 3,
        observedGeneration: 2,
        ready: false,
        selections: [
          {
            packageId: 'plugin-package',
            versionId: 'new-version',
            action: 'install',
          },
        ],
      },
    ],
  });
  const view = open('distribution');
  await flushPromises();
  const rows = view.findAll('tbody tr');
  expect(rows).toHaveLength(2);
  expect(rows[0]!.text()).toContain('old-version');
  expect(rows[1]!.text()).toContain('new-version');
  expect(rows[0]!.text()).not.toContain('resource.distribution_denied');
  view.unmount();
});
