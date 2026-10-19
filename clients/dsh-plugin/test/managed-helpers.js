import {
  generateKeyPairSync,
  sign as cryptoSign,
  randomBytes,
} from 'node:crypto';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { canonicalize } from '../src/managed/jcs.js';

export const INSTANCE_ID = '01936b1e-0000-7000-8000-000000000001';
export const OWNER_ID = '01936b1e-0000-7000-8000-0000000000a1';
export const OTHER_OWNER = '01936b1e-0000-7000-8000-0000000000a2';
export const TENANT_ID = '01936b1e-0000-7000-8000-0000000000t1';
export const SID = '01936b1e-0000-7000-8000-0000000000s1';

/** @returns {{ privateKey: import('node:crypto').KeyObject, publicKey: import('node:crypto').KeyObject, kid: string, jwk: object, keySet: { keys: object[] }, keyFile: string }} */
export function makeIdentityKeys(kid = 'edge-kid-1') {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const jwk = publicKey.export({ format: 'jwk' });
  jwk.kid = kid;
  jwk.alg = 'EdDSA';
  jwk.use = 'sig';
  jwk.kty = 'OKP';
  jwk.crv = 'Ed25519';
  const keySet = { keys: [{ ...jwk }] };
  const dir = mkdtempSync(join(tmpdir(), 'auriga-id-'));
  const keyFile = join(dir, 'identity-keys.json');
  writeFileSync(keyFile, JSON.stringify(keySet));
  return { privateKey, publicKey, kid, jwk, keySet, keyFile };
}

/**
 * @param {import('node:crypto').KeyObject} privateKey
 * @param {string} kid
 * @param {Partial<import('../src/managed/door.js').IdentityClaims> & { htm: string, htu: string }} claims
 */
export function signIdentity(privateKey, kid, claims) {
  const now = Math.floor(Date.now() / 1000);
  const payload = {
    iss: 'auriga-edge',
    aud: INSTANCE_ID,
    sub: OWNER_ID,
    tid: TENANT_ID,
    sid: SID,
    iat: now,
    exp: now + 60,
    jti: randomBytes(12).toString('base64url'),
    pv: 1,
    ...claims,
  };
  const header = Buffer.from(
    JSON.stringify({ alg: 'EdDSA', kid, typ: 'auriga-id+jwt' }),
  ).toString('base64url');
  const body = Buffer.from(JSON.stringify(payload)).toString('base64url');
  const signingInput = `${header}.${body}`;
  const sig = cryptoSign(null, Buffer.from(signingInput), privateKey).toString(
    'base64url',
  );
  return { token: `${signingInput}.${sig}`, claims: payload };
}

/**
 * @param {import('node:crypto').KeyObject} privateKey
 * @param {string} kid
 * @param {object} manifestWithoutSig
 */
export function signPanelManifest(privateKey, kid, manifestWithoutSig) {
  const input = Buffer.from(canonicalize(manifestWithoutSig), 'utf8');
  const value = cryptoSign(null, input, privateKey).toString('base64url');
  return {
    ...manifestWithoutSig,
    signature: { alg: 'EdDSA', kid, value },
  };
}

/** Minimal valid panel manifest body (caller signs). */
export function panelManifestBase(entry = 'panel.js') {
  const now = new Date();
  const expires = new Date(now.getTime() + 3600_000);
  return {
    schemaVersion: 1,
    version: '1.0.0',
    issuedAt: now.toISOString(),
    expiresAt: expires.toISOString(),
    protocol: 1,
    element: 'auriga-team-panel',
    entry,
    integrity: {
      [entry]: 'sha384-' + Buffer.alloc(48, 1).toString('base64'),
    },
  };
}
