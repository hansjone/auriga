import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import { adminApi } from '../src/admin/api';
import { resourceApi } from '../src/admin/resourceApi';
import {
  resourceCatalogApi as api,
  type CatalogVersion,
  type CatalogReview,
  type CatalogDistribution,
} from '../src/admin/resourceCatalogApi';
import {
  publicationReady,
  distributionApplied,
  riskOptions,
} from '../src/admin/resourceCatalogPolicy';
import { tarText, boundedBytes } from '../src/admin/resourceSkillPreview';
import * as preview from '../src/admin/resourceSkillPreview';
import * as uploadHelpers from '../src/admin/resourceCatalogUpload';
import * as stepUp from '../src/admin/stepUp';
import Catalog from '../src/views/resources/ResourceCatalogView.vue';
import Center from '../src/views/resources/ResourceCenterView.vue';

const v: CatalogVersion = {
  id: 'v1',
  packageId: 'p1',
  kind: 'skill',
  version: '1.0.0',
  digest: 'sha256:' + 'a'.repeat(64),
  state: 'pending_review',
  revision: 2,
  createdAt: '2026-11-12T00:00:00Z',
  manifestDownloadPath:
    '/api/resource/v1/catalog/packages/p1/versions/v1/manifest',
  risk: {
    level: 'r2',
    minimumLevel: 'r2',
    requiredReviewers: 2,
    requiredSecurityReviewers: 1,
  },
  manifest: {
    schemaVersion: 1,
    kind: 'skill',
    name: 'sample-skill',
    version: '1.0.0',
    description: 'Example',
    author: { displayName: 'Author' },
    license: 'MIT',
    permissions: { tools: ['terminal'], outboundHosts: [], dataAccess: [] },
    runtimes: [],
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: 'sha256:' + 'b'.repeat(64),
        sizeBytes: 20,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
    skill: {
      entry: 'SKILL.md',
      contentProfile: 'text-only',
      dshVersionRange: '*',
    },
  },
};
const r: CatalogReview = {
  reviewId: 'review-1',
  authorId: 'author',
  version: v,
  revision: 4,
  status: 'pending',
  decisions: [],
};
const record = (
  reviewerId: string,
  role: 'general' | 'security' = 'general',
) => ({
  reviewerId,
  role,
  decision: 'approve' as const,
  reason: 'Checked',
  versionRevision: 2,
  digest: v.digest,
  createdAt: v.createdAt,
});
const stubs = {
  RouterLink: true,
  ActionConfirm: {
    props: ['message'],
    template:
      '<button v-if="message" data-testid="confirm" @click="$emit(\'confirm\')">Confirm</button>',
  },
};
const views: ReturnType<typeof mount>[] = [];
function member(id = 'reviewer') {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    member: { id },
    tenant: { id: 'tenant' },
    permissions: [
      'resource.packages.read',
      'resource.packages.upload',
      'resource.versions.submit',
      'resource.versions.review',
      'resource.versions.publish',
      'resource.distributions.manage',
    ],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
}
function setup(
  phase: 'directory' | 'review' | 'published' | 'distribution' = 'review',
  locale: 'en' | 'zh-CN' = 'zh-CN',
  memberId = 'reviewer',
) {
  member(memberId);
  vi.spyOn(api, 'assignments').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'reviews').mockResolvedValue({ items: [r] });
  vi.spyOn(api, 'review').mockResolvedValue({ data: r, etag: '"review-tag"' });
  vi.spyOn(api, 'version').mockResolvedValue({
    data: v,
    etag: '"version-tag"',
  });
  const view = mount(Catalog, {
    props: { phase },
    global: { plugins: [createAurigaI18n(locale)], stubs },
  });
  views.push(view);
  return view;
}
afterEach(() => {
  views.splice(0).forEach((v) => v.unmount());
  vi.restoreAllMocks();
});

