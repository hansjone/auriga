import { createPublicKey, verify } from 'node:crypto';
import { requireValid } from './schema.mjs';

export class PlatformError extends Error {
  constructor(status, code) {
    super('platform request failed');
    this.status = status;
    this.code = code;
  }
}
export function trustedConfig(input) {
  const url = new URL(input.platformOrigin);
  if (
    url.protocol !== 'https:' ||
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
  if (!/^[\w.-]{1,128}$/.test(input.clientId ?? 'auriga-desktop'))
    throw new Error('invalid client id');
  return Object.freeze({
    platformOrigin: url.origin,
    issuer: issuer.href.replace(/\/$/, ''),
    clientId: input.clientId ?? 'auriga-desktop',
    deviceName: 'Auriga Desktop',
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
    const raw = await boundedBody(response);
    let data;
    try {
      data = raw.length ? JSON.parse(raw.toString('utf8')) : {};
    } catch {
      throw new PlatformError(response.status, 'common.unavailable');
    }
    if (!response.ok)
      throw new PlatformError(
        response.status,
        typeof data.error === 'string' ? data.error : data.code,
      );
    return data;
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
    if (
      header.alg !== 'ES256' ||
      !header.kid ||
      header.crit ||
      header.jku ||
      header.jwk ||
      header.x5u ||
      claims.iss !== config.issuer ||
      claims.aud !== audience ||
      claims.ptyp !== 'device' ||
      claims.ctx !== 'device' ||
      claims.scope !== 'auriga.connect' ||
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
      expected &&
      ['tid', 'sub', 'did'].some((k) => claims[k] !== expected[k])
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
    refresh: (token) =>
      request(issuerPath + '/oauth2/token', {
        form: true,
        body: {
          client_id: config.clientId,
          grant_type: 'refresh_token',
          refresh_token: token,
        },
      }),
    revoke: (token) =>
      request(issuerPath + '/oauth2/revoke', {
        form: true,
        body: { client_id: config.clientId, token },
      }),
    async profile(token, claims) {
      const me = await request('/api/iam/v1/me', { token });
      if (
        me.account?.id !== claims.sub ||
        me.account?.tenantId !== claims.tid ||
        me.account?.status !== 'active'
      )
        throw new Error('identity mismatch');
      const identity = {
        tenantId: claims.tid,
        memberId: claims.sub,
        deviceId: claims.did,
        providerId: me.session?.providerId,
        ...(me.session?.uacSessionBinding
          ? { uacSessionBinding: me.session.uacSessionBinding }
          : {}),
      };
      requireValid('ShellAuthIdentity', identity);
      if (
        identity.uacSessionBinding &&
        identity.uacSessionBinding.providerId !== identity.providerId
      )
        throw new Error('source mismatch');
      if (me.uacLoginInfo) requireValid('UacLoginInfo', me.uacLoginInfo);
      return { identity, uacLoginInfo: me.uacLoginInfo };
    },
    enterprise: (token) =>
      request('/api/iam/v1/me/uac/credentials', {
        token,
        method: 'POST',
        body: {},
      }),
    async audience(refreshToken, claims, audience) {
      if (audience !== 'auriga.ai-gateway')
        throw new Error('unsupported audience');
      const result = await request(issuerPath + '/oauth2/token', {
        form: true,
        body: {
          client_id: config.clientId,
          grant_type: 'refresh_token',
          refresh_token: refreshToken,
          resource: 'urn:auriga:ai-gateway',
        },
      });
      await verifyDevice(result.access_token, claims, audience);
      return result;
    },
  };
}
