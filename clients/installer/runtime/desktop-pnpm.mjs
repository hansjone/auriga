/** Package-manager entry for the Desktop-owned, writable plugin profile. */
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertLocalPath } from './host-lock.mjs';
import { desktopPnpmLayout } from './desktop-pnpm-layout.mjs';

const [dataHome, ...args] = process.argv.slice(2);
const profile = resolve(dataHome, 'profiles/auriga');
const store = resolve(dataHome, 'pnpm-store');
const samePath = (left, right) =>
  process.platform === 'win32'
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
if (!samePath(process.cwd(), profile))
  throw new Error('desktop package manager must run in its own profile');
const layout = desktopPnpmLayout(profile, store);

const manifest = join(profile, 'package.json');
const dependencies = () => {
  const value = JSON.parse(readFileSync(manifest, 'utf8'));
  return {
    ...value.dependencies,
    ...value.optionalDependencies,
    ...value.devDependencies,
  };
};
const installing = args[0] === 'add';
const before = installing ? dependencies() : {};
const pnpm = fileURLToPath(
  new URL('../../tools/pnpm/node_modules/pnpm/bin/pnpm.cjs', import.meta.url),
);
const result = spawnSync(process.execPath, [pnpm, ...layout, ...args], {
  cwd: profile,
  stdio: 'inherit',
  windowsHide: true,
});
if (result.error) throw result.error;
if (installing) {
  const after = dependencies();
  const reserved = Object.keys(after).filter((name) => {
    if (before[name] === after[name]) return false;
    if (name === 'auriga-dsh') return true;
    if (!/^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/.test(name))
      return false;
    const packageFile = join(profile, 'node_modules', name, 'package.json');
    return (
      existsSync(packageFile) &&
      JSON.parse(readFileSync(packageFile, 'utf8')).name === 'auriga-dsh'
    );
  });
  if (reserved.length) {
    const modules = join(profile, 'node_modules');
    assertLocalPath(modules);
    for (const name of reserved) {
      const projection = join(modules, name);
      assertLocalPath(dirname(projection));
      rmSync(projection, { recursive: true, force: true });
    }
    // PluginManager owns the manifest/lock transaction. Fail before it accepts
    // the operation or activates a bundle using the release's metadata.
    console.error(
      'Auriga Desktop: auriga-dsh is reserved for the release core.',
    );
    process.exit(1);
  }
}
process.exitCode = result.status ?? 1;
