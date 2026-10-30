// @ts-nocheck
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createManagedNetwork } from '../network/managed-device.js';
import { createConsentRoutes } from '../local/consent-routes.js';
import { createMessagingRoutes } from '../local/messaging-routes.js';
import {
  installHostAuthentication,
  validateBrowserOrigin,
  browserFenceOk,
} from '../managed/host-auth.js';
import { createIdentityKeyCache } from '../managed/keys.js';
import { createDoorLock, createReplayCache } from '../managed/door.js';
import {
  DEFAULT_WORKSPACE_ROOT,
  installManagedGuards,
  installNativePluginHttpGuard,
  guardPluginManager,
  guardWorkspaceRegistry,
} from '../managed/guards.js';
import {
  createActivityProbe,
  installActivityBridge,
  refreshActivityFromHost,
} from '../managed/activity.js';
import { registerHealthRoute, buildHealthBody } from '../managed/health.js';
import { createHeartbeatClient } from '../managed/heartbeat.js';
import {
  createAurigaRouteHandler,
  fetchVerifiedManifest,
  ROUTE_PREFIX,
} from '../managed/panel.js';
import { provideAurigaSdk, createAurigaSdk } from '../managed/sdk.js';
import {
  PROTOCOL_VERSION,
  protocolInfo,
  userAgent,
} from '../managed/protocol.js';

function readPkgVersion() {
  try {
    const pkg = JSON.parse(
      readFileSync(new URL('../../package.json', import.meta.url), 'utf8'),
    );
    return pkg.version || '0.0.0';
  } catch {
    return '0.0.0';
  }
}

/**
 * @param {NodeJS.ProcessEnv} env
 * @param {string} [inline]
 */
async function resolveInstanceToken(env, inline) {
  if (inline) return inline;
  const file = env.AURIGA_INSTANCE_TOKEN_FILE;
  if (file) return readFileSync(file, 'utf8').trim();
  if (env.AURIGA_INSTANCE_TOKEN) return env.AURIGA_INSTANCE_TOKEN;
  throw new Error('missing instance token (AURIGA_INSTANCE_TOKEN_FILE)');
}

/**
 * @param {{
 *   env?: NodeJS.ProcessEnv,
 *   now?: () => number,
 *   fetchImpl?: typeof fetch,
 *   identityKeys?: { keys: object[] },
 *   panelKeys?: { keys: object[] },
 *   instanceToken?: string,
 *   pluginVersion?: string,
 *   dshVersion?: string,
 *   workspaceRoot?: string,
 *   watchKeys?: boolean,
 *   startHeartbeat?: boolean,
 *   config?: import('../generated/managed-config.js').ManagedConfig,
 * }} [options]
 */
