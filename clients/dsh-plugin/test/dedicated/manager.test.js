import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  rmSync,
  readFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test, load } from './public.js';

test('actual rc.3 PluginManager saves bundle selection and reports no-hmr restart without inventing an active module', async (t) => {
  const [{ Context }, { Loader }, { default: Manager }, { initProfile }] =
    await Promise.all([
      load('cordis'),
      load('cordis-plugin-loader'),
      load('dsh-plugin-manager'),
      load('dsh-app-boot'),
    ]);
  const home = mkdtempSync(join(tmpdir(), 'auriga-dedicated-manager-')),
    dir = join(home, 'profile'),
    anchor = join(home, 'package.json');
  const ctx = new Context();
  t.after(async () => {
    await ctx.fiber.dispose();
    rmSync(home, { recursive: true, force: true });
  });
  writeFileSync(
    anchor,
    JSON.stringify({
      name: 'fixture-installation',
      version: '0.2.1-rc.3',
      dependencies: {},
    }),
  );
  initProfile(dir, []);
  const packageDir = join(dir, 'node_modules', 'example-dedicated');
  mkdirSync(packageDir, { recursive: true });
  writeFileSync(
    join(packageDir, 'package.json'),
    JSON.stringify({
      name: 'example-dedicated',
      version: '1.0.0',
      dsh: { bundle: { patch: './cordis.patch.yml' } },
    }),
  );
  writeFileSync(
    join(packageDir, 'cordis.patch.yml'),
    '- id: example-approved\n  name: example-dedicated-module\n',
  );
  writeFileSync(
    join(dir, 'package.json'),
    JSON.stringify({
      name: 'fixture-profile',
      private: true,
      dependencies: { 'example-dedicated': '1.0.0' },
      dsh: { bundles: [] },
    }),
  );
  ctx.provide('profileContext', {
    name: 'fixture',
    home,
    cwd: home,
    dir,
    patchPath: join(dir, 'cordis.patch.yml'),
    installAnchor: anchor,
    overlays: [],
    startedBundles: [],
  });
  new Loader(ctx);
  await ctx.plugin(Manager, {
    pnpmCommand: 'unused-package-process',
    fallbackRegistries: [],
    outputBytes: 4096,
    lockWaitMs: 0,
    inspectTimeoutMs: 1000,
    githubConnectionTimeoutMs: 1000,
    idleTimeoutMs: 1000,
  });
  const result = await ctx.pluginManager.setBundleEnabled(
    'example-dedicated',
    true,
  );
  assert.equal(result.target, 'example-dedicated');
  assert.equal(result.application, 'restart-required', JSON.stringify(result));
  assert.equal(result.restartReason, 'no-hmr');
  const bundle = (await ctx.pluginManager.listBundles()).find(
    (entry) => entry.name === 'example-dedicated',
  );
  assert.equal(bundle.enabled, true);
  assert.equal(bundle.installed, true);
  assert.equal(
    (await ctx.pluginManager.listPlugins()).some(
      (entry) =>
        entry.fiberPhase === 'active' &&
        entry.moduleName === 'example-dedicated-module',
    ),
    false,
  );
  assert.match(
    readFileSync(join(dir, 'package.json'), 'utf8'),
    /example-dedicated/,
  );
});
