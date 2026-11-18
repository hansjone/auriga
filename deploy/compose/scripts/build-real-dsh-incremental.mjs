#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import {
  checkoutFromEntry,
  commonInputs,
  recipePath,
  withRealDshContext,
} from './real-dsh-context.mjs';

const { values } = parseArgs({
  options: {
    'base-runtime-image': { type: 'string' },
    'image-tag': { type: 'string' },
  },
});
const baseImage = values['base-runtime-image'];
const imageTag = values['image-tag'];
if (!baseImage || !imageTag)
  throw new Error(
    'usage: build-real-dsh-incremental.mjs --base-runtime-image <repository@sha256:digest> --image-tag <tag>',
  );

const root = checkoutFromEntry(process.argv[1]);
await withRealDshContext(
  root,
  [
    `${recipePath}/Dockerfile.incremental`,
    `${recipePath}/incremental.mjs`,
    `${recipePath}/incremental-fs.mjs`,
    `${recipePath}/source-modes.mjs`,
    ...commonInputs,
  ],
  async (context, payload) => {
    const recipe = join(payload, recipePath);
    const { assertIncrementalBaseImage } = await import(
      pathToFileURL(join(recipe, 'incremental.mjs')).href
    );
    assertIncrementalBaseImage(baseImage);
    const result = spawnSync(
      'docker',
      [
        'build',
        '--platform=linux/amd64',
        '--provenance=false',
        '--sbom=false',
        '--build-arg',
        `BASE_RUNTIME_IMAGE=${baseImage}`,
        '-f',
        join(recipe, 'Dockerfile.incremental'),
        '-t',
        imageTag,
        context,
      ],
      { cwd: root, stdio: 'inherit' },
    );
    if (result.error || result.status !== 0)
      throw new Error('docker build failed');
  },
  'real-dsh-inputs',
);
