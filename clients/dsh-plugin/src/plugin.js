// @ts-nocheck
/**
 * Cordis / DSH host entry for managed mode.
 * Standalone and connect hosts should use the library API from index.js instead.
 */
import { createManaged } from './modes/managed.js';

export const name = 'auriga-dsh';
export const inject = ['webServer', 'connection'];

/** @type {Awaited<ReturnType<typeof createManaged>> | undefined} */
let runtime;

/**
 * @param {object} ctx
 */
export async function apply(ctx) {
  const mode = process.env.AURIGA_MODE ?? 'standalone';
  if (mode !== 'managed') {
    ctx.logger?.info?.(
      `[auriga-dsh] AURIGA_MODE=${mode}; Cordis apply is a no-op (use library API for standalone)`,
    );
    return;
  }
  runtime = await createManaged({
    env: process.env,
    startHeartbeat: false,
  });
  const dispose = runtime.apply(ctx);
  runtime.heartbeat.start();
  ctx.effect?.(
    () => () => {
      dispose?.();
      runtime?.close();
      runtime = undefined;
    },
    'auriga-dsh: dispose',
  );
  ctx.logger?.info?.(
    `[auriga-dsh] managed mode instance=${runtime.instanceId}`,
  );
}
