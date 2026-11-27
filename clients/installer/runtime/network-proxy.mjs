/**
 * Auriga Desktop shell-owned network proxy controller, routing engine,
 * loopback routing proxy server, and controlled HTTP(S)/WS transport.
 *
 * Design invariants:
 * - Default mode is `direct` (ignores OS / inherited environment proxies).
 * - Loopback targets (localhost, localhost., 127.0.0.0/8, ::1, ::ffff:127.0.0.0/104) are ALWAYS direct.
 * - Layered config resolution:
 *     1) User override: <dataHome>/auriga/network-proxy.json (mode 0600)
 *     2) Bundled site defaults: <versionDir>/auriga/defaults.json -> networkProxy (no credentials)
 *     3) Builtin fallback: direct (no proxy)
 * - Zero external dependencies (Node built-ins only).
 */
import { Buffer } from 'node:buffer';
import dns from 'node:dns';
import {
  closeSync,
  constants as fsConstants,
  existsSync,
  fstatSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve as resolvePath } from 'node:path';
import { randomUUID } from 'node:crypto';
import http from 'node:http';
import https from 'node:https';
import net, { BlockList, isIP } from 'node:net';
import tls from 'node:tls';
import { Readable } from 'node:stream';

export const PROXY_SCHEMA_VERSION = 1;
export const MAX_PROXY_RULES = 200;
export const MAX_NO_PROXY_ENTRIES = 200;
export const MAX_DEFAULT_PROXY_FILE_BYTES = 65536;
export const BUILTIN_LOOPBACK_NO_PROXY = Object.freeze([
  'localhost',
  '127.0.0.1',
  '::1',
]);

export const BUILTIN_DEFAULT_PROXY_CONFIG = Object.freeze({
  schemaVersion: PROXY_SCHEMA_VERSION,
  mode: 'direct',
  globalProxy: '',
  noProxy: Object.freeze([]),
  rules: Object.freeze([]),
});

const STRIPPED_PROXY_ENV_KEYS = new Set([
  'http_proxy',
  'https_proxy',
  'all_proxy',
  'no_proxy',
  'npm_config_proxy',
  'npm_config_https_proxy',
  'npm_config_http_proxy',
  'node_use_env_proxy',
]);

const HOP_BY_HOP_HEADERS = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'proxy-connection',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
]);

const CREDENTIAL_PLACEHOLDER = '__AURIGA_KEEP_PASSWORD__';

const LOOPBACK_BLOCKLIST = new BlockList();
LOOPBACK_BLOCKLIST.addSubnet('127.0.0.0', 8, 'ipv4');
LOOPBACK_BLOCKLIST.addAddress('::1', 'ipv6');
LOOPBACK_BLOCKLIST.addSubnet('::ffff:127.0.0.0', 104, 'ipv6');

function proxyError(code, message) {
  return Object.assign(new Error(message ?? code), { code });
}

function bareHost(hostname) {
  return String(hostname ?? '')
    .trim()
    .replace(/^\[|\]$/g, '');
}

/**
 * Create an HTTP/HTTPS Agent that explicitly bypasses Node's built-in
 * environment proxy resolution (Node 24+ `proxyEnv: {}`).
 */
export function createDirectHttpAgent(options = {}) {
  return new http.Agent({ proxyEnv: {}, ...options });
}

export function createDirectHttpsAgent(options = {}) {
  return new https.Agent({ proxyEnv: {}, ...options });
}

/**
 * Walk every existing ancestor segment of `absPath` to ensure no symlink
 * sits in the path hierarchy.
 * @param {string} absPath
 */
function assertNoSymlinkPath(absPath) {
  let cur = resolvePath(absPath);
  for (;;) {
    const st = lstatSync(cur);
    if (st.isSymbolicLink()) {
      throw proxyError('invalidProxyFile', '默认代理文件路径包含符号链接');
    }
    const parent = dirname(cur);
    if (parent === cur) break;
    cur = parent;
  }
}

/**
 * Read a regular, non-symlink JSON file from a single file descriptor with a
 * strict byte ceiling (`maxBytes + 1` buffer) to avoid TOCTOU races and
 * symlink traversal.
 * @param {string} file
 * @param {number} [maxBytes]
 */
export function readBoundedJsonFile(
  file,
  maxBytes = MAX_DEFAULT_PROXY_FILE_BYTES,
) {
  const abs = resolvePath(String(file));
  assertNoSymlinkPath(abs);
  const lst = lstatSync(abs, { bigint: true });
  if (!lst.isFile() || lst.isSymbolicLink()) {
    throw proxyError('invalidProxyFile', '默认代理文件必须是普通文件');
  }
  const flags =
    fsConstants.O_RDONLY |
    (typeof fsConstants.O_NOFOLLOW === 'number' ? fsConstants.O_NOFOLLOW : 0);
  const fd = openSync(abs, flags);
  try {
    const fst = fstatSync(fd, { bigint: true });
    if (
      !fst.isFile() ||
      fst.dev !== lst.dev ||
      fst.ino !== lst.ino ||
      fst.size > BigInt(maxBytes)
    ) {
      throw proxyError('invalidProxyFile', '默认代理文件无效或超过大小上限');
    }
    const buf = Buffer.allocUnsafe(maxBytes + 1);
    let offset = 0;
    while (offset < buf.length) {
      const n = readSync(fd, buf, offset, buf.length - offset, null);
      if (n === 0) break;
      offset += n;
    }
    if (offset > maxBytes) {
      throw proxyError('invalidProxyFile', '默认代理文件超过大小上限');
    }
    return JSON.parse(buf.subarray(0, offset).toString('utf8'));
  } finally {
    closeSync(fd);
  }
}

/**
 * Strip all inherited host/system proxy variables from an environment object.
 * @param {Record<string, string | undefined>} base
 * @returns {Record<string, string | undefined>}
 */
export function stripSystemProxyEnv(base = process.env) {
  const out = {};
  for (const [k, v] of Object.entries(base)) {
    if (STRIPPED_PROXY_ENV_KEYS.has(k.toLowerCase())) continue;
    out[k] = v;
  }
  return out;
}

/**
 * Normalize and validate an HTTP/HTTPS proxy URL.
 * Empty string is allowed when `optional` is true.
 * @param {unknown} input
 * @param {{ optional?: boolean, allowCredentials?: boolean }} [options]
 * @returns {string}
 */
