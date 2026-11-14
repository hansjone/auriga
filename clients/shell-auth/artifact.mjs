import {
  readFileSync,
  mkdirSync,
  copyFileSync,
  readdirSync,
  existsSync,
} from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import { safePath } from './vault.mjs';

// Consume one producer artifact. Never rebuild a missing or mismatched helper.
export function copyBrokerArtifact(source, output) {
  if (!source) throw new Error('broker artifact required');
  source = safePath(source);
  output = safePath(output);
  if (resolve(source) === resolve(output))
    throw new Error('distinct broker paths required');
  if (existsSync(output) && readdirSync(output).length)
    throw new Error('empty broker output required');
  const manifestBytes = readFileSync(
    safePath(join(source, 'broker-manifest.json')),
  );
  const manifest = JSON.parse(manifestBytes);
  if (
    manifest.version !== 1 ||
    !Array.isArray(manifest.files) ||
    !['windows/shell-helper.exe', 'desktop.mjs', 'broker.mjs'].every((path) =>
      manifest.files.some((file) => file.path === path),
    )
  )
    throw new Error('incomplete broker artifact');
  const seen = new Set();
  const files = manifest.files.map(({ path, sha256 }) => {
    if (
      typeof path !== 'string' ||
      !/^[a-zA-Z0-9_./-]+$/.test(path) ||
      path.split('/').some((part) => !part || part === '.' || part === '..') ||
      path.startsWith('/') ||
      seen.has(path) ||
      path === 'broker-manifest.json'
    )
      throw new Error('invalid broker artifact path');
    seen.add(path);
    const bytes = readFileSync(safePath(join(source, path)));
    if (createHash('sha256').update(bytes).digest('hex') !== sha256)
      throw new Error('broker artifact digest mismatch');
    return path;
  });
  // Reject unlisted files too: a package must be exactly this manifest closure.
  const walk = (directory, prefix = '') =>
    readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
      const path = prefix + entry.name;
      safePath(join(source, path));
      return entry.isDirectory()
        ? walk(join(source, path), path + '/')
        : [path];
    });
  if (
    walk(source).some(
      (path) => path !== 'broker-manifest.json' && !seen.has(path),
    )
  )
    throw new Error('unlisted broker artifact file');
  for (const path of [...files, 'broker-manifest.json']) {
    const target = safePath(join(output, path));
    mkdirSync(dirname(target), { recursive: true });
    copyFileSync(join(source, path), target);
  }
  return manifest;
}