export async function createManaged(options = {}) {
  const env = options.env ?? process.env;
  // Loader row config rendered by the node agent (cordis.patch.yml).
  const rowConfig = options.config ?? {};
  if (rowConfig.imBots !== undefined && rowConfig.imBots !== 'none') {
    throw new Error("auriga-dsh: config.imBots must be 'none' when set");
  }
  const imBots = rowConfig.imBots;
  if (
    rowConfig.terminalSources !== undefined &&
    rowConfig.terminalSources !== 'browser-only'
  ) {
    throw new Error(
      "auriga-dsh: config.terminalSources must be 'browser-only' when set",
    );
  }
  const terminalSources = rowConfig.terminalSources;
  if (
    !rowConfig ||
    typeof rowConfig !== 'object' ||
    Array.isArray(rowConfig) ||
    Object.keys(rowConfig).some(
      (key) => !['imBots', 'terminalSources', 'browserOrigin'].includes(key),
    )
  )
    throw new Error('auriga-dsh: unknown managed config field');
  const browserOrigin = validateBrowserOrigin(rowConfig.browserOrigin);
  if ((env.AURIGA_MODE ?? 'managed') !== 'managed') {
    throw new Error('createManaged requires AURIGA_MODE=managed');
  }

  const instanceId = env.AURIGA_INSTANCE_ID;
  const ownerId = env.AURIGA_OWNER_ID;
  const tenantId = env.AURIGA_TENANT_ID;
  const platformUrl = env.AURIGA_PLATFORM_URL;
  if (!instanceId || !ownerId) {
    throw new Error('AURIGA_INSTANCE_ID and AURIGA_OWNER_ID are required');
  }
  if (!platformUrl) throw new Error('AURIGA_PLATFORM_URL is required');

  if (!options.identityKeys && !env.AURIGA_IDENTITY_KEYS_FILE) {
    throw new Error(
      'auriga-dsh: AURIGA_IDENTITY_KEYS_FILE is required in managed mode',
    );
  }

  const pluginVersion = options.pluginVersion ?? readPkgVersion();
  const dshVersion =
    options.dshVersion ??
    env.DSH_CLIENT_VERSION ??
    env.DSH_VERSION ??
    'unknown';
  const workspaceRoot =
    options.workspaceRoot ??
    env.AURIGA_WORKSPACE_ROOT ??
    DEFAULT_WORKSPACE_ROOT;

  const now = options.now ?? (() => Math.floor(Date.now() / 1000));
  const keyCache = createIdentityKeyCache({
    file: options.identityKeys ? undefined : env.AURIGA_IDENTITY_KEYS_FILE,
    keySet: options.identityKeys,
    watch: options.watchKeys !== false,
    now,
  });

  /** @type {ReturnType<typeof createIdentityKeyCache> | undefined} */
  let panelKeyCache;
  if (options.panelKeys) {
    panelKeyCache = createIdentityKeyCache({
      keySet: options.panelKeys,
      watch: false,
      now,
    });
  } else if (env.AURIGA_PANEL_KEYS_FILE) {
    panelKeyCache = createIdentityKeyCache({
      file: env.AURIGA_PANEL_KEYS_FILE,
      watch: options.watchKeys !== false,
      now,
    });
  } else if (env.AURIGA_PANEL_KEYS) {
    panelKeyCache = createIdentityKeyCache({
      keySet: JSON.parse(env.AURIGA_PANEL_KEYS),
      watch: false,
      now,
    });
  }

  const replay = createReplayCache({ now });
  const activity = createActivityProbe({
    now: () => new Date(now() * 1000),
  });
  activity.markAllUnknown();

  const door = createDoorLock({
    instanceId,
    ownerId,
    getPublicKey: (kid) => keyCache.getPublicKey(kid),
    replay,
    now,
    // Any identity-gated HTTP request or WS upgrade (not /_auriga/health)
    // moves lastInteractionAt; frames inside an open WS do not.
    onAuthenticated: () => activity.touchInteraction(),
  });

  /** @type {{ ctx: object | null, getBrowsers?: () => number }} */
  const host = { ctx: null };
  /** Browser WebSocket upgrades (oclaw hub-member pattern). */
  const browserSockets = new Set();

  let lastManifestVersion = '';
  const getManifest = async () => {
    if (!panelKeyCache) {
      throw new Error(
        'auriga-dsh: panel keys required (AURIGA_PANEL_KEYS_FILE)',
      );
    }
    const manifest = await fetchVerifiedManifest({
      platformUrl,
      keys: () => panelKeyCache.snapshot(),
      pluginVersion,
      fetchImpl: options.fetchImpl,
      now: now(),
    });
    if (
      lastManifestVersion &&
      compareSemver(manifest.version, lastManifestVersion) < 0
    ) {
      const { PanelError } = await import('../managed/panel.js');
      throw new PanelError(
        'common.precondition_failed',
        'panel version rollback rejected',
      );
    }
    lastManifestVersion = manifest.version;
    return manifest;
  };

  const heartbeat = createHeartbeatClient({
    platformUrl,
    getInstanceToken: () => resolveInstanceToken(env, options.instanceToken),
    pluginVersion,
    dshVersion,
    activity,
    fetchImpl: options.fetchImpl,
    healthOk: () => host.ctx?.webServer?.server?.listening === true,
    refreshActivity: async () => {
      if (!host.ctx) return;
      await refreshActivityFromHost(host.ctx, activity, {
        browsers: host.getBrowsers?.(),
        imBots,
        terminalSources,
      });
    },
    onUnknownActivity: (fields) =>
      host.ctx?.logger?.warn?.(
        fields.length
          ? `[auriga-dsh] activity unknown (${fields.join(', ')})`
          : '[auriga-dsh] activity observations recovered',
      ),
  });

  if (options.startHeartbeat) heartbeat.start();

  const sdk = createAurigaSdk({
    getIdentity: () => ({
      uid: ownerId,
      displayName: ownerId,
      role: 'owner',
    }),
  });

  /** @type {(() => void) | undefined} */
  let disposeActivity;
  let disposeHostAuth;
  let disposeDoor;
  let disposeHttpGuard;
  let network;

  return {
    mode: /** @type {const} */ ('managed'),
    protocol: PROTOCOL_VERSION,
    protocolInfo: protocolInfo(),
    instanceId,
    ownerId,
    tenantId,
    pluginVersion,
    userAgent: userAgent(pluginVersion, dshVersion),
    door,
    activity,
    heartbeat,
    sdk,
    keyCache,
    panelKeyCache,
    workspaceRoot,
    get networkStatus() {
      return (
        network?.status ?? {
          available: false,
          connected: false,
          state: 'unconfigured',
        }
      );
    },
    healthBody: () => buildHealthBody({ pluginVersion, ready: true }),
    /**
     * @param {object} ctx
     */
    apply(ctx) {
      const server = ctx.webServer?.server;
      if (!server) {
        throw new Error(
          'auriga-dsh: managed mode requires ctx.webServer.server to install the identity door; refusing to start without a door lock',
        );
      }

      host.ctx = ctx;
      disposeHostAuth = installHostAuthentication(ctx.connection, door, {
        browserOrigin,
      });
      host.getBrowsers = () => browserSockets.size;
      installManagedGuards(ctx, { workspaceRoot });
      provideAurigaSdk(ctx, {
        getIdentity: () => ({
          uid: ownerId,
          displayName: ownerId,
          role: 'owner',
        }),
      });
      registerHealthRoute(ctx, { pluginVersion });

      // Same public host services, provider and network lifecycle as connect mode.
      // Profile storage is only durable partitioning; it is not an isolation claim.
      if (env.AURIGA_DEVICE_CREDENTIAL_FILE) {
        ctx.inject(['profileContext'], (profileCtx) => {
          const directory = profileCtx.profileContext?.dir;
          if (!directory)
            throw new Error(
              'managed network requires a host profile directory',
            );
          profileCtx.inject(
            ['tools', 'sessions', 'sessionController'],
            (networkCtx) => {
              const owned = createManagedNetwork({
                file: env.AURIGA_DEVICE_CREDENTIAL_FILE,
                directory: join(directory, 'auriga-managed'),
                tenantId,
                ownerId,
                instanceId,
                ctx: networkCtx,
                authorizeLocal: async (req) =>
                  door.isAuthenticated(req) &&
                  browserFenceOk(req, browserOrigin),
              });
              network = owned;
              owned.start();
              networkCtx.effect(
                () => () => {
                  if (network === owned) network = undefined;
                  return owned.close();
                },
                'auriga-dsh: managed network lifetime',
              );
              networkCtx.inject(['aurigaNetwork'], (providerCtx) => {
                const detach = owned.attach(providerCtx.aurigaNetwork);
                providerCtx.effect(
                  () => detach,
                  'auriga-dsh: managed network provider',
                );
              });
            },
          );
        });
        const authorized = (req) =>
          door.isAuthenticated(req) && browserFenceOk(req, browserOrigin);
        const facade = {
          resolve: authorized,
          checkCsrf: (req) =>
            authorized(req) && typeof req.headers.origin === 'string',
          consentUI: (req) => network.consentUI(req),
          messagingUI: (req) => network.messagingUI(req),
        };
        const consent = createConsentRoutes(facade),
          messaging = createMessagingRoutes(facade);
        ctx.effect(
          () =>
            ctx.webServer.register({
              kind: 'prefix',
              path: '/auriga/local-login',
              handler(req, res) {
                if (!authorized(req)) {
                  res.writeHead(401, { 'cache-control': 'no-store' });
                  res.end();
                  return;
                }
                const path = (req.url ?? '').split('?')[0];
                if (path.startsWith('/auriga/local-login/consent/'))
                  return consent(req, res);
                if (path.startsWith('/auriga/local-login/messaging/'))
                  return messaging(req, res);
                if (
                  req.method === 'GET' &&
                  path === '/auriga/local-login/status'
                ) {
                  res.writeHead(200, {
                    'content-type': 'application/json',
                    'cache-control': 'no-store',
                  });
                  res.end(
                    JSON.stringify({
                      authenticated: true,
                      network: network?.status ?? {
                        connected: false,
                        state: 'unavailable',
                      },
                    }),
                  );
                  return;
                }
                if (
                  req.method === 'POST' &&
                  path === '/auriga/local-login/logout' &&
                  facade.checkCsrf(req)
                ) {
                  network?.logout();
                  res.writeHead(200, {
                    'content-type': 'application/json',
                    'cache-control': 'no-store',
                  });
                  res.end(JSON.stringify({ ok: true }));
                  return;
                }
                res.writeHead(404, { 'cache-control': 'no-store' });
                res.end();
              },
            }),
          'auriga-dsh: managed network controls',
        );
      }

      // Count browser transports via upgrade (same idea as oclaw hub-member).
      // We do not filter by identity here: the door already gates HTTP; raw
      // TCP keepalives do not emit `upgrade`.
      const onUpgrade = (_req, socket) => {
        browserSockets.add(socket);
        socket.once('close', () => browserSockets.delete(socket));
      };
      server.on('upgrade', onUpgrade);

      disposeActivity = installActivityBridge(ctx, activity, {
        getBrowsers: () => browserSockets.size,
        imBots,
        terminalSources,
      });

      const handler = createAurigaRouteHandler({
        mode: 'managed',
        auth: ctx.connection ?? {
          requestRejection() {
            return undefined;
          },
        },
        getManifest,
        pluginVersion,
        panelBase: env.AURIGA_PANEL_BASE,
      });
      ctx.effect?.(
        () =>
          ctx.webServer?.register?.({
            kind: 'prefix',
            path: ROUTE_PREFIX,
            handler,
          }),
        'auriga-dsh: /auriga routes',
      );

      // Door is outermost: even forbidden native writes must authenticate first.
      disposeHttpGuard = installNativePluginHttpGuard(server);
      disposeDoor = door.install(server);

      if (options.startHeartbeat !== false) heartbeat.start();

      return () => {
        void network?.close();
        heartbeat.stop();
        disposeActivity?.();
        disposeDoor?.();
        disposeHttpGuard?.();
        disposeHostAuth?.();
        server.off('upgrade', onUpgrade);
        browserSockets.clear();
        host.ctx = null;
        host.getBrowsers = undefined;
        keyCache.close();
        panelKeyCache?.close();
      };
    },
    guardPluginManager,
    guardWorkspaceRegistry: (registry) =>
      guardWorkspaceRegistry(registry, workspaceRoot),
    getManifest,
    close() {
      void network?.close();
      heartbeat.stop();
      disposeActivity?.();
      disposeHostAuth?.();
      host.ctx = null;
      keyCache.close();
      panelKeyCache?.close();
    },
  };
}

/** @param {string} a @param {string} b */
function compareSemver(a, b) {
  const pa = String(a)
    .split('-')[0]
    .split('.')
    .map((n) => Number.parseInt(n, 10) || 0);
  const pb = String(b)
    .split('-')[0]
    .split('.')
    .map((n) => Number.parseInt(n, 10) || 0);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

export function managed() {
  return {
    mode: 'managed',
    /**
     * @param {Parameters<typeof createManaged>[0]} [options]
     */
    create: (options) => createManaged(options),
  };
}
