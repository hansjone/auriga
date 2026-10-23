#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';

// prepare keeps metadata isolated until bootstrap has accepted the installation.
const args = process.argv.slice(2);
if (args.length !== 0 && (args.length !== 2 || args[0] !== '--metadata-out' || !args[1])) {
  throw new Error('usage: build-real-dsh.mjs [--metadata-out <file>]');
}
const root = fileURLToPath(new URL('../../../', import.meta.url));
const nodeImage = process.env.NODE_IMAGE || '';
const tag = '127.0.0.1:5000/auriga/real-dsh:w3';
if (!/^node:22\.19\.0-bookworm-slim@sha256:[a-f0-9]{64}$/.test(nodeImage)) {
  throw new Error(
    'Set NODE_IMAGE=node:22.19.0-bookworm-slim@sha256:<reviewed manifest digest>',
  );
}
// Bind to the immutable input recorded in versions.json, not just its shape.
// Registry resolution establishes provenance; independent approval is separate.
const pinned = JSON.parse(
  readFileSync(new URL('../real-dsh/versions.json', import.meta.url), 'utf8'),
).nodeImage;
if (!pinned) {
  throw new Error(
    'real-dsh/versions.json nodeImage is empty: record the base image digest and provenance first',
  );
}
if (nodeImage !== pinned) {
  throw new Error(
    'NODE_IMAGE does not match the pinned real-dsh/versions.json nodeImage',
  );
}
function docker(args, capture = false) {
  const result = spawnSync('docker', args, {
    cwd: root,
    encoding: 'utf8',
    stdio: capture ? 'pipe' : 'inherit',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error || result.status !== 0)
    throw new Error(`docker ${args[0]} failed`);
  return result.stdout?.trim();
}
// Never send .git, var/, credentials or the workspace node_modules junction to
// the Docker daemon. Keep this list aligned with the Dockerfile's COPY inputs.
const context = mkdtempSync(join(tmpdir(), 'auriga-real-dsh-'));
try {
  for (const path of [
    'deploy/compose/real-dsh/Dockerfile',
    'deploy/compose/real-dsh/package.json',
    'deploy/compose/real-dsh/package-lock.json',
    'deploy/compose/real-dsh/versions.json',
    'deploy/compose/real-dsh/build.mjs',
    'deploy/compose/real-dsh/inputs.mjs',
    'deploy/compose/real-dsh/start.mjs',
    'clients/dsh-plugin/package.json',
    'clients/dsh-plugin/package-lock.json',
    'clients/dsh-plugin/LICENSE',
    'clients/dsh-plugin/src',
  ]) {
    const destination = join(context, path);
    mkdirSync(dirname(destination), { recursive: true });
    cpSync(join(root, path), destination, { recursive: true });
  }
  docker([
    'build',
    '--platform=linux/amd64',
    '--provenance=false',
    '--sbom=false',
    '--build-arg',
    `NODE_IMAGE=${nodeImage}`,
    '-f',
    join(context, 'deploy/compose/real-dsh/Dockerfile'),
    '-t',
    tag,
    context,
  ]);
} finally {
  rmSync(context, { recursive: true, force: true });
}
docker(['push', tag]);
const image = JSON.parse(
  docker(['image', 'inspect', tag], true),
)[0].RepoDigests.find((ref) =>
  ref.startsWith('127.0.0.1:5000/auriga/real-dsh@sha256:'),
);
if (!image) throw new Error('Missing immutable pushed image digest');
const manifest = JSON.parse(
  docker(
    [
      'run',
      '--rm',
      '--network=none',
      '--entrypoint=node',
      image,
      '-e',
      'process.stdout.write(require("node:fs").readFileSync("/opt/fixture/manifest.json"))',
    ],
    true,
  ),
);
const metadata = args.length ? resolve(args[1]) : resolve(root, 'deploy/compose/var/real-dsh.json');
mkdirSync(dirname(metadata), { recursive: true });
writeFileSync(
  metadata,
  JSON.stringify({ image, nodeImage, ...manifest }, null, 2) + '\n',
);
console.log(
  `Real DSH built: ${image}\nMetadata: ${metadata}`,
);
