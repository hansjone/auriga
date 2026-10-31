// @ts-nocheck
/**
 * Cordis / DSH host entry: managed runtime or authenticated local UI.
 * Local pages are unconfigured; identity library sessions are not mounted here.
 */
import { createManaged } from './modes/managed.js';
import { selectMode } from './index.js';
import { createLocalSurfaceHandler } from './local-ui.js';
import { createConnectRuntime } from './local/runtime.js';
import { savedPlatformConfig } from './local/platform.js';
import {
  installLocalAdmission,
  installHttpResponseTracking,
  installLocalLoginBootstrap,
} from './local/host.js';
import {
  createConnectRoutes,
  createUnconfiguredLoginRoutes,
} from './local/routes.js';
import { join } from 'node:path';
import { installSettings } from './settings/host.js';
import { MANAGED_MODEL_STATUS } from './ai/installation.js';
import { installNetworkDiagnosticSink } from './network/diagnostics.js';
import { installObservability, logEvent } from './observability/index.js';

export const name = 'auriga-dsh';
export const inject = ['webServer', 'connection'];

/**
 * @param {object} ctx
 * @param {import('./generated/managed-config.js').ManagedConfig} [config] Loader row config
 */
export async function apply(ctx, config) {
  installNetworkDiagnosticSink(ctx);
  installObservability(ctx);
  const mode = selectMode();
  let binding, profileOwner;
  // Unknown preferences must never temporarily expose native DSH surfaces.
  let configured = true;
  let admission;
  const setConfigured = (next) => {
    configured = next;
    // Close the door before destroying old transports: even synchronous close
    // listeners must reauthenticate before opening a new native Peer.
    if (configured) admission?.closeStandaloneSockets();
  };
  const settings = installSettings(
    ctx,
    config,
    mode === 'managed'
      ? {}
      : {
          activeOrigin: () => binding?.runtime.config.platformOrigin ?? '',
          nativeAuth: {
            requestRejection: (req) =>
              admission
                ? admission.nativeAuth.requestRejection(req)
                : ctx.connection.requestRejection(req),
          },
        },
  );
  if (mode !== 'managed') {
    installLocalLoginBootstrap(ctx);
    if (!ctx.webServer?.register || !ctx.connection?.requestRejection)
      throw new Error(
        'auriga-dsh: local UI requires native host authentication',
      );
    {
      // The parent owns the closed door while native providers are pending or
      // being unloaded. Only the injected child owns the runtime and tools.
      const current = () => {
        if (!binding) return undefined;
        try {
          if (
            !binding.ctx.tools?.register ||
            !binding.ctx.sessions?.get ||
            !binding.ctx.sessionController?.prompt
          )
            return undefined;
          return binding;
        } catch {
          return undefined;
        }
      };
      const gate = {
        isConfigured: () => configured,
        resolve: (req) => current()?.runtime.resolve(req) ?? null,
        bindSocket: (req) => current()?.runtime.bindSocket(req) ?? false,
        status: (req) =>
          current()?.runtime.status(req) ?? {
            connected: false,
            connection: null,
          },
        installHttpTracking: () =>
          installHttpResponseTracking(
            ctx,
            (req, res) =>
              !configured ||
              (current()?.runtime.bindHttpResponse(req, res) ?? false),
          ),
      };
      admission = installLocalAdmission(ctx.connection, gate, {
        protocol: ctx.webServer.protocol,
      });
      ctx.effect(
        () => () => admission?.dispose(),
        'auriga-dsh: persistent local door',
      );
      ctx.effect(
        () =>
          ctx.webServer.register({
            kind: 'prefix',
            path: '/auriga/local-login',
            handler(req, res) {
              const active = current();
              if (active) return active.handler(req, res);
              if (!configured)
                return createUnconfiguredLoginRoutes(
                  admission?.nativeAuth ?? ctx.connection,
                )(req, res);
              const rejected = admission.nativeAuth.requestRejection(req);
              res.writeHead(rejected ?? 503, { 'cache-control': 'no-store' });
              res.end();
            },
          }),
        'auriga-dsh: persistent login route',
      );
      ctx.effect(
        () =>
          ctx.webServer.register({
            kind: 'prefix',
            path: '/auriga',
            handler(req, res) {
              return createLocalSurfaceHandler({
                mode: configured ? 'connect' : 'standalone',
                auth: admission?.nativeAuth ?? ctx.connection,
                ...(configured ? { runtime: gate } : {}),
              })(req, res);
            },
          }),
        'auriga-dsh: connect surfaces',
      );
      ctx.inject(['profileContext'], async (profileCtx) => {
        const owner = {};
        profileOwner = owner;
        setConfigured(true);
        binding = undefined;
        profileCtx.effect(
          () => () => {
            if (profileOwner !== owner) return;
            profileOwner = undefined;
            setConfigured(true);
            binding = undefined;
          },
          'auriga-dsh: profile admission',
        );
        const profile = profileCtx.profileContext;
        if (!profile?.dir)
          throw new Error(
            'auriga-dsh: host profile directory required for persistent state',
          );
        const loading = settings.getStore(profile.dir).then((store) => {
          if (profileOwner !== owner) return null;
          setConfigured(!!store.snapshot().platformOrigin);
          const unwatchAdmission = store.subscribe(
            async (next) => {
              if (profileOwner === owner) setConfigured(!!next.platformOrigin);
            },
            async (next) => {
              if (
                profileOwner === owner &&
                next.platformOrigin !== store.snapshot().platformOrigin
              )
                setConfigured(true);
            },
          );
          profileCtx.effect(
            () => unwatchAdmission,
            'auriga-dsh: saved platform admission',
          );
          return store;
        });
        profileCtx.inject(
          ['tools', 'sessions', 'sessionController'],
          async (connectCtx) => {
            const store = await loading;
            if (!store || profileOwner !== owner) return;
            let owned, networkProvider, modelProvider;
            let detachNetwork, detachModels;
            let disposed = false;
            let configuring = Promise.resolve();
            const invalidate = async (origin) => {
              if (owned?.runtime.config.platformOrigin === origin) return;
              const previous = owned;
              binding = undefined;
              owned = undefined;
              detachNetwork = undefined;
              detachModels = undefined;
              if (previous) {
                try {
                  await previous.runtime.resetAuthentication();
                } finally {
                  await previous.runtime.close();
                }
              }
            };
            const configure = (origin) => {
              const operation = configuring.then(async () => {
                if (disposed || profileOwner !== owner) return;
                if (owned?.runtime.config.platformOrigin === origin) return;
                await invalidate(origin);
                if (!origin || disposed) return;
                const effective = await savedPlatformConfig(
                  config?.connect,
                  origin,
                  { saved: store.hasSavedSettings },
                );
                if (disposed) return;
                const runtime = await createConnectRuntime({
                  config: effective,
                  directory: join(profile.dir, 'auriga-connect'),
                  ctx: connectCtx,
                });
                try {
                  const saved = runtime.vault.snapshot().credential;
                  if (
                    saved &&
                    ((saved.platformOrigin ??
                      new URL(saved.claims.iss).origin) !==
                      effective.platformOrigin ||
                      (saved.issuer ?? saved.claims.iss) !== effective.issuer ||
                      (saved.clientId ?? effective.clientId) !==
                        effective.clientId)
                  )
                    await runtime.resetAuthentication();
                  if (disposed || profileOwner !== owner) {
                    await runtime.close();
                    return;
                  }
                  if (networkProvider)
                    detachNetwork = runtime.attachNetwork(networkProvider);
                  if (modelProvider)
                    detachModels = await runtime.attachModels(modelProvider);
                  if (disposed || profileOwner !== owner) {
                    await runtime.close();
                    return;
                  }
                  owned = {
                    ctx: connectCtx,
                    runtime,
                    handler: createConnectRoutes({ runtime, admission }),
                  };
                  binding = owned;
                } catch (error) {
                  await runtime.close();
                  throw error;
                }
              });
              configuring = operation.catch(() => {});
              return operation;
            };
            const unsubscribe = store.subscribe(
              (next) => configure(next.platformOrigin),
              async (next) => {
                await configuring;
                await invalidate(next.platformOrigin);
              },
            );
            connectCtx.effect(
              () => () => {
                disposed = true;
                unsubscribe();
                if (binding === owned) binding = undefined;
                return owned?.runtime.close();
              },
              'auriga-dsh: injected local runtime',
            );
            connectCtx.inject(['aurigaNetwork'], (networkCtx) => {
              networkProvider = networkCtx.aurigaNetwork;
              detachNetwork = owned?.runtime.attachNetwork(networkProvider);
              networkCtx.effect(
                () => () => {
                  networkProvider = undefined;
                  detachNetwork?.();
                  detachNetwork = undefined;
                },
                'auriga-dsh: network provider',
              );
            });
            connectCtx.inject(['llm'], async (llmCtx) => {
              modelProvider = llmCtx.llm;
              detachModels = await owned?.runtime.attachModels(modelProvider);
              llmCtx.effect(
                () => () => {
                  modelProvider = undefined;
                  detachModels?.();
                  detachModels = undefined;
                },
                'auriga-dsh: public model adapter',
              );
            });
            // Keep settings/retry ownership alive even when the platform is down.
            // The parent login door stays closed and activeOrigin remains empty.
            await configure(store.snapshot().platformOrigin).catch(() => {});
          },
        );
        await loading;
      });
      return;
    }
  }
  const runtime = await createManaged({
    env: process.env,
    config: config ?? {},
    startHeartbeat: false,
  });
  logEvent('INFO', 'AI installation', { state: MANAGED_MODEL_STATUS.state });
  let dispose;
  try {
    dispose = runtime.apply(ctx);
  } catch (error) {
    runtime.close();
    throw error;
  }
  runtime.heartbeat.start();
  ctx.effect?.(
    () => () => {
      dispose?.();
      runtime.close();
    },
    'auriga-dsh: dispose',
  );
  logEvent('INFO', 'Managed mode started', { instance_id: runtime.instanceId });
}
