#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  mkdirSync,
  readFileSync,
  readdirSync,
  lstatSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import {
  checkoutFromEntry,
  commonInputs,
  recipePath,
  withRealDshContext,
} from './real-dsh-context.mjs';
import { buildArgs, selectRepoDigest } from './runtime-image-input.mjs';

// prepare keeps metadata isolated until bootstrap has accepted the installation.
const commandArgs = process.argv.slice(2);
if (commandArgs.filter((arg) => arg === '--local-only').length > 1)
  throw new Error('Duplicate --local-only');
const localOnly = commandArgs.includes('--local-only');
const args = buildArgs(commandArgs.filter((arg) => arg !== '--local-only'));
const root = checkoutFromEntry(process.argv[1]);
const nodeImage = process.env.NODE_IMAGE || '';
const tag = `${args.image.fullRepository}:${args.image.tag}`;
if (!/^node:22\.19\.0-bookworm-slim@sha256:[a-f0-9]{64}$/.test(nodeImage)) {
  throw new Error(
    'Set NODE_IMAGE=node:22.19.0-bookworm-slim@sha256:<reviewed manifest digest>',
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
// Keep the whitelist aligned with the Dockerfile's COPY inputs.
const contextInputs = await withRealDshContext(
  root,
  [`${recipePath}/Dockerfile`, ...commonInputs],
  (context) => {
    // Bind to the reviewed versions.json from the same snapshot Docker will use.
    const pinned = JSON.parse(
      readFileSync(join(context, recipePath, 'versions.json'), 'utf8'),
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
    const inputs = contextManifest(context);
    docker([
      'build',
      '--platform=linux/amd64',
      '--provenance=false',
      '--sbom=false',
      '--build-arg',
      `NODE_IMAGE=${nodeImage}`,
      '-f',
      join(context, recipePath, 'Dockerfile'),
      '-t',
      tag,
      context,
    ]);
    if (JSON.stringify(contextManifest(context)) !== JSON.stringify(inputs))
      throw new Error('Build context changed during Docker build');
    return inputs;
  },
);
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
