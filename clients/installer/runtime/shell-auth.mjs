import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';

/**
 * Fixed, secret-free reason codes for the tray status. Raw exception text can
 * contain configuration, so only our own well-known messages are classified.
 */
export function shellAuthFailureReason(error) {
  const message = String(error?.message ?? '');
  if (/trusted HTTPS platform origin required|invalid issuer/.test(message))
    return 'origin_rejected';
  if (/Windows shell helper required/.test(message)) return 'helper_missing';
  if (/\bsecure\b/.test(message) || error?.cmd?.includes?.(' secure '))
    return 'acl_failed';
  if (/reparse path rejected/.test(message)) return 'reparse_rejected';
  if (error?.code === 'ERR_MODULE_NOT_FOUND') return 'component_missing';
  return 'unavailable';
}

/** Authentication failure preserves local DSH and leaves a safe diagnosis. */
export async function startShellAuth({
  root,
  dataHome,
  dir,
  onFailure = () => {},
}) {
  const directory = join(dir, 'auriga', 'shell-auth');
  try {
    const { startDesktopAuth } = await import(
      pathToFileURL(join(directory, 'desktop.mjs')).href
    );
    const auth = await startDesktopAuth({
      root,
      dataHome,
    });
    if (!auth) onFailure('not_configured');
    return auth;
  } catch (error) {
    onFailure(shellAuthFailureReason(error));
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
        process.platform === 'win32'
          ? join(directory, 'windows', 'shell-helper.exe')
          : null,
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

/** Only the trusted tray control channel can request browser recovery. */
export function registerFallbackLogin(control, getAuth, report) {
  control.on?.('login-fallback', async () => {
    let opened = false;
    try {
      const auth = getAuth();
      if (typeof auth?.openLoginFallback === 'function') {
        await auth.openLoginFallback();
        opened = true;
      }
    } catch {
      /* Report a fixed outcome without upstream details or URLs. */
    }
    report({ event: 'fallback-login', opened });
  });
}
