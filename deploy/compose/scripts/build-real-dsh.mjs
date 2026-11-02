#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  lstatSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';
import { buildArgs, selectRepoDigest } from './runtime-image-input.mjs';

// prepare keeps metadata isolated until bootstrap has accepted the installation.
const commandArgs = process.argv.slice(2);
if (commandArgs.filter((arg) => arg === '--local-only').length > 1)
  throw new Error('Duplicate --local-only');
const localOnly = commandArgs.includes('--local-only');
const args = buildArgs(commandArgs.filter((arg) => arg !== '--local-only'));
const root = fileURLToPath(new URL('../../../', import.meta.url));
const nodeImage = process.env.NODE_IMAGE || '';
const tag = `${args.image.fullRepository}:${args.image.tag}`;
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
function contextManifest(directory, prefix = '') {
  const files = [];
  for (const name of readdirSync(directory).sort()) {
    const path = join(directory, name),
      rel = prefix + name;
    const st = lstatSync(path);
    if (st.isSymbolicLink()) throw new Error('Build context links are refused');
    if (st.isDirectory()) files.push(...contextManifest(path, rel + '/'));
    else if (st.isFile())
      files.push({
        path: rel,
        mode: st.mode & 0o777,
        bytes: st.size,
        sha256: createHash('sha256').update(readFileSync(path)).digest('hex'),
      });
    else
      throw new Error('Build context must contain only regular source files');
  }
  return files;
}
let contextInputs;
try {
  for (const path of [
    'deploy/compose/real-dsh/Dockerfile',
    'deploy/compose/real-dsh/package.json',
    'deploy/compose/real-dsh/package-lock.json',
    'deploy/compose/real-dsh/versions.json',
    'deploy/compose/real-dsh/build.mjs',
    'deploy/compose/real-dsh/inputs.mjs',
    'deploy/compose/real-dsh/runtime.mjs',
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
  contextInputs = contextManifest(context);
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
  if (
    JSON.stringify(contextManifest(context)) !== JSON.stringify(contextInputs)
  )
    throw new Error('Build context changed during Docker build');
} finally {
  rmSync(context, { recursive: true, force: true });
}
if (!localOnly) docker(['push', tag]);
const inspected = JSON.parse(docker(['image', 'inspect', tag], true));
const image = localOnly
  ? inspected[0].Id
  : selectRepoDigest(inspected?.[0]?.RepoDigests, args.image.fullRepository);
const checkArgs = [
  'run',
  '--rm',
  '--network=none',
  '--read-only',
  '--cap-drop=ALL',
  '--security-opt=no-new-privileges',
  '--user=200000:200000',
  '--memory=512m',
  '--cpus=1',
  '--pids-limit=128',
  '--entrypoint=node',
];
// Load actual public/native modules as the runtime uid; never start a DSH host.
docker([
  ...checkArgs,
  '--name',
  `auriga-dsh-artifact-check-${process.pid}`,
  image,
  '/opt/fixture/runtime.mjs',
]);
const manifest = JSON.parse(
  docker(
    [
      ...checkArgs,
      '--name',
      `auriga-dsh-manifest-check-${process.pid}`,
      image,
      '-e',
      'process.stdout.write(require("node:fs").readFileSync("/opt/fixture/manifest.json"))',
    ],
    true,
  ),
);
const metadata = args.metadataOut
  ? resolve(args.metadataOut)
  : resolve(root, 'deploy/compose/var/real-dsh.json');
mkdirSync(dirname(metadata), { recursive: true });
writeFileSync(
  metadata,
  JSON.stringify(
    { image, localOnly, nodeImage, contextInputs, ...manifest },
    null,
    2,
  ) + '\n',
);
console.log(`Real DSH built: ${image}\nMetadata: ${metadata}`);
