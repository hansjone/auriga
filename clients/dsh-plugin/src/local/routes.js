import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { createConsentRoutes } from './consent-routes.js';
const BASE = '/auriga/local-login';
const assets = new Map([
  ['adapter.js', new URL('../login/local-adapter.js', import.meta.url)],
  ['index.js', new URL('../login/index.js', import.meta.url)],
  ['styles.js', new URL('../login/styles.js', import.meta.url)],
]);
/** @param {{runtime:any,admission:any}} options */
export function createConnectRoutes({ runtime, admission }) {
  const consent = createConsentRoutes(runtime);
  /** @param {import('node:http').ServerResponse} res @param {number} status @param {unknown} body @param {string|string[]} [cookies] */
  const json = (res, status, body, cookies) => {
    res.writeHead(status, {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
      'x-content-type-options': 'nosniff',
      ...(cookies ? { 'set-cookie': cookies } : {}),
    });
    res.end(JSON.stringify(body));
  };
  return async (
    /** @type {import('node:http').IncomingMessage} */ req,
    /** @type {import('node:http').ServerResponse} */ res,
  ) => {
    const rejected = admission.nativeAuth.requestRejection(req);
    if (rejected !== undefined) {
      res.writeHead(rejected);
      res.end();
      return;
    }
    const write = req.method === 'POST';
    if (!admission.sameOrigin(req, write)) {
      res.writeHead(403);
      res.end();
      return;
    }
    const path = (req.url ?? '').split('?')[0];
    if (path.startsWith(BASE + '/consent/')) return consent(req, res);
    try {
      if (req.method === 'GET' && path === BASE + '/status') {
        const result = runtime.prepare(req);
        return json(res, 200, result.body, result.setCookie);
      }
      if (write && (path === BASE + '/login' || path === BASE + '/logout')) {
        if (
          req.headers['content-type']?.split(';')[0].trim() !==
          'application/json'
        )
          return json(res, 415, { code: 'common.validation_failed' });
        if (!runtime.checkCsrf(req))
          return json(res, 403, { code: 'common.forbidden' });
        let length = 0;
        const parts = [];
        for await (const part of req) {
          length += part.length;
          if (length > 16384)
            return json(res, 413, { code: 'common.validation_failed' });
          parts.push(part);
        }
        let input;
        try {
          input = JSON.parse(Buffer.concat(parts).toString('utf8'));
        } catch {
          return json(res, 400, { code: 'common.validation_failed' });
        }
        if (
          !input ||
          typeof input !== 'object' ||
          Array.isArray(input) ||
          Object.keys(input).some((k) => !['action', 'input'].includes(k))
        )
          return json(res, 400, { code: 'common.validation_failed' });
        if (path === BASE + '/login') {
          const keys = Object.keys(input).sort().join(',');
          if (
            !['begin', 'advance', 'resume'].includes(input.action) ||
            (input.action === 'advance'
              ? keys !== 'action,input'
              : keys !== 'action')
          )
            return json(res, 400, { code: 'common.validation_failed' });
          if (input.action === 'advance') {
            const form = input.input;
            if (
              !form ||
              typeof form !== 'object' ||
              Object.keys(form).sort().join(',') !== 'formId,type,values' ||
              form.type !== 'form' ||
              typeof form.formId !== 'string' ||
              !form.formId ||
              form.formId.length > 128 ||
              !form.values ||
              typeof form.values !== 'object' ||
              Array.isArray(form.values) ||
              Object.values(form.values).some(
                (v) => !['string', 'boolean'].includes(typeof v),
              )
            )
              return json(res, 400, { code: 'common.validation_failed' });
          }
        } else if (Object.keys(input).length !== 0)
          return json(res, 400, { code: 'common.validation_failed' });
        const onClose = () => {
          if (!res.writableEnded)
            void runtime.cancelAttempt(req).catch(() => {});
        };
        if (path === BASE + '/login') res.once('close', onClose);
        let result;
        try {
          result =
            path === BASE + '/logout'
              ? await runtime.logout(req)
              : await runtime.login(req, input);
        } finally {
          res.off('close', onClose);
        }
        return json(
          res,
          200,
          result.body ?? { step: result.step },
          result.setCookie,
        );
      }
      if (
        req.method === 'GET' &&
        (path === BASE || path.startsWith(BASE + '/assets/'))
      ) {
        const file = assets.get(path.slice((BASE + '/assets/').length));
        if (path !== BASE && !file) {
          res.writeHead(404);
          res.end();
          return;
        }
        res.writeHead(200, {
          'content-type': file
            ? 'text/javascript; charset=utf-8'
            : 'text/html; charset=utf-8',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
          'content-security-policy':
            "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'",
        });
        res.end(
          file
            ? await readFile(file)
            : '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Auriga</title><body><main id="auriga-login"></main><script type="module" src="/auriga/local-login/assets/adapter.js"></script></body></html>',
        );
        return;
      }
      res.writeHead(404);
      res.end();
    } catch {
      json(res, 503, { code: 'common.unavailable' });
    }
  };
}
