// @ts-nocheck
import { posix } from 'node:path';
import { sendProblem } from './problem.js';
import { normalizeHttpPath, rawRequestPath } from './raw-path.js';

/** Native DSH pluginManager mutations blocked in managed mode (DSH 0.2.0-rc.2). */
export const NATIVE_PLUGIN_MUTATIONS = Object.freeze([
  'installBundle',
  'removeBundle',
  'setBundleEnabled',
  'setPluginEnabled',
  'setVersionExemption',
  'cancelInstall',
]);

export const NATIVE_PLUGIN_HTTP_PREFIX = '/api/pluginmanager/';

export const NATIVE_PLUGIN_BLOCKLIST = Object.freeze(
  NATIVE_PLUGIN_MUTATIONS.flatMap((name) => [
    { kind: 'rpc', method: `pluginManager/${name}` },
    { kind: 'http', method: 'POST', path: `/api/pluginManager/${name}` },
  ]),
);

/** Default user-visible workspace root inside the instance. */
export const DEFAULT_WORKSPACE_ROOT = '/home/dsh/workspace';

const WRITE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

/**
 * True when the HTTP request targets a native pluginManager mutation.
 * Path is normalized (decode, collapse `.`/`..`, strip trailing slash, case-insensitive).
 * Any write method is blocked (not only POST).
 * @param {string|undefined} method
 * @param {string|undefined} urlPath
 */
export function isNativePluginManageRequest(method, urlPath) {
  const m = String(method || 'GET').toUpperCase();
  if (!WRITE_METHODS.has(m)) return false;
  const path = normalizeHttpPath(rawRequestPath(urlPath));
  if (!path.startsWith(NATIVE_PLUGIN_HTTP_PREFIX)) return false;
  const rest = path.slice(NATIVE_PLUGIN_HTTP_PREFIX.length);
  const name = rest.split('/')[0];
  return NATIVE_PLUGIN_MUTATIONS.some((n) => n.toLowerCase() === name);
}

/** @param {string|undefined} rpcMethod */
export function isNativePluginManageRpc(rpcMethod) {
  const name = String(rpcMethod || '');
  return NATIVE_PLUGIN_BLOCKLIST.some(
    (rule) => rule.kind === 'rpc' && rule.method === name,
  );
}

function asPosix(p) {
  return String(p || '').replace(/\\/g, '/');
}

/**
 * String-level confinement only — does not resolve symlinks (document in docs).
 * @param {string} candidate
 * @param {string} [root]
 */
export function isUnderWorkspaceRoot(candidate, root = DEFAULT_WORKSPACE_ROOT) {
  if (typeof candidate !== 'string' || !candidate) return false;
  const base = posix.normalize(asPosix(root));
  let target = posix.normalize(asPosix(candidate));
  if (target.length > 1 && target.endsWith('/')) target = target.slice(0, -1);
  if (target === base) return true;
  return target.startsWith(base.endsWith('/') ? base : base + '/');
}

/**
 * @param {string} path
 * @param {string} [root]
 */
export function assertWorkspaceCreatePath(path, root = DEFAULT_WORKSPACE_ROOT) {
  if (typeof path !== 'string' || !path.trim()) {
    return { ok: false, code: 'common.validation_failed' };
  }
  if (!isUnderWorkspaceRoot(path, root)) {
    return { ok: false, code: 'common.forbidden' };
  }
  return { ok: true, path: posix.normalize(asPosix(path)) };
}

/**
 * @param {object} pluginManager
 */
export function guardPluginManager(pluginManager) {
  if (!pluginManager || pluginManager.__aurigaGuarded) return pluginManager;
  for (const name of NATIVE_PLUGIN_MUTATIONS) {
    if (typeof pluginManager[name] !== 'function') continue;
    pluginManager[name] = async () => {
      const error = new Error('plugin managed by Auriga');
      error.code = 'common.forbidden';
      throw error;
    };
  }
  Object.defineProperty(pluginManager, '__aurigaGuarded', { value: true });
  return pluginManager;
}

/**
 * @param {object} registry
 * @param {string} [root]
 */
export function guardWorkspaceRegistry(
  registry,
  root = DEFAULT_WORKSPACE_ROOT,
) {
  if (
    !registry ||
    typeof registry.create !== 'function' ||
    registry.__aurigaRootGuard
  )
    return registry;
  const orig = registry.create.bind(registry);
  registry.create = async (path, title) => {
    const checked = assertWorkspaceCreatePath(path, root);
    if (!checked.ok) {
      const err = new Error(checked.code);
      err.code = checked.code;
      throw err;
    }
    return orig(checked.path, title);
  };
  registry.__aurigaRootGuard = true;
  return registry;
}

/**
 * @param {import('node:http').Server} server
 */
export function installNativePluginHttpGuard(server) {
  if (!server || typeof server.emit !== 'function') return () => {};
  const original = server.emit.bind(server);
  server.emit = function guardedEmit(event, ...args) {
    if (event === 'request') {
      const req = args[0];
      const res = args[1];
      if (isNativePluginManageRequest(req?.method, req?.url)) {
        sendProblem(res, 403, 'common.forbidden');
        return false;
      }
    }
    return original(event, ...args);
  };
  return () => {
    server.emit = original;
  };
}

/**
 * @param {object} ctx
 * @param {{ workspaceRoot?: string }} [opts]
 */
export function installManagedGuards(ctx, opts = {}) {
  const root = opts.workspaceRoot ?? DEFAULT_WORKSPACE_ROOT;
  try {
    ctx.inject?.(['pluginManager'], (pctx) => {
      guardPluginManager(pctx.pluginManager);
    });
  } catch {
    /* unit tests / missing host */
  }
  try {
    ctx.inject?.(['workspaceRegistry'], (wctx) => {
      guardWorkspaceRegistry(wctx.workspaceRegistry, root);
    });
  } catch {
    /* optional */
  }
  return () => {};
}
