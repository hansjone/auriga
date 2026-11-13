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
  if (process.platform !== 'win32')
    throw new Error('supported system browser launcher unavailable');
  const parsed = new URL(url);
  if (!['https:', 'http:'].includes(parsed.protocol))
    throw new Error('invalid browser target');
  // argv boundary documented by design: only launch ticket, never platform token.
  await new Promise((resolve, reject) => {
    const child = spawn(
      join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/rundll32.exe'),
      ['url.dll,FileProtocolHandler', parsed.href],
      { windowsHide: true, stdio: 'ignore' },
    );
    child.once('error', () => reject(new Error('browser launch failed')));
    child.once('exit', (code) =>
      code === 0
        ? resolve(undefined)
        : reject(new Error('browser launch failed')),
    );
  });
}
export async function startDesktopAuth({
  root,
  dataHome,
  openBrowser = openSystemBrowser,
}) {
  const originFile = safePath(join(root, 'desktop-origin.json'));
  if (!existsSync(originFile)) return undefined;
  const installation = JSON.parse(readFileSync(originFile, 'utf8'));
  const origin = installation.origin;
  const helper = fileURLToPath(
    new URL('./windows/shell-helper.exe', import.meta.url),
  );
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
          issuer: installation.issuer,
          clientId: installation.clientId,
        }),
      ),
      vault,
      openBrowser,
      log,
    });
    await broker.initialize();
    loopback = await createLoopback({ broker, validate: valid });
    business = new BusinessProxy(
      broker,
      globalThis.fetch,
      privateDirectory(
        join(dataHome, 'auriga', 'shell-auth', 'uploads'),
        helper,
      ),
    );
    ipc = await startIpc({ helper, broker, business });
    const timer = setInterval(() => broker.check().catch(() => {}), 10000);
    timer.unref();
    await openBrowser(loopback.issue());
    return {
      broker,
      snapshot: () => broker.snapshot(),
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