export function normalizeProxyUrl(
  input,
  { optional = false, allowCredentials = true } = {},
) {
  if (input === undefined || input === null || input === '') {
    if (optional) return '';
    throw proxyError('invalidProxyUrl', '代理地址不能为空');
  }
  if (typeof input !== 'string' || input.length > 2048) {
    throw proxyError('invalidProxyUrl', '代理地址格式无效');
  }
  const trimmed = input.trim();
  if (!trimmed) {
    if (optional) return '';
    throw proxyError('invalidProxyUrl', '代理地址不能为空');
  }
  if (/[\r\n\0\s]/.test(trimmed)) {
    throw proxyError('invalidProxyUrl', '代理地址不能包含空白或控制字符');
  }
  let url;
  try {
    url = new URL(trimmed);
  } catch {
    throw proxyError('invalidProxyUrl', '代理地址格式无效');
  }
  if (!['http:', 'https:'].includes(url.protocol)) {
    throw proxyError('invalidProxyUrl', '仅支持 http:// 或 https:// 代理');
  }
  if (
    !url.hostname ||
    url.search ||
    url.hash ||
    (url.pathname && url.pathname !== '/')
  ) {
    throw proxyError('invalidProxyUrl', '代理地址不能包含路径、查询参数或片段');
  }
  if (!allowCredentials && (url.username || url.password)) {
    throw proxyError('invalidProxyUrl', '内置默认代理不允许包含用户名或密码');
  }
  try {
    decodeURIComponent(url.username);
    decodeURIComponent(url.password);
  } catch {
    throw proxyError('invalidProxyUrl', '代理用户名或密码包含无效的百分号编码');
  }
  const port = url.port
    ? Number(url.port)
    : url.protocol === 'https:'
      ? 443
      : 80;
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw proxyError('invalidProxyUrl', '代理端口无效');
  }
  const auth =
    url.username || url.password
      ? `${url.username}${url.password ? ':' + url.password : ''}@`
      : '';
  const explicitPort = url.port ? `:${url.port}` : '';
  return `${url.protocol}//${auth}${url.hostname}${explicitPort}`;
}

/**
 * Mask password in a proxy URL for UI / logs / diagnostics.
 * @param {string} raw
 * @returns {{ display: string, hasPassword: boolean }}
 */
export function maskProxyUrl(raw) {
  if (!raw) return { display: '', hasPassword: false };
  try {
    const url = new URL(raw);
    const hasPassword = Boolean(url.password);
    if (!url.username && !url.password) {
      return {
        display: `${url.protocol}//${url.hostname}${url.port ? ':' + url.port : ''}`,
        hasPassword: false,
      };
    }
    const auth = hasPassword ? `${url.username}:***` : url.username;
    return {
      display: `${url.protocol}//${auth}@${url.hostname}${url.port ? ':' + url.port : ''}`,
      hasPassword,
    };
  } catch {
    return { display: '', hasPassword: false };
  }
}

/**
 * Validate a domain / wildcard suffix / IP (IPv4/IPv6) / CIDR match pattern.
 * @param {unknown} input
 * @returns {string}
 */
export function normalizeMatchPattern(input) {
  if (typeof input !== 'string') {
    throw proxyError('invalidPattern', '匹配规则必须是字符串');
  }
  const raw = input.trim().toLowerCase();
  if (!raw || raw.length > 255 || /[\s\\?#@"'`\r\n\0]/.test(raw)) {
    throw proxyError('invalidPattern', `无效的域名或网段规则: ${input}`);
  }
  if (raw.includes('://')) {
    throw proxyError(
      'invalidPattern',
      '规则请填写域名、IP 或 CIDR，不要包含协议头',
    );
  }
  // 1. CIDR check (IPv4 or IPv6)
  if (raw.includes('/')) {
    const [ipPart, prefixPart, extra] = raw.split('/');
    if (extra !== undefined || !/^\d{1,3}$/.test(prefixPart)) {
      throw proxyError('invalidPattern', `无效的 CIDR 规则: ${input}`);
    }
    const bareIp = bareHost(ipPart);
    const family = isIP(bareIp);
    const prefix = Number(prefixPart);
    if (
      (family === 4 && prefix >= 0 && prefix <= 32) ||
      (family === 6 && prefix >= 0 && prefix <= 128)
    ) {
      return `${bareIp}/${prefix}`;
    }
    throw proxyError('invalidPattern', `无效的 CIDR 规则: ${input}`);
  }
  // 2. Bare or bracketed IP (IPv4 or IPv6)
  const bareIp = bareHost(raw);
  if (isIP(bareIp)) return bareIp;

  // Remaining forms must be domain/wildcard hostnames (no colons or slashes)
  if (raw.includes(':') || raw.includes('/')) {
    throw proxyError('invalidPattern', `无效的域名规则: ${input}`);
  }

  // 3. Wildcard suffix: *.example.com or .example.com
  if (raw.startsWith('*.') || raw.startsWith('.')) {
    const suffix = raw.startsWith('*.') ? raw.slice(2) : raw.slice(1);
    if (!suffix || suffix.includes('*') || !isValidHostname(suffix)) {
      throw proxyError('invalidPattern', `无效的通配域名规则: ${input}`);
    }
    return `*.${suffix}`;
  }

  // 4. Exact hostname
  if (raw.includes('*') || !isValidHostname(raw)) {
    throw proxyError('invalidPattern', `无效的域名规则: ${input}`);
  }
  return raw;
}

function isValidHostname(host) {
  if (
    !host ||
    host.length > 253 ||
    host.startsWith('.') ||
    host.endsWith('.')
  ) {
    return false;
  }
  const labels = host.split('.');
  return labels.every(
    (label) =>
      label.length >= 1 &&
      label.length <= 63 &&
      /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label),
  );
}

/**
 * Restore masked password placeholder from previous config when editing in UI.
 * @param {string} incoming
 * @param {string} previous
 */
function preserveMaskedPassword(incoming, previous) {
  if (!incoming || !previous) return incoming;
  try {
    const nextUrl = new URL(incoming);
    const prevUrl = new URL(previous);
    const maskedToken = decodeURIComponent(nextUrl.password);
    if (
      (maskedToken === '***' || maskedToken === CREDENTIAL_PLACEHOLDER) &&
      nextUrl.protocol === prevUrl.protocol &&
      nextUrl.hostname === prevUrl.hostname &&
      nextUrl.port === prevUrl.port &&
      nextUrl.username === prevUrl.username &&
      prevUrl.password
    ) {
      nextUrl.password = prevUrl.password;
      return normalizeProxyUrl(nextUrl.href);
    }
  } catch {
    // Let normalizeProxyUrl handle invalid inputs.
  }
  return incoming;
}

/**
 * Validate and normalize a full proxy config object.
 * @param {unknown} input
 * @param {ReturnType<typeof validateProxyConfig> | null} [previous]
 * @param {{ allowCredentials?: boolean }} [options]
 */
