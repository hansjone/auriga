// @ts-nocheck
import { traceFetch } from '../observability/index.js';
import {
  createHash,
  createPublicKey,
  verify as cryptoVerify,
} from 'node:crypto';
import { canonicalize } from './jcs.js';
import { PROTOCOL_VERSION } from './protocol.js';

export const PANEL_ELEMENT = 'auriga-team-panel';
export const ROUTE_PREFIX = '/auriga';

const SRI = /^sha(256|384|512)-[A-Za-z0-9+/=]+$/;
const SAFE_SEGMENT = /^[A-Za-z0-9._-]+$/;

export class PanelError extends Error {
  /** @param {string} code @param {string} [message] */
  constructor(code, message) {
    super(message ?? code);
    this.name = 'PanelError';
    this.code = code;
  }
}

/** @param {string} path */
export function assertSafePath(path) {
  if (typeof path !== 'string' || !path) {
    throw new PanelError('common.validation_failed', 'unsafe path');
  }
  const parts = path.split('/');
  for (const seg of parts) {
    if (!seg || seg === '.' || seg === '..' || !SAFE_SEGMENT.test(seg)) {
      throw new PanelError('common.validation_failed', `unsafe path: ${path}`);
    }
  }
}

/**
 * Shape check aligned with panel-manifest.schema.json (+ issuedAt/expiresAt).
 * @param {unknown} m
 */
export function assertManifestShape(m) {
  const bad = (why) => new PanelError('common.validation_failed', why);
  if (typeof m !== 'object' || m === null || Array.isArray(m))
    throw bad('manifest not an object');
  const manifest = /** @type {Record<string, unknown>} */ (m);
  if (manifest.schemaVersion !== 1) throw bad('schemaVersion must be 1');
  if (typeof manifest.version !== 'string' || !manifest.version)
    throw bad('version missing');
  if (manifest.protocol !== PROTOCOL_VERSION)
    throw new PanelError(
      'plugin.protocol_unsupported',
      `panel needs protocol ${String(manifest.protocol)}`,
    );
  if (manifest.element !== PANEL_ELEMENT) throw bad('element mismatch');
  if (typeof manifest.entry !== 'string') throw bad('entry missing');
  if (typeof manifest.issuedAt !== 'string') throw bad('issuedAt missing');
  if (typeof manifest.expiresAt !== 'string') throw bad('expiresAt missing');
  if (typeof manifest.integrity !== 'object' || !manifest.integrity)
    throw bad('integrity missing');
  for (const [path, digest] of Object.entries(
    /** @type {Record<string, string>} */ (manifest.integrity),
  )) {
    assertSafePath(path);
    if (typeof digest !== 'string' || !SRI.test(digest))
      throw bad(`integrity[${path}] invalid`);
  }
  const sig = manifest.signature;
  if (
    typeof sig !== 'object' ||
    sig === null ||
    /** @type {Record<string, unknown>} */ (sig).alg !== 'EdDSA' ||
    typeof (/** @type {Record<string, unknown>} */ (sig).kid) !== 'string' ||
    typeof (/** @type {Record<string, unknown>} */ (sig).value) !== 'string'
  )
    throw bad('signature missing');
  assertSafePath(/** @type {string} */ (manifest.entry));
  const integrity = /** @type {Record<string, string>} */ (manifest.integrity);
  if (!(/** @type {string} */ (manifest.entry) in integrity))
    throw bad('entry missing integrity');
  if (manifest.styles !== undefined) {
    if (
      !Array.isArray(manifest.styles) ||
      manifest.styles.some((s) => typeof s !== 'string')
    )
      throw bad('styles type error');
    for (const style of manifest.styles) {
      assertSafePath(style);
      if (!(style in integrity))
        throw bad(`styles missing integrity: ${style}`);
    }
  }
}

/** @param {object} manifest */
export function signingInput(manifest) {
  const { signature: _signature, ...rest } = manifest;
  return Buffer.from(canonicalize(rest), 'utf8');
}

/**
 * @param {object} manifest
 * @param {{ keys: object[] }} keySet
 * @param {number} [nowSec]
 */
export function verifyManifestSignature(
  manifest,
  keySet,
  nowSec = Math.floor(Date.now() / 1000),
) {
  const kid = manifest.signature.kid;
  const jwk = (keySet?.keys ?? []).find((k) => k.kid === kid);
  if (!jwk)
    throw new PanelError('common.unauthenticated', `unknown panel kid ${kid}`);
  if (jwk.kty !== 'OKP' || jwk.crv !== 'Ed25519')
    throw new PanelError('common.unauthenticated', 'panel key must be Ed25519');
  if (typeof jwk.nbf === 'number' && nowSec < jwk.nbf - 5)
    throw new PanelError('common.unauthenticated', 'panel key not yet valid');
  if (typeof jwk.exp === 'number' && nowSec > jwk.exp + 5)
    throw new PanelError('common.unauthenticated', 'panel key expired');
  let ok = false;
  try {
    const key = createPublicKey({
      key: { kty: 'OKP', crv: 'Ed25519', x: jwk.x },
      format: 'jwk',
    });
    const sig = Buffer.from(String(manifest.signature.value), 'base64url');
    ok = cryptoVerify(null, signingInput(manifest), key, sig);
  } catch {
    ok = false;
  }
  if (!ok)
    throw new PanelError('common.unauthenticated', 'panel signature invalid');
}

