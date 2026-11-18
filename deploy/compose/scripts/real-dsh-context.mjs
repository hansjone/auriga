import {
  cpSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const pluginSource = 'clients/dsh-plugin/src';
export const recipePath = 'deploy/compose/real-dsh';
const modeScript = `${recipePath}/source-modes.mjs`;
export const commonInputs = [
  `${recipePath}/package.json`,
  `${recipePath}/package-lock.json`,
  `${recipePath}/versions.json`,
  `${recipePath}/build.mjs`,
  `${recipePath}/inputs.mjs`,
  `${recipePath}/runtime.mjs`,
  `${recipePath}/start.mjs`,
  'clients/dsh-plugin/package.json',
  'clients/dsh-plugin/package-lock.json',
  'clients/dsh-plugin/LICENSE',
  'clients/dsh-plugin/cordis.patch.yml',
  pluginSource,
];

export function checkoutFromEntry(entry) {
  // Keep the lexical CLI path so Node's module realpath cannot hide a root link.
  return resolve(dirname(entry), '../../..');
}

function sourceStat(path) {
  const stat = lstatSync(path);
  if (stat.isSymbolicLink())
    throw new Error(`Build source link refused: ${path}`);
  return stat;
}

function sourcePath(root, relative, directory = false) {
  if (!sourceStat(root).isDirectory())
    throw new Error(`Build source root must be a directory: ${root}`);
  const parts = relative.split('/');
  let path = root;
  for (const [index, part] of parts.entries()) {
    path = join(path, part);
    const stat = sourceStat(path);
    const needsDirectory = index < parts.length - 1 || directory;
    if (needsDirectory ? !stat.isDirectory() : !stat.isFile())
      throw new Error(`Unexpected build source type: ${path}`);
  }
  return path;
}

function checkSourceTree(directory) {
  for (const name of readdirSync(directory)) {
    const path = join(directory, name);
    const stat = sourceStat(path);
    if (stat.isDirectory()) checkSourceTree(path);
    else if (!stat.isFile())
      throw new Error(`Unexpected build source type: ${path}`);
  }
}

// Check checkout paths before copying any bytes. Only the listed inputs go to Docker.
export async function withRealDshContext(root, inputs, build, prefix = '') {
  const modeModule = sourcePath(root, modeScript);
  const { normalizeSourceModes } = await import(pathToFileURL(modeModule).href);
  const sources = inputs.map((relative) => {
    const directory = relative === pluginSource;
    const path = sourcePath(root, relative, directory);
    if (directory) checkSourceTree(path);
    return { relative, path, directory };
  });

  const context = mkdtempSync(join(tmpdir(), 'auriga-real-dsh-'));
  try {
    const payload = join(context, prefix);
    for (const { relative, path, directory } of sources) {
      const destination = join(payload, relative);
      mkdirSync(dirname(destination), { recursive: true });
      cpSync(path, destination, { recursive: directory });
      // Docker COPY carries permission bits from the staged public inputs.
      normalizeSourceModes(destination);
    }
    return await build(context, payload);
  } finally {
    rmSync(context, { recursive: true, force: true });
  }
}
