/**
 * 现场客户端依赖 CLI（供 auriga deps … --client / client build 的 resolve-devkit 调用）。
 * 缓存布局：<cache>/inputs/client/<lockSha256>/，基线：<cache>/client-tools-baseline.json。
 * 缓存目录的 ACL 校验与并发锁由 Go 侧（auriga.exe）在调用本脚本前完成。
 */
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  HASH,
  ROOT,
  digest,
  lockIdentity,
  systemTools,
  verifyInputs,
} from './inputs.mjs';
import { CLIENT_TARGET } from './platform.mjs';
import { verifySignedInventory } from './trust.mjs';
import { prepareInputs } from './prepare.mjs';
import {
  BASELINE_FILENAME,
  registerBaseline,
  readBaseline,
} from './baseline.mjs';

export const CLIENT_ECO = 'client';
export const SITE_NOTICE =
  '现场客户端依赖写入可写缓存；输入摘要由 windows-client.json 与锁键约束，请人工核对 site-dependencies.json。';

const NEW_DEPS_REJECTED =
  '存在新增依赖，acceptNewDeps=false，未拉取任何依赖；审核清单后在配置中设置 deps.acceptNewDeps=true 或加 --accept-new-deps 重试';
const FINGERPRINT = /^[a-f0-9]{64}$/;

export function clientCacheEntry(cacheDir, lockSha256) {
  return join(resolve(cacheDir), 'inputs', CLIENT_ECO, lockSha256);
}

function looksLikeClientKit(dir) {
  return existsSync(join(dir, 'windows-client.json'));
}

function manifestDigest(dir) {
  return digest(readFileSync(join(dir, 'windows-client.json')));
}

/** 统一套件配置：字符串（无独立信任）或 { path, trustFingerprint }。 */
function normalizeKits(kits = []) {
  return kits.map((kit) =>
    typeof kit === 'string'
      ? { path: kit, trustFingerprint: null }
      : {
          path: kit.path,
          trustFingerprint: kit.trustFingerprint
            ? String(kit.trustFingerprint).replace(/^SHA256:/, '')
            : null,
        },
  );
}

/**
 * 可写缓存条目：以写入时（fetch/import）记录在 site-dependencies.json 的清单摘要为准，
 * 当前 windows-client.json 必须与之相同；缓存本身的权限边界由 Go 侧 ACL 校验保证。
 */
export function readCacheRecord(dir, lockSha256) {
  const recordPath = join(dir, 'site-dependencies.json');
  if (!existsSync(recordPath))
    throw new Error(
      `缓存条目缺少 site-dependencies.json，无法确认写入时记录的摘要：${dir}；请删除该条目后重新 deps fetch --client 或 deps import --client`,
    );
  let record;
  try {
    record = JSON.parse(readFileSync(recordPath, 'utf8'));
  } catch {
    throw new Error(`缓存条目记录不是有效 JSON：${recordPath}`);
  }
  if (
    record?.ecosystem !== CLIENT_ECO ||
    record.lockSha256 !== lockSha256 ||
    !HASH.test(record.manifestSha256 ?? '')
  )
    throw new Error(
      `缓存条目记录无效（生态、锁摘要或清单摘要不符）：${recordPath}；请删除该条目后重新准备`,
    );
  if (manifestDigest(dir) !== record.manifestSha256)
    throw new Error(
      `缓存条目 windows-client.json 与写入时记录的摘要不符（可能被改动）：${dir}`,
    );
  return record;
}

/** 发行签名的客户端套件：用配置的独立指纹验签，返回签名元数据绑定的清单摘要。 */
export function verifySignedClientKit(dir, lockSha256, trustFingerprint) {
  const signed = verifySignedInventory(dir, { trustFingerprint });
  const release = signed.verifyMetadata('client-inputs-release.json');
  if (
    release.schemaVersion !== 1 ||
    release.platform !== CLIENT_TARGET ||
    release.lockSha256 !== lockSha256 ||
    !HASH.test(release.manifestSha256 ?? '')
  )
    throw new Error(`套件签名元数据与当前锁摘要不符：${dir}`);
  if (manifestDigest(dir) !== release.manifestSha256)
    throw new Error(`套件 windows-client.json 与签名元数据不符：${dir}`);
  return {
    manifestSha256: release.manifestSha256,
    fingerprint: signed.fingerprint,
  };
}

