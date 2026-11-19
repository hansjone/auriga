// @ts-nocheck
import { readFile } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { dedicatedBootstrap } from './bootstrap.js';
import { fail } from '../shared/protocol.js';

async function json(req) {
  if (req.headers['content-type']?.split(';')[0].trim() !== 'application/json')
    fail('common.validation_failed');
  const chunks = [];
  let length = 0;
  for await (const chunk of req) {
    length += chunk.length;
    if (length > 1048576) fail('common.validation_failed');
    chunks.push(chunk);
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    fail('common.validation_failed');
  }
}
async function write(res, value, signal) {
  signal.throwIfAborted();
  const encoded = JSON.stringify(value) + '\n';
  if (Buffer.byteLength(encoded) > 8388608) fail('common.unavailable');
  if (!res.write(encoded))
    await new Promise((resolve) => {
      const done = () => {
        res.off('drain', done);
        res.off('close', done);
        resolve();
      };
      res.once('drain', done);
      res.once('close', done);
    });
  signal.throwIfAborted();
}
export function createDedicatedBrowser(ctx, { host, distIndex }) {
  if (
    typeof distIndex !== 'string' ||
    !isAbsolute(distIndex) ||
    !distIndex.endsWith('/index.html')
  )
    fail('common.validation_failed');
  let closed = false;
  const active = new Set();
  const handler = async (req, res) => {
    const abort = new AbortController();
    let monitor;
    const disconnect = () => abort.abort();
    active.add(abort);
    res.once('close', disconnect);
    try {
      const path = req.url;
      const index = path === '/' || path === '/index.html';
      if (
        closed ||
        (index
          ? req.method !== 'GET'
          : req.method !== 'POST' ||
            ![
              '/auriga/dedicated/browser/rpc',
              '/auriga/dedicated/browser/stream',
            ].includes(path))
      )
        fail('common.validation_failed');
      if (
        req.headers['sec-fetch-site'] === 'cross-site' ||
        typeof req.headers['auriga-identity'] !== 'string'
      )
        fail('shared.revoked');
      const row = await host.admit(req.headers['auriga-identity'], {
        method: req.method,
        canonicalPath: path,
      });
      const signal = globalThis.AbortSignal.any([
        abort.signal,
        row.abort.signal,
        row.native.signal,
      ]);
      await host.check(row);
      signal.throwIfAborted();
      if (index) {
        const raw = await readFile(distIndex, 'utf8');
        const html = ctx.webServer
          .renderIndex(raw)
          .replace(
            /<head(?:\s[^>]*)?>/i,
            (head) =>
              `${head}<base href="./"><script>${dedicatedBootstrap}</script>`,
          )
          .replace(
            /<\/body>/i,
            '<script>globalThis.__DSH_REMOTE_ACCESS__={type:"baseline",owner:true};</script></body>',
          );
        await host.check(row);
        signal.throwIfAborted();
        res.writeHead(200, {
          'content-type': 'text/html; charset=utf-8',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(html);
        return;
      }
      const input = await json(req);
      if (path.endsWith('/rpc')) {
        const result = await host.call(row, input, signal);
        signal.throwIfAborted();
        res.writeHead(200, {
          'content-type': 'application/json',
          'cache-control': 'no-store',
        });
        res.end(JSON.stringify(result));
        return;
      }
      const source = await host.open(row, input, signal);
      monitor = (async () => {
        try {
          while (!signal.aborted) {
            await delay(1000, undefined, { signal });
            await host.check(row);
          }
        } catch (error) {
          abort.abort(error);
        }
      })();
      res.writeHead(200, {
        'content-type': 'application/x-ndjson',
        'cache-control': 'no-store',
        'x-accel-buffering': 'no',
      });
      res.flushHeaders?.();
      try {
        for await (const value of source) {
          await host.check(row);
          await write(res, { type: 'item', value }, signal);
        }
        await host.check(row);
        await write(res, { type: 'end' }, signal);
      } catch (error) {
        if (!signal.aborted && !res.destroyed)
          await write(
            res,
            {
              type: 'error',
              error: ctx.typertGateway.wireStream.failure(error),
            },
            signal,
          );
      }
      res.end();
    } catch (error) {
      if (!res.headersSent && !res.destroyed) {
        res.writeHead(error.code === 'common.validation_failed' ? 400 : 403, {
          'content-type': 'application/json',
          'cache-control': 'no-store',
        });
        res.end('{"error":"dedicated.browser_unavailable"}');
      } else if (!res.destroyed) res.destroy();
    } finally {
      abort.abort();
      active.delete(abort);
      res.off('close', disconnect);
      await monitor;
    }
  };
  return {
    handler,
    close() {
      closed = true;
      for (const abort of active) abort.abort();
    },
  };
}
