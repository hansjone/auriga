import { cp, mkdir, lstat } from 'node:fs/promises';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyBrokerPackage } from '../src/local/broker-package.js';

/** C owns the artifact build. D copies only a complete verified artifact into
 * the npm package, without a runtime dependency on another repository/worktree.
 * @param {string} source @param {string} [destination]
 * @param {'desktop'|'managed-linux'} target */
export async function packageBroker(
  source,
  destination = fileURLToPath(new URL('../shell-auth/', import.meta.url)),
  target = 'desktop',
) {
  const artifact = await verifyBrokerPackage(source, target);
  let stat;
  try {
    stat = await lstat(destination);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  if (stat) {
    if (!stat.isDirectory() || stat.isSymbolicLink())
      throw new Error('broker output must be an owned directory');
    // An existing product is verified rather than recursively deleted/replaced.
    const current = await verifyBrokerPackage(destination, target);
    if (JSON.stringify(current.manifest) !== JSON.stringify(artifact.manifest))
      throw new Error('broker artifact version conflict');
    return current.manifest;
  }
  await mkdir(dirname(destination), { recursive: true });
  await cp(artifact.base, destination, {
    recursive: true,
    dereference: false,
    errorOnExist: true,
    force: false,
  });
  return (await verifyBrokerPackage(destination, target)).manifest;
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [source, target, ...extra] = process.argv.slice(2);
  if (
    !source ||
    (target !== undefined && target !== '--managed-linux') ||
    extra.length
  )
    throw new Error('usage: package-broker.mjs <artifact> [--managed-linux]');
  await packageBroker(
    source,
    undefined,
    target === '--managed-linux' ? 'managed-linux' : 'desktop',
  );
}