it('loads reviewId detail, exact preconditions and command risk floor; text stays inert', async () => {
  const view = setup();
  vi.spyOn(api, 'artifact').mockResolvedValue({} as Response);
  vi.spyOn(preview, 'skillPreview').mockResolvedValue([
    { path: 'SKILL.md', text: '<script>alert(1)</script>\nrun terminal' },
  ]);
  const decide = vi.spyOn(api, 'decide').mockResolvedValue(r);
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  expect(api.review).toHaveBeenCalledWith('review-1');
  expect(
    view.findAll('[data-testid="catalog-risk"] option').map((n) => n.text()),
  ).toEqual(['R2', 'R3']);
  await view.get('[data-testid="catalog-reason"]').setValue('Checked');
  await view.get('[data-testid="catalog-approve"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(decide).toHaveBeenCalledWith(
    'review-1',
    {
      decision: 'approve',
      reason: 'Checked',
      role: 'general',
      riskLevel: 'r2',
      versionRevision: 2,
      digest: v.digest,
    },
    '"review-tag"',
    expect.any(String),
  );
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  const button = view.findAll('button').find((b) => b.text() === '文本预览')!;
  await button.trigger('click');
  await flushPromises();
  expect(view.get('[data-testid="skill-text"]').text()).toContain('<script>');
  expect(view.find('script').exists()).toBe(false);
});
it('rejects author review and publication in the component', async () => {
  const view = setup('review', 'zh-CN', 'author');
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-reason"]').setValue('Reason');
  expect(
    view.get('[data-testid="catalog-approve"]').attributes('disabled'),
  ).toBeDefined();
  expect(
    view.get('[data-testid="catalog-publish"]').attributes('disabled'),
  ).toBeDefined();
});
it('counts distinct current non-author reviewers and enforces security duty', () => {
  const approved = {
    ...r,
    status: 'approved' as const,
    version: { ...v, state: 'approved' as const },
    decisions: [record('a')],
  };
  expect(publicationReady(approved, 'publisher')).toBe(false);
  expect(
    publicationReady(
      { ...approved, decisions: [record('a'), record('b')] },
      'publisher',
    ),
  ).toBe(false);
  expect(
    publicationReady(
      { ...approved, decisions: [record('a'), record('a', 'security')] },
      'publisher',
    ),
  ).toBe(false);
  expect(
    publicationReady(
      { ...approved, decisions: [record('a'), record('author', 'security')] },
      'publisher',
    ),
  ).toBe(false);
  expect(
    publicationReady(
      {
        ...approved,
        decisions: [
          record('a'),
          { ...record('b', 'security'), digest: 'different' },
        ],
      },
      'publisher',
    ),
  ).toBe(false);
  expect(
    publicationReady(
      { ...approved, decisions: [record('a'), record('b', 'security')] },
      'publisher',
    ),
  ).toBe(true);
  expect(riskOptions(r)).toEqual(['r2', 'r3']);
});
it('blocks the publish control until the full quorum is available', async () => {
  const view = setup();
  const approved = {
    ...r,
    status: 'approved' as const,
    version: { ...v, state: 'approved' as const },
    decisions: [record('a'), record('b', 'security')],
  };
  vi.mocked(api.review).mockResolvedValue({
    data: approved,
    etag: '"review-tag"',
  });
  vi.mocked(api.version).mockResolvedValue({
    data: approved.version,
    etag: '"version-tag"',
  });
  const publish = vi.spyOn(api, 'publish').mockResolvedValue(v);
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-reason"]').setValue('Publish');
  expect(
    view.get('[data-testid="catalog-publish"]').attributes('disabled'),
  ).toBeUndefined();
  await view.get('[data-testid="catalog-publish"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(publish).toHaveBeenCalledWith(
    'p1',
    'v1',
    'Publish',
    '"2"',
    expect.any(String),
  );
});
it('requires refresh after ETag conflict and retains identical retry after uncertain failure', async () => {
  const view = setup();
  const decide = vi
    .spyOn(api, 'decide')
    .mockRejectedValueOnce(new Error('Network'))
    .mockRejectedValueOnce(
      new ApiError({
        status: 412,
        title: 'Conflict',
        code: 'common.precondition_failed',
      }),
    );
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-reason"]').setValue('Checked');
  await view.get('[data-testid="catalog-approve"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  const retry = view.findAll('button').find((b) => b.text() === '继续原请求')!;
  await retry.trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(decide.mock.calls[1]).toEqual(decide.mock.calls[0]);
  expect(view.text()).toContain('数据已变化');
  expect(view.find('[data-testid="catalog-approve"]').exists()).toBe(false);
});
it.each([false, true])(
  'uses exact identity and preserves other selections on 202 registration (existing=%s)',
  async (existing) => {
    const view = setup('distribution');
    vi.spyOn(api, 'distributions').mockResolvedValue({ items: [] });
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        id: 'p1',
        tenantId: 'tenant',
        kind: 'skill',
      } as CatalogPackageForTest,
      etag: '"p"',
    });
    vi.mocked(api.version).mockResolvedValue({
      data: { ...v, state: 'published' },
      etag: '"v"',
    });
    const other = {
      identity: {
        tenantId: 'tenant',
        packageId: 'other',
        versionId: 'other-v',
        kind: 'skill' as const,
        version: '2.0.0',
        manifestDigest: v.digest,
      },
      action: 'enable' as const,
    };
    vi.spyOn(api, 'distribution').mockResolvedValue({
      data: {
        instanceId: 'instance',
        selections: [other],
      } as CatalogDistribution,
      etag: '"selection-etag"',
    });
    const distribute = vi
      .spyOn(api, 'distribute')
      .mockResolvedValue({} as CatalogDistribution);
    await flushPromises();
    const inputs = view.findAll('[data-testid="catalog-distribute"] input');
    await inputs[0]!.setValue('instance');
    await inputs[1]!.setValue('p1');
    await inputs[2]!.setValue('v1');
    if (existing) await inputs[3]!.setValue('existing');
    await view.get('[data-testid="catalog-distribute"]').trigger('submit');
    await flushPromises();
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(distribute).toHaveBeenCalledWith(
      {
        instanceId: 'instance',
        selections: [
          ...(existing ? [other] : []),
          {
            identity: {
              tenantId: 'tenant',
              packageId: 'p1',
              versionId: 'v1',
              kind: 'skill',
              version: '1.0.0',
              manifestDigest: v.digest,
            },
            action: 'install',
          },
        ],
      },
      existing ? '"selection-etag"' : '"0"',
      expect.any(String),
    );
    expect(view.text()).toContain('已登记，等待节点回报');
  },
);
type CatalogPackageForTest = Awaited<ReturnType<typeof api.get>>['data'];
it('requires matching node generation, selection revision and every resource report', () => {
  const identity = {
    tenantId: 't',
    packageId: 'p1',
    versionId: 'v1',
    kind: 'skill' as const,
    version: '1.0.0',
    manifestDigest: v.digest,
  };
  const d: CatalogDistribution = {
    distributionId: 'd',
    instanceId: 'i',
    ownerId: 'o',
    selectionRevision: 2,
    generation: 3,
    selections: [{ identity, action: 'install' }],
    state: 'applied',
    reportSource: 'node',
    reports: [],
  };
  expect(distributionApplied(d)).toBe(false);
  const report = {
    identity,
    clientId: 'c',
    installationRevision: 1,
    selectionRevision: 2,
    generation: 3,
    observedGeneration: 3,
    state: 'applied' as const,
    reportSource: 'node' as const,
    observedAt: v.createdAt,
  };
  expect(distributionApplied({ ...d, reports: [report] })).toBe(true);
  expect(
    distributionApplied({
      ...d,
      reports: [{ ...report, observedGeneration: 2 }],
    }),
  ).toBe(false);
  expect(
    distributionApplied({
      ...d,
      reports: [{ ...report, reportSource: 'member' }],
    }),
  ).toBe(false);
});
it('uses the unified source for both plugin and skill without legacy calls', async () => {
  member();
  const legacy = vi
    .spyOn(resourceApi, 'governance')
    .mockResolvedValue({ items: [] });
  const unified = vi.spyOn(api, 'reviews').mockResolvedValue({ items: [] });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/resources/review',
        component: Center,
        props: { phase: 'review' },
      },
    ],
  });
  await router.push('/resources/review');
  await router.isReady();
  const view = mount(Center, {
    props: { phase: 'review' },
    global: { plugins: [router, createAurigaI18n('en')], stubs },
  });
  views.push(view);
  await flushPromises();
  expect(legacy).not.toHaveBeenCalled();
  expect(unified).toHaveBeenCalledWith(undefined, 'plugin');
  await view.get('[aria-pressed="false"]').trigger('click');
  await flushPromises();
  expect(unified).toHaveBeenLastCalledWith(undefined, 'skill');
  expect(legacy).not.toHaveBeenCalled();
  expect(view.text()).toContain('Skills');
});
it('rejects preview traversal, links, invalid UTF-8 and oversized streams', async () => {
  const tar = (name: string, type = '0', byte = 65) => {
    const b = new Uint8Array(2048);
    b.set(new TextEncoder().encode(name));
    b.set(new TextEncoder().encode('00000000001\0'), 124);
    b[156] = type.charCodeAt(0);
    b[512] = byte;
    return b;
  };
  expect(tarText(tar('SKILL.md'))).toEqual([{ path: 'SKILL.md', text: 'A' }]);
  expect(() => tarText(tar('../SKILL.md'))).toThrow();
  expect(() => tarText(tar('SKILL.md', '2'))).toThrow();
  expect(() => tarText(tar('SKILL.md', '0', 255))).toThrow();
  await expect(
    boundedBytes(
      new ReadableStream({
        start(c) {
          c.enqueue(new Uint8Array(11 * 1024 * 1024));
          c.close();
        },
      }),
    ),
  ).rejects.toThrow('Preview budget');
});