/**
 * 按锁摘要查找客户端套件（D5：先可写缓存，再 deps.kits）。
 * 缓存条目以写入记录核对；套件必须带独立信任 trustFingerprint 并通过发行签名校验，
 * 不接受套件用自带公钥或自身清单摘要自证。
 */
export function findClientKit(cacheDir, lockSha256, kits = []) {
  if (!HASH.test(lockSha256)) throw new Error('客户端锁摘要无效');
  const cacheHit = clientCacheEntry(cacheDir, lockSha256);
  if (looksLikeClientKit(cacheHit)) {
    const record = readCacheRecord(cacheHit, lockSha256);
    return {
      location: cacheHit,
      source: 'cache',
      devkitSha256: record.manifestSha256,
      signed: false,
    };
  }
  for (const kit of normalizeKits(kits)) {
    const root = resolve(kit.path);
    const candidates = [
      join(root, 'inputs', CLIENT_ECO, lockSha256),
      join(root, `auriga-client-windows-${lockSha256}`),
      root,
    ];
    for (const dir of candidates) {
      if (!looksLikeClientKit(dir)) continue;
      let manifest;
      try {
        manifest = JSON.parse(
          readFileSync(join(dir, 'windows-client.json'), 'utf8'),
        );
      } catch {
        continue;
      }
      if (manifest.lockSha256 !== lockSha256) continue;
      if (!FINGERPRINT.test(kit.trustFingerprint ?? ''))
        throw new Error(
          `套件 ${root} 含锁摘要 ${lockSha256}，但没有独立信任 trustFingerprint，拒绝使用（不接受套件自证）`,
        );
      const verified = verifySignedClientKit(
        dir,
        lockSha256,
        kit.trustFingerprint,
      );
      return {
        location: dir,
        source: 'kit',
        kit: root,
        devkitSha256: verified.manifestSha256,
        signed: true,
        fingerprint: verified.fingerprint,
      };
    }
  }
  return null;
}

export function clientStatus({
  cacheDir,
  sourceRoot = ROOT,
  kits = [],
  allowPublic = false,
  acceptNewDeps = false,
  filesSource,
  npmRegistry,
} = {}) {
  const identity = lockIdentity(sourceRoot);
  const hit = findClientKit(cacheDir, identity.lockSha256, kits);
  let baseline = null;
  try {
    baseline = readBaseline(cacheDir);
  } catch {
    baseline = null;
  }
  const missing = [];
  if (!filesSource && !allowPublic) missing.push('deps.sources.files');
  if (!npmRegistry && !allowPublic) missing.push('deps.sources.npm');
  return {
    ecosystem: CLIENT_ECO,
    lockSha256: identity.lockSha256,
    buildNode: identity.buildNode,
    present: Boolean(hit),
    location: hit?.location ?? null,
    source: hit?.source ?? null,
    devkitSha256: hit?.devkitSha256 ?? null,
    allowPublic,
    acceptNewDeps,
    filesSource: filesSource || null,
    npmRegistry: npmRegistry || null,
    baseline: baseline
      ? {
          path: join(resolve(cacheDir), BASELINE_FILENAME),
          registeredAt: baseline.registeredAt,
          windowsVersion: baseline.windowsVersion,
        }
      : null,
    remedy: hit
      ? null
      : missing.length
        ? `配置 ${missing.join('、')} 或 allowPublic=true 后 auriga deps fetch --client --accept-new-deps；或 auriga deps import --client <套件目录>`
        : 'auriga deps fetch --client --accept-new-deps',
  };
}

function writeOrigin(dir, origin) {
  writeFileSync(
    join(dir, 'auriga-origin.json'),
    JSON.stringify(origin, null, 2) + '\n',
    { flag: 'wx' },
  );
}

function writeSiteDependencies(dir, report) {
  writeFileSync(
    join(dir, 'site-dependencies.json'),
    JSON.stringify(report, null, 2) + '\n',
    { flag: 'wx' },
  );
}

