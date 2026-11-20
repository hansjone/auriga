/**
 * 套件清单格式兼容（client-inputs-release.json、windows-client.json）。
 *
 * 与服务端 deploy/offline/devkit_compat.go 同一规则。Node 读取方默认忽略未知字段，
 * 风险是旧脚本默默忽略会影响校验或构建的新字段，所以：
 *   - 每个顶层字段都在 FIELD_REGISTRY 登记，三类：
 *     BASELINE：本机制之前就有的字段，所有脚本都认识；
 *     INFO：之后新增的说明性字段，不认识的脚本可以忽略；
 *     其他字符串：能力名。字段有值就会改变校验或构建行为，不认识的脚本必须拒绝。
 *   - 写出时只有出现非基线字段才带顶层 requires（只有说明性字段时为空数组），
 *     否则内容与旧格式逐字节相同，旧脚本照常读取。
 *   - 读到带 requires 的清单：列表里有当前脚本不认识的能力就拒绝；
 *     requires 类型错误、大小写变体键（Requires 等）也拒绝。
 *   - 没有 requires 的清单按旧方式读取，忽略未知字段。
 *   - 清单格式只增不删：删字段或改字段含义必须出整套新套件。
 * 以后在写出侧加能力字段，维护侧 Go 的 deploy/offline/release_client_kit.go
 * 对 client-inputs-release.json 是严格解码，必须同步改 Go 结构体，否则 Go 会拒绝；
 * 对 windows-client.json 是非严格解码，会默默忽略新字段，同样要同步处理。
 * 测试 site-release.test.mjs 会检查写出的字段都已登记，漏登记就失败。
 */
export const BASELINE = '';
export const INFO = 'info';

export const FIELD_REGISTRY = {
  'client-inputs-release.json': {
    schemaVersion: BASELINE,
    version: BASELINE,
    platform: BASELINE,
    lockSha256: BASELINE,
    manifestSha256: BASELINE,
    buildNodeSha256: BASELINE,
  },
  'windows-client.json': {
    schema: BASELINE,
    platform: BASELINE,
    buildNode: BASELINE,
    locks: BASELINE,
    lockSha256: BASELINE,
    tools: BASELINE,
    files: BASELINE,
    bytes: BASELINE,
  },
};

export const NEWER_HINT =
  '套件需要更新的客户端发行脚本，请使用该套件发行页对应版本的源码';

/** 当前脚本认识的全部能力名。 */
export function supportedCapabilities(registry = FIELD_REGISTRY) {
  const out = new Set();
  for (const fields of Object.values(registry))
    for (const feature of Object.values(fields))
      if (feature !== BASELINE && feature !== INFO) out.add(feature);
  return out;
}

/** 读取侧：清单已解析为对象后调用；不认识的能力、格式错误的 requires 一律拒绝。 */
export function checkRequires(name, manifest, registry = FIELD_REGISTRY) {
  if (!manifest || typeof manifest !== 'object' || Array.isArray(manifest))
    throw new Error(`${name} 必须是 JSON 对象`);
  let found = false;
  for (const key of Object.keys(manifest)) {
    if (key.toLowerCase() !== 'requires') continue;
    if (key !== 'requires' || found)
      throw new Error(`${name} 的 requires 键无效`);
    found = true;
  }
  if (!found) return;
  const list = manifest.requires;
  if (!Array.isArray(list) || list.some((item) => typeof item !== 'string'))
    throw new Error(`${name} 的 requires 必须是字符串数组`);
  const supported = supportedCapabilities(registry);
  const missing = [...new Set(list.filter((item) => !supported.has(item)))];
  if (missing.length)
    throw new Error(
      `${name} 需要更新的客户端发行脚本（当前版本不支持：${missing.join('、')}）。${NEWER_HINT}`,
    );
}

/**
 * 写出侧：校验每个字段都已登记，并按需补顶层 requires。
 * 全是基线字段时原样返回同一对象（逐字节不变）。
 */
export function stampRequires(name, manifest, registry = FIELD_REGISTRY) {
  const fields = registry[name];
  if (!fields) throw new Error(`未登记的清单文件：${name}`);
  const capabilities = new Set();
  let needs = false;
  for (const key of Object.keys(manifest)) {
    if (key === 'requires') continue;
    if (!Object.hasOwn(fields, key))
      throw new Error(`${name} 的字段 ${key} 尚未在 compat.mjs 登记`);
    const feature = fields[key];
    if (feature === BASELINE) continue;
    needs = true;
    if (feature !== INFO) capabilities.add(feature);
  }
  if (!needs) return manifest;
  return { ...manifest, requires: [...capabilities].sort() };
}
