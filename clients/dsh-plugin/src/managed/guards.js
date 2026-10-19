// @ts-nocheck
import { posix } from 'node:path';
import { sendProblem } from './problem.js';

/** Native DSH pluginManager mutations blocked in managed mode (DSH 0.2.0-rc.2). */
export const NATIVE_PLUGIN_MUTATIONS = Object.freeze([
  'installBundle',
  'removeBundle',
  'setBundleEnabled',
  'setPluginEnabled',
  'setVersionExemption',
  'cancelInstall',
]);

export const NATIVE_PLUGIN_BLOCKLIST = Object.freeze(
  NATIVE_PLUGIN_MUTATIONS.flatMap((name) => [
    { kind: 'rpc', method: `pluginManager/${name}` },
    { kind: 'http', method: 'POST', path: `/api/pluginManager/${name}` },
  ]),
);

/** Default user-visible workspace root inside the instance. */
export const DEFAULT_WORKSPACE_ROOT = '/home/dsh/workspace';

/** @param {string|undefined} method @param {string|undefined} urlPath */
export function isNativePluginManageRequest(method, urlPath) {
  const path = String(urlPath || '').split('?')[0];
  const m = String(method || 'GET').toUpperCase();
  return NATIVE_PLUGIN_BLOCKLIST.some((rule) => {
    if (rule.kind !== 'http') return false;
    return rule.method === m && path === rule.path;
  });
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
 * @returns {{ ok: true, path: string } | { ok: false, code: string }}
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
 * HTTP layer for native plugin manage endpoints.
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
 * Install Cordis-style guards when services appear.
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
