import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { Broker } from './broker.mjs';
import { createPlatform, trustedConfig } from './platform.mjs';
import { Vault, safePath, privateDirectory } from './vault.mjs';
import { valid } from './schema.mjs';
import { createLoopback } from './loopback.mjs';
import { startIpc } from './ipc.mjs';
import { BusinessProxy } from './business.mjs';
import { createLog } from './diagnostics.mjs';

export async function openSystemBrowser(url) {
  const parsed = new URL(url);
  if (!['https:', 'http:'].includes(parsed.protocol))
    throw new Error('invalid browser target');
  // argv boundary documented by design: only launch ticket, never platform token.
  await new Promise((resolve, reject) => {
    let child;
    if (process.platform === 'win32') {
      child = spawn(
        join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/rundll32.exe'),
        ['url.dll,FileProtocolHandler', parsed.href],
        { windowsHide: true, stdio: 'ignore' },
      );
    } else if (process.platform === 'linux') {
      child = spawn('xdg-open', [parsed.href], {
        stdio: 'ignore',
        detached: true,
      });
      child.unref?.();
    } else {
      reject(new Error('supported system browser launcher unavailable'));
      return;
    }
    child.once('error', () => reject(new Error('browser launch failed')));
    // xdg-open often exits immediately after handing off; treat spawn success as OK.
    if (process.platform === 'linux') {
      child.once('spawn', () => resolve(undefined));
      return;
    }
    child.once('exit', (code) =>
      code === 0
        ? resolve(undefined)
        : reject(new Error('browser launch failed')),
    );
  });
}

function shellHelperPath() {
  if (process.platform !== 'win32') return null;
  return fileURLToPath(new URL('./windows/shell-helper.exe', import.meta.url));
}

export async function startDesktopAuth({
  root,
  dataHome,
  openBrowser = openSystemBrowser,
  onSkipLogin,
  fetcher = globalThis.fetch,
  resolveWsAgent,
}) {
  const originFile = safePath(join(root, 'desktop-origin.json'));
  if (!existsSync(originFile)) return undefined;
  const installation = JSON.parse(readFileSync(originFile, 'utf8'));
  const origin = installation.origin;
  const helper = shellHelperPath();
  const vault = new Vault(join(dataHome, 'auriga', 'shell-auth'), helper);
  let broker, ipc, loopback, business;
  const log = createLog(join(dataHome, 'auriga', 'logs'), helper, () => {
    const identity = broker?.projection?.identity;
    return identity
      ? { tenantId: identity.tenantId, providerId: identity.providerId }
      : {};
  });
  try {
    broker = new Broker({
      platform: createPlatform(
        trustedConfig({
          platformOrigin: origin,
          accessMode: installation.accessMode,
          confirmedOrigin: installation.confirmedOrigin,
          issuer: installation.issuer,
          clientId: installation.clientId,
          nativeClientId: installation.nativeClientId,
          network: installation.network,
        }),
        fetcher,
      ),
      vault,
      openBrowser,
      log,
    });
    await broker.initialize();
    loopback = await createLoopback({ broker, validate: valid, onSkipLogin });
    business = new BusinessProxy(
      broker,
      fetcher,
      privateDirectory(
        join(dataHome, 'auriga', 'shell-auth', 'uploads'),
        helper,
      ),
      {
        networkDirectory: privateDirectory(
          join(dataHome, 'auriga', 'shell-auth', 'network'),
          helper,
        ),
        readConfig: () =>
          JSON.parse(readFileSync(safePath(originFile), 'utf8')),
        ...(resolveWsAgent ? { resolveWsAgent } : {}),
      },
    );
    ipc = await startIpc({ helper, broker, business });
    const timer = setInterval(() => broker.check().catch(() => {}), 10000);
    timer.unref();
    return {
      broker,
      snapshot: () => broker.snapshot(),
      // Explicit recovery only; startup and tray use the DSH modal.
      openLoginFallback: () => openBrowser(loopback.issue()),
      attach: (child) => ipc.attach(child),
      async close() {
        clearInterval(timer);
        await ipc.close();
        await loopback.close();
        business.close();
        await broker.close();
      },
    };
  } catch (error) {
    await ipc?.close();
    await loopback?.close();
    business?.close();
    await broker?.close();
    throw error;
  }
}
