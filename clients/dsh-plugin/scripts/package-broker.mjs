import { cp, mkdir, lstat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { verifyBrokerPackage } from '../src/local/broker-package.js';

/** C owns the artifact build. D copies only a complete verified artifact into
 * the npm package, without a runtime dependency on another repository/worktree.
 * @param {string} source @param {string} [destination] */
export async function packageBroker(
  source,
  destination = fileURLToPath(new URL('../shell-auth/', import.meta.url)),
) {
  const artifact = await verifyBrokerPackage(source);
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
    const current = await verifyBrokerPackage(destination);
    if (JSON.stringify(current.manifest) !== JSON.stringify(artifact.manifest))
      throw new Error('broker artifact version conflict');
    return current.manifest;
  }
  await mkdir(destination, { recursive: true });
  await cp(artifact.base, destination, {
    recursive: true,
    dereference: false,
    errorOnExist: true,
    force: false,
  });
  return (await verifyBrokerPackage(destination)).manifest;
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (!process.argv[2]) throw new Error('C broker artifact directory required');
  await packageBroker(process.argv[2]);
}
