// @ts-nocheck
import { installBaseToolsHost } from './network/base-tools-host.js';
/**
 * Cordis / DSH host entry: managed runtime or authenticated local UI.
 * Local pages are unconfigured; identity library sessions are not mounted here.
 */
import { createManaged } from './modes/managed.js';
import { selectMode } from './index.js';
import { createLocalSurfaceHandler } from './local-ui.js';
import { createConnectRuntime } from './local/runtime.js';
import { savedPlatformConfig } from './local/config.js';
import { installNativeLoginRPC } from './local/native-rpc.js';
import { installLocalAdmission } from './local/host.js';
import {
  createConnectRoutes,
  createUnconfiguredLoginRoutes,
} from './local/routes.js';
import { join } from 'node:path';
import { installSettings } from './settings/host.js';
import { installNetworkDiagnosticSink } from './network/diagnostics.js';
import { installObservability, logEvent } from './observability/index.js';
import { installResourceChannel } from './resources/channel.js';
import { createMemberRoutes } from './resources/member-routes.js';

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
  let binding, profileOwner, memberResources, managedRuntime;
  installBaseToolsHost(ctx, () =>
    mode === 'managed' ? managedRuntime?.baseTools : binding?.runtime.baseTools,
  );
  // Preferences select Auriga surfaces; DSH owns local admission independently.
  let configured = true;
  let admission;
  const settings = installSettings(
    ctx,
    config,
    mode === 'managed'
      ? {
          mode: 'managed',
          networkControl: () => managedRuntime?.networkControl,
        }
      : {
          activeOrigin: () => binding?.runtime.config.platformOrigin ?? '',
          desktop: () => binding?.runtime.desktop,
          broker: () => binding?.runtime.broker,
          networkControl: () => binding?.runtime.networkControl,
          nativeAuth: {
            requestRejection: (req) =>
              admission
                ? admission.nativeAuth.requestRejection(req)
                : ctx.connection.requestRejection(req),
          },
        },
  );
  if (mode !== 'managed') {
    if (!ctx.webServer?.register || !ctx.connection?.requestRejection)
      throw new Error(
        'auriga-dsh: local UI requires native host authentication',
      );
    {
      // The parent keeps local owner admission alive while platform services load.
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
        bindNativeRequest: (peer, req) =>
          current()?.runtime.bindNativeRequest(peer, req) ?? null,
        status: (req) =>
          current()?.runtime.status(req) ?? {
            connected: false,
            connection: null,
          },
      };
      admission = installLocalAdmission(ctx.connection, gate, {
        protocol: ctx.webServer.protocol,
      });
      installNativeLoginRPC(ctx, () => current()?.runtime);
      ctx.effect(
        () =>
          ctx.webServer.register({
            kind: 'prefix',
            path: '/dsh-team',
            handler(req, res) {
              const runtime = current()?.runtime;
              if (runtime?.enterprise)
                return runtime.enterprise.handle(req, res);
              res.writeHead(503, { 'cache-control': 'no-store' });
              res.end();
            },
          }),
        'auriga: enterprise skill compatibility',
      );
      installResourceChannel(ctx, {
        auth: admission.nativeAuth,
        current: () => current()?.runtime,
        memberHandler: (runtime, req, res) =>
          memberResources
            ? memberResources.handle(runtime, req, res)
            : Promise.resolve(res.writeHead(503).end()),
      });
      ctx.effect(
        () => () => admission?.dispose(),
        'auriga-dsh: persistent local door',
      );
      ctx.effect(
        () =>
          ctx.webServer.register({
            kind: 'prefix',
            path: '/auriga/shell',
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
                memberResourcesAvailable: !!memberResources,
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
        configured = true;
        binding = undefined;
        profileCtx.effect(
          () => () => {
            if (profileOwner !== owner) return;
            profileOwner = undefined;
            configured = true;
            binding = undefined;
          },
          'auriga-dsh: profile admission',
        );
        const profile = profileCtx.profileContext;
        if (!profile?.dir)
          throw new Error(
            'auriga-dsh: host profile directory required for persistent state',
          );
        const resources = createMemberRoutes({
          directory: profile.dir,
          ctx: profileCtx,
          trustFile: process.env.AURIGA_RESOURCE_TRUST_FILE,
          current: () => binding?.runtime,
          sameOrigin: (req, write) => admission.sameOrigin(req, write),
        });
        memberResources = resources;
        profileCtx.effect(
          () => () => {
            if (memberResources === resources) memberResources = undefined;
            return resources.close();
          },
          'auriga-dsh: member resource lifetime',
        );
        const loading = settings.getStore(profile.dir).then((store) => {
          if (profileOwner !== owner) return null;
          configured = !!store.snapshot().platformOrigin;
          const unwatchAdmission = store.subscribe(
            async (next) => {
              if (profileOwner === owner) configured = !!next.platformOrigin;
            },
            async (next) => {
              if (
                profileOwner === owner &&
                next.platformOrigin !== store.snapshot().platformOrigin
              )
                configured = true;
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
                if (!origin || disposed) {
                  await resources.attach(undefined);
                  return;
                }
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
                  await resources.beforeIdentity(runtime);
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
                  await resources.attach(runtime).catch(() => {});
                } catch (error) {
                  await runtime.close();
                  throw error;
                }
              });
              configuring = operation.catch(async () => {
                await resources.attach(undefined).catch(() => {});
              });
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
            // Only platform status is unavailable when its runtime cannot load.
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
  managedRuntime = runtime;
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
