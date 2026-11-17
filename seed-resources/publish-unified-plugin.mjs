import { readFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';

const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');

export function manifestFor(payload, options) {
  return {
    schemaVersion: 1,
    kind: 'plugin',
    name: options.name ?? 'dsh-memwiki',
    version: options.version,
    description: options.description ?? 'Shared memory wiki for DSH',
    author: { displayName: options.author ?? 'Auriga seed publisher' },
    license: options.license ?? 'MIT',
    permissions: {
      tools: ['memwiki'],
      outboundHosts: [],
      dataAccess: ['workspace_read', 'workspace_write'],
    },
    runtimes: [],
    plugin: {
      packageName: options.packageName ?? 'dsh-memwiki',
      dshVersionRange: options.dshVersionRange ?? '*',
    },
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: digest(payload),
        sizeBytes: payload.length,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
  };
}

// The server enforces distinct current identities, recent step-up and reviewer qualification.
// Tokens are environment inputs only; errors and receipts never contain them.
export async function publishPlugin(
  origin,
  payload,
  options,
  tokens,
  send = fetch,
) {
  const manifest = manifestFor(payload, options);
  const raw = Buffer.from(JSON.stringify(manifest));
  const versionMetadata = {
    version: manifest.version,
    expectedDigest: digest(raw),
  };
  async function call(path, token, body, etag, multipart = false) {
    const headers = {
      Authorization: 'Bearer ' + token,
      'If-Match': etag,
      'Idempotency-Key': randomUUID(),
    };
    if (!multipart) headers['Content-Type'] = 'application/json';
    const response = await send(new URL(path, origin), {
      method: 'POST',
      headers,
      body: multipart ? body : JSON.stringify(body),
      redirect: 'error',
    });
    if (!response.ok) {
      throw new Error(
        `Unified seed request rejected: ${path} (${response.status})`,
      );
    }
    return response.json();
  }
  const etag = (value) => '"' + value.revision + '"';
  const base = '/api/resource/v1/catalog/packages';
  const pack = await call(
    base,
    tokens.author,
    { name: manifest.name, kind: 'plugin', visibility: 'tenant' },
    '"0"',
  );
  const packagePath = base + '/' + pack.id;
  const form = new FormData();
  form.set(
    'metadata',
    new Blob([JSON.stringify(versionMetadata)], { type: 'application/json' }),
  );
  form.set(
    'manifest',
    new Blob([raw], { type: 'application/json' }),
    'auriga-resource.json',
  );
  form.append(
    'artifacts',
    new Blob([payload], { type: 'application/gzip' }),
    'plugin.tgz',
  );
  const version = await call(
    packagePath + '/versions',
    tokens.author,
    form,
    etag(pack),
    true,
  );
  const versionPath = packagePath + '/versions/' + version.id;
  let review = await call(
    versionPath + '/submissions',
    tokens.author,
    { reason: 'Seed submission' },
    etag(version),
  );
  for (const [role, token] of [
    ['general', tokens.general],
    ['security', tokens.security],
  ]) {
    review = await call(
      '/api/resource/v1/admin/catalog/reviews/' +
        review.reviewId +
        '/decisions',
      token,
      {
        decision: 'approve',
        reason: 'Seed artifact reviewed',
        role,
        versionRevision: review.version.revision,
        digest: review.version.digest,
      },
      etag(review),
    );
  }
  const published = await call(
    '/api/resource/v1/admin/catalog/packages/' +
      pack.id +
      '/versions/' +
      version.id +
      '/publications',
    tokens.publisher,
    { reason: 'Publish reviewed seed' },
    etag(review.version),
  );
  return {
    packageId: pack.id,
    versionId: version.id,
    kind: 'plugin',
    state: published.state,
    manifestDigest: digest(raw),
  };
}

async function main() {
  const [archive, version] = process.argv
    .slice(2)
    .filter((arg) => arg !== '--publish');
  if (!archive || !version)
    throw new Error(
      'Usage: node seed-resources/publish-unified-plugin.mjs <plugin.tgz> <version> [--publish]',
    );
  const payload = await readFile(archive);
  if (payload.length < 1 || payload.length > 67108864)
    throw new Error('Plugin tgz must be 1..67108864 bytes');
  const options = {
    version,
    dshVersionRange: process.env.AURIGA_SEED_DSH_RANGE ?? '*',
  };
  if (!process.argv.includes('--publish')) {
    console.log(JSON.stringify(manifestFor(payload, options), null, 2));
    return;
  }
  const origin = new URL(process.env.AURIGA_SEED_ORIGIN);
  if (
    !['http:', 'https:'].includes(origin.protocol) ||
    origin.username ||
    origin.password ||
    origin.pathname !== '/' ||
    origin.search ||
    origin.hash
  )
    throw new Error('AURIGA_SEED_ORIGIN must be a plain HTTP(S) origin');
  const tokens = Object.fromEntries(
    ['author', 'general', 'security', 'publisher'].map((role) => [
      role,
      process.env['AURIGA_SEED_' + role.toUpperCase() + '_TOKEN'],
    ]),
  );
  if (Object.values(tokens).some((token) => !token))
    throw new Error('All four AURIGA_SEED_*_TOKEN variables are required');
  console.log(
    JSON.stringify(await publishPlugin(origin, payload, options, tokens)),
  );
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
