/* global AbortSignal */
import { readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

/** Each received chunk renews the idle deadline, regardless of total transfer time.
 * @param {string} url
 * @param {RequestInit} init
 * @param {AbortSignal} lifetime
 * @param {number} idleMs
 */
export async function idleFetch(url, init, lifetime, idleMs = 60_000) {
  const controller = new AbortController();
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let timer;
  const reset = () => {
    clearTimeout(timer);
    timer = setTimeout(
      () => controller.abort(new Error('desktop transfer idle timeout')),
      idleMs,
    );
  };
  reset();
  try {
    const response = await fetch(url, {
      ...init,
      signal: AbortSignal.any([lifetime, controller.signal]),
    });
    // Error responses have no body consumed by the updater.
    if (!response.ok) {
      clearTimeout(timer);
      await response.body?.cancel();
      return response;
    }
    clearTimeout(timer);
    return {
      ok: response.ok,
      status: response.status,
      headers: response.headers,
      async cancel() {
        clearTimeout(timer);
        controller.abort();
        await response.body?.cancel().catch(() => {});
      },
      body: (async function* () {
        reset();
        const reader = response.body?.getReader();
        try {
          if (reader)
            for (;;) {
              const { value, done } = await reader.read();
              if (done) break;
              reset();
              yield value;
            }
        } finally {
          clearTimeout(timer);
          await reader?.cancel().catch(() => {});
          reader?.releaseLock();
        }
      })(),
    };
  } catch (error) {
    clearTimeout(timer);
    throw error;
  }
}

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
      return idleFetch(
        url,
        {
          ...init,
          credentials: 'omit',
          redirect: 'error',
          headers: { Accept: 'application/json', ...init.headers },
        },
        lifetime.signal,
      );
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
