import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';

/** Authentication failure preserves local DSH and leaves a safe diagnosis. */
export async function startShellAuth({ root, dataHome, dir }) {
  const directory = join(dir, 'auriga', 'shell-auth');
  try {
    const { startDesktopAuth } = await import(
      pathToFileURL(join(directory, 'desktop.mjs')).href
    );
    return await startDesktopAuth({ root, dataHome });
  } catch {
    const event = {
      stage: 'shell_identity',
      phase: 'end',
      result: 'failure',
      state: 'unreachable',
      reason: 'shell_restarted',
      correlationId: randomUUID(),
    };
    try {
      const { createLog } = await import(
        pathToFileURL(join(directory, 'diagnostics.mjs')).href
      );
      createLog(
        join(dataHome, 'auriga', 'logs'),
        join(directory, 'windows', 'shell-helper.exe'),
      )(event);
    } catch {
      // Missing helper/sink must not create an unprotected fallback file or
      // expose the original exception (which can contain configuration secrets).
      console.error(
        JSON.stringify({
          ...event,
          service: 'shell-auth',
          level: 'WARN',
          timestamp: new Date().toISOString(),
        }),
      );
    }
    console.error('Auriga 平台认证暂不可用，继续使用本地 DSH。');
    return undefined;
  }
}