export function validateProxyConfig(
  input,
  previous = null,
  { allowCredentials = true } = {},
) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) {
    throw proxyError('invalidSettings', '代理配置格式无效');
  }
  const obj = /** @type {Record<string, unknown>} */ (input);
  const schemaVersion = obj.schemaVersion ?? PROXY_SCHEMA_VERSION;
  if (schemaVersion !== PROXY_SCHEMA_VERSION) {
    throw proxyError('invalidSettings', '不支持的代理配置版本');
  }
  if (obj.mode !== 'direct' && obj.mode !== 'proxy') {
    throw proxyError('invalidSettings', '代理模式必须为 direct 或 proxy');
  }

  const rawGlobal =
    typeof obj.globalProxy === 'string' && previous?.globalProxy
      ? preserveMaskedPassword(obj.globalProxy, previous.globalProxy)
      : obj.globalProxy;
  const globalProxy = normalizeProxyUrl(rawGlobal ?? '', {
    optional: true,
    allowCredentials,
  });

  const rawNoProxy = obj.noProxy ?? [];
  if (!Array.isArray(rawNoProxy) || rawNoProxy.length > MAX_NO_PROXY_ENTRIES) {
    throw proxyError('invalidSettings', '直连例外列表无效或超过上限');
  }
  const noProxySet = new Set();
  for (const item of rawNoProxy) {
    if (typeof item === 'string' && !item.trim()) continue;
    noProxySet.add(normalizeMatchPattern(item));
  }

  const rawRules = obj.rules ?? [];
  if (!Array.isArray(rawRules) || rawRules.length > MAX_PROXY_RULES) {
    throw proxyError('invalidSettings', '域名规则列表无效或超过上限');
  }
  const prevById = new Map((previous?.rules ?? []).map((r) => [r.id, r]));
  const seenIds = new Set();
  const rules = [];
  for (let i = 0; i < rawRules.length; i += 1) {
    const item = rawRules[i];
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw proxyError('invalidSettings', '域名规则项格式无效');
    }
    const id =
      typeof item.id === 'string' && item.id.trim()
        ? item.id.trim().slice(0, 64)
        : `rule-${i + 1}`;
    if (seenIds.has(id)) {
      throw proxyError('invalidSettings', `重复的规则 ID: ${id}`);
    }
    seenIds.add(id);
    const enabled = item.enabled !== false;
    const pattern = normalizeMatchPattern(item.pattern);
    if (item.action !== 'proxy' && item.action !== 'direct') {
      throw proxyError('invalidSettings', '规则动作必须为 proxy 或 direct');
    }
    let proxy = '';
    if (item.action === 'proxy') {
      const prevRule = prevById.get(id);
      const candidate =
        typeof item.proxy === 'string' && prevRule?.proxy
          ? preserveMaskedPassword(item.proxy, prevRule.proxy)
          : item.proxy;
      proxy = normalizeProxyUrl(candidate, {
        optional: false,
        allowCredentials,
      });
    }
    rules.push({ id, enabled, pattern, action: item.action, proxy });
  }

  return {
    schemaVersion: PROXY_SCHEMA_VERSION,
    mode: obj.mode,
    globalProxy,
    noProxy: [...noProxySet],
    rules,
  };
}

/**
 * Redact passwords before returning config to settings UI or diagnostics.
 * @param {ReturnType<typeof validateProxyConfig>} config
 */
export function redactProxyConfig(config) {
  const globalMasked = maskProxyUrl(config.globalProxy);
  return {
    schemaVersion: config.schemaVersion,
    mode: config.mode,
    globalProxy: globalMasked.display,
    globalProxyHasPassword: globalMasked.hasPassword,
    noProxy: [...config.noProxy],
    rules: config.rules.map((rule) => {
      const masked = maskProxyUrl(rule.proxy);
      return {
        id: rule.id,
        enabled: rule.enabled,
        pattern: rule.pattern,
        action: rule.action,
        proxy: masked.display,
        hasPassword: masked.hasPassword,
      };
    }),
  };
}

// ------------------------------------------------------------ routing engine

/**
 * Built-in loopback protection: never proxy local endpoints.
 * Handles localhost, trailing-dot localhost., 127.0.0.0/8, ::1, and
 * IPv4-mapped IPv6 loopback (e.g. ::ffff:127.0.0.1 / ::ffff:7f00:1).
 * @param {string} hostname
 */
export function isLoopbackHost(hostname) {
  const host = bareHost(hostname).toLowerCase().replace(/\.+$/, '');
  if (!host) return false;
  if (host === 'localhost' || host.endsWith('.localhost')) {
    return true;
  }
  const family = isIP(host);
  if (family === 4) {
    return LOOPBACK_BLOCKLIST.check(host, 'ipv4');
  }
  if (family === 6) {
    return LOOPBACK_BLOCKLIST.check(host, 'ipv6');
  }
  return false;
}

/**
 * Check if a target hostname matches a normalized pattern.
 * @param {string} targetHost
 * @param {string} pattern
 */
export function matchesPattern(targetHost, pattern) {
  const host = bareHost(targetHost).toLowerCase().replace(/\.+$/, '');
  if (!host || !pattern) return false;

  if (pattern.includes('/')) {
    const [ipPart, prefixStr] = pattern.split('/');
    const ipFamily = isIP(ipPart);
    const hostFamily = isIP(host);
    if (!ipFamily || ipFamily !== hostFamily) return false;
    const list = new BlockList();
    list.addSubnet(ipPart, Number(prefixStr), ipFamily === 4 ? 'ipv4' : 'ipv6');
    return list.check(host, hostFamily === 4 ? 'ipv4' : 'ipv6');
  }

  const patIpFamily = isIP(pattern);
  if (patIpFamily) {
    const hostFamily = isIP(host);
    if (patIpFamily !== hostFamily) return false;
    const list = new BlockList();
    list.addAddress(pattern, patIpFamily === 4 ? 'ipv4' : 'ipv6');
    return list.check(host, hostFamily === 4 ? 'ipv4' : 'ipv6');
  }

  if (pattern.startsWith('*.')) {
    const suffix = pattern.slice(1); // '.example.com'
    return host.endsWith(suffix) && host.length > suffix.length;
  }

  return host === pattern;
}

/**
 * Deterministic proxy resolution:
 *  1) Built-in loopback -> direct (cannot be overridden)
 *  2) mode === 'direct' -> direct
 *  3) rules (ordered, enabled only, first match wins)
 *  4) noProxy -> direct
 *  5) globalProxy -> proxy (if set) else direct
 *
 * @param {string} targetHost
 * @param {number | undefined} targetPort
 * @param {ReturnType<typeof validateProxyConfig>} config
 * @param {{ selfPort?: number }} [options]
 * @returns {{ action: 'direct' | 'proxy', proxy: string, matchedBy: string, ruleId?: string }}
 */
export function resolveProxy(
  targetHost,
  targetPort,
  config,
  { selfPort } = {},
) {
  const host = bareHost(targetHost).toLowerCase().replace(/\.+$/, '');

  if (selfPort && isLoopbackHost(host) && Number(targetPort) === selfPort) {
    throw proxyError('proxy_loop', '目标地址不能指向客户端本地分流端口');
  }

  if (isLoopbackHost(host)) {
    return { action: 'direct', proxy: '', matchedBy: 'builtin_loopback' };
  }
  if (!config || config.mode !== 'proxy') {
    return { action: 'direct', proxy: '', matchedBy: 'mode_direct' };
  }

  const checkLoop = (proxyStr) => {
    if (!selfPort || !proxyStr) return;
    try {
      const u = new URL(proxyStr);
      const p = u.port ? Number(u.port) : u.protocol === 'https:' ? 443 : 80;
      if (isLoopbackHost(u.hostname) && p === selfPort) {
        throw proxyError('proxy_loop', '代理地址不能指向客户端本地分流端口');
      }
    } catch (err) {
      if (err?.code === 'proxy_loop') throw err;
    }
  };

  for (const rule of config.rules ?? []) {
    if (!rule.enabled) continue;
    if (matchesPattern(host, rule.pattern)) {
      if (rule.action === 'direct') {
        return {
          action: 'direct',
          proxy: '',
          matchedBy: `rule:${rule.pattern}`,
          ruleId: rule.id,
        };
      }
      checkLoop(rule.proxy);
      return {
        action: 'proxy',
        proxy: rule.proxy,
        matchedBy: `rule:${rule.pattern}`,
        ruleId: rule.id,
      };
    }
  }

  for (const entry of config.noProxy ?? []) {
    if (matchesPattern(host, entry)) {
      return {
        action: 'direct',
        proxy: '',
        matchedBy: `noProxy:${entry}`,
      };
    }
  }

  if (config.globalProxy) {
    checkLoop(config.globalProxy);
    return {
      action: 'proxy',
      proxy: config.globalProxy,
      matchedBy: 'globalProxy',
    };
  }

  return { action: 'direct', proxy: '', matchedBy: 'default_direct' };
}

