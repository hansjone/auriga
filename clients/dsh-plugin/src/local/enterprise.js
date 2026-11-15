import { randomBytes } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { request as httpsRequest } from 'node:https';
import { lookup } from 'node:dns/promises';

export const MAX_ENTERPRISE_BODY = 2 * 1024 * 1024;

const requestHeaders = new Set([
  'content-type',
  'accept',
  'accept-language',
  'x-lang-id',
  'x-requested-with',
]);
const localErrors = new Set([
  'caller_unauthenticated',
  'raw_token_disabled',
  'invalid_json',
  'loopback_only',
  'method_not_allowed',
  'no_skill_credentials',
  'invalid_url',
  'host_not_allowed',
  'not_ready',
]);
const loopback = (/** @type {string} */ address) =>
  ['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(address);
const binding = (/** @type {any} */ s) =>
  s?.state === 'authenticated'
    ? JSON.stringify([
        s.shellEpoch,
        s.identity?.tenantId,
        s.identity?.memberId,
        s.identity?.deviceId,
        s.identity?.providerId,
        s.identity?.uacSessionBinding?.sourceSessionId,
        s.identity?.uacSessionBinding?.authGeneration,
      ])
    : null;
const json = (
  /** @type {any} */ res,
  /** @type {number} */ status,
  /** @type {any} */ body,
  headers = {},
) => {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
    ...headers,
  });
  res.end(JSON.stringify(body));
};
const failure = (/** @type {number} */ status, /** @type {string} */ error) =>
  Object.assign(new Error(error), { status, code: error });

/** The shared enterprise transport sends one HTTPS request to a pinned address.
 * Callers supply their admission check. URL authority owns Host, SNI and
 * certificate identity. No pooling or redirect can change the pinned target.
 * @param {URL} url @param {{address:string,family:number}} address
 * @param {{method:string,headers:import('node:http').OutgoingHttpHeaders,body?:string|Buffer,signal:AbortSignal,check:()=>void}} options */
export function requestPinnedEnterpriseHttps(url, address, options) {
  if (
    url.protocol !== 'https:' ||
    url.username ||
    url.password ||
    url.hash ||
    Object.keys(options.headers).some((key) => key.toLowerCase() === 'host')
  )
    throw new Error('invalid enterprise HTTPS target');
  options.signal.throwIfAborted();
  options.check();
  /** @type {Promise<{statusCode:number|undefined,headers:import('node:http').IncomingHttpHeaders,body:Buffer}>} */
  return new Promise((resolve, reject) => {
    const req = httpsRequest(
      url,
      {
        method: options.method,
        headers: options.headers,
        agent: false,
        rejectUnauthorized: true,
        lookup: (_hostname, lookupOptions, callback) => {
          if (/** @type {any} */ (lookupOptions).all)
            callback(null, [
              { address: address.address, family: address.family },
            ]);
          else callback(null, address.address, address.family);
        },
        signal: options.signal,
      },
      (res) => {
        const chunks = /** @type {Buffer[]} */ ([]);
        let size = 0;
        res.on('data', (chunk) => {
          size += chunk.length;
          if (size > MAX_ENTERPRISE_BODY)
            res.destroy(new Error('response limit'));
          else chunks.push(chunk);
        });
        res.on('error', reject);
        res.on('end', () =>
          resolve({
            statusCode: res.statusCode,
            headers: res.headers,
            body: Buffer.concat(chunks),
          }),
        );
      },
    );
    req.on('error', reject);
    req.end(options.body);
  });
}

/** Resolve once after the skill's private host/scope checks, then pin HTTPS.
 * @param {URL} url @param {any} options */
export async function enterpriseRequest(url, options) {
  options.signal.throwIfAborted();
  /** @type {Awaited<ReturnType<typeof lookup>>} */
  const address = await new Promise((resolve, reject) => {
    const abort = () => reject(new Error('request cancelled'));
    options.signal.addEventListener('abort', abort, { once: true });
    lookup(url.hostname, { family: 0 })
      .then(resolve, reject)
      .finally(() => options.signal.removeEventListener('abort', abort));
  });
  const response = await requestPinnedEnterpriseHttps(url, address, options);
  const body = response.body.toString('utf8');
  let value = null;
  try {
    value = JSON.parse(body);
  } catch {
    /* Non-JSON is a valid upstream body. */
  }
  return { ...response, body, json: value };
}

