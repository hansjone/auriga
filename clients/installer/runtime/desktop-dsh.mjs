/** Boot the signed Desktop composition through the public DSH profile API. */
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { assertLocalPath } from './host-lock.mjs';
import { DESKTOP_CORE_DIR } from './verify.mjs';

const coreName = 'auriga-dsh';
const dir = fileURLToPath(new URL('../../', import.meta.url));
const anchor = join(dir, 'upstream/app/package.json');
const core = join(dir, DESKTOP_CORE_DIR);
assertLocalPath(core);
const require = createRequire(anchor);
const entry = require.resolve(coreName);
if (entry !== join(core, 'src', 'plugin.js'))
  throw new Error('desktop core entry is outside the release');
assertLocalPath(entry);
const coreUrl = pathToFileURL(entry).href;

const fromInstallation = (name) =>
  import(pathToFileURL(require.resolve(name)).href);
const [boot, { runProfile }] = await Promise.all([
  fromInstallation('@deepseek-ai/dsh-app-boot'),
  fromInstallation('@deepseek-ai/dsh/profile-boot'),
]);

const home = resolve(process.env.DSH_HOME);
const profileName = 'auriga';
const profileDir = join(home, 'profiles', profileName);
const [mode, ...args] = process.argv.slice(2);
const pnpm = {
  command: process.execPath,
  args: [join(dir, 'tools/pnpm/node_modules/pnpm/bin/pnpm.cjs')],
  env: { npm_config_store_dir: join(home, 'pnpm-store') },
};

if (mode === 'plugin') {
  const { runPluginCommand } = await fromInstallation(
    '@deepseek-ai/dsh-plugin-manager/operations',
  );
  if (args[0] !== 'remove' || args.includes(coreName))
    throw new Error('unsupported Desktop maintenance operation');
  const result = await runPluginCommand(
    {
      profile: profileName,
      dir: profileDir,
      installAnchor: anchor,
      home,
      cwd: profileDir,
    },
    args,
    { ...pnpm, execution: 'cli', outputBytes: 16384 },
  );
  process.exitCode = result.exitCode ?? 1;
} else {
  if (mode === 'check')
    boot.initProfile(profileDir, [
      '@deepseek-ai/dsh-base',
      '@deepseek-ai/dsh-web-app',
      coreName,
    ]);
  const profile = boot.loadProfileDirectory('dsh', profileDir, anchor);
  const packaged = boot.loadOverlayPatches(
    'dsh',
    join(core, 'cordis.patch.yml'),
  );
  const config = profile.patches
    .filter(
      (patch) =>
        patch.id === coreName &&
        !patch.insert &&
        (!patch.name || patch.name === coreName),
    )
    .findLast((patch) => Object.hasOwn(patch, 'config'))?.config;
  const coreEntries = packaged
    .flatMap((patch) => patch.insert ?? [])
    .filter((row) => row.id === coreName && row.name === coreName);
  if (coreEntries.length !== 1)
    throw new Error('invalid packaged desktop core entry');
  const [coreEntry] = coreEntries;
  coreEntry.name = coreUrl;
  coreEntry.disabled = false;
  if (config !== undefined) coreEntry.config = config;
  packaged.push({
    insert: [
      {
        id: 'auriga-desktop-core-policy',
        name: new URL('./desktop-core-policy.mjs', import.meta.url).href,
        config: { packageName: coreName, moduleUrl: coreUrl },
      },
    ],
  });
  const working = mkdtempSync(join(tmpdir(), 'auriga-core-'));
  const overlay = join(working, 'core.yml');
  let runtime;
  try {
    writeFileSync(overlay, JSON.stringify(packaged), {
      mode: 0o600,
      flag: 'wx',
    });
    runtime = await runProfile({
      environment: boot.loadLayeredEnv('dsh'),
      profile: profileName,
      resolvedProfile: { profile, installAnchor: anchor },
      patchFiles: [overlay],
      args: mode === 'check' ? ['--port', '0', '--no-open'] : args,
      packageManager: pnpm,
    });
  } finally {
    rmSync(working, { recursive: true, force: true });
  }
  if (mode === 'check') {
    try {
      const manager = runtime.ctx.get('pluginManager');
      const bundle = (await manager.listBundles()).find(
        (item) => item.name === coreName,
      );
      const plugins = await manager.listPlugins();
      const mounted = plugins.find((item) => item.moduleName === coreUrl);
      const browser = runtime.ctx.get('clientModules')?.graph().entries;
      const expected = JSON.parse(
        readFileSync(join(core, 'package.json'), 'utf8'),
      ).version;
      if (
        bundle?.availability !== 'installation' ||
        bundle.version !== expected ||
        bundle.removable ||
        bundle.readOnlyReason !== 'management-required' ||
        mounted?.fiberPhase !== 'active' ||
        !browser?.some((item) => item.id === coreName)
      )
        throw new Error(
          'desktop core failed its installation activation check',
        );
      console.log('auriga-core-active');
    } catch (error) {
      process.exitCode = 1;
      throw error;
    } finally {
      await runtime.shutdown.shutdown(process.exitCode ?? 0);
    }
  }
}
