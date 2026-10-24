// @ts-nocheck
/**
 * Cordis / DSH host entry: managed runtime or authenticated local UI.
 * Local pages are unconfigured; identity library sessions are not mounted here.
 */
import { createManaged } from './modes/managed.js';
import { selectMode } from './index.js';
import { createLocalSurfaceHandler } from './local-ui.js';
import { installLocalResources } from './local-resources/index.js';

export const name = 'auriga-dsh';
export const inject = ['webServer', 'connection'];

/**
 * @param {object} ctx
 * @param {import('./generated/managed-config.js').ManagedConfig} [config] Loader row config
 */
export async function apply(ctx, config) {
  const mode = selectMode();
  installLocalResources(ctx, mode);
  if (mode !== 'managed') {
    if (!ctx.webServer?.register || !ctx.connection?.requestRejection)
      throw new Error(
        'auriga-dsh: local UI requires native host authentication',
      );
    ctx.effect?.(
      () =>
        ctx.webServer.register({
          kind: 'prefix',
          path: '/auriga',
          handler: createLocalSurfaceHandler({ mode, auth: ctx.connection }),
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
