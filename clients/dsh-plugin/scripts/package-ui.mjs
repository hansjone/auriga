/** Release preparation only; published installs never run a build or contact the platform. */
import { existsSync, readdirSync, realpathSync } from 'node:fs';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { basename, delimiter, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { packageNetworkTrust } from './package-network-trust.mjs';

function tryRealpath(filePath) {
  try {
    return realpathSync(filePath);
  } catch {
    return filePath;
  }
}

export function resolveViteCli(
  repoRoot = fileURLToPath(new URL('../../../', import.meta.url)),
) {
  const roots = [
    join(repoRoot, 'package.json'),
    join(repoRoot, 'web', 'package.json'),
    join(repoRoot, 'web', 'panel', 'package.json'),
  ];
  const errors = [];
  let vitePkgPath = null;
  for (const base of roots) {
    if (!existsSync(base)) continue;
    try {
      vitePkgPath = tryRealpath(
        createRequire(base).resolve('vite/package.json'),
      );
      break;
    } catch (error) {
      errors.push(`${base}: ${error.message}`);
    }
  }
  const pnpmDirs = [
    join(repoRoot, 'web', 'node_modules', '.pnpm'),
    join(repoRoot, 'web', 'web', 'node_modules', '.pnpm'),
    join(repoRoot, 'node_modules', '.pnpm'),
  ].filter((dir) => existsSync(dir));
  if (!vitePkgPath) {
    for (const pnpmDir of pnpmDirs) {
      for (const entry of readdirSync(pnpmDir, { withFileTypes: true })) {
        if (!entry.isDirectory() || !entry.name.startsWith('vite@')) continue;
        const candidate = join(
          pnpmDir,
          entry.name,
          'node_modules',
          'vite',
          'package.json',
        );
        if (existsSync(candidate)) {
          vitePkgPath = tryRealpath(candidate);
          break;
        }
      }
      if (vitePkgPath) break;
    }
  }
  if (!vitePkgPath)
    throw new Error(`Cannot resolve vite/package.json:\n${errors.join('\n')}`);
  const viteDir = dirname(vitePkgPath);
  const viteBin = join(viteDir, 'bin', 'vite.js');
  const nodePathEntries = [];
  const peerNm = dirname(viteDir);
  if (basename(peerNm) === 'node_modules') nodePathEntries.push(peerNm);
  for (const dir of [
    join(repoRoot, 'web', 'node_modules'),
    join(repoRoot, 'node_modules'),
  ]) {
    if (existsSync(dir)) nodePathEntries.push(dir);
  }
  for (const pnpmDir of pnpmDirs) {
    const hoisted = join(pnpmDir, 'node_modules');
    if (existsSync(hoisted)) nodePathEntries.push(hoisted);
    for (const entry of readdirSync(pnpmDir, { withFileTypes: true })) {
      if (
        !entry.isDirectory() ||
        (!entry.name.startsWith('vite@') && !entry.name.startsWith('rollup@'))
      )
        continue;
      const nm = join(pnpmDir, entry.name, 'node_modules');
      if (existsSync(nm)) nodePathEntries.push(nm);
    }
  }
  const seen = new Set();
  const nodePaths = [];
  for (const dir of nodePathEntries) {
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) continue;
    seen.add(key);
    nodePaths.push(dir);
  }
  return { viteBin, nodePaths };
}

export async function runPackageUi(
  root = new URL('../../../', import.meta.url),
) {
  await packageNetworkTrust();
  const repoRoot = fileURLToPath(root);
  const { viteBin, nodePaths } = resolveViteCli(repoRoot);
  const existingNodePath = process.env.NODE_PATH
    ? process.env.NODE_PATH.split(delimiter).filter(Boolean)
    : [];
  const env = {
    ...process.env,
    NODE_PATH: [...nodePaths, ...existingNodePath].join(delimiter),
  };
  const result = spawnSync(process.execPath, [viteBin, 'build'], {
    cwd: fileURLToPath(new URL('web/panel/', root)),
    env,
    stdio: 'inherit',
  });
  if (result.status !== 0) process.exit(result.status ?? 1);
  const source = await readFile(
    new URL('web/panel/dist/auriga-team-panel.js', root),
  );
  const pkg = JSON.parse(
    await readFile(new URL('web/panel/package.json', root), 'utf8'),
  );
  const output = new URL('../ui/', import.meta.url);
  await mkdir(output, { recursive: true });
  await writeFile(new URL('auriga-team-panel.js', output), source);
  await writeFile(
    new URL('metadata.json', output),
    JSON.stringify({
      source: 'package',
      version: pkg.version,
      integrity:
        'sha384-' + createHash('sha384').update(source).digest('base64'),
    }) + '\n',
  );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  await runPackageUi();
}