it('enforces plugin R2 and distinct security quorum even on a malformed lower server assessment', () => {
  const pluginReview = {
    ...r,
    version: {
      ...v,
      kind: 'plugin' as const,
      state: 'approved' as const,
      risk: {
        level: 'r1' as const,
        minimumLevel: 'r1' as const,
        requiredReviewers: 1,
        requiredSecurityReviewers: 0,
      },
    },
    status: 'approved' as const,
    decisions: [record('a')],
  };
  expect(riskOptions(pluginReview)).toEqual(['r2', 'r3']);
  expect(publicationReady(pluginReview, 'publisher')).toBe(false);
  expect(
    publicationReady(
      { ...pluginReview, decisions: [record('a'), record('b', 'security')] },
      'publisher',
    ),
  ).toBe(true);
});
it.each(['plugin', 'skill'] as const)(
  'creates, uploads and submits a %s using parsed manifest metadata and actual ETags',
  async (kind) => {
    const view = setup('directory');
    await view.setProps({ kind });
    const pack: CatalogPackageForTest = {
      id: 'p1',
      tenantId: 'tenant',
      kind,
      name: v.manifest.name,
      ownerId: 'author',
      visibility: 'tenant',
      groupIds: [],
      revision: 1,
      enabled: false,
      defaultVersionId: null,
      createdAt: v.createdAt,
    };
    const draft = { ...v, kind, state: 'draft' as const };
    vi.spyOn(api, 'packages').mockResolvedValue({ items: [pack] });
    vi.spyOn(api, 'versions').mockResolvedValue({ items: [] });
    vi.spyOn(api, 'get').mockResolvedValue({
      data: pack,
      etag: '"package-tag"',
    });
    const create = vi.spyOn(api, 'create').mockResolvedValue(pack);
    const upload = vi.spyOn(api, 'upload').mockResolvedValue(draft);
    const submit = vi
      .spyOn(api, 'submit')
      .mockResolvedValue({ ...r, version: draft });
    vi.mocked(api.version).mockResolvedValue({
      data: draft,
      etag: '"draft-tag"',
    });
    const file = new File(['{}'], 'auriga-resource.json'),
      payload = new File(
        ['tgz'],
        kind === 'plugin' ? 'payload.tgz' : 'skill.tar.gz',
      );
    if (v.manifest.kind !== 'skill') throw new Error('Skill fixture');
    const manifest =
      kind === 'plugin'
        ? {
            ...v.manifest,
            kind: 'plugin' as const,
            plugin: { packageName: '@example/sample', dshVersionRange: '*' },
          }
        : v.manifest;
    draft.manifest = manifest;
    vi.spyOn(uploadHelpers, 'prepareUpload').mockResolvedValue({
      manifest,
      file,
      artifacts: [payload],
      metadata: { version: '1.0.0', expectedDigest: v.digest },
    });
    await flushPromises();
    await view
      .get('[data-testid="catalog-create"] input')
      .setValue(v.manifest.name);
    await view.get('[data-testid="catalog-create"]').trigger('submit');
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(create).toHaveBeenCalledWith(
      { name: v.manifest.name, kind, visibility: 'tenant', groupIds: [] },
      expect.any(String),
    );
    for (const [testId, selectedFile] of [
      ['catalog-manifest', file],
      ['catalog-artifacts', payload],
    ] as const) {
      const input = view.get(`[data-testid="${testId}"]`);
      Object.defineProperty(input.element, 'files', {
        value: [selectedFile],
        configurable: true,
      });
      await input.trigger('change');
      await flushPromises();
    }
    expect(view.get('[data-testid="catalog-upload-preview"]').text()).toContain(
      v.digest,
    );
    if (kind === 'plugin') expect(view.text()).toContain('@example/sample');
    await view.get('[data-testid="catalog-upload"]').trigger('submit');
    await flushPromises();
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(upload).toHaveBeenCalledWith(
      'p1',
      { version: '1.0.0', expectedDigest: v.digest },
      file,
      [payload],
      '"package-tag"',
      expect.any(String),
    );
    if (kind === 'plugin') expect(view.text()).not.toContain('对比版本 ID');
    await view
      .get('[data-testid="catalog-reason"]')
      .setValue('Review these bytes');
    await view
      .findAll('button')
      .find((b) => b.text() === '送审')!
      .trigger('click');
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(submit).toHaveBeenCalledWith(
      'p1',
      'v1',
      'Review these bytes',
      '"draft-tag"',
      expect.any(String),
    );
  },
);
it('recovers approved review evidence from its real ID and blocks mismatched versions', async () => {
  const view = setup('published');
  vi.spyOn(api, 'published').mockResolvedValue({
    items: [{ ...v, state: 'approved' }],
  });
  const approved = {
    ...r,
    status: 'approved' as const,
    version: { ...v, state: 'approved' as const },
    decisions: [record('a'), record('b', 'security')],
  };
  vi.mocked(api.version).mockResolvedValue({
    data: approved.version,
    etag: '"v"',
  });
  vi.mocked(api.review).mockResolvedValue({ data: approved, etag: '"r"' });
  await flushPromises();
  await view.get('[data-testid="catalog-version"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-review-id"]').setValue('review-1');
  await view.get('[data-testid="catalog-read-review"]').trigger('submit');
  await flushPromises();
  await view.get('[data-testid="catalog-reason"]').setValue('Publish');
  expect(
    view.get('[data-testid="catalog-publish"]').attributes('disabled'),
  ).toBeUndefined();
  vi.mocked(api.review).mockResolvedValue({
    data: { ...approved, version: { ...v, id: 'another' } },
    etag: '"r"',
  });
  await view.get('[data-testid="catalog-version"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-review-id"]').setValue('wrong-review');
  await view.get('[data-testid="catalog-read-review"]').trigger('submit');
  await flushPromises();
  expect(view.find('[data-testid="catalog-publish"]').exists()).toBe(false);
});
it.each(['withdrawn', 'malicious'] as const)(
  'sends %s with the version ETag, including revocation after withdrawal',
  async (mode) => {
    const view = setup('published');
    const state = mode === 'malicious' ? 'withdrawn' : 'published';
    vi.spyOn(api, 'published').mockResolvedValue({ items: [{ ...v, state }] });
    vi.mocked(api.version).mockResolvedValue({
      data: { ...v, state },
      etag: '"published-tag"',
    });
    const withdraw = vi.spyOn(api, 'withdraw').mockResolvedValue(v);
    await flushPromises();
    await view.get('[data-testid="catalog-version"]').trigger('click');
    await flushPromises();
    await view.get('[data-testid="catalog-reason"]').setValue('Reason');
    await view
      .findAll('button')
      .find((b) => b.text() === (mode === 'malicious' ? '紧急撤销' : '下架'))!
      .trigger('click');
    await view.get('[data-testid="confirm"]').trigger('click');
    await flushPromises();
    expect(withdraw).toHaveBeenCalledWith(
      'p1',
      'v1',
      mode,
      'Reason',
      '"published-tag"',
      expect.any(String),
    );
  },
);
it('continues the identical review decision after explicit step-up and supports security duty', async () => {
  const view = setup();
  const decide = vi
    .spyOn(api, 'decide')
    .mockRejectedValueOnce(
      new ApiError({
        title: 'Verify',
        status: 403,
        code: 'iam.step_up_required',
      }),
    )
    .mockResolvedValue(r);
  vi.spyOn(stepUp, 'requestStepUp').mockResolvedValue();
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  await view.get('[data-testid="catalog-role"]').setValue('security');
  await view.get('[data-testid="catalog-reason"]').setValue('Security checked');
  await view.get('[data-testid="catalog-approve"]').trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  await view
    .findAll('button')
    .find((b) => b.text() === '重新验证身份')!
    .trigger('click');
  await flushPromises();
  await view
    .findAll('button')
    .find((b) => b.text() === '继续原请求')!
    .trigger('click');
  await view.get('[data-testid="confirm"]').trigger('click');
  await flushPromises();
  expect(decide.mock.calls[0]![1].role).toBe('security');
  expect(decide.mock.calls[0]).toEqual(decide.mock.calls[1]);
});
it('reads reviewer evidence through the admin endpoint when member version reads reject unpublished versions', async () => {
  const view = setup();
  vi.mocked(api.version).mockRejectedValue(
    new ApiError({ status: 404, title: 'Not found', code: 'common.not_found' }),
  );
  await flushPromises();
  await view.get('[data-testid="catalog-review"]').trigger('click');
  await flushPromises();
  expect(view.find('[data-testid="catalog-approve"]').exists()).toBe(true);
  expect(api.version).not.toHaveBeenCalled();
});
