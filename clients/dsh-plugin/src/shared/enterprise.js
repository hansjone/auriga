// @ts-nocheck
import { Buffer } from 'node:buffer';
import { fail } from './protocol.js';

const requestHeaders = new Set([
  'content-type',
  'accept',
  'accept-language',
  'x-lang-id',
  'x-requested-with',
]);
const responseHeaders = new Set([
  'content-type',
  'content-length',
  'content-language',
]);
const loopback = (ip) =>
  ip === '127.0.0.1' || ip === '::1' || ip === '::ffff:127.0.0.1';
function alias(...values) {
  const present = values.filter((v) => v !== undefined);
  if (
    !present.length ||
    present.some((v) => typeof v !== 'string' || !v || v !== present[0])
  )
    fail('common.validation_failed');
  return present[0];
}
/** IAM and outbound ports are server-owned. No raw materials leave credentials().
 * outbound MUST disable redirects and receive an already validated target URL.
 */
export function createEnterpriseAdapter({ runtime, ports, config }) {
  const settings = () => {
    const value = config();
    if (
      value.allowRawAgentToken !== undefined &&
      typeof value.allowRawAgentToken !== 'boolean'
    )
      fail('common.unavailable');
    if (
      value.outboundAllowedHosts !== undefined &&
      (!Array.isArray(value.outboundAllowedHosts) ||
        value.outboundAllowedHosts.some(
          (h) => typeof h !== 'string' || !h || h.includes('*'),
        ))
    )
      fail('common.unavailable');
    return value;
  };
  async function readMaterial(c) {
    try {
      const material = await ports.credentials(c.binding, c.claims);
      if (
        typeof material?.empNo !== 'string' ||
        !material.empNo ||
        typeof material.token !== 'string' ||
        !material.token ||
        !Number.isSafeInteger(material.credentialVersion)
      )
        fail();
      return material;
    } catch {
      fail('common.unavailable');
    }
  }
  async function authorize(call, scope) {
    if (!loopback(call.remoteAddress)) fail('local_only');
    const sessionId = alias(
      call.body?.sessionId,
      call.body?.dshSessionId,
      call.headers?.['x-dsh-session-id'],
    );
    const token = alias(
      call.body?.taskCapability,
      call.body?.capability,
      call.headers?.['x-dsh-team-task-capability'],
    );
    let taskId;
    try {
      taskId = await ports.taskId(token);
    } catch {
      fail('shared.revoked');
    }
    if (scope === 'credentials' && settings().allowRawAgentToken === false)
      fail('raw_token_disabled');
    return {
      sessionId,
      token,
      taskId,
      ...(await runtime.capability(token, sessionId, taskId, scope)),
    };
  }
  async function recheck(c, scope) {
    const next = await authorize(
      {
        remoteAddress: '127.0.0.1',
        body: { sessionId: c.sessionId, taskCapability: c.token },
      },
      scope,
    );
    if (next.claims.credentialVersion !== c.claims.credentialVersion) fail();
  }
  async function credentials(call) {
    const c = await authorize(call, 'credentials');
    const material = await readMaterial(c);
    await recheck(c, 'credentials');
    if (material.credentialVersion !== c.claims.credentialVersion) fail();
    return {
      empNo: material.empNo,
      token: material.token,
      updatedAt: material.updatedAt ?? null,
    };
  }
  async function outbound(call) {
    const c = await authorize(call, 'outbound');
    const body = call.body ?? {},
      cfg = settings();
    let url;
    try {
      url = new URL(body.url);
    } catch {
      fail('common.validation_failed');
    }
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.hash ||
      !cfg.outboundAllowedHosts?.includes(url.hostname)
    )
      fail('host_not_allowed');
    const method = body.method ?? 'POST',
      timeoutMs = body.timeoutMs ?? 30000;
    if (
      !['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].includes(method) ||
      !Number.isSafeInteger(timeoutMs) ||
      timeoutMs < 1 ||
      timeoutMs > 60000
    )
      fail('common.validation_failed');
    const headers = {};
    for (const [k, v] of Object.entries(body.headers ?? {})) {
      if (typeof v !== 'string' || /[\r\n]/.test(v))
        fail('common.validation_failed');
      if (requestHeaders.has(k.toLowerCase())) headers[k.toLowerCase()] = v;
    }
    // Private IAM adapter injects enterprise headers; browser values cannot override them.
    const material = await readMaterial(c);
    await recheck(c, 'outbound');
    if (material.credentialVersion !== c.claims.credentialVersion) fail();
    let result;
    try {
      result = await ports.outbound(
        {
          url: url.href,
          method,
          headers,
          body: body.body,
          timeoutMs,
          redirect: 'error',
        },
        material,
        c.binding,
      );
    } catch {
      fail('outbound_failed');
    }
    await recheck(c, 'outbound');
    // Never forward authentication response fields or unfiltered transport errors.
    const safeHeaders = Object.fromEntries(
      Object.entries(result.headers ?? {}).filter(([k]) =>
        responseHeaders.has(k.toLowerCase()),
      ),
    );
    const secrets = [
      material.token,
      ...Object.values(material.cookies ?? {}),
    ].filter((v) => typeof v === 'string' && v);
    const redact = (value) => {
      if (typeof value === 'string')
        return secrets.reduce((s, k) => s.split(k).join('[REDACTED]'), value);
      if (Array.isArray(value)) return value.map(redact);
      if (value && typeof value === 'object')
        return Object.fromEntries(
          Object.entries(value)
            .filter(([k]) => !/token|cookie|authorization|empNo/i.test(k))
            .map(([k, v]) => [k, redact(v)]),
        );
      return value;
    };
    return redact({
      statusCode: result.statusCode,
      headers: safeHeaders,
      body: result.body,
      json: result.json ?? null,
    });
  }
  async function handler(req, res) {
    const path = (req.url ?? '').split('?')[0];
    if (!['/dsh-team/agent-credentials', '/dsh-team/outbound'].includes(path)) {
      res.writeHead(404);
      res.end();
      return;
    }
    const raw = path.endsWith('/agent-credentials');
    try {
      if (!loopback(req.socket.remoteAddress)) fail('local_only');
      if (req.method !== 'POST' && !(raw && req.method === 'GET'))
        fail('common.validation_failed');
      let body = {};
      if (req.method === 'POST') {
        const chunks = [];
        let size = 0;
        for await (const chunk of req) {
          size += chunk.length;
          if (size > 1048576) fail('common.validation_failed');
          chunks.push(chunk);
        }
        try {
          body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          fail('common.validation_failed');
        }
        if (!body || typeof body !== 'object' || Array.isArray(body))
          fail('common.validation_failed');
      }
      const call = {
        remoteAddress: req.socket.remoteAddress,
        headers: req.headers,
        body,
      };
      const result = await (raw ? credentials(call) : outbound(call));
      res.writeHead(raw ? 200 : result.statusCode, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        ...(!raw
          ? { 'X-Dsh-Team-Outbound-Status': String(result.statusCode) }
          : {}),
      });
      res.end(JSON.stringify(result));
    } catch (e) {
      const code = [
        'local_only',
        'raw_token_disabled',
        'host_not_allowed',
        'common.validation_failed',
        'common.unavailable',
        'outbound_failed',
      ].includes(e.code)
        ? e.code
        : 'unauthorized';
      const status = {
        local_only: 403,
        raw_token_disabled: 403,
        host_not_allowed: 403,
        'common.validation_failed': 400,
        'common.unavailable': 503,
        outbound_failed: 502,
        unauthorized: 401,
      }[code];
      res.writeHead(status, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
      });
      res.end(JSON.stringify({ error: code, message: code }));
    }
  }
  return { credentials, outbound, handler };
}
