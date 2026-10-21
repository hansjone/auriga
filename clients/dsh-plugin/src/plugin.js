// @ts-nocheck
/**
 * Cordis / DSH host entry for managed mode.
 * Standalone and connect hosts should use the library API from index.js instead.
 */
import { createManaged } from './modes/managed.js';

export const name = 'auriga-dsh';
export const inject = ['webServer', 'connection'];

/**
 * @param {object} ctx
 * @param {{ imBots?: 'none', trustedHosts?: string[] }} [config] Loader row config
 */
export async function apply(ctx, config) {
  const mode = process.env.AURIGA_MODE ?? 'standalone';
  if (mode !== 'managed') {
    ctx.logger?.info?.(
      `[auriga-dsh] AURIGA_MODE=${mode}; Cordis apply is a no-op (use library API for standalone)`,
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