// ------------------------------------------------------------ layered store

export function userProxyConfigPath(dataHome) {
  return join(dataHome, 'auriga', 'network-proxy.json');
}

export function bundledDefaultsPath(versionDir) {
  return versionDir ? join(versionDir, 'auriga', 'defaults.json') : null;
}

function fileStamp(path) {
  if (!path) return 'none';
  try {
    const st = statSync(path);
    return `${st.mtimeMs}:${st.size}`;
  } catch {
    return 'missing';
  }
}

function readBundledProxyDefault(defaultsFile) {
  if (!defaultsFile || !existsSync(defaultsFile)) return null;
  try {
    const parsed = JSON.parse(readFileSync(defaultsFile, 'utf8'));
    if (!parsed?.networkProxy) return null;
    return validateProxyConfig(parsed.networkProxy, null, {
      allowCredentials: false,
    });
  } catch {
    return null;
  }
}

function writeJsonAtomic(file, data) {
  mkdirSync(dirname(file), { recursive: true });
  const temp = `${file}.${randomUUID()}.tmp`;
  try {
    writeFileSync(temp, JSON.stringify(data, null, 2) + '\n', {
      mode: 0o600,
      flag: 'wx',
    });
    renameSync(temp, file);
  } finally {
    rmSync(temp, { force: true });
  }
}

/**
 * Create a layered, hot-reloading proxy configuration controller.
 * Resolution order: user override > bundled defaults.json > builtin direct.
 *
 * @param {{ dataHome: string, defaultsFile?: string | null, onChange?: (snapshot: any) => void }} options
 */
export function createProxyConfigStore({
  dataHome,
  defaultsFile = null,
  onChange,
} = {}) {
  const userFile = userProxyConfigPath(dataHome);
  let cache = null;

  function loadFresh() {
    const userStamp = fileStamp(userFile);
    const defStamp = fileStamp(defaultsFile);
    if (cache && cache.userStamp === userStamp && cache.defStamp === defStamp) {
      return cache.value;
    }

    const bundled = readBundledProxyDefault(defaultsFile);
    const fallbackConfig = bundled ?? {
      schemaVersion: PROXY_SCHEMA_VERSION,
      mode: 'direct',
      globalProxy: '',
      noProxy: [],
      rules: [],
    };
    const fallbackSource = bundled ? 'bundled' : 'builtin';

    let config = fallbackConfig;
    let source = fallbackSource;
    let degraded = false;

    if (userStamp !== 'missing') {
      try {
        config = validateProxyConfig(
          JSON.parse(readFileSync(userFile, 'utf8')),
        );
        source = 'user';
      } catch {
        // Corrupt user override safely falls back without blocking launch or reverting to OS proxy.
        config = fallbackConfig;
        source = fallbackSource;
        degraded = true;
      }
    }

    const value = {
      config,
      source,
      hasBundledDefault: Boolean(bundled),
      degraded,
    };
    cache = { userStamp, defStamp, value };
    return value;
  }

  return {
    userFile,
    defaultsFile,
    get() {
      return loadFresh();
    },
    snapshot() {
      const current = loadFresh();
      return {
        ...redactProxyConfig(current.config),
        source: current.source,
        hasBundledDefault: current.hasBundledDefault,
        degraded: current.degraded,
        builtinNoProxy: [...BUILTIN_LOOPBACK_NO_PROXY],
      };
    },
    save(input) {
      const previous = loadFresh().config;
      const validated = validateProxyConfig(input, previous);
      writeJsonAtomic(userFile, validated);
      cache = null;
      const next = loadFresh();
      onChange?.(next);
      return this.snapshot();
    },
    reset() {
      rmSync(userFile, { force: true });
      cache = null;
      const next = loadFresh();
      onChange?.(next);
      return this.snapshot();
    },
  };
}

// ------------------------------------------------------------ upstream tunnel

function buildProxyAuthHeader(proxyUrl) {
  if (!proxyUrl.username && !proxyUrl.password) return undefined;
  let user = '';
  let pass = '';
  try {
    user = decodeURIComponent(proxyUrl.username);
    pass = decodeURIComponent(proxyUrl.password);
  } catch {
    throw proxyError('invalidProxyUrl', '代理凭据编码无效');
  }
  return `Basic ${Buffer.from(`${user}:${pass}`).toString('base64')}`;
}

/**
 * Establish a raw TCP tunnel to `targetHost:targetPort` through an upstream HTTP or HTTPS proxy
 * using the HTTP CONNECT method.
 *
 * @param {string} proxyUrlStr
 * @param {string} targetHost
 * @param {number} targetPort
 * @param {{ timeoutMs?: number, signal?: AbortSignal }} [options]
 * @returns {Promise<{ socket: net.Socket, buffered: Buffer }>}
 */
