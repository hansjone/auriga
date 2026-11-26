/**
 * Thin wrapper around the bundled native Win32 helper (`auriga-win-helper.exe`).
 * Falls back cleanly to null when the helper binary is unavailable (e.g. non-Windows
 * or lightweight unit-test fixtures that do not compile C# helpers).
 */
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

export function winHelperPath() {
  if (process.platform !== 'win32') return null;
  const override = process.env.AURIGA_WIN_HELPER;
  if (override && existsSync(override)) return override;
  const bundled = join(here, 'auriga-win-helper.exe');
  return existsSync(bundled) ? bundled : null;
}

export function execWinHelper(args, { timeout = 15000 } = {}) {
  const helper = winHelperPath();
  if (!helper) return null;
  return execFileSync(helper, args, {
    encoding: 'utf8',
    timeout,
    windowsHide: true,
    maxBuffer: 32 * 1024 * 1024,
  });
}

export function spawnWinHelper(args, { timeout = 60000 } = {}) {
  const helper = winHelperPath();
  if (!helper) return null;
  return spawnSync(helper, args, {
    encoding: 'utf8',
    timeout,
    windowsHide: true,
  });
}
