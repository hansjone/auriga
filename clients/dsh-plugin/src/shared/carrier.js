// @ts-nocheck
import { Buffer } from 'node:buffer';
import { fail, SHARED_RISK } from './protocol.js';

/** Self-owned JSON carrier. It deliberately does not replace native browser/WS identity. */
export function createSharedCarrier({ host, runtime }) {
  return async (req, res) => {
    let peer;
    try {
      const canonicalPath = (req.url ?? '').split('?')[0];
      if (
        req.method !== 'POST' ||
        ![
          '/auriga/shared/session',
          '/auriga/shared/invoke',
          '/auriga/shared/resources',
          '/auriga/shared/status',
        ].includes(canonicalPath)
      )
        fail('common.validation_failed');
      peer = await host.admit(req.headers['auriga-identity'], {
        method: req.method,
        canonicalPath,
      });
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 1048576) fail('common.validation_failed');
        chunks.push(chunk);
      }
      let body;
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}');
      } catch {
        fail('common.validation_failed');
      }
      if (!body || typeof body !== 'object' || Array.isArray(body))
        fail('common.validation_failed');
      let result;
      if (canonicalPath.endsWith('/session'))
        result = await host.create(peer, body);
      else if (canonicalPath.endsWith('/invoke')) {
        if (
          Object.keys(body).some(
            (k) => !['action', 'sessionId', 'args'].includes(k),
          )
        )
          fail('common.validation_failed');
        result = await host.invoke(
          peer,
          body.action,
          body.sessionId,
          body.args,
        );
      } else if (canonicalPath.endsWith('/resources')) {
        if (Object.keys(body).some((k) => k !== 'sessionId'))
          fail('common.validation_failed');
        await runtime.authorize(peer, 'session/page', body.sessionId);
        result = await runtime.resources(body.sessionId);
      } else {
        if (Object.keys(body).length) fail('common.validation_failed');
        result = {
          risk: SHARED_RISK,
          settings: { source: 'platform', editable: false },
        };
      }
      res.writeHead(200, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
      });
      res.end(JSON.stringify(result));
    } catch (e) {
      const code = [
        'common.validation_failed',
        'shared.signature_invalid',
        'shared.expired',
        'shared.binding_mismatch',
        'shared.revoked',
        'common.unavailable',
      ].includes(e.code)
        ? e.code
        : 'common.unavailable';
      const status = {
        'common.validation_failed': 400,
        'shared.signature_invalid': 401,
        'shared.expired': 401,
        'shared.binding_mismatch': 403,
        'shared.revoked': 403,
        'common.unavailable': 503,
      }[code];
      res.writeHead(status, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
      });
      res.end(JSON.stringify({ code }));
    } finally {
      await peer?.dispose?.();
    }
  };
}
