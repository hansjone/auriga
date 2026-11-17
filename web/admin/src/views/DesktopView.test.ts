import { mount, flushPromises } from '@vue/test-utils';
import { beforeEach, it, expect, vi } from 'vitest';
import { createHash, generateKeyPairSync } from 'node:crypto';
import { Blob as NodeBlob } from 'node:buffer';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import DesktopView from './DesktopView.vue';
const api = vi.hoisted(() => ({
  membership: vi.fn(),
  get: vi.fn(),
  delete: vi.fn(),
  request: vi.fn(),
  verify: vi.fn(),
}));
vi.mock('../admin/api', () => ({ adminApi: { membership: api.membership } }));
vi.mock('../api', () => ({ apiClient: api }));
vi.mock('../admin/stepUp', () => ({ requestStepUp: api.verify }));
const base = '/api/resource/v1/admin/desktop/releases';
const spki = generateKeyPairSync('ed25519').publicKey.export({
  type: 'spki',
  format: 'der',
});
const fingerprint = createHash('sha256').update(spki).digest('hex');
const body = {
  schema: 2,
  product: 'Auriga Desktop',
  platform: 'win-x64',
  packageId: 'package-2',
  sequence: 2,
  version: '2.0.0',
  channel: 'beta',
  minimumVersion: '1.0.0',
  rolloutPercentage: 25,
  rollbackFrom: '3.0.0',
  sizeBytes: 3,
  sha256: createHash('sha256').update('zip').digest('hex'),
  publishedAt: '2026-11-12T00:00:00Z',
  notAfter: '2099-12-01T00:00:00Z',
  notes: 'release',
  components: {
    nodeVersion: '24.0.0',
    dshVersion: '0.2.1',
    pluginVersion: '1.0.0',
  },
  signature: {
    alg: 'Ed25519',
    kid: 'auriga-desktop-release-1',
    value: 'a'.repeat(86),
  },
};
const ledger = {
  highestSequence: 1,
  items: [],
  channels: {},
  publicKeySpki: spki.toString('base64'),
  publicKeyFingerprint: fingerprint,
};
const denied = (code: string, status = 403) =>
  new ApiError({ title: 'Error', code, status });
function view(locale: 'zh-CN' | 'en' = 'zh-CN') {
  return mount(DesktopView, {
    global: { plugins: [createAurigaI18n(locale)] },
  });
}
function file(data: string | Uint8Array, name: string) {
  const bytes =
    typeof data === 'string' ? new TextEncoder().encode(data) : data;
  const result = new File([new Uint8Array(bytes)], name);
  Object.defineProperty(result, 'text', {
    value: async () => new TextDecoder().decode(bytes),
  });
  Object.defineProperty(result, 'slice', {
    value: (start = 0, end = bytes.length) => ({
      arrayBuffer: async () => bytes.slice(start, end).buffer,
    }),
  });
  return result;
}
async function select(
  wrapper: ReturnType<typeof view>,
  manifest = body,
  bytes: string | Uint8Array = 'zip',
) {
  for (const [index, value] of [
    file(JSON.stringify(manifest), 'release.json'),
    file(bytes, 'setup.exe'),
  ].entries()) {
    const input = wrapper.findAll('input[type="file"]')[index]!;
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [value],
    });
    await input.trigger('change');
  }
  await flushPromises();
  return wrapper
    .findAll('button')
    .find((b) => b.text() === '校验文件')!
    .trigger('click')
    .then(flushPromises);
}
async function confirm(wrapper: ReturnType<typeof view>) {
  await wrapper.find('input[type="checkbox"]').setValue(true);
  await wrapper.find('form').trigger('submit');
  await flushPromises();
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.stubGlobal('Blob', NodeBlob);
  api.membership.mockResolvedValue({
    permissions: ['resource.versions.publish', 'iam.network.manage'],
  });
  api.get.mockImplementation(async (path: string) =>
    path.startsWith('/api/iam')
      ? { items: [] }
      : path.includes('/desktop/latest')
        ? body
        : structuredClone(ledger),
  );
  api.request.mockImplementation(async (path: string) =>
    path.endsWith('/preflight') ? { publicKeyFingerprint: fingerprint } : body,
  );
});
it('无发布权限不读取发行记录', async () => {
  api.membership.mockResolvedValue({ permissions: [] });
  const wrapper = view();
  await flushPromises();
  expect(api.get).not.toHaveBeenCalled();
  expect(wrapper.find('form').exists()).toBe(false);
  wrapper.unmount();
});
it.each(['common.forbidden', 'iam.step_up_required', 'common.unavailable'])(
  '台账加载失败显示真实原因 %s，绝不显示空台账',
  async (code) => {
    api.get.mockRejectedValue(
      denied(code, code === 'common.unavailable' ? 503 : 403),
    );
    const wrapper = view();
    await flushPromises();
    expect(wrapper.find('[role="alert"]').text()).toContain(code);
    expect(wrapper.text()).not.toContain('暂无版本');
    expect(wrapper.text()).not.toContain('暂无生效版本');
    wrapper.unmount();
  },
);
it('空台账成功加载才显示暂无版本，并提示写操作需验证', async () => {
  const wrapper = view();
  await flushPromises();
  expect(wrapper.text()).toContain('暂无版本');
  expect(wrapper.text()).toContain('发布/下架前需验证身份');
  expect(api.verify).not.toHaveBeenCalled();
  wrapper.unmount();
});
it('显示真实发行记录并下架', async () => {
  api.get.mockImplementation(async (path) =>
    path.startsWith('/api/iam')
      ? { items: [] }
      : {
          ...ledger,
          items: [{ manifest: body, withdrawn: false }],
          channels: { beta: body },
        },
  );
  const wrapper = view();
  await flushPromises();
  expect(wrapper.text()).toContain('2.0.0');
  expect(wrapper.text()).toContain('25%');
  await wrapper
    .findAll('button')
    .find((b) => b.text() === '下架')!
    .trigger('click');
  await flushPromises();
  expect(api.delete).toHaveBeenCalledWith(`${base}/2`);
  wrapper.unmount();
});
it.each([
  { sizeBytes: 4 },
  { sha256: '0'.repeat(64) },
  { sequence: 1 },
  { channel: 'wrong' },
  { signature: undefined },
  { sizeBytes: null },
  { minimumVersion: {} },
])('本地校验不符阻止发布 %j', async (change) => {
  const wrapper = view();
  await flushPromises();
  await select(wrapper, { ...body, ...change } as typeof body);
  await wrapper.find('form').trigger('submit');
  await flushPromises();
  expect(api.request).not.toHaveBeenCalled();
  expect(wrapper.find('[data-test="validation-error"]').exists()).toBe(true);
  wrapper.unmount();
});
it('公钥指纹不符阻止预检和发布', async () => {
  api.get.mockResolvedValue({
    ...ledger,
    publicKeyFingerprint: '0'.repeat(64),
  });
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  expect(api.request).not.toHaveBeenCalled();
  expect(wrapper.text()).toContain('公钥指纹');
  wrapper.unmount();
});
it('错签清单由预检拒绝，不上传安装包', async () => {
  api.request.mockRejectedValue(denied('common.validation_failed', 400));
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  await wrapper.find('form').trigger('submit');
  expect(api.request).toHaveBeenCalledTimes(1);
  expect(api.request.mock.calls[0]![0]).toBe(`${base}/preflight`);
  wrapper.unmount();
});

