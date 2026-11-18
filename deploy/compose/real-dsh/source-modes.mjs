import { chmodSync, lstatSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

// Apply only to the explicit public inputs, below the private staging root.
export function normalizeSourceModes(path) {
  const stat = lstatSync(path);
  if (stat.isSymbolicLink()) throw new Error('Build context links are refused');
  const directory = stat.isDirectory();
  if (!directory && !stat.isFile())
    throw new Error('Build context must contain only regular source files');
  const mode = directory ? 0o755 : 0o644 | (stat.mode & 0o111);
  if ((stat.mode & 0o7777) !== mode) chmodSync(path, mode);
  if (directory)
    for (const name of readdirSync(path))
      normalizeSourceModes(join(path, name));
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  for (const path of process.argv.slice(2)) normalizeSourceModes(path);
}
