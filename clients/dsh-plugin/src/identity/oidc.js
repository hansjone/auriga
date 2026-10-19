import { createPublicKey, createVerify } from 'node:crypto';
import { failResult } from './errors.js';
import { pkceChallenge, randomToken, same } from './crypto.js';
import { createOidcHttp } from './oidc-http.js';

const DEFAULT_SCOPES = ['openid', 'profile', 'email'];
const SPI_VERSION = '1';

/**
 * @param {string} issuer
 */
function normalizeIssuer(issuer) {
  let value = issuer.trim();
  while (value.endsWith('/')) value = value.slice(0, -1);
  return value;
}

/**
 * @param {string} issuer
 */
function requireIssuerUri(issuer) {
  let url;
  try {
    url = new URL(normalizeIssuer(issuer));
  } catch {
    throw Object.assign(new Error('invalid issuer'), {
      code: 'common.validation_failed',
    });
  }
  if (
    !(url.protocol === 'https:' || url.protocol === 'http:') ||
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw Object.assign(new Error('invalid issuer'), {
      code: 'common.validation_failed',
    });
  return url;
}

/**
 * @param {{ allowLocalHttp?: boolean, fetchImpl?: typeof fetch, now?: () => Date, http?: ReturnType<typeof createOidcHttp> }} [deps]
 */
export function createOidcConnector(deps = {}) {
  const now = deps.now ?? (() => new Date());
  const http = deps.http ?? createOidcHttp(deps);
  /** @type {Map<string, { doc: OidcDoc, fetchedAt: number }>} */
  const cache = new Map();

  /**
   * @typedef {{
   *   issuer: string,
   *   authorizationEndpoint: string,
   *   tokenEndpoint: string,
   *   userinfoEndpoint: string|null,
   *   jwksUri: string|null,
   * }} OidcDoc
   */

  /**
   * @param {string} issuer
   * @returns {Promise<OidcDoc>}
   */
  async function getMetadata(issuer) {
    requireIssuerUri(issuer);
    http.validateUrl(normalizeIssuer(issuer));
    const key = normalizeIssuer(issuer);
    const cached = cache.get(key);
    if (cached && cached.fetchedAt + 10 * 60_000 > Date.now())
      return cached.doc;
    const discovery = `${key}/.well-known/openid-configuration`;
    const response = await http.send(discovery, 'GET');
    if (response.statusCode !== 200)
      throw Object.assign(new Error('discovery failed'), {
        code: 'iam.connector_unavailable',
      });
    const body = JSON.parse(response.body);
    const docIssuer = normalizeIssuer(String(body.issuer ?? ''));
    if (docIssuer !== key)
      throw Object.assign(new Error('issuer mismatch'), {
        code: 'common.validation_failed',
      });
    const authorizationEndpoint = String(body.authorization_endpoint ?? '');
    const tokenEndpoint = String(body.token_endpoint ?? '');
    if (!authorizationEndpoint || !tokenEndpoint)
      throw Object.assign(new Error('incomplete discovery'), {
        code: 'common.validation_failed',
      });
    try {
      http.validateUrl(authorizationEndpoint);
      http.validateUrl(tokenEndpoint);
      if (body.userinfo_endpoint)
        http.validateUrl(String(body.userinfo_endpoint));
      if (body.jwks_uri) http.validateUrl(String(body.jwks_uri));
    } catch {
      throw Object.assign(new Error('discovery endpoints unsafe'), {
        code: 'iam.connector_unavailable',
      });
    }
    const authUrl = new URL(authorizationEndpoint);
    const issuerUrl = new URL(key);
    if (authUrl.hostname !== issuerUrl.hostname)
      throw Object.assign(new Error('authorization_endpoint host mismatch'), {
        code: 'iam.connector_unavailable',
      });
    /** @type {OidcDoc} */
    const doc = {
      issuer: key,
      authorizationEndpoint,
      tokenEndpoint,
      userinfoEndpoint: body.userinfo_endpoint
        ? String(body.userinfo_endpoint)
        : null,
      jwksUri: body.jwks_uri ? String(body.jwks_uri) : null,
    };
    cache.set(key, { doc, fetchedAt: Date.now() });
    return doc;
  }

  /**
   * @param {Record<string, unknown>} config
   */
  function scopes(config) {
    const list = Array.isArray(config.scopes)
      ? config.scopes.map(String).filter(Boolean)
      : [];
    const resolved = list.length ? list : [...DEFAULT_SCOPES];
    if (!resolved.includes('openid'))
      throw Object.assign(new Error('scopes must include openid'), {
        code: 'common.validation_failed',
      });
    return resolved.join(' ');
  }

  /**
   * @param {string} value
   */
  function enc(value) {
    return encodeURIComponent(value);
  }

  /**
   * @param {OidcDoc} doc
   * @param {Record<string, unknown>} config
   * @param {string} code
   * @param {string} redirect
   * @param {string} verifier
   */
  async function exchange(doc, config, code, redirect, verifier) {
    if (!verifier || !/^[A-Za-z0-9._~-]{43,128}$/.test(verifier))
      throw Object.assign(new Error('bad verifier'), {
        code: 'iam.external_denied',
      });
    const body =
      `grant_type=authorization_code` +
      `&code=${enc(code)}` +
      `&redirect_uri=${enc(redirect)}` +
      `&client_id=${enc(String(config.clientId))}` +
      `&client_secret=${enc(String(config.clientSecret))}` +
      `&code_verifier=${enc(verifier)}`;
    const response = await http.send(doc.tokenEndpoint, 'POST', body);
    if (response.statusCode >= 400 && response.statusCode < 500)
      throw Object.assign(new Error('token denied'), {
        code: 'iam.external_denied',
      });
    if (response.statusCode !== 200)
      throw Object.assign(new Error('token unavailable'), {
        code: 'iam.connector_unavailable',
      });
    const tokens = JSON.parse(response.body);
    if (tokens.error)
      throw Object.assign(new Error('token error'), {
        code: 'iam.external_denied',
      });
    return tokens;
  }

  /**
   * @param {string} idToken
   * @param {OidcDoc} doc
   * @param {Record<string, unknown>} config
   * @param {string} nonce
   */
  async function verifyIdToken(idToken, doc, config, nonce) {
    const parts = idToken.split('.');
    if (parts.length !== 3)
      throw Object.assign(new Error('bad jwt'), {
        code: 'iam.external_denied',
      });
    const header = JSON.parse(
      Buffer.from(parts[0], 'base64url').toString('utf8'),
    );
    const payload = JSON.parse(
      Buffer.from(parts[1], 'base64url').toString('utf8'),
    );
    if (header.crit && Array.isArray(header.crit) && header.crit.length)
      throw Object.assign(new Error('crit'), { code: 'iam.external_denied' });
    if (header.alg !== 'RS256' && header.alg !== 'ES256')
      throw Object.assign(new Error('alg'), { code: 'iam.external_denied' });
    if (!doc.jwksUri)
      throw Object.assign(new Error('no jwks'), {
        code: 'iam.external_denied',
      });

    const jwksResponse = await http.send(doc.jwksUri, 'GET');
    if (jwksResponse.statusCode !== 200)
      throw Object.assign(new Error('jwks'), {
        code: 'iam.connector_unavailable',
      });
    const jwks = JSON.parse(jwksResponse.body);
    const keys = Array.isArray(jwks.keys) ? jwks.keys : [];
    const signingInput = `${parts[0]}.${parts[1]}`;
    const signature = Buffer.from(parts[2], 'base64url');
    let verified = false;
    for (const jwk of keys) {
      if (jwk.d || (jwk.use && jwk.use !== 'sig')) continue;
      if (header.kid && jwk.kid && header.kid !== jwk.kid) continue;
      if (jwk.alg && jwk.alg !== header.alg) continue;
      try {
        const keyObject = createPublicKey({ key: jwk, format: 'jwk' });
        if (header.alg === 'RS256') {
          const verify = createVerify('RSA-SHA256');
          verify.update(signingInput);
          verify.end();
          if (verify.verify(keyObject, signature)) {
            verified = true;
            break;
          }
        } else if (header.alg === 'ES256') {
          const verify = createVerify('SHA256');
          verify.update(signingInput);
          verify.end();
          if (
            verify.verify(
              { key: keyObject, dsaEncoding: 'ieee-p1363' },
              signature,
            )
          ) {
            verified = true;
            break;
          }
        }
      } catch {
        // try next key
      }
    }
    if (!verified)
      throw Object.assign(new Error('bad signature'), {
        code: 'iam.external_denied',
      });

    if (!payload.sub)
      throw Object.assign(new Error('no sub'), { code: 'iam.external_denied' });
    if (
      normalizeIssuer(String(payload.iss ?? '')) !==
      normalizeIssuer(String(config.issuer))
    )
      throw Object.assign(new Error('iss'), { code: 'iam.external_denied' });
    const audience = Array.isArray(payload.aud)
      ? payload.aud.map(String)
      : payload.aud != null
        ? [String(payload.aud)]
        : [];
    const clientId = String(config.clientId);
    if (!audience.includes(clientId))
      throw Object.assign(new Error('aud'), { code: 'iam.external_denied' });
    if (
      (audience.length > 1 || payload.azp != null) &&
      String(payload.azp ?? '') !== clientId
    )
      throw Object.assign(new Error('azp'), { code: 'iam.external_denied' });

    const clock = now();
    const skewMs = 30_000;
    if (payload.iat == null || payload.iat * 1000 > clock.getTime() + skewMs)
      throw Object.assign(new Error('iat'), { code: 'iam.external_denied' });
    if (payload.nbf != null && payload.nbf * 1000 > clock.getTime() + skewMs)
      throw Object.assign(new Error('nbf'), { code: 'iam.external_denied' });
    if (payload.exp == null || payload.exp * 1000 < clock.getTime() - skewMs)
      throw Object.assign(new Error('exp'), { code: 'iam.external_denied' });
    if (nonce && !same(nonce, String(payload.nonce ?? '')))
      throw Object.assign(new Error('nonce'), { code: 'iam.external_denied' });
    return payload;
  }

  /**
   * @param {Record<string, unknown>} claims
   * @param {Record<string, unknown>|undefined} mapping
   */
  function toProfile(claims, mapping) {
    /** @param {string} field @param {string} fallback */
    const claimName = (field, fallback) => {
      const custom = mapping?.[field];
      return typeof custom === 'string' && custom ? custom : fallback;
    };
    /** @param {string} name */
    const text = (name) => {
      const n = claims[name];
      if (n == null) return '';
      if (Array.isArray(n) && n.length) return String(n[0]);
      return String(n);
    };
    const subject = text(claimName('subject', 'sub'));
    /** @type {Record<string, unknown>} */
    const profile = { subject, attributes: { ...claims } };
    let display = text(claimName('displayName', 'name'));
    if (!display) display = text('preferred_username');
    if (display) profile.displayName = display;
    const email = text(claimName('email', 'email'));
    if (email) {
      profile.email = email;
      const verified = claims[claimName('emailVerified', 'email_verified')];
      if (typeof verified === 'boolean') profile.emailVerified = verified;
      else if (typeof verified === 'string')
        profile.emailVerified = verified.toLowerCase() === 'true';
    }
    const phone = text(claimName('phone', 'phone_number'));
    if (phone) profile.phone = phone;
    if (typeof claims.auth_time === 'number')
      profile.authTime = new Date(claims.auth_time * 1000).toISOString();
    return profile;
  }

  return {
    descriptor() {
      return {
        type: 'oidc',
        spiVersion: SPI_VERSION,
        displayName: { en: 'OIDC / OAuth 2.0', 'zh-CN': 'OIDC / OAuth 2.0' },
        description: {
          en: 'Connect any standards-based OIDC or OAuth 2.0 identity provider.',
          'zh-CN': '对接任意标准 OIDC / OAuth 2.0 身份提供方。',
        },
        steps: ['redirect'],
        configSchema: {
          type: 'object',
          additionalProperties: false,
          required: ['issuer', 'clientId', 'clientSecret'],
          properties: {
            issuer: { type: 'string', format: 'uri', minLength: 1 },
            clientId: { type: 'string', minLength: 1 },
            clientSecret: { type: 'string', minLength: 1, 'x-secret': true },
            scopes: {
              type: 'array',
              minItems: 1,
              items: { type: 'string', minLength: 1 },
              default: DEFAULT_SCOPES,
            },
            claimMapping: {
              type: 'object',
              additionalProperties: false,
              properties: {
                subject: { type: 'string', minLength: 1, default: 'sub' },
                displayName: { type: 'string', minLength: 1, default: 'name' },
                email: { type: 'string', minLength: 1, default: 'email' },
                emailVerified: {
                  type: 'string',
                  minLength: 1,
                  default: 'email_verified',
                },
                phone: {
                  type: 'string',
                  minLength: 1,
                  default: 'phone_number',
                },
              },
            },
          },
        },
        multipleInstances: true,
      };
    },

    /** @param {Record<string, unknown>} config */
    async validateConfig(config) {
      requireIssuerUri(String(config.issuer ?? ''));
      if (!String(config.clientId ?? '') || !String(config.clientSecret ?? ''))
        throw Object.assign(new Error('missing client'), {
          code: 'common.validation_failed',
        });
      scopes(config); // requires openid
      await getMetadata(String(config.issuer));
    },

    /**
     * @param {Record<string, unknown>} ctx
     * @param {Record<string, unknown>} config
     */
    async begin(ctx, config) {
      try {
        const doc = await getMetadata(String(config.issuer));
        const oauthState = randomToken();
        const nonce = randomToken();
        const codeVerifier = randomToken(32);
        const challenge = pkceChallenge(codeVerifier);
        const scope = scopes(config);
        const sep = doc.authorizationEndpoint.includes('?') ? '&' : '?';
        const url =
          doc.authorizationEndpoint +
          sep +
          `response_type=code` +
          `&client_id=${enc(String(config.clientId))}` +
          `&redirect_uri=${enc(String(ctx.callbackUrl))}` +
          `&scope=${enc(scope)}` +
          `&state=${enc(oauthState)}` +
          `&nonce=${enc(nonce)}` +
          `&code_challenge=${enc(challenge)}` +
          `&code_challenge_method=S256`;
        return {
          step: { type: 'redirect', url, method: 'GET' },
          state: { oauthState, nonce, codeVerifier, scopes: scope },
        };
      } catch (e) {
        const code =
          /** @type {{ code?: string }} */ (e).code === 'iam.external_denied'
            ? 'iam.external_denied'
            : 'iam.connector_unavailable';
        return failResult(code);
      }
    },

    /**
     * @param {Record<string, unknown>} ctx
     * @param {Record<string, unknown>} config
     * @param {Record<string, unknown>} input
     */
    async advance(ctx, config, input) {
      if (input.type !== 'callback') return failResult('iam.external_denied');
      const params = /** @type {Record<string, string>} */ (input.params ?? {});
      const state = /** @type {Record<string, string>|null} */ (ctx.state);
      if (!state || typeof state !== 'object')
        return failResult('iam.external_denied');
      if (
        !state.oauthState ||
        !same(state.oauthState, String(params.state ?? ''))
      )
        return failResult('iam.external_denied');
      if (params.error) return failResult('iam.external_denied');
      const code = String(params.code ?? '');
      if (!code) return failResult('iam.external_denied');
      try {
        const doc = await getMetadata(String(config.issuer));
        const tokens = await exchange(
          doc,
          config,
          code,
          String(ctx.callbackUrl),
          String(state.codeVerifier ?? ''),
        );
        const idToken = String(tokens.id_token ?? '');
        if (!idToken) return failResult('iam.external_denied');
        const claims = await verifyIdToken(
          idToken,
          doc,
          config,
          String(state.nonce ?? ''),
        );
        const access = String(tokens.access_token ?? '');
        if (doc.userinfoEndpoint && access) {
          const infoResponse = await http.send(
            doc.userinfoEndpoint,
            'GET',
            null,
            {
              Authorization: `Bearer ${access}`,
            },
          );
          if (infoResponse.statusCode >= 400 && infoResponse.statusCode < 500)
            return failResult('iam.external_denied');
          if (infoResponse.statusCode !== 200)
            return failResult('iam.connector_unavailable');
          const info = JSON.parse(infoResponse.body);
          if (idToken && String(claims.sub ?? '') !== String(info.sub ?? ''))
            return failResult('iam.external_denied');
          for (const [k, v] of Object.entries(info)) {
            if (
              !(k in claims) &&
              ![
                'iss',
                'aud',
                'nonce',
                'azp',
                'exp',
                'iat',
                'nbf',
                'auth_time',
                'amr',
                'acr',
              ].includes(k)
            )
              claims[k] = v;
          }
        }
        if (!Object.keys(claims).length)
          return failResult('iam.external_denied');
        const profile = toProfile(
          claims,
          /** @type {Record<string, unknown>|undefined} */ (
            config.claimMapping
          ),
        );
        if (!profile.subject) return failResult('iam.external_denied');
        return { step: { type: 'done', profile } };
      } catch (e) {
        const code = /** @type {{ code?: string }} */ (e).code;
        if (
          code === 'iam.external_denied' ||
          code === 'iam.connector_unavailable'
        )
          return failResult(code);
        if (code === 'common.validation_failed')
          return failResult('iam.external_denied');
        return failResult('iam.connector_unavailable');
      }
    },

    /**
     * @internal test helper
     * @param {string} issuer
     */
    _invalidate(issuer) {
      cache.delete(normalizeIssuer(issuer));
    },
  };
}

export const oidcConnector = createOidcConnector();
