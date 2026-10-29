import { tracedFetch } from '../observability/index.js';
/* global Headers, AbortSignal */
import { createPublicKey, verify } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { networkInstallation } from '../network/installation.js';
import { normalizeOrigin } from '../settings/store.js';

/** Saved preferences own the platform origin. Loader metadata is a default for
 * that platform only; switching never sends its provider id or network pins to
 * another installation. Public provider discovery does not authenticate anyone.
 * @param {import('../generated/connect-config.js').ConnectConfig|undefined} loader
 * @param {string} origin
 * @param {{saved?:boolean}} [options] Only the profile store may assert a saved preference. */
export async function savedPlatformConfig(
  loader,
  origin,
  { saved = false } = {},
) {
  const platformOrigin = normalizeOrigin(origin);
  const same =
    loader && normalizeOrigin(loader.platformOrigin) === platformOrigin;
  const config = platformConfig({
    ...(same ? loader : {}),
    platformOrigin,
    issuer: same ? loader.issuer : platformOrigin,
    clientId: loader?.clientId ?? 'auriga-dsh',
    ...(loader?.deviceName ? { deviceName: loader.deviceName } : {}),
    providerId: same ? loader.providerId : 'discover',
    ...(saved && platformOrigin.startsWith('http:')
      ? { allowLocalHttp: true }
      : {}),
  });
  if (same) return config;
  const providers = await createPlatform(config).request(
    '/api/iam/v1/login/providers',
  );
  const teams = providers.items?.filter(
    (/** @type {any} */ p) =>
      p.connectorType === 'team-account' && p.primaryStep === 'form',
  );
  if (teams?.length !== 1)
    throw new Error(
      'one team-account form provider or explicit loader configuration required',
    );
  return platformConfig({ ...config, providerId: teams[0].id });
}

/** Installation-owned origin. No URL, issuer or redirect is accepted from UI/model input.
 * @param {any} input
 * @returns {Readonly<Omit<import('../generated/connect-config.js').ConnectConfig,'network'>> & {readonly network?:import('../network/runtime-types.js').NetworkInstallation}} */
export function platformConfig(input) {
  if (
    !input ||
    typeof input !== 'object' ||
    Array.isArray(input) ||
    Object.keys(input).some(
      (k) =>
        ![
          'platformOrigin',
          'issuer',
          'clientId',
          'providerId',
          'deviceName',
          'allowLocalHttp',
          'network',
        ].includes(k),
    )
  )
    throw new Error('invalid platform configuration');
  const url = new URL(normalizeOrigin(input.platformOrigin));
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    (url.protocol !== 'https:' &&
      !(input.allowLocalHttp === true && url.protocol === 'http:'))
  )
    throw new Error('auriga-dsh: trusted HTTPS platform origin required');
  if (input.allowLocalHttp && url.protocol !== 'http:')
    throw new Error('auriga-dsh: explicit private HTTP origin required');
  if (
    ![input.clientId, input.providerId].every(
      (x) => typeof x === 'string' && x.length > 0 && x.length <= 128,
    ) ||
    typeof input.issuer !== 'string' ||
    (input.deviceName !== undefined &&
      (typeof input.deviceName !== 'string' ||
        input.deviceName.length < 1 ||
        input.deviceName.length > 64)) ||
    (input.allowLocalHttp !== undefined &&
      typeof input.allowLocalHttp !== 'boolean')
  )
    throw new Error('auriga-dsh: clientId, providerId and issuer required');
  const issuer = new URL(input.issuer);
  if (
    issuer.origin !== url.origin ||
    issuer.search ||
    issuer.hash ||
    issuer.username ||
    issuer.password
  )
    throw new Error('auriga-dsh: issuer must belong to platform origin');
  return Object.freeze({
    ...input,
    platformOrigin: url.origin,
    issuer: input.issuer.replace(/\/$/, ''),
    ...(Object.hasOwn(input, 'network')
      ? { network: networkInstallation(input.network) }
      : {}),
  });
}

export class PlatformError extends Error {
  /** @param {number} status @param {string} [code] */
  constructor(status, code) {
    super('Auriga platform request failed');
    this.status = status;
    this.code = code;
  }
}

/** One fresh jar per login attempt. Never shared with browser, persisted, or used by Team. */
export class LoginJar {
  /** @type {Map<string,{value:string,path:string}>} */
  cookies = new Map();
  /** @param {Headers} headers */
  accept(headers) {
    for (const line of headers.getSetCookie()) {
      const [pair, ...attributes] = line.split(';');
      const split = pair.indexOf('=');
      const name = pair.slice(0, split).trim(),
        value = pair.slice(split + 1);
      if (split < 1 || !/^[A-Za-z0-9_-]+$/.test(name) || /[\r\n;]/.test(value))
        throw new Error('invalid platform cookie');
      const attrs = attributes.map((x) => x.trim().toLowerCase());
      if (attrs.some((x) => x.startsWith('domain=')))
        throw new Error('domain cookie refused');
      const path =
        attributes
          .find((x) => x.trim().toLowerCase().startsWith('path='))
          ?.trim()
          .slice(5) || '/';
      if (!path.startsWith('/')) throw new Error('invalid cookie path');
      if (attrs.includes('max-age=0')) this.cookies.delete(name);
      else this.cookies.set(name, { value, path });
    }
  }
  /** @param {string} path */
  header(path) {
    return [...this.cookies]
      .filter(([, v]) => path.startsWith(v.path))
      .map(([k, v]) => k + '=' + v.value)
      .join('; ');
  }
  csrf() {
    return (
      this.cookies.get('__Host-auriga_csrf')?.value ??
      this.cookies.get('auriga_csrf')?.value
    );
  }
  clear() {
    this.cookies.clear();
  }
}

