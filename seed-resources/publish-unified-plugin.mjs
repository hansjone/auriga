import { readFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { isIP } from 'node:net';
import { pathToFileURL } from 'node:url';
import { parseStrictJson } from '../clients/dsh-plugin/src/resources/json.js';
import { schemaValid } from '../clients/dsh-plugin/src/resources/schema.js';
import {
  catalogTrim,
  validCatalogHost,
  verifyCatalogPluginArchive,
} from './catalog-plugin-preflight.mjs';

const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const semver =
  /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const validRange = (range) =>
  range === '*' ||
  (range === catalogTrim(range) &&
    !range.includes('\0') &&
    range.split('||').every((branch) =>
      catalogTrim(branch)
        .split(/[ \t\n\v\f\r]+/)
        .every((term) => semver.test(term.replace(/^(>=|<=|>|<|=|\^|~)/, ''))),
    ));

async function verifySeed(payload, rawManifest) {
  if (!rawManifest.length || rawManifest.length > 262144)
    throw new Error('Plugin manifest must be 1..262144 bytes');
  const manifest = parseStrictJson(rawManifest, true);
  if (!schemaValid('package', 'PluginManifest', manifest))
    throw new Error('Invalid plugin manifest');
  if (!manifest.permissions.outboundHosts.every(validCatalogHost))
    throw new Error(
      'Plugin manifest outboundHosts must contain lowercase DNS names or valid numeric IP addresses',
    );
  if (
    manifest.plugin.packageName === 'auriga-dsh' ||
    manifest.plugin.packageName.startsWith('@deepseek-ai/')
  )
    throw new Error('Plugin manifest uses a reserved npm package name');
  if (
    !validRange(manifest.plugin.dshVersionRange) ||
    manifest.runtimes.some(({ versionRange }) => !validRange(versionRange))
  )
    throw new Error(
      'Plugin manifest ranges must use full SemVer (e.g. >=22.0.0); rerun scripts/resource-seed/prepare.mjs',
    );
  const [artifact] = manifest.artifacts;
  if (
    artifact.sizeBytes !== payload.length ||
    artifact.digest !== digest(payload)
  )
    throw new Error('Plugin tgz does not match its manifest size and digest');
  await verifyCatalogPluginArchive(payload, manifest);
  return manifest;
}

const adminEntryError =
  'Seed publishing requires the HTTPS admin origin in domain mode. In IP mode, use /resources/plugins in the browser admin console.';

function domainOrigin(input) {
  let origin;
  try {
    origin = new URL(input);
  } catch {
    throw new Error(adminEntryError);
  }
  if (
    origin.protocol !== 'https:' ||
    isIP(origin.hostname.replace(/^\[|\]$/g, '')) ||
    origin.username ||
    origin.password ||
    origin.pathname !== '/' ||
    origin.search ||
    origin.hash
  )
    throw new Error(adminEntryError);
  return origin;
}

async function checkAdminEntry(origin, publisherToken, send) {
  const get = (path, headers) =>
    send(new URL(path, origin), { method: 'GET', redirect: 'error', headers });
  const page = await get('/resources/plugins');
  if (
    !page.ok ||
    !/^text\/html(?:;|$)/i.test(page.headers.get('content-type') ?? '') ||
    !/<meta name="auriga-access-mode" content="domain"\s*\/?>/.test(
      await page.text(),
    )
  )
    throw new Error(adminEntryError);

  const published =
    '/api/resource/v1/admin/catalog/published?kind=plugin&limit=1';
  // The protected route returns 401 at the admin entry and 403 at the portal.
  // Check it anonymously before sending a bearer to the configured origin.
  const anonymous = await get(published);
  if (anonymous.status !== 401) throw new Error(adminEntryError);
  const authorized = await get(published, {
    Authorization: 'Bearer ' + publisherToken,
  });
  if (
    !authorized.ok ||
    !/^application\/json(?:;|$)/i.test(
      authorized.headers.get('content-type') ?? '',
    ) ||
    !Array.isArray((await authorized.json().catch(() => null))?.items)
  )
    throw new Error(
      `Seed publisher preflight failed (${authorized.status}); use a current admin user access token with publish permission and recent step-up`,
    );
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
  origin = domainOrigin(origin);
  await checkAdminEntry(origin, tokens.publisher, send);
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
  const origin = domainOrigin(process.env.AURIGA_SEED_ORIGIN);
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
