import { join } from 'node:path';

/** Use absolute roots. pnpm records the virtual store relative to modulesDir. */
export function desktopPnpmLayout(profile, store) {
  const modules = join(profile, 'node_modules');
  return [
    '--ignore-workspace',
    '--ignore-pnpmfile',
    '--store-dir',
    store,
    '--config.node-linker=isolated',
    '--config.modules-dir=' + modules,
    '--config.virtual-store-dir=' + join(modules, '.pnpm'),
    '--config.lockfile-dir=' + profile,
    '--config.enable-global-virtual-store=false',
  ];
}
