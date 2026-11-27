import { join } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';

/** Use absolute roots. pnpm records the virtual store relative to modulesDir. */
export function desktopPnpmLayout(profile, store) {
  const modules = join(profile, 'node_modules');
  const virtualStore = join(modules, '.pnpm');
  for (const path of [
    profile,
    store,
    join(profile, 'package.json'),
    join(profile, 'pnpm-lock.yaml'),
    join(profile, 'pnpm-workspace.yaml'),
    join(modules, '.modules.yaml'),
    virtualStore,
  ])
    assertLocalPath(path);
  return [
    '--ignore-workspace',
    '--ignore-pnpmfile',
    '--store-dir',
    store,
    '--config.node-linker=isolated',
    '--config.modules-dir=' + modules,
    '--config.virtual-store-dir=' + virtualStore,
    '--config.lockfile-dir=' + profile,
    '--config.enable-global-virtual-store=false',
  ];
}
