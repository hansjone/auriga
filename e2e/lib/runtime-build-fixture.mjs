import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  dependencyFiles,
  hashDependencyFile,
  sealDependencies,
  verifyDependencies,
} from '../../deploy/compose/real-dsh/dependencies.mjs';

// Context tests never install these opaque bytes. Only the private synthetic
// checkout gets their matching pin; the repository's approved pins stay intact.
export function matchingOfflineRuntimeInputs(root, directory) {
  mkdirSync(directory, { recursive: true });
  const tarball = join(directory, 'pnpm.tgz');
  const bytes = Buffer.from('synthetic context-only pnpm distribution');
  writeFileSync(tarball, bytes);
  const integrity =
    'sha512-' + createHash('sha512').update(bytes).digest('base64');
  for (const [name, update] of [
    [
      'clients/installer/upstream.json',
      (value) => {
        value.pnpmIntegrity = integrity;
      },
    ],
    [
      'deploy/compose/real-dsh/tooling/package-lock.json',
      (value) => {
        value.packages['node_modules/pnpm'].integrity = integrity;
      },
    ],
  ]) {
    const path = join(root, name);
    const value = JSON.parse(readFileSync(path, 'utf8'));
    update(value);
    writeFileSync(path, JSON.stringify(value));
  }
  const inputs = join(directory, 'runtime-dependencies');
  mkdirSync(inputs);
  for (const [kind, name] of Object.entries(dependencyFiles)) {
    if (kind !== 'manifest')
      writeFileSync(join(inputs, name), 'synthetic context-only ' + name);
  }
  const config = 'sha256:' + 'd'.repeat(64);
  const metadata = sealDependencies(root, inputs, config, 'a'.repeat(64));
  const sha256 = hashDependencyFile(join(inputs, dependencyFiles.manifest));
  verifyDependencies(root, inputs, sha256);
  return { tarball, inputs, sha256, config, metadata };
}