export function connectViaHttpProxy(
  proxyUrlStr,
  targetHost,
  targetPort,
  { timeoutMs = 15_000, signal } = {},
) {
  let proxyUrl;
  let authHeader;
  try {
    proxyUrl = new URL(proxyUrlStr);
    authHeader = buildProxyAuthHeader(proxyUrl);
  } catch (err) {
    return Promise.reject(
      err?.code ? err : proxyError('invalidProxyUrl', '代理地址无效'),
    );
  }
  const proxyHost = bareHost(proxyUrl.hostname);
  const proxyPort = proxyUrl.port
    ? Number(proxyUrl.port)
    : proxyUrl.protocol === 'https:'
      ? 443
      : 80;
  const cleanTarget = bareHost(targetHost);
  const hostHeader =
    isIP(cleanTarget) === 6
      ? `[${cleanTarget}]:${targetPort}`
      : `${cleanTarget}:${targetPort}`;

  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(proxyError('aborted', '连接已取消'));
      return;
    }

    let settled = false;
    let headerBuf = Buffer.alloc(0);

    const socket =
      proxyUrl.protocol === 'https:'
        ? tls.connect({
            host: proxyHost,
            port: proxyPort,
            servername: isIP(proxyHost) ? undefined : proxyHost,
          })
        : net.connect({ host: proxyHost, port: proxyPort });

    const cleanupListeners = () => {
      globalThis.clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      socket.removeListener('data', onData);
      socket.removeListener('error', onError);
      socket.removeListener('close', onClose);
    };

    const fail = (err) => {
      if (settled) return;
      settled = true;
      cleanupListeners();
      socket.destroy();
      reject(err);
    };

    const onAbort = () => fail(proxyError('aborted', '连接已取消'));
    const onError = (err) =>
      fail(
        Object.assign(proxyError('proxy_unreachable', err.message), {
          cause: err,
        }),
      );
    const onClose = () =>
      fail(proxyError('proxy_unreachable', '代理连接提前关闭'));

    const timer = setTimeout(() => {
      fail(proxyError('timeout', '代理连接超时'));
    }, timeoutMs);
    timer.unref?.();

    const onData = (chunk) => {
      headerBuf = Buffer.concat([headerBuf, chunk]);
      if (headerBuf.length > 32 * 1024) {
        fail(proxyError('proxy_error', '代理响应头过大'));
        return;
      }
      const sep = headerBuf.indexOf('\r\n\r\n');
      if (sep === -1) return;
      const headText = headerBuf.subarray(0, sep).toString('latin1');
      const rest = headerBuf.subarray(sep + 4);
      const statusLine = headText.split('\r\n', 1)[0] ?? '';
      const match = statusLine.match(/^HTTP\/\d\.\d\s+(\d{3})/i);
      const statusCode = match ? Number(match[1]) : 0;

      if (statusCode === 200) {
        settled = true;
        cleanupListeners();
        resolve({ socket, buffered: rest });
        return;
      }
      if (statusCode === 407) {
        fail(
          Object.assign(proxyError('proxy_auth_required', '代理需要身份验证'), {
            statusCode: 407,
          }),
        );
        return;
      }
      fail(
        Object.assign(
          proxyError(
            'proxy_error',
            `代理拒绝连接 (HTTP ${statusCode || 'unknown'})`,
          ),
          { statusCode },
        ),
      );
    };

    signal?.addEventListener('abort', onAbort, { once: true });
    socket.on('error', onError);
    socket.on('close', onClose);
    socket.on('data', onData);

    const onConnected = () => {
      const lines = [
        `CONNECT ${hostHeader} HTTP/1.1`,
        `Host: ${hostHeader}`,
        'Proxy-Connection: Keep-Alive',
      ];
      if (authHeader) lines.push(`Proxy-Authorization: ${authHeader}`);
      lines.push('', '');
      socket.write(lines.join('\r\n'));
    };

    if (proxyUrl.protocol === 'https:') {
      socket.once('secureConnect', onConnected);
    } else {
      socket.once('connect', onConnected);
    }
  });
}

// ------------------------------------------------------------ loopback router

function stripHopByHop(headers) {
  const out = {};
  const connectionTokens = new Set(
    String(headers.connection ?? '')
      .split(',')
      .map((s) => s.trim().toLowerCase())
      .filter(Boolean),
  );
  for (const [k, v] of Object.entries(headers)) {
    const lower = k.toLowerCase();
    if (HOP_BY_HOP_HEADERS.has(lower) || connectionTokens.has(lower)) continue;
    out[k] = v;
  }
  return out;
}

function createLoopGuardLookup(
  targetPort,
  getSelfPort,
  baseLookup = dns.lookup,
) {
  return (hostname, options, callback) => {
    const cb = typeof options === 'function' ? options : callback;
    const opts = typeof options === 'function' ? {} : options;
    baseLookup(hostname, opts, (err, address, family) => {
      if (err) {
        cb(err);
        return;
      }
      const selfPort = getSelfPort();
      if (selfPort && Number(targetPort) === selfPort) {
        const list = Array.isArray(address)
          ? address.map((item) => item.address)
          : [address];
        if (list.some((addr) => isLoopbackHost(addr))) {
          cb(proxyError('proxy_loop', '目标地址解析到客户端本地分流端口'));
          return;
        }
      }
      cb(null, address, family);
    });
  };
}

/**
 * Start the local loopback routing proxy on 127.0.0.1:<random free port>.
 * All DSH & child processes point HTTP_PROXY/HTTPS_PROXY here so:
 * - System/OS proxy is completely bypassed.
 * - Per-domain routing works transparently for Python, pip, curl, git, Node, etc.
 * - Config updates apply live without restarting DSH.
 * - Upstream proxy credentials never enter child process environments.
 *
 * @param {{ getConfig: () => ReturnType<typeof validateProxyConfig>, connectTimeoutMs?: number, idleTimeoutMs?: number, lookup?: typeof dns.lookup }} options
 */