it('预检验签公钥变化时阻止发布', async () => {
  api.request.mockResolvedValue({ publicKeyFingerprint: '0'.repeat(64) });
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  expect(wrapper.text()).toContain('公钥指纹');
  await wrapper.find('form').trigger('submit');
  expect(api.request).toHaveBeenCalledTimes(1);
  wrapper.unmount();
});

it('重新选择文件会清除已校验与人工确认状态', async () => {
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  await wrapper.find('input[type="checkbox"]').setValue(true);
  const input = wrapper.findAll('input[type="file"]')[1]!;
  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: [file('bad', 'setup.exe')],
  });
  await input.trigger('change');
  await wrapper.find('form').trigger('submit');
  expect(api.request).toHaveBeenCalledTimes(1);
  expect(
    wrapper.find('input[type="checkbox"]').attributes('disabled'),
  ).toBeDefined();
  wrapper.unmount();
});
it('预检完成后必须人工确认，发布成功显示 latest 自检', async () => {
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  await wrapper.find('form').trigger('submit');
  await flushPromises();
  expect(api.request).toHaveBeenCalledTimes(1);
  await confirm(wrapper);
  expect(api.request).toHaveBeenCalledTimes(2);
  expect(api.request).toHaveBeenLastCalledWith(
    base,
    expect.objectContaining({ method: 'POST', body: expect.any(File) }),
  );
  expect(wrapper.text()).toContain('latest 自检通过');
  wrapper.unmount();
});
it('发布成功后续验过期只提示刷新台账，不误报失败或再次发布', async () => {
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  api.get.mockImplementation(async (path) => {
    if (path === base) throw denied('iam.step_up_required');
    return body;
  });
  await confirm(wrapper);
  expect(api.request).toHaveBeenCalledTimes(2);
  expect(wrapper.text()).toContain('发行已发布，台账刷新失败');
  expect(wrapper.text()).toContain('latest 自检通过');
  wrapper.unmount();
});
it('latest 自检失败保留发布成功并提示先核台账', async () => {
  const wrapper = view();
  await flushPromises();
  await select(wrapper);
  api.get.mockImplementation(async (path) =>
    path.includes('/latest') ? { ...body, sequence: 9 } : ledger,
  );
  await confirm(wrapper);
  expect(wrapper.text()).toContain('发行已发布');
  expect(wrapper.text()).toContain('latest 自检未通过');
  expect(api.request).toHaveBeenCalledTimes(2);
  wrapper.unmount();
});
it('下架已提交但台账刷新失败时仍说明下架完成', async () => {
  api.get.mockResolvedValue({
    ...ledger,
    items: [{ manifest: body, withdrawn: false }],
  });
  const wrapper = view();
  await flushPromises();
  api.get.mockRejectedValue(denied('common.unavailable', 503));
  await wrapper
    .findAll('button')
    .find((b) => b.text() === '下架')!
    .trigger('click');
  await flushPromises();
  expect(api.delete).toHaveBeenCalledTimes(1);
  expect(wrapper.text()).toContain('发行已下架，台账刷新失败');
  wrapper.unmount();
});
it('设备按真实上报版本分布并保留未上报数，排除吊销设备', async () => {
  api.get.mockImplementation(async (path) =>
    path.startsWith('/api/iam')
      ? {
          items: [
            { status: 'active', clientVersion: '0.1.9' },
            { status: 'active', clientVersion: '0.1.9' },
            { status: 'active' },
            { status: 'revoked', clientVersion: '9.0.0' },
          ],
        }
      : ledger,
  );
  const wrapper = view();
  await flushPromises();
  expect(wrapper.find('[data-test="devices"]').text()).toContain('0.1.9');
  expect(wrapper.find('[data-test="devices"]').text()).toContain('2');
  expect(wrapper.find('[data-test="devices"]').text()).toContain('1');
  expect(wrapper.find('[data-test="devices"]').text()).not.toContain('9.0.0');
  wrapper.unmount();
});
it('没有设备查看权限时不访问设备接口且明确说明', async () => {
  api.membership.mockResolvedValue({
    permissions: ['resource.versions.publish'],
  });
  const wrapper = view();
  await flushPromises();
  expect(api.get.mock.calls.some((c) => c[0].startsWith('/api/iam'))).toBe(
    false,
  );
  expect(wrapper.text()).toContain('iam.network.manage');
  wrapper.unmount();
});
it('英文界面翻译策略预览、台账和已完成但刷新失败的提示', async () => {
  api.get.mockResolvedValue({
    ...ledger,
    items: [{ manifest: body, withdrawn: true }],
    channels: { beta: body },
  });
  const wrapper = view('en');
  await flushPromises();
  expect(wrapper.text()).toContain('Rollback source');
  expect(wrapper.text()).toContain('25%');
  expect(wrapper.text()).not.toMatch(/[\u4e00-\u9fff]/);
  wrapper.unmount();
});
it.each([0, 55, 56, 64, 65, 4194305])(
  '流式SHA256覆盖填充与跨块边界 %i',
  async (size) => {
    const bytes = new Uint8Array(size).fill(97);
    const manifest = {
      ...body,
      sizeBytes: size,
      sha256: createHash('sha256').update(bytes).digest('hex'),
    };
    const wrapper = view();
    await flushPromises();
    await select(wrapper, manifest, bytes);
    if (size === 0) expect(api.request).not.toHaveBeenCalled();
    else
      expect(api.request).toHaveBeenCalledWith(
        `${base}/preflight`,
        expect.anything(),
      );
    wrapper.unmount();
  },
);
it('按真实渠道上报统计已更新、未更新、失败和回退，旧登记不冒充上报', async () => {
  const report = (version: string, channel: string, result?: string) => ({
    status: 'active',
    clientVersion: version,
    desktopReport: {
      clientVersion: version,
      channel,
      ...(result ? { lastUpdate: { result } } : {}),
    },
  });
  api.get.mockImplementation(async (path) =>
    path.startsWith('/api/iam')
      ? {
          items: [
            report('2.0.0', 'beta', 'succeeded'),
            report('1.0.0', 'beta', 'failed'),
            report('1.0.0', 'beta', 'rolled_back'),
            report('1.0.0', 'stable'),
            { status: 'active', clientVersion: '0.1.9' },
            { ...report('1.0.0', 'beta', 'failed'), status: 'revoked' },
          ],
        }
      : { ...ledger, channels: { beta: body } },
  );
  const wrapper = view();
  await flushPromises();
  expect(wrapper.find('[data-test="updated"]').text()).toContain('1');
  expect(wrapper.find('[data-test="pending"]').text()).toContain('2');
  expect(wrapper.find('[data-test="unknown"]').text()).toContain('1');
  expect(wrapper.find('[data-test="failed"]').text()).toContain('2');
  expect(wrapper.find('[data-test="rolled-back"]').text()).toContain('1');
  expect(wrapper.find('[data-test="unreported"]').text()).toContain('1');
  wrapper.unmount();
});
