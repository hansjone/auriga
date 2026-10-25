// @ts-nocheck
/**
 * Cordis / DSH host entry: managed runtime or authenticated local UI.
 * Local pages are unconfigured; identity library sessions are not mounted here.
 */
import { createManaged } from './modes/managed.js';
import { selectMode } from './index.js';
import { createLocalSurfaceHandler } from './local-ui.js';
import { createConnectRuntime } from './local/runtime.js';
import {
  installLocalAdmission,
  installHttpResponseTracking,
} from './local/host.js';
import { createConnectRoutes } from './local/routes.js';
import { join } from 'node:path';

export const name = 'auriga-dsh';
export const inject = ['webServer', 'connection'];

/**
 * @param {object} ctx
 * @param {import('./generated/managed-config.js').ManagedConfig} [config] Loader row config
 */
export async function apply(ctx, config) {
  const mode = selectMode();
  if (mode !== 'managed') {
    if (!ctx.webServer?.register || !ctx.connection?.requestRejection)
      throw new Error(
        'auriga-dsh: local UI requires native host authentication',
      );
    if (config?.connect) {
      // The parent owns the closed door while native providers are pending or
      // being unloaded. Only the injected child owns the runtime and tools.
      let binding;
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
              current()?.runtime.bindHttpResponse(req, res) ?? false,
          ),
      };
      const admission = installLocalAdmission(ctx.connection, gate, {
        protocol: ctx.webServer.protocol,
      });
      ctx.effect(
        () => () => admission.dispose(),
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
            handler: createLocalSurfaceHandler({
              mode: 'connect',
              auth: ctx.connection,
              runtime: gate,
            }),
          }),
        'auriga-dsh: connect surfaces',
      );
      ctx.inject(
        ['profileContext', 'tools', 'sessions', 'sessionController'],
        async (connectCtx) => {
          const profile = connectCtx.profileContext;
          if (!profile?.dir)
            throw new Error(
              'auriga-dsh: isolated host profile directory required',
            );
          const runtime = await createConnectRuntime({
            config: config.connect,
            directory: join(profile.dir, 'auriga-connect'),
            ctx: connectCtx,
          });
          const owned = {
            ctx: connectCtx,
            runtime,
            handler: createConnectRoutes({ runtime, admission }),
          };
          try {
            connectCtx.effect(() => {
              binding = owned;
              return () => {
                if (binding === owned) binding = undefined;
                runtime.close();
              };
            }, 'auriga-dsh: injected local runtime');
          } catch (error) {
            runtime.close();
            throw error;
          }
        },
      );
      return;
    }
    ctx.effect?.(
      () =>
        ctx.webServer.register({
          kind: 'prefix',
          path: '/auriga',
          handler: createLocalSurfaceHandler({
            mode,
            auth: ctx.connection,
          }),
        }),
      'auriga-dsh: local surfaces',
    );
    return;
  }
  const runtime = await createManaged({
    env: process.env,
    config: config ?? {},
    startHeartbeat: false,
  });
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
  ctx.logger?.info?.(
    `[auriga-dsh] managed mode instance=${runtime.instanceId}`,
  );
}