/**
 * 联网（或 files 源）准备客户端输入并写入 inputs/client/<lockSha256>/。
 * 缓存/套件均未命中且 acceptNewDeps=false 时拒绝；allowPublic=false 时必须配置 files 与 npm 源。
 */
export async function clientFetch(options) {
  const {
    cacheDir,
    sourceRoot = ROOT,
    kits = [],
    allowPublic = false,
    acceptNewDeps = false,
    filesSource,
    npmRegistry,
    caFile,
    prepare = prepareInputs,
    log = console.log,
  } = options;
  const cache = resolve(cacheDir);
  mkdirSync(cache, { recursive: true });
  const identity = lockIdentity(sourceRoot);
  const existing = findClientKit(cache, identity.lockSha256, kits);
  if (existing) {
    log(
      JSON.stringify(
        {
          ok: true,
          action: 'hit',
          lockSha256: identity.lockSha256,
          location: existing.location,
          source: existing.source,
        },
        null,
        2,
      ),
    );
    return { ...existing, lockSha256: identity.lockSha256, action: 'hit' };
  }
  if (!acceptNewDeps) throw new Error(NEW_DEPS_REJECTED);
  if (!filesSource && !allowPublic)
    throw new Error(
      '缺少 deps.sources.files，且 allowPublic=false，无法准备客户端文件类输入（构建 Node、CPython 与 wheel、Node runtime 等）',
    );
  if (!npmRegistry && !allowPublic)
    throw new Error(
      '缺少 deps.sources.npm，且 allowPublic=false，无法准备 pnpm、DSH runtime、插件与前端 npm 依赖',
    );

  const stagingParent = join(cache, '_client-fetch-staging');
  rmSync(stagingParent, { recursive: true, force: true });
  mkdirSync(stagingParent, { recursive: true });
  const target = clientCacheEntry(cache, identity.lockSha256);
  mkdirSync(dirname(target), { recursive: true });
  try {
    const manifest = await prepare(stagingParent, {
      filesSource,
      allowPublic,
      npmRegistry,
      caFile,
      root: sourceRoot,
    });
    const prepared = manifest.directory;
    if (existsSync(target)) throw new Error(`缓存条目已存在：${target}`);
    if (manifestDigest(prepared) !== manifest.manifestSha256)
      throw new Error('准备结果清单摘要不一致');
    // 记录先写进暂存目录再整体改名，缓存里不会出现缺记录的条目。
    writeSiteDependencies(prepared, {
      schemaVersion: 1,
      online: true,
      ecosystem: CLIENT_ECO,
      lockSha256: identity.lockSha256,
      manifestSha256: manifest.manifestSha256,
      ecosystems: [CLIENT_ECO],
      added: [],
      removed: [],
      changedLockFiles: Object.keys(identity.locks),
      filesSource: filesSource || null,
      npmRegistry: npmRegistry || null,
      allowPublic,
      notice: SITE_NOTICE,
    });
    writeOrigin(prepared, {
      schemaVersion: 1,
      kind: 'fetch',
      ecosystem: CLIENT_ECO,
      lockDigest: identity.lockSha256,
      from: [
        filesSource ? `files:${filesSource}` : 'public',
        npmRegistry ? `npm:${npmRegistry}` : 'npm:public',
      ],
      at: new Date().toISOString(),
      signed: false,
      sumsSha256: manifest.manifestSha256,
    });
    renameSync(prepared, target);
    rmSync(stagingParent, { recursive: true, force: true });
    log(
      JSON.stringify(
        {
          ok: true,
          action: 'fetch',
          lockSha256: identity.lockSha256,
          location: target,
          manifestSha256: manifest.manifestSha256,
        },
        null,
        2,
      ),
    );
    return {
      location: target,
      source: 'cache',
      lockSha256: identity.lockSha256,
      action: 'fetch',
      manifestSha256: manifest.manifestSha256,
    };
  } catch (error) {
    rmSync(stagingParent, { recursive: true, force: true });
    throw error;
  }
}

