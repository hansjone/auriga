import { createHash } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { mkdir, readFile, link, rm, open } from 'node:fs/promises';
import { resolve, dirname, basename } from 'node:path';
import { pipeline } from 'node:stream/promises';
import { Transform } from 'node:stream';
import { pathToFileURL } from 'node:url';

export function validate(config, manifest) {
  const origin = new URL(config.forgejo);
  if (
    origin.protocol !== 'https:' ||
    origin.username ||
    origin.password ||
    origin.pathname !== '/' ||
    origin.search ||
    origin.hash
  )
    throw Error('Forgejo must be an HTTPS origin');
  if (!Array.isArray(manifest.artifacts) || !manifest.artifacts.length)
    throw Error('Empty artifact manifest');
  if (config.owner && !/^[a-zA-Z0-9_-]+$/.test(config.owner))
    throw Error('Invalid package owner');
  const seen = new Set();
  for (const a of manifest.artifacts) {
    if (
      !/^[a-zA-Z0-9._+-]+$/.test(a.file ?? '') ||
      a.file === '.' ||
      a.file === '..' ||
      seen.has(a.file) ||
      !/^[a-f0-9]{64}$/.test(a.sha256 ?? '') ||
      !Number.isSafeInteger(a.bytes) ||
      a.bytes < 1 ||
      !/^\/api\/packages\/[^/]+\/generic\/[^/]+\/[^/]+\/[^/]+$/.test(
        a.path ?? '',
      )
    ) {
      throw Error('Invalid artifact manifest');
    }
    const path = a.path.replace('{owner}', config.owner ?? '{owner}');
    if (/[{}]/.test(path)) throw Error('Missing package owner');
    const target = new URL(path, origin);
    if (
      target.origin !== origin.origin ||
      target.pathname !== path ||
      target.search ||
      target.hash ||
      /%|\\/.test(a.path) ||
      a.path.split('/').some((p) => p === '..' || p === '.')
    )
      throw Error('Invalid artifact path');
    seen.add(a.file);
  }
  return origin;
}

export async function download(config, manifest, output, request = fetch) {
  const origin = validate(config, manifest);
  // Validate the entire manifest before making requests or creating output.
  await mkdir(output, { recursive: true });
  const headers = {};
  if (config.tokenEnv) {
    const token = process.env[config.tokenEnv];
    if (!token) throw Error('Missing Forgejo credential');
    headers.Authorization = 'token ' + token;
  }
  for (const a of manifest.artifacts) {
    const dest = resolve(output, a.file);
    if (dirname(dest) !== resolve(output) || basename(dest) !== a.file)
      throw Error('Invalid output path');
    const partial = dest + '.partial';
    const hash = createHash('sha256');
    let bytes = 0;
    let handle;
    try {
      handle = await open(partial, 'wx');
      const response = await request(
        new URL(a.path.replace('{owner}', config.owner ?? ''), origin),
        {
          headers,
          redirect: 'error',
          signal: AbortSignal.timeout(600000),
        },
      );
      if (!response.ok) throw Error('Download HTTP ' + response.status);
      await pipeline(
        response.body,
        new Transform({
          transform(chunk, encoding, callback) {
            bytes += chunk.length;
            if (bytes > a.bytes)
              return callback(Error('Artifact size exceeded'));
            hash.update(chunk);
            callback(null, chunk);
          },
        }),
        createWriteStream(partial, { fd: handle.fd, autoClose: false }),
      );
      await handle.close();
      if (bytes !== a.bytes || hash.digest('hex') !== a.sha256)
        throw Error('Artifact checksum mismatch');
      // Do not silently replace an existing release input.
      await link(partial, dest);
      await rm(partial);
    } catch (error) {
      if (handle) {
        await handle.close().catch(() => {});
        await rm(partial, { force: true });
      }
      throw error;
    }
    console.log(
      JSON.stringify({ file: a.file, bytes, sha256: a.sha256, verified: true }),
    );
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const [configPath, manifestPath, output] = process.argv.slice(2);
  if (!configPath || !manifestPath || !output)
    throw Error('Usage: node download.mjs CONFIG MANIFEST OUTPUT');
  await download(
    JSON.parse(await readFile(configPath, 'utf8')),
    JSON.parse(await readFile(manifestPath, 'utf8')),
    output,
  );
}
