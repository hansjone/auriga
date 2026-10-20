#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const nodeImage = process.env.NODE_IMAGE || '';
const tag = '127.0.0.1:5000/auriga/real-dsh:w3';
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
docker([
  'build',
  '--platform=linux/amd64',
  '--build-arg',
  `NODE_IMAGE=${nodeImage}`,
  '-f',
  'deploy/compose/real-dsh/Dockerfile',
  '-t',
  tag,
  '.',
]);
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
const out = resolve(root, 'deploy/compose/var');
mkdirSync(out, { recursive: true });
writeFileSync(
  resolve(out, 'real-dsh.json'),
  JSON.stringify({ image, nodeImage, ...manifest }, null, 2) + '\n',
);
console.log(
  `Real DSH built: ${image}\nMetadata: deploy/compose/var/real-dsh.json`,
);
