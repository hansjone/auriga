import { readFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { validateArchive } from '../clients/dsh-plugin/src/resources/archive.js';
import { parseStrictJson } from '../clients/dsh-plugin/src/resources/json.js';
import { schemaValid } from '../clients/dsh-plugin/src/resources/schema.js';

const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');

async function verifySeed(payload, rawManifest) {
  if (!rawManifest.length || rawManifest.length > 262144)
    throw new Error('Plugin manifest must be 1..262144 bytes');
  const manifest = parseStrictJson(rawManifest, true);
  if (!schemaValid('package', 'PluginManifest', manifest))
    throw new Error('Invalid plugin manifest');
  const [artifact] = manifest.artifacts;
  if (
    artifact.sizeBytes !== payload.length ||
    artifact.digest !== digest(payload)
  )
    throw new Error('Plugin tgz does not match its manifest size and digest');
  await validateArchive(payload, {
    name: manifest.plugin.packageName,
    version: manifest.version,
  });
  return manifest;
}

// The server enforces distinct current identities, recent step-up and reviewer qualification.
// Tokens are environment inputs only; errors and receipts never contain them.
export async function publishPlugin(
  origin,
  payload,
  rawManifest,
  tokens,
  send = globalThis.fetch,
) {
  const manifest = await verifySeed(payload, rawManifest);
  const manifestDigest = digest(rawManifest);
  const versionMetadata = {
    version: manifest.version,
    expectedDigest: manifestDigest,
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
  const form = new globalThis.FormData();
  form.set(
    'metadata',
    new globalThis.Blob([JSON.stringify(versionMetadata)], {
      type: 'application/json',
    }),
  );
  form.set(
    'manifest',
    new globalThis.Blob([rawManifest], { type: 'application/json' }),
    'auriga-resource.json',
  );
  form.append(
    'artifacts',
    new globalThis.Blob([payload], { type: 'application/gzip' }),
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
        digest: manifestDigest,
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
    manifestDigest,
    artifactDigest: manifest.artifacts[0].digest,
  };
}

async function main() {
  const [archive, manifestPath, mode, ...extra] = process.argv.slice(2);
  if (
    !archive ||
    !manifestPath ||
    (mode && mode !== '--publish') ||
    extra.length
  )
    throw new Error(
      'Usage: node seed-resources/publish-unified-plugin.mjs <plugin.tgz> <manifest.json> [--publish]',
    );
  const [payload, rawManifest] = await Promise.all([
    readFile(archive),
    readFile(manifestPath),
  ]);
  if (!mode) {
    await verifySeed(payload, rawManifest);
    process.stdout.write(rawManifest);
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
    JSON.stringify(await publishPlugin(origin, payload, rawManifest, tokens)),
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