/** Host-only port. snapshot() must already enforce the shell snapshot deadline.
 * getOwner reads the host's trusted session ownership, not HTTP/model input.
 * credentials reads current IAM material through the authenticated broker.
 * @param {{snapshot:()=>any,getOwner:(id:string)=>string|null,
 * credentials:(sessionId:string,purpose:'raw'|'outbound',executionContextId?:string,execution?:any,retained?:boolean)=>Promise<any>,onRevoke?:(id:string)=>void,config?:()=>any,send?:typeof enterpriseRequest,
 * now?:()=>number,monotonic?:()=>number,binding?:()=>string|null,ownerProof?:(id:string)=>()=>string|null}} options */
export function createEnterpriseAuth(options) {
  const config = options.config ?? (() => ({}));
  const raw = () => config().allowRawAgentToken !== false;
  const now = options.now ?? Date.now;
  const monotonic = options.monotonic ?? (() => performance.now());
  const capabilities = new Map();
  let closed = false;
  function revoke(/** @type {string} */ token) {
    const cap = capabilities.get(token);
    capabilities.delete(token);
    if (cap?.executionContextId) options.onRevoke?.(cap.executionContextId);
  }
  function mint(
    /** @type {string} */ sessionId,
    /** @type {string|undefined} */ executionContextId = undefined,
    /** @type {any} */ execution = undefined,
  ) {
    if (closed) return null;
    const snapshot = options.snapshot(),
      key = options.binding ? options.binding() : binding(snapshot);
    if (
      snapshot?.state !== 'authenticated' ||
      !key ||
      options.getOwner(sessionId) !== snapshot.identity.memberId
    )
      return null;
    for (const [token, cap] of capabilities)
      if (cap.until <= now() || cap.deadline <= monotonic()) revoke(token);
    if (capabilities.size >= 4096) return null;
    const token = randomBytes(32).toString('base64url');
    capabilities.set(token, {
      sessionId,
      executionContextId,
      execution,
      retained: false,
      owner:
        options.ownerProof?.(sessionId) ?? (() => options.getOwner(sessionId)),
      memberId: snapshot.identity.memberId,
      key,
      audience: 'dsh-team-agent',
      scopes: raw() ? ['outbound', 'credentials'] : ['outbound'],
      until: now() + 600000,
      deadline: monotonic() + 600000,
    });
    return token;
  }
  function aliases(/** @type {any[]} */ values) {
    const present = values.filter((value) => value !== undefined);
    if (
      !present.length ||
      present.some((v) => typeof v !== 'string' || !v || v !== present[0])
    )
      throw failure(401, 'caller_unauthenticated');
    return present[0];
  }
  function authorize(
    /** @type {any} */ req,
    /** @type {any} */ body,
    /** @type {string} */ scope,
  ) {
    const sessionId = aliases([
      req.headers['x-dsh-session-id'],
      body.sessionId,
      body.dshSessionId,
    ]);
    const token = aliases([
      req.headers['x-dsh-team-task-capability'],
      body.taskCapability,
      body.capability,
    ]);
    const cap = capabilities.get(token);
    const check = () => {
      const s = options.snapshot();
      if (
        closed ||
        !cap ||
        capabilities.get(token) !== cap ||
        cap.until <= now() ||
        cap.deadline <= monotonic() ||
        cap.audience !== 'dsh-team-agent' ||
        cap.sessionId !== sessionId ||
        !cap.scopes.includes(scope) ||
        cap.key !== (options.binding ? options.binding() : binding(s)) ||
        cap.owner() !== cap.memberId
      )
        throw failure(401, 'caller_unauthenticated');
      if (scope === 'credentials' && !raw())
        throw failure(403, 'raw_token_disabled');
      if (s?.state !== 'authenticated')
        throw failure(503, 'no_skill_credentials');
    };
    check();
    return {
      check,
      sessionId,
      cap,
      executionContextId: cap.executionContextId,
    };
  }
  async function body(/** @type {any} */ req) {
    if (req.method === 'GET') return {};
    let length = 0;
    const chunks = /** @type {Buffer[]} */ ([]);
    for await (const chunk of req) {
      length += chunk.length;
      if (length > 2097152) throw failure(400, 'invalid_json');
      chunks.push(chunk);
    }
    try {
      const value = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      if (!value || typeof value !== 'object' || Array.isArray(value))
        throw new Error();
      return value;
    } catch {
      throw failure(400, 'invalid_json');
    }
  }
  return {
    mint,
    retain(/** @type {string} */ token) {
      const cap = capabilities.get(token);
      if (cap) cap.retained = true;
    },
    revoke,
    revokeAll() {
      capabilities.clear();
    },
    close() {
      closed = true;
      capabilities.clear();
    },
    async handle(/** @type {any} */ req, /** @type {any} */ res) {
      const path = (req.url ?? '').split('?')[0];
      const credentials = path === '/dsh-team/agent-credentials';
      if (!credentials && path !== '/dsh-team/outbound')
        return json(res, 404, { error: 'not_found', message: 'Not found' });
      try {
        if (!loopback(req.socket?.remoteAddress))
          throw failure(403, 'loopback_only');
        if (credentials && !raw()) throw failure(403, 'raw_token_disabled');
        if (!(credentials ? ['GET', 'POST'] : ['POST']).includes(req.method))
          throw failure(405, 'method_not_allowed');
        const input = await body(req);
        const { check, sessionId, executionContextId, cap } = authorize(
          req,
          input,
          credentials ? 'credentials' : 'outbound',
        );
        let material;
        try {
          material = await options.credentials(
            sessionId,
            credentials ? 'raw' : 'outbound',
            executionContextId,
            cap.execution,
            cap.retained,
          );
          if (material?.executionContextId)
            cap.executionContextId = material.executionContextId;
        } catch {
          check();
          throw failure(503, 'no_skill_credentials');
        }
        check();
        if (
          typeof material?.empNo !== 'string' ||
          !material.empNo ||
          material.empNo.length > 128 ||
          typeof material?.token !== 'string' ||
          !material.token ||
          material.token.length > 16384 ||
          (material.updatedAt != null &&
            !Number.isFinite(Date.parse(material.updatedAt)))
        )
          throw failure(503, 'no_skill_credentials');
        if (credentials)
          return json(res, 200, {
            empNo: material.empNo,
            token: material.token,
            updatedAt: material.updatedAt ?? null,
          });
        let url;
        try {
          url = new URL(input.url);
        } catch {
          throw failure(400, 'invalid_url');
        }
        if (
          url.protocol !== 'https:' ||
          url.username ||
          url.password ||
          url.hash
        )
          throw failure(400, 'invalid_url');
        if (
          !(config().outboundAllowedHosts ?? []).some(
            (/** @type {string} */ host) =>
              (host === url.hostname && !url.port) ||
              host === `${url.hostname}:${url.port || '443'}`,
          )
        )
          throw failure(403, 'host_not_allowed');
        const method = String(input.method || 'POST').toUpperCase();
        if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].includes(method))
          throw failure(400, 'method_not_allowed');
        const headers = /** @type {Record<string,string>} */ ({});
        for (const [key, value] of Object.entries(input.headers ?? {}))
          if (requestHeaders.has(key.toLowerCase()) && value != null)
            headers[key.toLowerCase()] = String(value).slice(0, 1024);
        // Private enterprise mapping is mandatory: no company field names in public code.
        const mapping = config().identityHeaders;
        if (
          !mapping?.empNo ||
          !mapping?.token ||
          mapping.empNo.toLowerCase() === mapping.token.toLowerCase()
        )
          throw failure(503, 'not_ready');
        headers[mapping.empNo] = material.empNo;
        headers[mapping.token] = material.token;
        if (input.body != null && !headers['content-type'])
          headers['content-type'] = 'application/json;charset=UTF-8';
        const controller = new AbortController();
        const timeout = Math.min(Number(input.timeoutMs) || 30000, 60000);
        const timer = setTimeout(
          () => controller.abort(),
          Math.max(1, timeout),
        );
        const poll = setInterval(() => {
          try {
            check();
          } catch {
            controller.abort();
          }
        }, 100);
        try {
          check();
          const result = await (options.send ?? enterpriseRequest)(url, {
            method,
            headers,
            check,
            signal: controller.signal,
            timeoutMs: timeout,
            body:
              input.body == null
                ? undefined
                : typeof input.body === 'string'
                  ? input.body
                  : JSON.stringify(input.body),
          });
          check();
          const safe = /** @type {Record<string,string>} */ ({});
          for (const [key, value] of Object.entries(result.headers ?? {}))
            if (
              !/token|cookie|auth/i.test(key) &&
              !Object.values(mapping).some(
                (v) => String(v).toLowerCase() === key.toLowerCase(),
              )
            )
              safe[key] = String(value);
          return json(
            res,
            result.statusCode,
            { ...result, headers: safe },
            { 'x-dsh-team-outbound-status': String(result.statusCode) },
          );
        } finally {
          clearTimeout(timer);
          clearInterval(poll);
        }
      } catch (error) {
        const e = /** @type {any} */ (error);
        const known =
          [400, 401, 403, 405, 503].includes(e?.status) &&
          localErrors.has(e?.code);
        return json(res, known ? e.status : 502, {
          error: known ? e.code : 'upstream_failed',
          message: 'Enterprise request unavailable',
        });
      }
    },
  };
}
