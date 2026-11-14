/** Desktop-only offline release trust. Provision before building a production installer. */
import { createPublicKey, verify } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { existsSync, readFileSync } from 'node:fs';
import { compare } from './semver-lite.mjs';

// Deliberately unprovisioned: never trust a key supplied by the platform/manifest.
const keyFile = new URL('./release-public-key.txt', import.meta.url);
export const RELEASE_PUBLIC_KEY = existsSync(keyFile)
  ? readFileSync(keyFile, 'utf8')
  : '';
export const RELEASE_KID = 'auriga-desktop-release-1';
export const MAX_PACKAGE_BYTES = 4 * 1024 ** 3;

// Same integer-only canonical domain as ResourceSigning (plugin marketplace).
export function canonical(value) {
  if (value === null || typeof value === 'boolean')
    return JSON.stringify(value);
  if (typeof value === 'number' && Number.isSafeInteger(value))
    return String(value);
  if (typeof value === 'string') {
    if (!value.isWellFormed()) throw new Error('invalid Unicode');
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value && typeof value === 'object')
    return (
      '{' +
      Object.keys(value)
        .sort()
        .map((k) => canonical(k) + ':' + canonical(value[k]))
        .join(',') +
      '}'
    );
  throw new Error('outside integer canonical domain');
}

export function verifyRelease(
  envelope,
  {
    publicKey = RELEASE_PUBLIC_KEY,
    now = Date.now(),
    currentVersion = '0.0.0',
    highestSequence = 0,
    highestPublishedAt,
    highestVersion,
    acceptedDigest = null,
  } = {},
) {
  const { signature, ...body } = envelope ?? {};
  if (
    !publicKey ||
    signature?.alg !== 'Ed25519' ||
    signature?.kid !== RELEASE_KID ||
    !/^[A-Za-z0-9_-]{86}$/.test(signature?.value ?? '')
  )
    throw new Error('untrusted release signature');
  const key =
    publicKey?.type === 'public' ? publicKey : createPublicKey(publicKey);
  if (
    key.asymmetricKeyType !== 'ed25519' ||
    !verify(
      null,
      Buffer.from(canonical(body)),
      key,
      Buffer.from(signature.value, 'base64url'),
    )
  )
    throw new Error('invalid release signature');
  const fields = [
    'schema',
    'product',
    'version',
    'packageId',
    'channel',
    'platform',
    'sizeBytes',
    'sha256',
    'minimumVersion',
    'publishedAt',
    'notAfter',
    'sequence',
    'notes',
    'components',
  ];
  if (
    Object.keys(body).length !== fields.length ||
    fields.some((k) => !(k in body)) ||
    body.schema !== 1 ||
    body.product !== 'Auriga Desktop' ||
    body.channel !== 'stable' ||
    body.platform !== 'win-x64' ||
    typeof body.packageId !== 'string' ||
    !/^[A-Za-z0-9.+-]{1,200}$/.test(body.packageId) ||
    typeof body.sha256 !== 'string' ||
    !/^[a-f0-9]{64}$/.test(body.sha256) ||
    !Number.isSafeInteger(body.sizeBytes) ||
    body.sizeBytes < 1 ||
    body.sizeBytes > MAX_PACKAGE_BYTES ||
    !Number.isSafeInteger(body.sequence) ||
    body.sequence < 1 ||
    typeof body.notes !== 'string' ||
    body.notes.length > 8192
  )
    throw new Error('malformed release');
  if (
    typeof body.publishedAt !== 'string' ||
    typeof body.notAfter !== 'string' ||
    !/^\d{4}-\d\d-\d\dT/.test(body.publishedAt) ||
    !/^\d{4}-\d\d-\d\dT/.test(body.notAfter)
  )
    throw new Error('malformed release timestamps');
  if (
    !body.components ||
    Object.keys(body.components).sort().join(',') !==
      'dshVersion,nodeVersion,pluginVersion'
  )
    throw new Error('malformed release components');
  for (const version of Object.values(body.components))
    compare(version, version);
  const published = Date.parse(body.publishedAt),
    expiry = Date.parse(body.notAfter);
  if (
    !Number.isFinite(published) ||
    !Number.isFinite(expiry) ||
    published > now ||
    expiry <= now ||
    expiry <= published
  )
    throw new Error('expired or future release');
  if (
    compare(body.version, currentVersion) < 0 ||
    (highestVersion && compare(body.version, highestVersion) < 0) ||
    compare(currentVersion, body.minimumVersion) < 0
  )
    throw new Error('release downgrade or minimum version');
  if (
    (highestPublishedAt && published < Date.parse(highestPublishedAt)) ||
    body.sequence < highestSequence ||
    (body.sequence === highestSequence && body.sha256 !== acceptedDigest)
  )
    throw new Error('release replay');
  return body;
}