export async function startLocalProxyServer({
  getConfig,
  connectTimeoutMs = 15_000,
  idleTimeoutMs = 120_000,
  lookup = dns.lookup,
}) {
  let selfPort = 0;
  const activeSockets = new Set();
  const directHttpAgent = createDirectHttpAgent({ keepAlive: false });
  const directHttpsAgent = createDirectHttpsAgent({ keepAlive: false });

  const server = http.createServer((clientReq, clientRes) => {
    try {
      const rawUrl = clientReq.url ?? '';
      let target;
      try {
        target = new URL(
          /^https?:\/\//i.test(rawUrl)
            ? rawUrl
            : `http://${clientReq.headers.host ?? ''}${rawUrl}`,
        );
      } catch {
        clientRes.writeHead(400, { 'content-type': 'text/plain' });
        clientRes.end('Bad Request');
        return;
      }

      const targetHost = bareHost(target.hostname);
      const targetPort = target.port
        ? Number(target.port)
        : target.protocol === 'https:'
          ? 443
          : 80;

      let decision;
      try {
        decision = resolveProxy(targetHost, targetPort, getConfig(), {
          selfPort,
        });
      } catch (err) {
        clientRes.writeHead(502, { 'content-type': 'text/plain' });
        clientRes.end(
          err?.code === 'proxy_loop' ? 'Proxy Loop Detected' : 'Bad Gateway',
        );
        return;
      }

      const headers = stripHopByHop(clientReq.headers);
      let upstreamReq;

      if (decision.action === 'direct') {
        const mod = target.protocol === 'https:' ? https : http;
        upstreamReq = mod.request({
          hostname: targetHost,
          port: targetPort,
          method: clientReq.method,
          path: `${target.pathname}${target.search}`,
          headers: { ...headers, host: target.host },
          lookup: createLoopGuardLookup(targetPort, () => selfPort, lookup),
          agent:
            target.protocol === 'https:' ? directHttpsAgent : directHttpAgent,
          timeout: idleTimeoutMs,
        });
      } else {
        const proxyUrl = new URL(decision.proxy);
        const proxyHost = bareHost(proxyUrl.hostname);
        const proxyPort = proxyUrl.port
          ? Number(proxyUrl.port)
          : proxyUrl.protocol === 'https:'
            ? 443
            : 80;
        const authHeader = buildProxyAuthHeader(proxyUrl);
        const mod = proxyUrl.protocol === 'https:' ? https : http;
        upstreamReq = mod.request({
          hostname: proxyHost,
          port: proxyPort,
          method: clientReq.method,
          path: target.href,
          headers: {
            ...headers,
            host: target.host,
            ...(authHeader ? { 'proxy-authorization': authHeader } : {}),
          },
          agent:
            proxyUrl.protocol === 'https:' ? directHttpsAgent : directHttpAgent,
          timeout: idleTimeoutMs,
        });
      }

      let upstreamResRef = null;
      const cancelUpstream = () => {
        if (!upstreamReq.destroyed) upstreamReq.destroy();
        if (upstreamResRef && !upstreamResRef.destroyed) {
          upstreamResRef.destroy();
        }
      };

      upstreamReq.on('response', (upstreamRes) => {
        upstreamResRef = upstreamRes;
        upstreamRes.on('error', () => {
          if (!clientRes.destroyed) clientRes.destroy();
        });
        clientRes.writeHead(upstreamRes.statusCode ?? 502, upstreamRes.headers);
        upstreamRes.pipe(clientRes);
      });
      upstreamReq.on('timeout', () => {
        upstreamReq.destroy(proxyError('timeout', 'Gateway Timeout'));
      });
      upstreamReq.on('error', () => {
        if (!clientRes.headersSent && !clientRes.destroyed) {
          clientRes.writeHead(502, { 'content-type': 'text/plain' });
          clientRes.end('Bad Gateway');
        } else if (!clientRes.destroyed) {
          clientRes.destroy();
        }
      });
      clientReq.on('aborted', cancelUpstream);
      clientReq.on('error', cancelUpstream);
      clientRes.on('close', () => {
        if (!clientRes.writableEnded) cancelUpstream();
      });
      clientReq.pipe(upstreamReq);
    } catch {
      if (!clientRes.headersSent && !clientRes.destroyed) {
        clientRes.writeHead(502, { 'content-type': 'text/plain' });
        clientRes.end('Bad Gateway');
      } else if (!clientRes.destroyed) {
        clientRes.destroy();
      }
    }
  });

  server.on('connect', (req, clientSocket, head) => {
    activeSockets.add(clientSocket);
    clientSocket.once('close', () => activeSockets.delete(clientSocket));
    clientSocket.setTimeout(idleTimeoutMs, () => clientSocket.destroy());

    try {
      const authority = req.url ?? '';
      let targetHost = '';
      let targetPort = 443;
      const ipv6Match = authority.match(/^\[([^\]]+)\]:(\d+)$/);
      if (ipv6Match) {
        targetHost = ipv6Match[1];
        targetPort = Number(ipv6Match[2]);
      } else {
        const idx = authority.lastIndexOf(':');
        if (idx > 0) {
          targetHost = authority.slice(0, idx);
          targetPort = Number(authority.slice(idx + 1));
        }
      }
      targetHost = bareHost(targetHost);
      if (
        !targetHost ||
        !Number.isInteger(targetPort) ||
        targetPort < 1 ||
        targetPort > 65535
      ) {
        clientSocket.end('HTTP/1.1 400 Bad Request\r\n\r\n');
        return;
      }

      let decision;
      try {
        decision = resolveProxy(targetHost, targetPort, getConfig(), {
          selfPort,
        });
      } catch {
        clientSocket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n');
        return;
      }

      const wireTunnel = (upstreamSocket, buffered = Buffer.alloc(0)) => {
        activeSockets.add(upstreamSocket);
        upstreamSocket.once('close', () =>
          activeSockets.delete(upstreamSocket),
        );
        upstreamSocket.setTimeout(idleTimeoutMs, () =>
          upstreamSocket.destroy(),
        );

        if (clientSocket.destroyed) {
          upstreamSocket.destroy();
          return;
        }
        clientSocket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
        if (buffered.length > 0) clientSocket.write(buffered);
        if (head && head.length > 0) upstreamSocket.write(head);

        upstreamSocket.pipe(clientSocket);
        clientSocket.pipe(upstreamSocket);

        const teardown = () => {
          if (!clientSocket.destroyed) clientSocket.destroy();
          if (!upstreamSocket.destroyed) upstreamSocket.destroy();
        };
        clientSocket.on('error', teardown);
        upstreamSocket.on('error', teardown);
        clientSocket.once('close', teardown);
        upstreamSocket.once('close', teardown);
      };

      if (decision.action === 'direct') {
        const upstream = net.connect({
          host: targetHost,
          port: targetPort,
          lookup: createLoopGuardLookup(targetPort, () => selfPort, lookup),
        });
        const timer = setTimeout(() => {
          upstream.destroy(proxyError('timeout', 'connect timeout'));
        }, connectTimeoutMs);
        timer.unref?.();

        const onClientClose = () => {
          globalThis.clearTimeout(timer);
          upstream.destroy();
        };
        clientSocket.once('close', onClientClose);

        upstream.once('connect', () => {
          globalThis.clearTimeout(timer);
          clientSocket.removeListener('close', onClientClose);
          wireTunnel(upstream);
        });
        upstream.once('error', () => {
          globalThis.clearTimeout(timer);
          clientSocket.removeListener('close', onClientClose);
          if (!clientSocket.destroyed) {
            clientSocket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n');
          }
        });
        return;
      }

      const handshakeAbort = new globalThis.AbortController();
      const onClientCloseBeforeReady = () => handshakeAbort.abort();
      clientSocket.once('close', onClientCloseBeforeReady);

      connectViaHttpProxy(decision.proxy, targetHost, targetPort, {
        timeoutMs: connectTimeoutMs,
        signal: handshakeAbort.signal,
      })
        .then(({ socket, buffered }) => {
          clientSocket.removeListener('close', onClientCloseBeforeReady);
          wireTunnel(socket, buffered);
        })
        .catch((err) => {
          clientSocket.removeListener('close', onClientCloseBeforeReady);
          if (!clientSocket.destroyed) {
            const code = err?.statusCode === 407 ? 407 : 502;
            const text =
              code === 407 ? 'Proxy Authentication Required' : 'Bad Gateway';
            clientSocket.end(`HTTP/1.1 ${code} ${text}\r\n\r\n`);
          }
        });
    } catch {
      if (!clientSocket.destroyed) {
        clientSocket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n');
      }
    }
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.removeListener('error', reject);
      const addr = server.address();
      selfPort = typeof addr === 'object' && addr ? addr.port : 0;
      resolve(undefined);
    });
  });

  const url = `http://127.0.0.1:${selfPort}`;
  return {
    port: selfPort,
    url,
    env() {
      const noProxy = BUILTIN_LOOPBACK_NO_PROXY.join(',');
      return {
        HTTP_PROXY: url,
        HTTPS_PROXY: url,
        http_proxy: url,
        https_proxy: url,
        NO_PROXY: noProxy,
        no_proxy: noProxy,
        // Enable Node 24+ native fetch/http/https to route through our loopback router.
        NODE_USE_ENV_PROXY: '1',
      };
    },
    async close() {
      for (const s of activeSockets) s.destroy();
      activeSockets.clear();
      directHttpAgent.destroy();
      directHttpsAgent.destroy();
      await new Promise((resolve) => server.close(() => resolve(undefined)));
    },
  };
}

// ------------------------------------------------------------ controlled fetch & WS agent

/**
 * Create an HTTP/HTTPS Agent that tunnels through an upstream HTTP/HTTPS proxy.
 * Suitable for both `node:http`/`node:https` and `ws` (`new WebSocket(url, { agent })`).
 *
 * @param {string} proxyUrlStr
 * @param {{ isTls: boolean, tlsOptions?: tls.ConnectionOptions, timeoutMs?: number }} options
 */
