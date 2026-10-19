import { createServer } from 'node:http';
import { generateKeyPairSync, createSign, randomBytes } from 'node:crypto';
import { enrollEmergency, totpCode } from '../src/modes/standalone.js';
import { ARGON2_LIMITS } from '../src/identity/crypto.js';

/** Fastest Argon2id params still accepted by validateConfig floors. */
export const TEST_ARGON2 = {
  t: ARGON2_LIMITS.tMin,
  m: ARGON2_LIMITS.mMin,
  p: 1,
  saltLen: ARGON2_LIMITS.saltMin,
  dkLen: ARGON2_LIMITS.dkLenMin,
};

/**
 * @param {string} [username]
 * @param {string} [password]
 */
export function testEmergency(
  username = 'alice',
  password = 'correct-horse-battery',
) {
  const enrolled = enrollEmergency(username, password, { argon2: TEST_ARGON2 });
  return { ...enrolled, password };
}

/** @param {string} secret @param {Date|number} [now] */
export function otpFor(secret, now = new Date()) {
  const epoch =
    typeof now === 'number' ? now : Math.floor(now.getTime() / 1000);
  return totpCode(secret, Math.floor(epoch / 30));
}

/**
 * Minimal in-process OIDC IdP for connector tests.
 */
export async function startFakeOidc() {
  const { privateKey, publicKey } = generateKeyPairSync('rsa', {
    modulusLength: 2048,
  });
  const jwk = publicKey.export({ format: 'jwk' });
  jwk.kid = 'test-key';
  jwk.alg = 'RS256';
  jwk.use = 'sig';

  /** @type {Map<string, string>} */
  const verifiers = new Map();
  /** @type {Set<string>} */
  const codes = new Set();
  let expectedNonce = '';
  let tokenOverride = /** @type {string|null} */ (null);
  let advertiseWrongKey = false;
  let authEndpointOverride = /** @type {string|null} */ (null);

  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', `http://127.0.0.1`);
    const respond = (status, body) => {
      const raw = typeof body === 'string' ? body : JSON.stringify(body);
      res.writeHead(status, { 'Content-Type': 'application/json' });
      res.end(raw);
    };

    if (url.pathname === '/.well-known/openid-configuration') {
      return respond(200, {
        issuer,
        authorization_endpoint: authEndpointOverride ?? `${issuer}/authorize`,
        token_endpoint: `${issuer}/token`,
        userinfo_endpoint: `${issuer}/userinfo`,
        jwks_uri: `${issuer}/jwks`,
      });
    }
    if (url.pathname === '/jwks') {
      const key = advertiseWrongKey
        ? { ...jwk, n: Buffer.from(randomBytes(256)).toString('base64url') }
        : jwk;
      return respond(200, { keys: [{ ...key, d: undefined }] });
    }
    if (url.pathname === '/token' && req.method === 'POST') {
      const chunks = [];
      for await (const c of req) chunks.push(c);
      const form = new URLSearchParams(Buffer.concat(chunks).toString('utf8'));
      const code = form.get('code') ?? '';
      const verifier = form.get('code_verifier') ?? '';
      if (!codes.has(code) || verifiers.get(code) !== verifier) {
        return respond(400, { error: 'invalid_grant' });
      }
      codes.delete(code);
      const idToken =
        tokenOverride ??
        signIdToken({
          privateKey,
          issuer,
          nonce: expectedNonce,
          aud: 'example-client',
          sub: 'alice',
          name: 'Alice Example',
          email: 'alice@example.com',
        });
      return respond(200, {
        access_token: 'access-example',
        token_type: 'Bearer',
        id_token: idToken,
      });
    }
    if (url.pathname === '/userinfo') {
      if (req.headers.authorization !== 'Bearer access-example') {
        res.writeHead(401);
        return res.end();
      }
      return respond(200, {
        sub: 'alice',
        name: 'Alice Example',
        email: 'alice@example.com',
        email_verified: true,
      });
    }
    res.writeHead(404);
    res.end();
  });

  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const addr = /** @type {import('node:net').AddressInfo} */ (server.address());
  const issuer = `http://127.0.0.1:${addr.port}`;

  return {
    issuer,
    server,
    jwk,
    privateKey,
    /** @param {string} nonce @param {string} verifier */
    issueCode(nonce, verifier) {
      expectedNonce = nonce;
      const code = randomBytes(16).toString('hex');
      codes.add(code);
      verifiers.set(code, verifier);
      return code;
    },
    /** @param {string|null} token */
    setTokenOverride(token) {
      tokenOverride = token;
    },
    setWrongJwks(value) {
      advertiseWrongKey = value;
    },
    /** @param {string|null} url */
    setAuthEndpoint(url) {
      authEndpointOverride = url;
    },
    async close() {
      await new Promise((resolve, reject) =>
        server.close((err) => (err ? reject(err) : resolve(undefined))),
      );
    },
  };
}

/**
 * @param {{
 *   privateKey: import('node:crypto').KeyObject,
 *   issuer: string,
 *   nonce: string,
 *   aud: string,
 *   sub: string,
 *   name?: string,
 *   email?: string,
 *   expOffsetSec?: number,
 *   extra?: Record<string, unknown>,
 * }} opts
 */
export function signIdToken(opts) {
  const header = Buffer.from(
    JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: 'test-key' }),
  ).toString('base64url');
  const nowSec = Math.floor(Date.now() / 1000);
  const payload = Buffer.from(
    JSON.stringify({
      iss: opts.issuer,
      sub: opts.sub,
      aud: opts.aud,
      nonce: opts.nonce,
      iat: nowSec,
      exp: nowSec + (opts.expOffsetSec ?? 300),
      name: opts.name,
      email: opts.email,
      email_verified: true,
      ...opts.extra,
    }),
  ).toString('base64url');
  const sign = createSign('RSA-SHA256');
  sign.update(`${header}.${payload}`);
  sign.end();
  const signature = sign.sign(opts.privateKey).toString('base64url');
  return `${header}.${payload}.${signature}`;
}

/**
 * @param {string} url
 */
export function parseRedirect(url) {
  const u = new URL(url);
  return {
    state: u.searchParams.get('state') ?? '',
    nonce: u.searchParams.get('nonce') ?? '',
    challenge: u.searchParams.get('code_challenge') ?? '',
    method: u.searchParams.get('code_challenge_method') ?? '',
  };
}