function inside(child, parent) {
  const rel = relative(resolve(parent), resolve(child));
  return rel === '' || (!rel.startsWith('..') && !isAbsolute(rel));
}

/**
 * 把已有客户端套件目录导入可写缓存（不联网）。先整体复制到缓存内暂存目录，再对副本
 * 校验（避免校验后源目录被改动），通过后改名为 inputs/client/<锁摘要>/。
 * 指定 trustFingerprint 时副本必须带发行签名并通过验签；否则按清单逐文件自洽校验，
 * 此时导入源本身须位于管理员受控目录（Go 侧 ACL 校验）。
 */
export function clientImport({
  cacheDir,
  from,
  sourceRoot = ROOT,
  trustFingerprint,
  log = console.log,
} = {}) {
  const identity = lockIdentity(sourceRoot);
  const src = resolve(from);
  if (!looksLikeClientKit(src))
    throw new Error(`导入源不是客户端套件（缺少 windows-client.json）：${src}`);
  const cache = resolve(cacheDir);
  if (inside(src, cache) || inside(cache, src))
    throw new Error('不能从缓存目录自身导入，导入源也不能包含缓存');
  const target = clientCacheEntry(cache, identity.lockSha256);
  if (existsSync(target))
    throw new Error(
      `缓存已有 ${CLIENT_ECO}/${identity.lockSha256}，保留原条目`,
    );
  const fp = trustFingerprint
    ? String(trustFingerprint).replace(/^SHA256:/, '')
    : null;
  if (fp && !FINGERPRINT.test(fp))
    throw new Error('--trust-fingerprint 需要 SHA256:<64 位小写十六进制>');
  mkdirSync(dirname(target), { recursive: true });
  const staging = join(cache, '_client-import-staging');
  rmSync(staging, { recursive: true, force: true });
  try {
    cpSync(src, staging, { recursive: true, verbatimSymlinks: true });
    let expected;
    let signed = false;
    let fingerprint = null;
    if (fp) {
      if (!existsSync(join(staging, 'SHA256SUMS.sig')))
        throw new Error('指定了 --trust-fingerprint，但导入源没有发行签名');
      const verified = verifySignedClientKit(staging, identity.lockSha256, fp);
      expected = verified.manifestSha256;
      signed = true;
      fingerprint = verified.fingerprint;
    } else {
      expected = manifestDigest(staging);
    }
    // 逐文件核对副本（锁摘要必须与当前源码一致）；系统工具留到构建时按清单/基线核对。
    verifyInputs(staging, expected, sourceRoot, { checkTools: false });
    let sourceRecord = null;
    const oldRecord = join(staging, 'site-dependencies.json');
    if (existsSync(oldRecord)) {
      try {
        sourceRecord = JSON.parse(readFileSync(oldRecord, 'utf8'));
      } catch {
        sourceRecord = { invalid: true };
      }
      rmSync(oldRecord);
    }
    rmSync(join(staging, 'auriga-origin.json'), { force: true });
    writeSiteDependencies(staging, {
      schemaVersion: 1,
      online: false,
      ecosystem: CLIENT_ECO,
      lockSha256: identity.lockSha256,
      manifestSha256: expected,
      ecosystems: [CLIENT_ECO],
      added: [],
      removed: [],
      notice: SITE_NOTICE,
      importedFrom: src,
      signed,
      ...(fingerprint ? { fingerprint } : {}),
      ...(sourceRecord ? { sourceRecord } : {}),
    });
    writeOrigin(staging, {
      schemaVersion: 1,
      kind: 'import',
      ecosystem: CLIENT_ECO,
      lockDigest: identity.lockSha256,
      from: [src],
      at: new Date().toISOString(),
      signed,
      sumsSha256: expected,
    });
    renameSync(staging, target);
  } catch (error) {
    rmSync(staging, { recursive: true, force: true });
    throw error;
  }
  log(
    JSON.stringify(
      {
        ok: true,
        action: 'import',
        lockSha256: identity.lockSha256,
        location: target,
      },
      null,
      2,
    ),
  );
  return {
    location: target,
    lockSha256: identity.lockSha256,
    action: 'import',
  };
}