export function createProxyTunnelAgent(
  proxyUrlStr,
  { isTls, tlsOptions = {}, timeoutMs = 15_000 },
) {
  const Base = isTls ? https.Agent : http.Agent;
  class ProxyTunnelAgent extends Base {
    createConnection(options, callback) {
      const targetHost = bareHost(options.host ?? options.hostname);
      const targetPort = Number(options.port ?? (isTls ? 443 : 80));
      connectViaHttpProxy(proxyUrlStr, targetHost, targetPort, { timeoutMs })
        .then(({ socket, buffered }) => {
          if (buffered.length > 0) socket.unshift(buffered);
          if (!isTls) {
            callback(null, socket);
            return;
          }
          const servername =
            options.servername ?? (isIP(targetHost) ? undefined : targetHost);
          const tlsSocket = tls.connect({
            ...tlsOptions,
            socket,
            ...(servername ? { servername } : {}),
            ...(options.ca !== undefined ? { ca: options.ca } : {}),
            ...(options.rejectUnauthorized !== undefined
              ? { rejectUnauthorized: options.rejectUnauthorized }
              : {}),
          });
          callback(null, tlsSocket);
        })
        .catch((err) => callback(err));
    }
  }
  return new ProxyTunnelAgent({ keepAlive: false, proxyEnv: {} });
}

/**
 * Create a WebSocket Agent for `ws` based on dynamic proxy configuration.
 * Always returns an explicit proxy-isolated Agent (direct uses `proxyEnv: {}`
 * so Node 24+ never falls back to inherited host proxy env vars).
 *
 * @param {string} urlStr
 * @param {() => ReturnType<typeof validateProxyConfig>} getConfig
 * @param {{ ca?: any, rejectUnauthorized?: boolean, timeoutMs?: number }} [tlsOpts]
 */
export function resolveWsProxyAgent(urlStr, getConfig, tlsOpts = {}) {
  const parsed = new URL(urlStr);
  const isTls = parsed.protocol === 'wss:' || parsed.protocol === 'https:';
  const port = parsed.port ? Number(parsed.port) : isTls ? 443 : 80;
  const decision = resolveProxy(bareHost(parsed.hostname), port, getConfig());
  if (decision.action === 'direct') {
    return isTls
      ? createDirectHttpsAgent({
          keepAlive: false,
          ...(tlsOpts.ca !== undefined ? { ca: tlsOpts.ca } : {}),
          ...(tlsOpts.rejectUnauthorized !== undefined
            ? { rejectUnauthorized: tlsOpts.rejectUnauthorized }
            : {}),
        })
      : createDirectHttpAgent({ keepAlive: false });
  }
  return createProxyTunnelAgent(decision.proxy, {
    isTls,
    tlsOptions: {
      ...(tlsOpts.ca !== undefined ? { ca: tlsOpts.ca } : {}),
      ...(tlsOpts.rejectUnauthorized !== undefined
        ? { rejectUnauthorized: tlsOpts.rejectUnauthorized }
        : {}),
    },
    timeoutMs: tlsOpts.timeoutMs,
  });
}

async function bodyToPayload(body) {
  if (body === undefined || body === null) {
    return { buffer: null, contentType: null };
  }
  if (
    (typeof globalThis.FormData !== 'undefined' &&
      body instanceof globalThis.FormData) ||
    (typeof globalThis.Blob !== 'undefined' && body instanceof globalThis.Blob)
  ) {
    const encoded = new globalThis.Response(body);
    const contentType = encoded.headers.get('content-type');
    const buffer = Buffer.from(await encoded.arrayBuffer());
    return { buffer, contentType };
  }
  if (typeof body === 'string') {
    return { buffer: Buffer.from(body, 'utf8'), contentType: null };
  }
  if (Buffer.isBuffer(body)) {
    return { buffer: body, contentType: null };
  }
  if (body instanceof Uint8Array) {
    return {
      buffer: Buffer.from(body.buffer, body.byteOffset, body.byteLength),
      contentType: null,
    };
  }
  if (body instanceof ArrayBuffer) {
    return { buffer: Buffer.from(body), contentType: null };
  }
  if (
    typeof globalThis.URLSearchParams !== 'undefined' &&
    body instanceof globalThis.URLSearchParams
  ) {
    return {
      buffer: Buffer.from(body.toString(), 'utf8'),
      contentType: 'application/x-www-form-urlencoded;charset=UTF-8',
    };
  }
  if (typeof body.getReader === 'function') {
    const reader = body.getReader();
    const chunks = [];
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(Buffer.from(value));
    }
    return { buffer: Buffer.concat(chunks), contentType: null };
  }
  if (typeof body[Symbol.asyncIterator] === 'function') {
    const chunks = [];
    for await (const chunk of body) chunks.push(Buffer.from(chunk));
    return { buffer: Buffer.concat(chunks), contentType: null };
  }
  return { buffer: Buffer.from(String(body), 'utf8'), contentType: null };
}

function normalizeHeaders(headersInit) {
  const out = {};
  if (!headersInit) return out;
  if (typeof headersInit.forEach === 'function') {
    headersInit.forEach((v, k) => {
      out[k] = v;
    });
    return out;
  }
  if (Array.isArray(headersInit)) {
    for (const [k, v] of headersInit) out[k] = v;
    return out;
  }
  for (const [k, v] of Object.entries(headersInit)) {
    if (v !== undefined) out[k] = String(v);
  }
  return out;
}

/**
 * Create a Fetch-compatible function that strictly obeys Auriga proxy rules
 * and NEVER uses OS/environment proxy settings.
 *
 * @param {() => ReturnType<typeof validateProxyConfig>} getConfig
 * @returns {typeof globalThis.fetch}
 */
