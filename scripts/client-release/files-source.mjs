/** 文件类依赖源：按锁定 URL 映射到 deps.sources.files，支持 file:// 与 https。 */
import { spawnSync } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  statSync,
  unlinkSync,
} from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

/**
 * 把锁定 URL 映射到 files 源下的候选位置：先 `<files>/<pathname>`，再 `<files>/<basename>`。
 * pathname 去掉开头的 `/` 后逐段做百分号解码（如 `%2B` → `+`，介质上按真实文件名存放），
 * 解码后的段不得为空、`.`、`..`，不得含 `/ \ :`、控制字符或 Windows 保留字符，
 * 防止借编码穿越到 files 源之外或指向备用数据流。files 源可为 https://、file:// 或本地目录。
 */
export function mapLockedUrlCandidates(lockedUrl, filesSource) {
  if (!filesSource || typeof filesSource !== 'string' || !filesSource.trim())
    throw new Error('files 依赖源未配置');
  let locked;
  try {
    locked = new URL(lockedUrl);
  } catch {
    throw new Error(`锁定 URL 无效：${lockedUrl}`);
  }
  if (locked.protocol !== 'https:' && locked.protocol !== 'file:')
    throw new Error(`锁定 URL 只支持 https 或 file：${lockedUrl}`);
  const raw = locked.pathname.replace(/^\/+/, '');
  if (!raw) throw new Error(`锁定 URL 路径不安全：${lockedUrl}`);
  const segments = raw.split('/').map((part) => {
    let decoded;
    try {
      decoded = decodeURIComponent(part);
    } catch {
      throw new Error(`锁定 URL 路径编码无效：${lockedUrl}`);
    }
    if (
      !decoded ||
      decoded === '.' ||
      decoded === '..' ||
      /[\\/:<>"|?*]/.test(decoded) ||
      [...decoded].some((c) => c.charCodeAt(0) < 32) ||
      /[. ]$/.test(decoded)
    )
      throw new Error(`锁定 URL 路径不安全：${lockedUrl}`);
    return decoded;
  });
  const root = filesSource.trim().replace(/\/+$/, '');
  return [
    joinFilesSource(root, segments),
    joinFilesSource(root, segments.slice(-1)),
  ];
}

function joinFilesSource(root, segments) {
  let asUrl = null;
  try {
    asUrl = new URL(root);
  } catch {
    asUrl = null;
  }
  // 单字母“协议”其实是 Windows 盘符（D:\x），按本地目录处理。
  if (asUrl && asUrl.protocol.length > 2) {
    if (asUrl.protocol === 'https:') {
      if (asUrl.username || asUrl.password || asUrl.search || asUrl.hash)
        throw new Error('files 依赖源不能内嵌凭据、查询或片段');
      return `${root}/${segments.map(encodeURIComponent).join('/')}`;
    }
    if (asUrl.protocol === 'file:') {
      const dir = fileURLToPath(asUrl);
      return pathToFileURL(join(dir, ...segments)).href;
    }
    throw new Error(`files 依赖源只支持 https 或 file：${root}`);
  }
  // 非 URL（含 Windows 盘符路径 D:\x）：当作本地目录路径。
  return pathToFileURL(join(resolve(root), ...segments)).href;
}

/** 解析候选为本地路径（仅 file://）或远程 https URL。 */
export function describeCandidate(candidate) {
  const u = new URL(candidate);
  if (u.protocol === 'file:')
    return { kind: 'file', path: fileURLToPath(u), href: candidate };
  if (u.protocol === 'https:')
    return { kind: 'https', path: null, href: candidate };
  throw new Error(`不支持的候选协议：${candidate}`);
}

function curlHttps(url, path, caFile) {
  const result = spawnSync(
    'curl.exe',
    [
      ...(caFile ? ['--cacert', caFile] : []),
      '--fail',
      '--silent',
      '--show-error',
      '--location',
      '--proto',
      '=https',
      '--proto-redir',
      '=https',
      '--max-time',
      '300',
      '--output',
      path,
      url,
    ],
    { stdio: 'inherit', windowsHide: true },
  );
  if (result.status !== 0)
    throw new Error(`缺少联网准备输入：${url}（curl ${result.status}）`);
}

function copyFileUrl(fileUrl, path) {
  const src = fileURLToPath(fileUrl);
  if (!existsSync(src) || !statSync(src).isFile())
    throw new Error(`文件依赖源缺少：${src}`);
  mkdirSync(dirname(path), { recursive: true });
  copyFileSync(src, path);
}

/**
 * 创建下载器。配置了 filesSource 时只从映射候选取（pathname → basename），不回落公网；
 * 未配置时：allowPublic=true 用锁定原 URL（https/file），否则失败并说明缺什么。
 */
export function createDownloader({
  filesSource,
  allowPublic = false,
  caFile,
} = {}) {
  return async function download(url, path) {
    if (existsSync(path)) throw new Error('准备输入禁止覆盖');
    mkdirSync(dirname(path), { recursive: true });

    let candidates;
    if (filesSource) {
      candidates = mapLockedUrlCandidates(url, filesSource);
    } else if (allowPublic) {
      candidates = [url];
    } else {
      const name = (() => {
        try {
          return basename(new URL(url).pathname);
        } catch {
          return url;
        }
      })();
      throw new Error(
        `缺少文件类依赖源（deps.sources.files），无法取得 ${name}（${url}）。请配置 files 源或 deps.import，或显式 allowPublic=true`,
      );
    }

    const errors = [];
    for (const candidate of candidates) {
      try {
        const desc = describeCandidate(candidate);
        if (desc.kind === 'file') copyFileUrl(candidate, path);
        else curlHttps(candidate, path, caFile);
        return;
      } catch (error) {
        errors.push(`${candidate}：${error.message}`);
        if (existsSync(path)) {
          try {
            unlinkSync(path);
          } catch {
            /* 半成品清不掉时留给上层 */
          }
        }
      }
    }
    throw new Error(
      `无法从文件依赖源取得 ${url}。已尝试：\n${errors.map((e) => `  - ${e}`).join('\n')}`,
    );
  };
}
