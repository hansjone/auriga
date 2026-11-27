import { readFile } from 'node:fs/promises';

const ASSETS = new Map([
  ['/auriga/shell-ui/overview.js', './overview.js'],
  ['/auriga/shell-ui/icons.js', './icons.js'],
]);

/**
 * Serve overview UI modules to the native client (same pattern as settings/view.js).
 * @param {any} ctx
 */
export function installShellUi(ctx) {
  if (typeof ctx.inject !== 'function' || !ctx.webServer?.register) return;
  ctx.inject(['profileContext'], async (/** @type {any} */ scoped) => {
    if (!scoped.profileContext?.dir) return;
    scoped.effect(
      () =>
        scoped.webServer.register({
          kind: 'prefix',
          path: '/auriga/shell-ui',
          handler: async (req, res) => {
            if (req.method !== 'GET' && req.method !== 'HEAD') {
              res.writeHead(405, { 'cache-control': 'no-store' });
              res.end();
              return;
            }
            const path = req.url?.split('?')[0];
            const file = ASSETS.get(path);
            if (!file) {
              res.writeHead(404, { 'cache-control': 'no-store' });
              res.end();
              return;
            }
            const body = await readFile(new URL(file, import.meta.url));
            res.writeHead(200, {
              'content-type': 'text/javascript; charset=utf-8',
              'cache-control': 'no-store',
              'x-content-type-options': 'nosniff',
              'content-length': body.length,
            });
            if (req.method !== 'HEAD') res.end(body);
            else res.end();
          },
        }),
      'auriga-dsh: shell-ui',
    );
  });
}