export function createControlledFetch(getConfig) {
  const directHttpAgent = createDirectHttpAgent({ keepAlive: true });
  const directHttpsAgent = createDirectHttpsAgent({ keepAlive: true });

  return async function controlledFetch(input, init = {}) {
    const urlStr =
      typeof input === 'string'
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
    const url = new URL(urlStr);
    const targetHost = bareHost(url.hostname);
    const isHttps = url.protocol === 'https:';
    const port = url.port ? Number(url.port) : isHttps ? 443 : 80;
    const method = (
      init.method ??
      (typeof input === 'object' && 'method' in input ? input.method : 'GET')
    ).toUpperCase();
    const headers = normalizeHeaders(init.headers);
    const signal = init.signal;
    const { buffer: bodyBuf, contentType } = await bodyToPayload(init.body);
    if (
      contentType &&
      !Object.keys(headers).some((k) => k.toLowerCase() === 'content-type')
    ) {
      headers['content-type'] = contentType;
    }
    if (
      bodyBuf &&
      !Object.keys(headers).some((k) => k.toLowerCase() === 'content-length')
    ) {
      headers['content-length'] = String(bodyBuf.length);
    }

    const decision = resolveProxy(targetHost, port, getConfig());

    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(
          signal.reason ??
            Object.assign(new Error('The operation was aborted'), {
              name: 'AbortError',
            }),
        );
        return;
      }

      let req;
      try {
        if (decision.action === 'direct') {
          const mod = isHttps ? https : http;
          req = mod.request({
            hostname: targetHost,
            port,
            method,
            path: `${url.pathname}${url.search}`,
            headers,
            agent: isHttps ? directHttpsAgent : directHttpAgent,
          });
        } else if (isHttps) {
          const agent = createProxyTunnelAgent(decision.proxy, { isTls: true });
          req = https.request({
            hostname: targetHost,
            port,
            method,
            path: `${url.pathname}${url.search}`,
            headers,
            agent,
          });
        } else {
          const proxyUrl = new URL(decision.proxy);
          const proxyHost = bareHost(proxyUrl.hostname);
          const proxyPort = proxyUrl.port
            ? Number(proxyUrl.port)
            : proxyUrl.protocol === 'https:'
              ? 443
              : 80;
          const authHeader = buildProxyAuthHeader(proxyUrl);
          const mod = proxyUrl.protocol === 'https:' ? https : http;
          req = mod.request({
            hostname: proxyHost,
            port: proxyPort,
            method,
            path: url.href,
            headers: {
              ...headers,
              host: url.host,
              ...(authHeader ? { 'proxy-authorization': authHeader } : {}),
            },
            agent:
              proxyUrl.protocol === 'https:'
                ? directHttpsAgent
                : directHttpAgent,
          });
        }
      } catch (err) {
        reject(err);
        return;
      }

      let settled = false;
      let activeRes = null;

      const onAbort = () => {
        const err =
          signal.reason ??
          Object.assign(new Error('The operation was aborted'), {
            name: 'AbortError',
          });
        if (activeRes && !activeRes.destroyed) activeRes.destroy(err);
        if (!req.destroyed) req.destroy(err);
        if (!settled) {
          settled = true;
          reject(err);
        }
      };
      signal?.addEventListener('abort', onAbort, { once: true });

      req.once('error', (err) => {
        if (!activeRes) signal?.removeEventListener('abort', onAbort);
        if (!settled) {
          settled = true;
          reject(err);
        }
      });

      req.once('response', (res) => {
        activeRes = res;
        const status = res.statusCode ?? 502;
        const cleanupAbort = () =>
          signal?.removeEventListener('abort', onAbort);

        // Honor redirect: 'error' (used by shell-auth & updater).
        if (
          init.redirect === 'error' &&
          status >= 300 &&
          status < 400 &&
          res.headers.location
        ) {
          cleanupAbort();
          res.resume();
          if (!settled) {
            settled = true;
            reject(new TypeError('Failed to fetch: unexpected redirect'));
          }
          return;
        }

        const respHeaders = new globalThis.Headers();
        for (const [k, v] of Object.entries(res.headers)) {
          if (v === undefined) continue;
          if (Array.isArray(v)) {
            for (const item of v) respHeaders.append(k, item);
          } else {
            respHeaders.set(k, v);
          }
        }

        const nullBody =
          status === 204 ||
          status === 205 ||
          status === 304 ||
          method === 'HEAD';
        if (nullBody) {
          cleanupAbort();
          res.resume();
        } else {
          // Keep abort signal active throughout body consumption!
          res.once('end', cleanupAbort);
          res.once('close', cleanupAbort);
          res.once('error', cleanupAbort);
        }

        const stream = nullBody ? null : Readable.toWeb(res);
        const response = new globalThis.Response(stream, {
          status,
          statusText: res.statusMessage ?? '',
          headers: respHeaders,
        });
        if (!settled) {
          settled = true;
          resolve(response);
        }
      });

      if (bodyBuf) req.end(bodyBuf);
      else req.end();
    });
  };
}

/**
 * Probe connectivity to a target URL through the active proxy configuration.
 * Returns matched rule, redacted proxy, reachability, latency or fixed error reason.
 *
 * @param {string} targetInput
 * @param {() => ReturnType<typeof validateProxyConfig>} getConfig
 * @param {{ timeoutMs?: number }} [options]
 */
export async function testProxyTarget(
  targetInput,
  getConfig,
  { timeoutMs = 8_000 } = {},
) {
  if (
    typeof targetInput !== 'string' ||
    !targetInput.trim() ||
    targetInput.length > 2048
  ) {
    throw proxyError('invalidTarget', '请输入有效的测试域名或 URL');
  }
  const trimmed = targetInput.trim();
  let url;
  try {
    url = new URL(
      /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`,
    );
  } catch {
    throw proxyError('invalidTarget', '测试目标格式无效');
  }
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname) {
    throw proxyError('invalidTarget', '仅支持测试 HTTP/HTTPS 目标');
  }
  const port = url.port
    ? Number(url.port)
    : url.protocol === 'https:'
      ? 443
      : 80;
  const config = getConfig();
  const decision = resolveProxy(bareHost(url.hostname), port, config);
  const redactedProxy = decision.proxy
    ? maskProxyUrl(decision.proxy).display
    : '';

  const startedAt = Date.now();
  const controller = new globalThis.AbortController();
  const timer = setTimeout(
    () => controller.abort(proxyError('timeout', '连接超时')),
    timeoutMs,
  );
  timer.unref?.();

  try {
    const fetcher = createControlledFetch(() => config);
    const response = await fetcher(url.href, {
      method: 'HEAD',
      redirect: 'manual',
      signal: controller.signal,
    });
    await response.body?.cancel().catch(() => {});
    if (decision.action === 'proxy' && response.status === 407) {
      return {
        url: url.href,
        action: decision.action,
        proxy: redactedProxy,
        matchedBy: decision.matchedBy,
        reachable: false,
        status: 407,
        reason: 'proxy_auth_required',
        latencyMs: Date.now() - startedAt,
      };
    }
    return {
      url: url.href,
      action: decision.action,
      proxy: redactedProxy,
      matchedBy: decision.matchedBy,
      reachable: true,
      status: response.status,
      latencyMs: Date.now() - startedAt,
    };
  } catch (err) {
    const code = err?.code ?? '';
    let reason = 'network';
    if (
      code === 'timeout' ||
      err?.name === 'TimeoutError' ||
      err?.name === 'AbortError'
    ) {
      reason = 'timeout';
    } else if (code === 'proxy_auth_required') {
      reason = 'proxy_auth_required';
    } else if (code === 'proxy_unreachable' || code === 'proxy_error') {
      reason = 'proxy_unreachable';
    } else if (code === 'proxy_loop') {
      reason = 'proxy_loop';
    } else if (
      /tls|cert|ssl/i.test(String(code) + ' ' + String(err?.message ?? ''))
    ) {
      reason = 'tls_error';
    }
    return {
      url: url.href,
      action: decision.action,
      proxy: redactedProxy,
      matchedBy: decision.matchedBy,
      reachable: false,
      reason,
      latencyMs: Date.now() - startedAt,
    };
  } finally {
    globalThis.clearTimeout(timer);
  }
}

/**
 * Shell-owned proxy runtime factory loaded by DSH plugin (analogous to update.mjs).
 * @param {{ dataHome: string, defaultsFile?: string | null }} options
 */
export function createProxyRuntime({ dataHome, defaultsFile = null }) {
  const store = createProxyConfigStore({ dataHome, defaultsFile });
  return {
    snapshot: () => store.snapshot(),
    save: (input) => store.save(input),
    reset: () => store.reset(),
    test: (targetUrl) => testProxyTarget(targetUrl, () => store.get().config),
    getConfig: () => store.get().config,
    createFetch: () => createControlledFetch(() => store.get().config),
  };
}
