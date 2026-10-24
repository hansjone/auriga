// @ts-nocheck
/**
 * Cordis / DSH host entry: managed runtime or authenticated local UI.
 * Local pages are unconfigured; identity library sessions are not mounted here.
 */
import { createManaged } from './modes/managed.js';
import { selectMode } from './index.js';
import { createLocalSurfaceHandler } from './local-ui.js';
import { createConnectRuntime } from './local/runtime.js';
import { installLocalAdmission } from './local/host.js';
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
    let connectRuntime;
    if (config?.connect) {
      const profile = ctx.get?.('profileContext');
      if (!profile?.dir)
        throw new Error('auriga-dsh: isolated host profile directory required');
      const runtime = await createConnectRuntime({
        config: config.connect,
        directory: join(profile.dir, 'auriga-connect'),
        ctx,
      });
      connectRuntime = runtime;
      let admission, unregister;
      try {
        admission = installLocalAdmission(ctx.connection, runtime, {
          protocol: ctx.webServer.protocol,
        });
        unregister = ctx.webServer.register({
          kind: 'prefix',
          path: '/auriga/local-login',
          handler: createConnectRoutes({ runtime, admission }),
        });
        ctx.effect(
          () => () => {
            unregister();
            admission.dispose();
            runtime.close();
          },
          'auriga-dsh: local connection',
        );
      } catch (error) {
        unregister?.();
        admission?.dispose();
        runtime.close();
        throw error;
      }
    }
    ctx.effect?.(
      () =>
        ctx.webServer.register({
          kind: 'prefix',
          path: '/auriga',
          handler: createLocalSurfaceHandler({
            mode: connectRuntime ? 'connect' : mode,
            auth: ctx.connection,
            runtime: connectRuntime,
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