export function clientBaseline({ cacheDir, log = console.log } = {}) {
  const baseline = registerBaseline(cacheDir, systemTools());
  const path = join(resolve(cacheDir), BASELINE_FILENAME);
  log(
    JSON.stringify(
      {
        ok: true,
        action: 'baseline',
        path,
        windowsVersion: baseline.windowsVersion,
        registeredAt: baseline.registeredAt,
        tools: baseline.tools.length,
      },
      null,
      2,
    ),
  );
  return { path, baseline };
}

/** 解析构建用的客户端套件路径：缓存 → 套件 → 缺失。 */
export function resolveDevkit({
  cacheDir,
  sourceRoot = ROOT,
  kits = [],
  log = console.log,
} = {}) {
  const identity = lockIdentity(sourceRoot);
  const hit = findClientKit(cacheDir, identity.lockSha256, kits);
  if (!hit) {
    const err = new Error(
      `缺少客户端套件（锁摘要 ${identity.lockSha256}）。请先 auriga deps fetch --client 或 deps import --client，或在 deps.kits 配置含该锁且带 trustFingerprint 的发行套件`,
    );
    err.code = 'CLIENT_KIT_MISSING';
    throw err;
  }
  const out = {
    ok: true,
    action: 'resolve-devkit',
    lockSha256: identity.lockSha256,
    location: hit.location,
    source: hit.source,
    siteDevkit: hit.location,
    devkitSha256: hit.devkitSha256,
    signed: hit.signed,
    ...(hit.fingerprint ? { fingerprint: hit.fingerprint } : {}),
  };
  log(JSON.stringify(out, null, 2));
  return out;
}

const VALUED = new Set([
  'cache',
  'source',
  'files',
  'npm',
  'ca-file',
  'from',
  'trust-fingerprint',
]);

export function parseArgs(argv) {
  const args = { _: [], kit: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--') continue;
    if (a.startsWith('--')) {
      const key = a.slice(2);
      if (
        key === 'allow-public' ||
        key === 'accept-new-deps' ||
        key === 'json'
      ) {
        args[key] = true;
        continue;
      }
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--'))
        throw new Error(`参数 ${a} 需要值`);
      i += 1;
      if (key === 'kit') args.kit.push({ path: next, trustFingerprint: null });
      else if (key === 'kit-fingerprint') {
        const last = args.kit.at(-1);
        if (!last || last.trustFingerprint)
          throw new Error('--kit-fingerprint 必须紧跟对应的 --kit');
        last.trustFingerprint = next;
      } else if (VALUED.has(key)) args[key] = next;
      else throw new Error(`未知参数 ${a}`);
      continue;
    }
    args._.push(a);
  }
  return args;
}

export async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv);
  const command = args._[0];
  if (!command)
    throw new Error(
      '用法：site-deps.mjs status|fetch|import|baseline|resolve-devkit …',
    );
  const cacheDir = args.cache;
  if (!cacheDir) throw new Error(`${command} 需要 --cache`);
  const sourceRoot = args.source ? resolve(args.source) : ROOT;
  const common = {
    cacheDir,
    sourceRoot,
    kits: args.kit,
    allowPublic: Boolean(args['allow-public']),
    acceptNewDeps: Boolean(args['accept-new-deps']),
    filesSource: args.files,
    npmRegistry: args.npm,
    caFile: args['ca-file'],
  };
  switch (command) {
    case 'status': {
      const report = clientStatus(common);
      console.log(JSON.stringify(report, null, 2));
      return report;
    }
    case 'fetch':
      return await clientFetch(common);
    case 'import': {
      const from = args.from || args._[1];
      if (!from) throw new Error('import 需要 --from <套件目录>');
      return clientImport({
        cacheDir,
        from,
        sourceRoot,
        trustFingerprint: args['trust-fingerprint'],
      });
    }
    case 'baseline':
      return clientBaseline({ cacheDir });
    case 'resolve-devkit':
      return resolveDevkit({ cacheDir, sourceRoot, kits: args.kit });
    default:
      throw new Error(`未知子命令：${command}`);
  }
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
