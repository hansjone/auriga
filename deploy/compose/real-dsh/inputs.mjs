/** Derive the production closure solely from reviewed input versions/integrities. */
export function productionInputs(sourcePackage, sourceLock) {
  const pkg = structuredClone(sourcePackage);
  const lock = structuredClone(sourceLock);
  delete pkg.devDependencies;
  delete pkg.scripts;
  lock.packages[''] = {
    name: pkg.name,
    version: pkg.version,
    dependencies: pkg.dependencies,
  };
  for (const [path, entry] of Object.entries(lock.packages)) {
    if (entry.dev) delete lock.packages[path];
    else if (
      path &&
      (!entry.integrity ||
        !entry.resolved?.startsWith('https://registry.npmjs.org/'))
    ) {
      throw new Error(`Unlocked plugin dependency: ${path}`);
    }
  }
  for (const [name, version] of Object.entries(pkg.dependencies)) {
    if (lock.packages[`node_modules/${name}`]?.version !== version) {
      throw new Error(`Plugin dependency is not pinned: ${name}`);
    }
  }
  return { pkg, lock };
}