/**
 * @param {object} manifest
 * @param {{ keys: object[], pluginVersion: string, now?: number }} opts
 */
export function verifyManifest(manifest, opts) {
  assertManifestShape(manifest);
  const nowSec = opts.now ?? Math.floor(Date.now() / 1000);
  const expires = Date.parse(String(manifest.expiresAt));
  if (!Number.isFinite(expires) || expires / 1000 < nowSec - 5)
    throw new PanelError('common.unauthenticated', 'panel manifest expired');
  if (
    manifest.minPluginVersion != null &&
    !versionAtLeast(opts.pluginVersion, String(manifest.minPluginVersion))
  ) {
    throw new PanelError(
      'common.precondition_failed',
      `plugin too old for panel (need >= ${manifest.minPluginVersion})`,
    );
  }
  verifyManifestSignature(manifest, opts.keys, nowSec);
  return manifest;
}

/** @param {string} a @param {string} b */
function versionAtLeast(a, b) {
  const x = String(a)
    .split('-')[0]
    .split('.')
    .map((n) => Number.parseInt(n, 10) || 0);
  const y = String(b)
    .split('-')[0]
    .split('.')
    .map((n) => Number.parseInt(n, 10) || 0);
  for (let i = 0; i < 3; i++) {
    const d = (x[i] ?? 0) - (y[i] ?? 0);
    if (d !== 0) return d > 0;
  }
  return true;
}

/**
 * Managed mode: fetch and verify panel manifest from platform /panel/.
 * @param {{
 *   platformUrl: string,
 *   keys: { keys: object[] } | (() => { keys: object[] }),
 *   pluginVersion: string,
 *   fetchImpl?: typeof fetch,
 *   now?: number,
 * }} opts
 */
export async function fetchVerifiedManifest(opts) {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const url = new URL('/panel/manifest.json', opts.platformUrl);
  let response;
  try {
    response = await traceFetch(fetchImpl, url, {
      headers: {
        accept: 'application/json',
        'auriga-protocol': String(PROTOCOL_VERSION),
      },
      redirect: 'error',
    });
  } catch {
    throw new PanelError('common.unavailable', 'panel manifest fetch failed');
  }
  if (!response.ok)
    throw new PanelError('common.unavailable', 'panel manifest fetch failed');
  const text = await response.text();
  if (text.length > 256 * 1024)
    throw new PanelError('common.validation_failed', 'manifest too large');
  let manifest;
  try {
    manifest = JSON.parse(text);
  } catch {
    throw new PanelError('common.validation_failed', 'manifest not JSON');
  }
  const keys = typeof opts.keys === 'function' ? opts.keys() : opts.keys;
  return verifyManifest(manifest, {
    keys,
    pluginVersion: opts.pluginVersion,
    now: opts.now,
  });
}

/**
 * @param {{
 *   mode: 'managed',
 *   auth: { requestRejection(req: object): number | undefined },
 *   getManifest: () => Promise<object>,
 *   pluginVersion: string,
 *   panelBase?: string,
 * }} o
 */
export function createAurigaRouteHandler(o) {
  /** @param {object} req @param {import('node:http').ServerResponse} res */
  return async function handle(req, res) {
    const rejection = o.auth.requestRejection?.(req);
    if (rejection !== undefined) {
      res.writeHead(rejection);
      res.end(rejection === 401 ? 'unauthorized' : 'forbidden');
      return;
    }
    const url = new URL(req.url ?? '/', 'http://192.0.2.10');
    const path = url.pathname.slice(ROUTE_PREFIX.length);

    if (path === '/bootstrap') {
      if (req.method !== 'GET') {
        res.writeHead(405);
        res.end();
        return;
      }
      try {
        const manifest = await o.getManifest();
        const body = {
          mode: o.mode,
          protocol: PROTOCOL_VERSION,
          pluginVersion: o.pluginVersion,
          manifest,
          panelBase: o.panelBase ?? '/panel/',
          fetchBase: '/',
          platformUrl: null,
        };
        res.writeHead(200, {
          'content-type': 'application/json; charset=utf-8',
          'cache-control': 'no-store',
        });
        res.end(JSON.stringify(body));
      } catch (error) {
        const code =
          error instanceof PanelError ? error.code : 'common.unavailable';
        res.writeHead(502, {
          'content-type': 'application/problem+json; charset=utf-8',
        });
        res.end(
          JSON.stringify({
            type: 'about:blank',
            title: 'Bad Gateway',
            status: 502,
            code,
            detail: 'panel bootstrap failed',
          }),
        );
      }
      return;
    }

    res.writeHead(404, { 'content-type': 'application/problem+json' });
    res.end(
      JSON.stringify({
        type: 'about:blank',
        title: 'Not Found',
        status: 404,
        code: 'common.not_found',
      }),
    );
  };
}

/** @param {string} alg @param {Buffer|string} body */
export function sriDigest(alg, body) {
  return `${alg}-${createHash(alg).update(body).digest('base64')}`;
}
