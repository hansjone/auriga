import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { PROTOCOL_VERSION } from './managed/protocol.js';

/**
 * Local packaged UI only. It grants no Auriga identity, resource transport or session.
 * @param {{ mode: 'standalone' | 'connect', auth: { requestRejection(req: object): number | undefined } }} options
 */
export function createLocalSurfaceHandler({ mode, auth }) {
  /** @param {import('node:http').IncomingMessage} req @param {import('node:http').ServerResponse} res */
  return function handle(req, res) {
    const rejection = auth.requestRejection(req);
    if (rejection !== undefined) {
      res.writeHead(rejection);
      res.end();
      return;
    }
    const path = (req.url ?? '').split('?')[0];
    if (!['/auriga/bootstrap', '/auriga/local-panel.js'].includes(path)) {
      res.writeHead(404);
      res.end();
      return;
    }
    if (req.method !== 'GET') {
      res.writeHead(405);
      res.end();
      return;
    }
    try {
      const source = readFileSync(
        new URL('../ui/auriga-team-panel.js', import.meta.url),
      );
      const metadata = JSON.parse(
        readFileSync(new URL('../ui/metadata.json', import.meta.url), 'utf8'),
      );
      const integrity =
        'sha384-' + createHash('sha384').update(source).digest('base64');
      if (metadata.integrity !== integrity)
        throw new Error('Packaged UI integrity mismatch');
      if (path === '/auriga/local-panel.js') {
        res.writeHead(200, {
          'content-type': 'text/javascript; charset=utf-8',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(source);
        return;
      }
      const pkg = JSON.parse(
        readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
      );
      res.writeHead(200, {
        'content-type': 'application/json; charset=utf-8',
        'cache-control': 'no-store',
      });
      res.end(
        JSON.stringify({
          mode,
          protocol: PROTOCOL_VERSION,
          pluginVersion: pkg.version,
          status: 'unconfigured',
          localPanel: {
            source: 'package',
            version: metadata.version,
            entry: 'auriga/local-panel.js',
            integrity,
          },
          fetchBase: null,
        }),
      );
    } catch {
      res.writeHead(503, {
        'content-type': 'application/problem+json',
        'cache-control': 'no-store',
      });
      res.end(
        JSON.stringify({
          type: 'about:blank',
          status: 503,
          code: 'common.unavailable',
          detail: 'packaged UI unavailable',
        }),
      );
    }
  };
}
