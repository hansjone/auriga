/* global AbortSignal */
import { readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

/** Installer-owned capability. An ordinary/plugin-only DSH has no updater.
 * @param {{origin:string}} options */
export async function desktopUpdater({ origin }) {
  const root = process.env.AURIGA_DESKTOP_ROOT;
  if (!root || !process.env.AURIGA_DESKTOP_RUNTIME) return undefined;
  const state = JSON.parse(await readFile(join(root, 'install.json'), 'utf8'));
  if (!origin.startsWith('https:')) return undefined;
  try {
    await writeFile(
      join(root, 'desktop-origin.json'),
      JSON.stringify({ origin }),
      { flag: 'wx', mode: 0o600 },
    );
  } catch (error) {
    if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'EEXIST')
      throw error;
  }
  const bound = JSON.parse(
    await readFile(join(root, 'desktop-origin.json'), 'utf8'),
  );
  if (bound.origin !== origin || !origin.startsWith('https:')) return undefined;
  const modulePath = resolve(
    root,
    'versions',
    state.current,
    'auriga/runtime/update.mjs',
  );
  if (modulePath !== resolve(process.env.AURIGA_DESKTOP_RUNTIME))
    return undefined;
  const module = await import(pathToFileURL(modulePath).href);
  const lifetime = new AbortController();
  const updater = module.createUpdater({
    root,
    origin,
    request: async (/** @type {string} */ url, /** @type {any} */ init) => {
      if (new URL(url).origin !== origin)
        throw new Error('untrusted desktop download');
      return fetch(url, {
        ...init,
        credentials: 'omit',
        redirect: 'error',
        headers: { Accept: 'application/json' },
        signal: AbortSignal.any([
          lifetime.signal,
          AbortSignal.timeout(15 * 60 * 1000),
        ]),
      });
    },
  });
  const stop = updater.start();
  return {
    ...updater,
    close() {
      stop();
      lifetime.abort();
    },
  };
}
