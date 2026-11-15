import { createPublicKey, verify } from 'node:crypto';
import { isIP } from 'node:net';
import { requireValid } from './schema.mjs';

export class PlatformError extends Error {
  constructor(status, code) {
    super('platform request failed');
    this.status = status;
    this.code = code;
  }
}
/** HTTP only for the installer's explicitly confirmed IP binding (ADR 0022): a
 * private or loopback literal IP, no path, and the same rule as the installer. */
function confirmedIpOrigin(input) {
  if (
    input.accessMode !== 'ip' ||
    typeof input.platformOrigin !== 'string' ||
    input.confirmedOrigin !== input.platformOrigin
  )
    return false;
  // Validate the original literal before WHATWG URL rewrites decimal/octal IPs.
  const host = input.platformOrigin.match(
    /^http:\/\/(\[[^\]]+\]|[^/:?#@]+)(?::\d+)?$/i,
  )?.[1];
  const literal = host?.replace(/^\[|\]$/g, '');
  const kind = literal ? isIP(literal) : 0;
  const octets = kind === 4 ? literal.split('.').map(Number) : [];
  const local =
    kind === 4
      ? octets[0] === 127 ||
        octets[0] === 10 ||
        (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
        (octets[0] === 192 && octets[1] === 168)
      : kind === 6 && (literal === '::1' || /^f[cd][\da-f]{2}:/i.test(literal));
  return local && new URL(input.platformOrigin).origin === input.platformOrigin;
}

export function trustedConfig(input) {
  const url = new URL(input.platformOrigin);
  if (
    (url.protocol !== 'https:' &&
      !(url.protocol === 'http:' && confirmedIpOrigin(input))) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/'
  )
    throw new Error('trusted HTTPS platform origin required');
  const issuer = new URL(input.issuer ?? url.origin);
  if (
    issuer.origin !== url.origin ||
    issuer.search ||
    issuer.hash ||
    issuer.username ||
    issuer.password
  )
    throw new Error('invalid issuer');
  if (!/^[\w.-]{1,128}$/.test(input.clientId ?? 'auriga-dsh'))
    throw new Error('invalid client id');
  if (
    !/^[\w.-]{1,128}$/.test(input.nativeClientId ?? 'auriga-dsh-native') ||
    (input.nativeClientId ?? 'auriga-dsh-native') ===
      (input.clientId ?? 'auriga-dsh')
  )
    throw new Error('distinct native client id required');
  return Object.freeze({
    platformOrigin: url.origin,
    issuer: issuer.href.replace(/\/$/, ''),
    clientId: input.clientId ?? 'auriga-dsh',
    nativeClientId: input.nativeClientId ?? 'auriga-dsh-native',
    deviceName: 'Auriga Desktop',
    ...(input.network ? { network: structuredClone(input.network) } : {}),
  });
}
export async function boundedBody(response, limit = 2097152) {
  const chunks = [];
  let length = 0;
  if (response.body) {
    const reader = response.body.getReader();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        length += value.length;
        if (length > limit) {
          await reader.cancel();
          throw new Error('response limit');
        }
        chunks.push(value);
      }
    } finally {
      reader.releaseLock();
    }
  }
  return Buffer.concat(chunks);
}
/** Only installation-owned routes. Callers never receive OAuth responses. */
export function createPlatform(config, fetcher = globalThis.fetch) {
  const issuerPath = new URL(config.issuer).pathname.replace(/\/$/, '');
  /** @param {string} path @param {{form?:boolean,token?:string,method?:string,body?:object,signal?:AbortSignal,headers?:object}} options */
  async function request(
    path,
    {
      form,
      token,
      method = form ? 'POST' : 'GET',
      body,
      signal,
      headers = {},
      cookieJar,
    } = {},
  ) {
    const url = new URL(path, config.platformOrigin);
    if (
      url.origin !== config.platformOrigin ||
      !path.startsWith('/') ||
      path.startsWith('//') ||
      path.includes('\\')
    )
      throw new Error('invalid route');
    const response = await fetcher(url.href, {
      method,
      redirect: 'error',
      credentials: 'omit',
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(15000)])
        : AbortSignal.timeout(15000),
      headers: {
        ...headers,
        ...(cookieJar
          ? {
              Origin: config.platformOrigin,
              'Sec-Fetch-Site': 'same-origin',
              'Sec-Fetch-Mode': 'cors',
              'Sec-Fetch-Dest': 'empty',
              ...(cookieJar.size
                ? {
                    Cookie: [...cookieJar]
                      .map(([k, v]) => k + '=' + v)
                      .join('; '),
                  }
                : {}),
            }
          : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(body !== undefined
          ? {
              'Content-Type': form
                ? 'application/x-www-form-urlencoded'
                : 'application/json',
            }
          : {}),
      },
      ...(body !== undefined
        ? {
            body: form
              ? new URLSearchParams(body).toString()
              : JSON.stringify(body),
          }
        : {}),
    });
    if (cookieJar)
      for (const cookie of response.headers.getSetCookie()) {
        const pair = cookie.split(';')[0];
        const at = pair.indexOf('=');
        const name = pair.slice(0, at),
          value = pair.slice(at + 1);
        if (
          /^(?:__Host-)?auriga_portal_login_tx$/.test(name) &&
          value.length <= 4096
        )
          value ? cookieJar.set(name, value) : cookieJar.delete(name);
      }
    const raw = await boundedBody(response);
    const rawCorrelationId = response.headers.get('x-auriga-correlation-id');
    const correlationId = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(
      rawCorrelationId ?? '',
    )
      ? rawCorrelationId
      : undefined;
    let data;
    try {
      data = raw.length ? JSON.parse(raw.toString('utf8')) : {};
    } catch {
      throw Object.assign(
        new PlatformError(response.status, 'common.unavailable'),
        { correlationId },
      );
    }
    if (!response.ok)
      throw Object.assign(
        new PlatformError(
          response.status,
          typeof data.error === 'string' ? data.error : data.code,
        ),
        {
          correlationId: /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(
            correlationId ?? '',
          )
            ? correlationId
            : undefined,
        },
      );
    if (
      correlationId &&
      /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(correlationId)
    )
      Object.defineProperty(data, 'correlationId', {
        value: correlationId,
        enumerable: false,
      });
    return data;
  }
  function selectedClient(id = config.clientId) {
    if (![config.clientId, config.nativeClientId].includes(id))
      throw new Error('unregistered client id');
    return id;
  }
  async function clientTokens(clientId, body, signal) {
    const id = selectedClient(clientId);
    const result = await request(issuerPath + '/oauth2/token', {
      form: true,
      body: { ...body, client_id: id },
      signal,
    });
    return { ...result, client_id: id };
  }
  async function verifyDevice(token, expected, audience = 'auriga') {
    if (typeof token !== 'string' || token.length > 16384)
      throw new Error('invalid credential');
    const parts = token.split('.');
    if (parts.length !== 3 || parts.some((s) => !/^[\w-]+$/.test(s)))
      throw new Error('invalid credential');
    const header = JSON.parse(Buffer.from(parts[0], 'base64url').toString()),
      claims = JSON.parse(Buffer.from(parts[1], 'base64url').toString());
    const now = Date.now() / 1000;
    // Standard devices use scalar aud; native TokenService may encode a singleton array.
    // Both representations must identify this one exact audience.
    if (
      header.alg !== 'ES256' ||
      !header.kid ||
      header.crit ||
      header.jku ||
      header.jwk ||
      header.x5u ||
      claims.iss !== config.issuer ||
      (claims.aud !== audience &&
        !(
          Array.isArray(claims.aud) &&
          claims.aud.length === 1 &&
          claims.aud[0] === audience
        )) ||
      claims.ptyp !== 'device' ||
      claims.ctx !== 'device' ||
      claims.scope !== 'auriga.connect' ||
      claims.sid !== undefined ||
      claims.instanceId !== undefined ||
      !Number.isSafeInteger(claims.exp) ||
      claims.exp <= now ||
      !Number.isSafeInteger(claims.iat) ||
      claims.iat > now + 30 ||
      (claims.nbf !== undefined &&
        (!Number.isSafeInteger(claims.nbf) || claims.nbf > now + 30)) ||
      ![claims.tid, claims.sub, claims.did].every(
        (s) => typeof s === 'string' && /^[0-9a-f-]{36}$/i.test(s),
      )
    )
      throw new Error('invalid credential');
    if (
      audience === 'auriga.ai-gateway' &&
      (header.typ !== 'at+jwt' ||
        !requireValid('StandardDeviceAiClaims', claims) ||
        claims.exp - claims.iat > 300 ||
        claims.exp <= claims.iat ||
        claims.exp > now + 330)
    )
      throw new Error('invalid AI credential');
    if (
      expected &&
      (['tid', 'sub', 'did'].some((k) => claims[k] !== expected[k]) ||
        Boolean(claims.uacSessionBinding) !==
          Boolean(expected.uacSessionBinding) ||
        ['sourceSessionId', 'providerId', 'authGeneration'].some(
          (k) =>
            claims.uacSessionBinding?.[k] !== expected.uacSessionBinding?.[k],
        ))
    )
      throw new Error('credential identity changed');
    const jwks = await request(issuerPath + '/oauth2/jwks');
    const keys = jwks.keys?.filter(
      (k) =>
        k.kid === header.kid &&
        k.kty === 'EC' &&
        k.crv === 'P-256' &&
        !k.d &&
        (!k.use || k.use === 'sig') &&
        (!k.alg || k.alg === 'ES256'),
    );
    if (
      keys?.length !== 1 ||
      !verify(
        'sha256',
        Buffer.from(parts[0] + '.' + parts[1]),
        {
          key: createPublicKey({ key: keys[0], format: 'jwk' }),
          dsaEncoding: 'ieee-p1363',
        },
        Buffer.from(parts[2], 'base64url'),
      )
    )
      throw new Error('invalid signature');
    return claims;
  }
  return {
    config,
    request,
    verifyDevice,
    loginSession: (signal) => {
      const cookieJar = new Map();
      return (path, options = {}) => {
        if (
          !/^\/api\/iam\/v1\/login\/(?:providers|transactions(?:\/[0-9a-f-]{36}(?:\/advance)?)?|uac\/challenges(?:\/[0-9a-f-]{36}(?:\/complete)?)?)$/.test(
            path,
          )
        )
          throw new Error('invalid login route');
        return request(path, {
          ...options,
          signal: options.method === 'DELETE' ? undefined : signal,
          cookieJar,
        });
      };
    },
    nativePoll: (code, verifier, signal) =>
      clientTokens(
        config.nativeClientId,
        {
          grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
          device_code: code,
          code_verifier: verifier,
        },
        signal,
      ),
    begin: (signal) =>
      request(issuerPath + '/oauth2/device_authorization', {
        form: true,
        body: { client_id: config.clientId, scope: 'auriga.connect' },
        signal,
      }),
    poll: (code, signal) =>
      request(issuerPath + '/oauth2/token', {
        form: true,
        body: {
          client_id: config.clientId,
          grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
          device_code: code,
        },
        signal,
      }),
    refresh: (token, clientId) =>
      clientTokens(clientId, {
        grant_type: 'refresh_token',
        refresh_token: token,
      }),
    revoke: (token, clientId) =>
      request(issuerPath + '/oauth2/revoke', {
        form: true,
        body: { client_id: selectedClient(clientId), token },
      }),
    async profile(token, claims, clientId) {
      const me = requireValid(
        'StandardDeviceIdentity',
        await request('/api/iam/v1/me/device-session', { token }),
      );
      if (
        me.accountId !== claims.sub ||
        me.tenantId !== claims.tid ||
        me.deviceId !== claims.did ||
        me.clientId !== selectedClient(clientId) ||
        Date.parse(me.expiresAt) <= Date.now() ||
        Boolean(me.uacSessionBinding) !== Boolean(claims.uacSessionBinding) ||
        (me.uacSessionBinding &&
          ['sourceSessionId', 'providerId', 'authGeneration'].some(
            (k) => me.uacSessionBinding[k] !== claims.uacSessionBinding?.[k],
          ))
      )
        throw new Error('identity mismatch');
      const identity = {
        tenantId: claims.tid,
        memberId: me.memberId,
        deviceId: claims.did,
        providerId: me.providerId,
        ...(me.uacSessionBinding
          ? {
              uacSessionBinding: {
                sourceSessionId: me.uacSessionBinding.sourceSessionId,
                providerId: me.uacSessionBinding.providerId,
                authGeneration: me.uacSessionBinding.authGeneration,
              },
            }
          : {}),
      };
      requireValid('ShellAuthIdentity', identity);
      if (
        identity.uacSessionBinding &&
        identity.uacSessionBinding.providerId !== identity.providerId
      )
        throw new Error('source mismatch');
      return { identity, expiresAt: me.expiresAt };
    },
    enterprise: (token) =>
      request('/api/iam/v1/me/uac/credentials', {
        token,
        method: 'POST',
        body: {},
      }),
    async audience(refreshToken, claims, audience, clientId) {
      if (audience !== 'auriga.ai-gateway')
        throw new Error('unsupported audience');
      const result = await clientTokens(clientId, {
        grant_type: 'refresh_token',
        refresh_token: refreshToken,
        resource: 'urn:auriga:ai-gateway',
      });
      await verifyDevice(result.access_token, claims, audience);
      return result;
    },
  };
}
