// @ts-nocheck
import { readFile } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { Buffer } from 'node:buffer';
import { setTimeout as delay } from 'node:timers/promises';
import { browserBootstrap } from './bootstrap.js';
import { createNativeBrowser } from './native.js';
import { fail } from '../shared/protocol.js';

export const name = 'auriga-dsh-shared-browser';
export const inject = [
  'webServer',
  'connection',
  'remoteAccess',
  'typertGateway',
  'aurigaShared',
];
const prefix = '/auriga/shared/browser';
const maximum = 1048576;

async function jsonBody(req) {
  if (req.headers['content-type']?.split(';')[0].trim() !== 'application/json')
    fail('common.validation_failed');
  let length = 0;
  const chunks = [];
  for await (const chunk of req) {
    length += chunk.length;
    if (length > maximum) fail('common.validation_failed');
    chunks.push(chunk);
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    fail('common.validation_failed');
  }
}
async function write(res, frame, signal) {
  signal.throwIfAborted();
  const encoded = JSON.stringify(frame) + '\n';
  if (Buffer.byteLength(encoded) > 8388608) fail('common.unavailable');
  if (!res.write(encoded))
    await new Promise((resolve) => {
      const ready = () => {
        res.off('drain', ready);
        res.off('close', ready);
        resolve();
      };
      res.once('drain', ready);
      res.once('close', ready);
    });
  signal.throwIfAborted();
}
function safeScript(value) {
  return JSON.stringify(value).replaceAll('<', '\\u003c');
}

/** Uses only public rc.3 services. It does not alter Connection admission or
 * the original Gateway, and never issues a native browser owner credential. */
export function createBrowserHandler(ctx, { distIndex, recheckMs = 1000 }) {
  if (
    typeof distIndex !== 'string' ||
    !isAbsolute(distIndex) ||
    !distIndex.endsWith('index.html') ||
    !Number.isInteger(recheckMs) ||
    recheckMs < 10 ||
    recheckMs > 1000
  )
    fail('common.unavailable');
  const host = ctx.aurigaShared;
  for (const method of [
    'admitBrowser',
    'authorize',
    'refresh',
    'create',
    'messagingSessions',
    'messagingSettings',
  ])
    if (typeof host?.[method] !== 'function') fail('common.unavailable');
  const native = createNativeBrowser({
    host,
    gateway: ctx.typertGateway,
    remoteAccess: ctx.remoteAccess,
  });
  const active = new Set();
  let closed = false;
  const handler = async (req, res) => {
    const abort = new AbortController();
    const signal = abort.signal;
    const disconnected = () =>
      abort.abort(new Error('shared browser disconnected'));
    active.add(abort);
    res.once('close', disconnected);
    let peer, monitor;
    try {
      if (closed) fail('common.unavailable');
      const path = req.url;
      const index = path === '/' || path === '/index.html';
      if (
        index
          ? req.method !== 'GET'
          : req.method !== 'POST' ||
            ![prefix + '/rpc', prefix + '/stream'].includes(path)
      ) {
        res.writeHead(405, { 'cache-control': 'no-store' });
        res.end();
        return;
      }
      if (req.headers['sec-fetch-site'] === 'cross-site')
        fail('shared.revoked');
      const token = req.headers['auriga-identity'];
      if (typeof token !== 'string') fail('shared.revoked');
      // Cookie, Authorization and body cannot override this admission.
      peer = await host.admitBrowser(token, {
        method: req.method,
        canonicalPath: path,
      });
      if (peer.principal.kind === 'owner') fail('shared.revoked');
      signal.throwIfAborted();
      if (index) {
        const snapshot = await native.snapshot(peer);
        const raw = await readFile(distIndex, 'utf8');
        // The owner baseline is supplied by native remoteAccess during render;
        // the public contract requires the member snapshot after ALL rows.
        const html = ctx.webServer
          .renderIndex(raw)
          .replace(
            /<head(?:\s[^>]*)?>/i,
            (open) =>
              `${open}<base href="./"><script>${browserBootstrap}</script>`,
          )
          .replace(
            /<\/body>/i,
            `<script>globalThis.__DSH_REMOTE_ACCESS__=${safeScript(snapshot)};</script></body>`,
          );
        await host.refresh(peer);
        signal.throwIfAborted();
        res.writeHead(200, {
          'content-type': 'text/html; charset=utf-8',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(html);
        return;
      }
      const envelope = await jsonBody(req);
      if (path.endsWith('/rpc')) {
        const result = await native.call(peer, envelope, signal);
        signal.throwIfAborted();
        res.writeHead(200, {
          'content-type': 'application/json',
          'cache-control': 'no-store',
        });
        res.end(JSON.stringify(result));
        return;
      }
      const { source, check } = await native.open(peer, envelope, signal);
      // Revalidate even while the real native iterator is idle, so logout,
      // replacement or authority outage cannot leave a live stale stream.
      monitor = (async () => {
        try {
          while (!signal.aborted) {
            await delay(recheckMs, undefined, { signal });
            await check();
          }
        } catch (error) {
          if (!signal.aborted) abort.abort(error);
        }
      })();
      res.writeHead(200, {
        'content-type': 'application/x-ndjson',
        'cache-control': 'no-store',
        'x-accel-buffering': 'no',
        'x-content-type-options': 'nosniff',
      });
      res.flushHeaders?.();
      try {
        for await (const value of source) {
          await check();
          signal.throwIfAborted();
          await write(res, { type: 'item', value }, signal);
        }
        await check();
        await write(res, { type: 'end' }, signal);
      } catch (error) {
        if (!signal.aborted && !res.destroyed)
          await write(
            res,
            { type: 'error', error: native.failure(error) },
            signal,
          );
      }
      res.end();
    } catch (error) {
      if (!res.headersSent && !res.destroyed) {
        res.writeHead(error?.code === 'common.validation_failed' ? 400 : 403, {
          'content-type': 'application/json',
          'cache-control': 'no-store',
        });
        res.end(JSON.stringify({ error: 'shared.browser_unavailable' }));
      } else if (!res.destroyed) res.destroy();
    } finally {
      abort.abort();
      active.delete(abort);
      res.off('close', disconnected);
      await monitor;
      await peer?.dispose();
    }
  };
  return {
    handler,
    close() {
      closed = true;
      for (const abort of active)
        abort.abort(new Error('shared browser closed'));
    },
  };
}

export function apply(ctx, config) {
  const browser = createBrowserHandler(ctx, config ?? {});
  const disposers = [];
  try {
    for (const path of ['/', '/index.html'])
      disposers.push(
        ctx.webServer.register({
          kind: 'exact',
          path,
          handler: browser.handler,
        }),
      );
    disposers.push(
      ctx.webServer.register({
        kind: 'prefix',
        path: prefix,
        handler: browser.handler,
      }),
    );
    // The Edge profile also blocks native WS upgrades and /connect. This tap
    // denies every old owner-cookie HTTP request before the original bridge.
    disposers.push(
      ctx.on('connection/request', (_req, res) => {
        res.writeHead(403, { 'cache-control': 'no-store' });
        res.end();
      }),
    );
  } catch (error) {
    for (const detach of disposers.reverse()) detach();
    browser.close();
    throw error;
  }
  ctx.effect(
    () => () => {
      browser.close();
      for (const detach of disposers.reverse()) detach();
    },
    'auriga-shared: native browser',
  );
}