/** Every network request has a timeout, body cap and redirect refusal. No automatic write retries.
 * @param {ReturnType<typeof platformConfig>} config */
export function createPlatform(config) {
  /** @param {string} path @param {any} [options] */
  async function request(
    path,
    {
      method = 'GET',
      body,
      token,
      jar,
      signal,
      headers = {},
      form = false,
    } = {},
  ) {
    const url = new URL(path, config.platformOrigin);
    if (
      url.origin !== config.platformOrigin ||
      !path.startsWith('/') ||
      path.startsWith('//')
    )
      throw new Error('untrusted platform path');
    const out = new Headers(headers);
    if (!out.has('accept')) out.set('accept', 'application/json');
    if (jar) out.set('origin', config.platformOrigin);
    else out.delete('origin');
    if (token) out.set('authorization', 'Bearer ' + token);
    if (jar?.header(url.pathname)) out.set('cookie', jar.header(url.pathname));
    if (jar?.csrf()) out.set('X-Auriga-CSRF', jar.csrf());
    if (body !== undefined)
      out.set(
        'content-type',
        form ? 'application/x-www-form-urlencoded' : 'application/json',
      );
    const response = await tracedFetch(url, {
      method,
      headers: out,
      body:
        body === undefined
          ? undefined
          : form
            ? new URLSearchParams(body)
            : JSON.stringify(body),
      redirect: 'error',
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(15000)])
        : AbortSignal.timeout(15000),
    });
    jar?.accept(response.headers);
    let size = 0;
    const parts = [];
    if (response.body) {
      const reader = response.body.getReader();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > 1048576) {
            await reader.cancel().catch(() => {});
            throw new Error('platform response too large');
          }
          parts.push(value);
        }
      } finally {
        reader.releaseLock();
      }
    }
    let data;
    try {
      data = size ? JSON.parse(Buffer.concat(parts).toString('utf8')) : {};
    } catch {
      if (response.ok) throw new Error('invalid platform response');
      data = {};
    }
    if (!response.ok)
      throw new PlatformError(
        response.status,
        typeof data.error === 'string' ? data.error : data.code,
      );
    return data;
  }
  /** @param {string} token @param {any} [expected] @param {boolean} [ai] */
  async function verifyDevice(token, expected, ai = false) {
    if (typeof token !== 'string' || token.length > 16384)
      throw new Error('invalid device credential');
    const parts = token.split('.');
    if (parts.length !== 3 || parts.some((x) => !x || !/^[\w-]+$/.test(x)))
      throw new Error('invalid device credential');
    const header = JSON.parse(
      Buffer.from(parts[0], 'base64url').toString('utf8'),
    );
    const claims = JSON.parse(
      Buffer.from(parts[1], 'base64url').toString('utf8'),
    );
    const now = Math.floor(Date.now() / 1000);
    if (
      header.alg !== 'ES256' ||
      !header.kid ||
      header.crit ||
      header.jku ||
      header.jwk ||
      header.x5u ||
      claims.iss !== config.issuer ||
      claims.aud !== (ai ? 'auriga.ai-gateway' : 'auriga') ||
      claims.ptyp !== 'device' ||
      claims.ctx !== 'device' ||
      claims.scope !== 'auriga.connect' ||
      !Number.isSafeInteger(claims.exp) ||
      claims.exp <= now ||
      !Number.isSafeInteger(claims.iat) ||
      claims.iat > now + 5 ||
      (claims.nbf !== undefined &&
        (!Number.isSafeInteger(claims.nbf) || claims.nbf > now)) ||
      ![claims.tid, claims.sub, claims.did].every(
        (x) => typeof x === 'string' && x.length > 0 && x.length <= 512,
      )
    )
      throw new Error('invalid device credential');
    if (
      ai &&
      (header.typ !== 'at+jwt' ||
        claims.exp - claims.iat > 300 ||
        claims.exp <= claims.iat ||
        claims.exp > now + 300 ||
        claims.sid !== undefined ||
        claims.instanceId !== undefined ||
        !Array.isArray(claims.perms) ||
        claims.perms.length !== 1 ||
        claims.perms[0] !== 'auriga.connect.models.invoke')
    )
      throw new Error('invalid device AI credential');
    if (
      expected &&
      ['tid', 'sub', 'did'].some((k) => expected[k] !== claims[k])
    )
      throw new Error('device identity changed');
    const issuerPath = new URL(config.issuer).pathname.replace(/\/$/, '');
    const jwks = await request(issuerPath + '/oauth2/jwks');
    const keys = jwks.keys?.filter(
      /** @param {any} k */
      (k) =>
        k.kid === header.kid &&
        k.kty === 'EC' &&
        k.crv === 'P-256' &&
        (!k.alg || k.alg === 'ES256') &&
        (!k.use || k.use === 'sig') &&
        !k.d,
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
      throw new Error('invalid device signature');
    return claims;
  }
  return {
    request,
    verifyDevice,
    verifyAi: (/** @type {string} */ token, /** @type {any} */ expected) =>
      verifyDevice(token, expected, true),
  };
}
