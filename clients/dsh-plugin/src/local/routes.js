import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { createConsentRoutes } from './consent-routes.js';
import { createMessagingRoutes } from './messaging-routes.js';
import { PlatformError } from './platform.js';
import { localFailureDiagnostic } from './diagnostic.js';
import { logEvent } from '../observability/index.js';
const BASE = '/auriga/local-login';
const assets = new Map([
  ['adapter.js', new URL('../login/local-adapter.js', import.meta.url)],
  ['index.js', new URL('../login/index.js', import.meta.url)],
  ['styles.js', new URL('../login/styles.js', import.meta.url)],
  ['errors.js', new URL('../login/errors.js', import.meta.url)],
  [
    'local-controller.js',
    new URL('../login/local-controller.js', import.meta.url),
  ],
  ['logout.js', new URL('../login/logout.js', import.meta.url)],
  ['page.js', new URL('../login/page.js', import.meta.url)],
]);
/** Unconfigured standalone mode exposes only the fixed page and assets.
 * Native launch authentication is still required; no local identity is minted.
 * @param {{requestRejection(req:import('node:http').IncomingMessage):number|undefined}} connection */
export function createUnconfiguredLoginRoutes(connection) {
  return createConnectRoutes({
    runtime: null,
    admission: {
      nativeAuth: connection,
      sameOrigin: (/** @type {import('node:http').IncomingMessage} */ req) =>
        req.method === 'GET',
    },
  });
}
/** @param {{runtime:any,admission:any,diagnostic?:(value:object)=>void}} options */
export function createConnectRoutes({
  runtime,
  admission,
  diagnostic = (value) => {
    logEvent('WARN', 'Local login failed', { ...value });
    console.warn('[auriga-dsh] local route failure ' + JSON.stringify(value));
  },
}) {
  const consent = createConsentRoutes(runtime);
  const messaging = createMessagingRoutes(runtime);
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
    let operation = 'request';
    if (!runtime && req.method === 'GET' && path === BASE + '/status')
      return json(res, 200, {
        available: true,
        configured: false,
        loggedIn: false,
        connected: false,
        account: null,
        network: { connected: false, state: 'unconfigured' },
      });
    if (!runtime && path !== BASE && !path.startsWith(BASE + '/assets/'))
      return json(res, 404, { code: 'common.not_found' });
    if (path.startsWith(BASE + '/consent/')) return consent(req, res);
    if (path.startsWith(BASE + '/messaging/')) return messaging(req, res);
    try {
      if (req.method === 'GET' && path === BASE + '/status') {
        operation = 'status';
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
        operation = path === BASE + '/logout' ? 'logout' : input.action;
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
    } catch (error) {
      if (
        error instanceof Error &&
        error.message === 'unsupported native login step'
      )
        return json(res, 501, { code: 'common.unavailable' });
      if (
        error instanceof Error &&
        ['login attempt expired', 'invalid device credential'].includes(
          error.message,
        )
      )
        return json(res, 401, { code: 'common.unauthenticated' });
      if (error instanceof PlatformError) {
        if (error.status === 400 && error.code === 'common.validation_failed')
          return json(res, 400, { code: error.code });
        if (['invalid_grant', 'invalid_token'].includes(error.code ?? ''))
          return json(res, 401, { code: 'common.unauthenticated' });
        if (
          [
            'iam.login_tx_expired',
            'iam.invalid_credentials',
            'common.rate_limited',
            'common.unauthenticated',
          ].includes(error.code ?? '')
        )
          return json(res, error.status, { code: error.code });
      }
      try {
        diagnostic({ operation, ...localFailureDiagnostic(error) });
      } catch {
        // Diagnostics must never change the existing failure-closed response.
      }
      json(res, 503, { code: 'common.unavailable' });
    }
  };
}
