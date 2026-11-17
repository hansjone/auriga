import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import Panel from '../src/views/resources/ResourceUploadPanel.vue';
import {
  resourceCatalogApi as api,
  type CatalogPackage,
  type CatalogVersion,
} from '../src/admin/resourceCatalogApi';
import * as parser from '../src/admin/resourceUploadParser';
import { File as NodeFile } from 'node:buffer';
const manifest = {
  schemaVersion: 1,
  kind: 'skill',
  name: 'sample',
  version: '1.0.0',
  description: 'Test',
  author: { displayName: 'Alice' },
  license: 'MIT',
  permissions: { tools: [], outboundHosts: [], dataAccess: [] },
  runtimes: [],
  artifacts: [],
  signature: { mode: 'detached', algorithm: 'Ed25519' },
  skill: {
    entry: 'SKILL.md',
    contentProfile: 'text-only',
    dshVersionRange: '*',
  },
} as parser.ResourceSource['manifest'];
const file = new NodeFile(['x'], 'skill.tar.gz') as unknown as File;
const pack = {
  id: 'p1',
  name: 'sample',
  kind: 'skill',
  revision: 1,
  tenantId: 'tenant',
  ownerId: 'owner',
  createdAt: '2026-11-18T00:00:00Z',
  enabled: true,
  defaultVersionId: null,
  visibility: 'tenant',
  groupIds: [],
} as CatalogPackage;
const draft = {
  id: 'v1',
  packageId: 'p1',
  kind: 'skill',
  version: '1.0.0',
  state: 'draft',
  revision: 2,
  manifest,
} as CatalogVersion;
const wrappers: ReturnType<typeof mount>[] = [];
function setup() {
  vi.spyOn(parser, 'parseResourceSource').mockResolvedValue({
    manifest,
    payload: file,
    embedded: false,
  });
  vi.spyOn(parser, 'finalizeResourceUpload').mockResolvedValue({
    manifest,
    file,
    artifacts: [file],
    metadata: { version: '1.0.0', expectedDigest: 'sha256:' + 'a'.repeat(64) },
  });
  vi.spyOn(api, 'packages').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'create').mockResolvedValue(pack);
  vi.spyOn(api, 'get').mockResolvedValue({ data: pack, etag: '"3"' });
  vi.spyOn(api, 'versions').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'upload').mockResolvedValue(draft);
  vi.spyOn(api, 'submit').mockResolvedValue({
    reviewId: 'r1',
    version: draft,
  } as Awaited<ReturnType<typeof api.submit>>);
  const wrapper = mount(Panel, {
    props: { kind: 'skill', author: 'Alice', canSubmit: true },
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  wrappers.push(wrapper);
  return wrapper;
}
async function select(w: ReturnType<typeof mount>) {
  const input = w.get('[data-testid="catalog-folder"]');
  Object.defineProperty(input.element, 'files', {
    value: [file],
    configurable: true,
  });
  await input.trigger('change');
  await flushPromises();
}
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount());
  vi.restoreAllMocks();
});
it('previews without resource ID or raw manifest inputs and requires permission confirmation', async () => {
  const w = setup();
  await select(w);
  expect(w.find('[data-testid="catalog-manifest"]').exists()).toBe(false);
  expect(w.text()).not.toContain('资源 ID');
  expect(
    w.get('[data-testid="upload-submit"]').attributes('disabled'),
  ).toBeDefined();
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="catalog-upload"]').trigger('submit');
  await flushPromises();
  expect(api.create).toHaveBeenCalledWith(
    { name: 'sample', kind: 'skill', visibility: 'tenant', groupIds: [] },
    expect.any(String),
  );
  expect(api.upload).toHaveBeenCalledWith(
    'p1',
    expect.any(Object),
    file,
    [file],
    '"3"',
    expect.any(String),
  );
  expect(api.submit).toHaveBeenCalledWith(
    'p1',
    'v1',
    expect.any(String),
    '"2"',
    expect.any(String),
  );
  expect(w.emitted('completed')).toHaveLength(1);
});
it('matches a package beyond the first page and saves only a draft', async () => {
  const w = setup();
  vi.mocked(api.packages)
    .mockResolvedValueOnce({ items: [], nextCursor: 'next' })
    .mockResolvedValueOnce({ items: [pack] });
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="upload-draft"]').trigger('click');
  await flushPromises();
  expect(api.packages).toHaveBeenNthCalledWith(2, 'next', 'skill');
  expect(api.create).not.toHaveBeenCalled();
  expect(api.submit).not.toHaveBeenCalled();
});
it('finds duplicate versions beyond the first page and never uploads', async () => {
  const w = setup();
  vi.mocked(api.packages).mockResolvedValue({ items: [pack] });
  vi.mocked(api.versions)
    .mockResolvedValueOnce({ items: [], nextCursor: 'older' })
    .mockResolvedValueOnce({ items: [draft] });
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="upload-draft"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('版本 1.0.0 已存在');
  expect(api.upload).not.toHaveBeenCalled();
});
it('retries submission with its original key and does not upload the saved draft again', async () => {
  const w = setup();
  vi.mocked(api.submit).mockRejectedValueOnce(
    new ApiError({
      status: 503,
      code: 'common.internal',
      title: 'Unavailable',
    }),
  );
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="catalog-upload"]').trigger('submit');
  await flushPromises();
  expect(w.text()).toContain('草稿已保存');
  await w.get('[data-testid="upload-retry"]').trigger('click');
  await flushPromises();
  expect(api.upload).toHaveBeenCalledTimes(1);
  expect(api.submit).toHaveBeenCalledTimes(2);
  expect(vi.mocked(api.submit).mock.calls[0]).toEqual(
    vi.mocked(api.submit).mock.calls[1],
  );
});
it('ignores a stale parse after a newer selection', async () => {
  const w = setup();
  let finish: (s: parser.ResourceSource) => void = () => {};
  vi.mocked(parser.parseResourceSource).mockReturnValueOnce(
    new Promise((r) => {
      finish = r;
    }),
  );
  const input = w.get('[data-testid="catalog-folder"]');
  Object.defineProperty(input.element, 'files', {
    value: [file],
    configurable: true,
  });
  await input.trigger('change');
  await input.trigger('change');
  await flushPromises();
  finish({
    manifest: { ...manifest, name: 'stale' },
    payload: file,
    embedded: false,
  });
  await flushPromises();
  expect(
    (w.get('[data-testid="upload-name"]').element as HTMLInputElement).value,
  ).toBe('sample');
});
it('does not submit after navigating away while upload is in flight', async () => {
  const w = setup();
  let finish: (v: CatalogVersion) => void = () => {};
  vi.mocked(api.upload).mockReturnValueOnce(
    new Promise((r) => {
      finish = r;
    }),
  );
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="catalog-upload"]').trigger('submit');
  await flushPromises();
  w.unmount();
  finish(draft);
  await flushPromises();
  expect(api.submit).not.toHaveBeenCalled();
});
it('preserves upload bytes, ETag and idempotency key after an uncertain upload result', async () => {
  const w = setup();
  vi.mocked(api.upload).mockRejectedValueOnce(new Error('Network interrupted'));
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="catalog-upload"]').trigger('submit');
  await flushPromises();
  await w.get('[data-testid="upload-retry"]').trigger('click');
  await flushPromises();
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(api.upload).toHaveBeenCalledTimes(2);
  expect(vi.mocked(api.upload).mock.calls[0]).toEqual(
    vi.mocked(api.upload).mock.calls[1],
  );
});
it('allows correcting a definite upload rejection with a fresh operation', async () => {
  const w = setup();
  vi.mocked(api.upload).mockRejectedValueOnce(
    new ApiError({
      status: 400,
      code: 'common.validation_failed',
      title: 'Invalid',
    }),
  );
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="catalog-upload"]').trigger('submit');
  await flushPromises();
  expect(w.find('[data-testid="upload-retry"]').exists()).toBe(false);
  expect(
    w.get('[data-testid="upload-name"]').attributes('disabled'),
  ).toBeUndefined();
});
it.each([400, 403, 409, 412])(
  'opens the saved draft after definite submission rejection %s instead of uploading the version again',
  async (status) => {
    const w = setup();
    vi.mocked(api.submit).mockRejectedValueOnce(
      new ApiError({ status, code: 'common.conflict', title: 'Rejected' }),
    );
    await select(w);
    await w.get('[data-testid="permissions-confirm"]').setValue(true);
    await w.get('[data-testid="catalog-upload"]').trigger('submit');
    await flushPromises();
    expect(w.text()).toContain('草稿已保存');
    expect(w.text()).not.toContain('修改版本号');
    expect(w.find('[data-testid="catalog-upload"]').exists()).toBe(false);
    expect(w.find('[data-testid="upload-retry"]').exists()).toBe(false);
    await w.get('[data-testid="upload-open-draft"]').trigger('click');
    expect(w.emitted('resumeDraft')).toEqual([[draft]]);
    expect(api.upload).toHaveBeenCalledTimes(1);
  },
);
it('explains an invisible name conflict and leaves the name editable', async () => {
  const w = setup();
  vi.mocked(api.create).mockRejectedValueOnce(
    new ApiError({ status: 409, code: 'common.conflict', title: 'Conflict' }),
  );
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="upload-draft"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('同名资源已存在但你无权查看，请换名或联系管理员');
  expect(
    w.get('[data-testid="upload-name"]').attributes('disabled'),
  ).toBeUndefined();
  expect(api.upload).not.toHaveBeenCalled();
});
it('distinguishes a visible package created concurrently from an invisible name conflict', async () => {
  const w = setup();
  vi.mocked(api.packages)
    .mockResolvedValueOnce({ items: [] })
    .mockResolvedValueOnce({ items: [pack] });
  vi.mocked(api.create).mockRejectedValueOnce(
    new ApiError({ status: 409, code: 'common.conflict', title: 'Conflict' }),
  );
  await select(w);
  await w.get('[data-testid="permissions-confirm"]').setValue(true);
  await w.get('[data-testid="upload-draft"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('状态发生冲突');
  expect(w.text()).not.toContain('无权查看');
  expect(api.upload).not.toHaveBeenCalled();
});
it('shows editable metadata notices and clear unsigned/snapshot information', async () => {
  const w = setup();
  vi.mocked(parser.parseResourceSource).mockResolvedValueOnce({
    manifest: { ...manifest, name: 'a'.repeat(80) },
    payload: file,
    embedded: false,
    warnings: ['nameInvalid', 'rangeDefaulted'],
  });
  await select(w);
  expect(w.get('[data-testid="upload-name"]').element).toHaveProperty(
    'value',
    'a'.repeat(80),
  );
  expect(w.get('[data-testid="upload-preview-notices"]').text()).toContain(
    '兼容范围',
  );
  expect(w.text()).toContain('未签发（发布时由平台签名）');
  expect(w.text()).toContain('规范 SKILL.md 顶部格式');
  expect(w.text()).not.toContain(' ?');
});
