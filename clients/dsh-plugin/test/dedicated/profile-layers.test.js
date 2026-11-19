import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  rmSync,
  realpathSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { test, load, root } from './public.js';
import {
  createDedicatedResourceModuleResolver,
  DEDICATED_PROFILE_DIR,
} from '../../src/dedicated/profile-resolution.js';
import { createRequire } from 'node:module';

function temporary(t, label) {
  const parent = realpathSync(tmpdir()),
    home = realpathSync(mkdtempSync(join(parent, label)));
  assert.ok(home.startsWith(parent + sep));
  t.after(() => rmSync(home, { recursive: true, force: true }));
  return home;
}

test('real public writable profile reads selected bundles after restart and keeps SDK identity through PluginPackages', async (t) => {
  const [{ Context, Service }, { Loader }, { default: Manager }, boot] =
    await Promise.all([
      load('cordis'),
      load('cordis-plugin-loader'),
      load('dsh-plugin-manager'),
      load('dsh-app-boot'),
    ]);
  const home = temporary(t, 'auriga-dedicated-layers-'),
    harness = join(home, '.dsh'),
    dir = join(harness, 'profiles', 'auriga-dedicated');
  boot.initProfile(dir, []);
  const initial = readFileSync(join(dir, 'package.json'), 'utf8');
  boot.initProfile(dir, []);
  assert.equal(readFileSync(join(dir, 'package.json'), 'utf8'), initial);
  const name = '@fixture/dedicated-resource',
    packageDir = join(dir, 'node_modules', name);
  mkdirSync(packageDir, { recursive: true });
  writeFileSync(
    join(packageDir, 'package.json'),
    JSON.stringify({
      name,
      type: 'module',
      version: '1.0.0',
      exports: { '.': './index.js', './package.json': './package.json' },
      dsh: { bundle: { patch: './cordis.patch.yml' } },
    }),
  );
  writeFileSync(
    join(packageDir, 'index.js'),
    "import { Service } from '@deepseek-ai/cordis'; export default class FixtureResource extends Service { constructor(ctx,config){super(ctx,'fixtureResource');this.value=config.value;} sameSdk(value){return value===Service;} }",
  );
  writeFileSync(
    join(packageDir, 'cordis.patch.yml'),
    '- insert:\n    - id: approved-resource\n      name: "@fixture/dedicated-resource"\n      config: {value: bundle}\n',
  );
  const manifest = JSON.parse(initial);
  manifest.dependencies = { [name]: '1.0.0' };
  writeFileSync(join(dir, 'package.json'), JSON.stringify(manifest));
  writeFileSync(
    join(dir, 'cordis.patch.yml'),
    '- id: approved-resource\n  config: {value: profile}\n',
  );
  mkdirSync(harness, { recursive: true });
  writeFileSync(
    join(harness, 'cordis.patch.yml'),
    '- id: approved-resource\n  config: {value: home}\n',
  );
  const profileContext = {
    name: 'auriga-dedicated',
    dir,
    home: harness,
    cwd: home,
    installAnchor: join(root, 'package.json'),
    patchPath: join(dir, 'cordis.patch.yml'),
    overlays: [{ id: 'approved-resource', config: { value: 'overlay' } }],
    startedBundles: [],
  };
  let ctx;
  try {
    ctx = new Context();
    new Loader(ctx);
    ctx.provide('profileContext', profileContext);
    await ctx.plugin(Manager, { fallbackRegistries: [] });
    const enabled = await ctx.pluginManager.setBundleEnabled(name, true);
    assert.equal(
      enabled.application,
      'restart-required',
      JSON.stringify(enabled),
    );
    assert.equal(ctx.get('fixtureResource'), undefined);
    await ctx.fiber.dispose();
    ctx = undefined;
    const selected = boot.loadProfileDirectory(
      'fixture',
      dir,
      profileContext.installAnchor,
    );
    assert.deepEqual(selected.skippedBundles, []);
    assert.deepEqual(
      selected.layers.map((layer) => layer.packageName),
      [name],
    );
    const patches = boot.readProfilePatches(
      'fixture',
      profileContext,
      selected,
    );
    assert.equal(
      boot
        .composeEntries([patches])
        .find((row) => row.id === 'approved-resource').config.value,
      'overlay',
    );
    const resolution = await boot.createRuntimeResolution({
      installAnchor: profileContext.installAnchor,
      profile: selected,
      home: harness,
    });
    assert.ok(
      resolution.localPackageNames.includes(name),
      'direct profile dependencies use the public local package table',
    );
    const resolveModule = createDedicatedResourceModuleResolver({
      requireFrom(anchor) {
        assert.equal(anchor, DEDICATED_PROFILE_DIR + '/package.json');
        return createRequire(join(dir, 'package.json'));
      },
      realpath(path) {
        return path === join(DEDICATED_PROFILE_DIR, 'node_modules')
          ? realpathSync(join(dir, 'node_modules'))
          : realpathSync(path);
      },
    });
    const resolved = boot.composeEntries([patches]).map((row) => ({
      ...row,
      name: resolveModule({
        packageName: name,
        specifier: row.name,
        version: '1.0.0',
      }),
    }));
    const configFile = join(dir, 'cordis.yml');
    writeFileSync(configFile, JSON.stringify(resolved));
    ctx = new Context();
    ctx.baseUrl = pathToFileURL(dir).href + '/';
    await ctx.plugin(Loader);
    ctx.provide('profileContext', {
      ...profileContext,
      startedBundles: [name],
    });
    await ctx.plugin(boot.PluginPackages, { resolution });
    // Only one inert fixture service is mounted. No DSH boot, listener, Agent,
    // model, real package process or Shell is started by this composition.
    await boot.mountRootInclude(
      ctx,
      configFile,
      [],
      pathToFileURL(profileContext.installAnchor).href,
      'fixture',
    );
    await ctx.loader.await();
    await boot.auditStartupEntries(ctx, 'fixture');
    assert.equal(ctx.fixtureResource.value, 'overlay');
    assert.equal(ctx.fixtureResource.sameSdk(Service), true);
    await ctx.plugin(Manager, { fallbackRegistries: [] });
    const bundle = (await ctx.pluginManager.listBundles()).find(
        (row) => row.name === name,
      ),
      plugins = await ctx.pluginManager.listPlugins();
    assert.equal(bundle.installed, true);
    assert.equal(bundle.enabled, true);
    assert.ok(bundle.rows.length > 0);
    for (const row of bundle.rows)
      assert.ok(
        plugins.some(
          (plugin) =>
            plugin.entryId === row.entryId &&
            plugin.enabled &&
            plugin.fiberPhase === 'active' &&
            plugin.moduleName === resolved[0].name,
        ),
        'bundle declaration and actual normalized Loader entry must observe the same live fiber',
      );
    const disabled = await ctx.pluginManager.setBundleEnabled(name, false);
    assert.equal(disabled.application, 'restart-required');
    assert.equal(
      ctx.fixtureResource.value,
      'overlay',
      'disk selection alone does not unload a live native module',
    );
    await ctx.fiber.dispose();
    ctx = undefined;
    const next = boot.loadProfileDirectory(
      'fixture',
      dir,
      profileContext.installAnchor,
    );
    assert.equal(next.layers.length, 0);
    assert.equal(
      boot.composeEntries([
        boot.readProfilePatches('fixture', profileContext, next),
      ]).length,
      0,
    );
    const invalid = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
    invalid.dsh.profile.bundles = ['@fixture/missing'];
    writeFileSync(join(dir, 'package.json'), JSON.stringify(invalid));
    assert.equal(
      boot.loadProfileDirectory('fixture', dir, profileContext.installAnchor)
        .skippedBundles.length,
      1,
      'Node must reject the explicit skipped-bundle diagnostic',
    );
  } finally {
    await ctx?.fiber.dispose();
  }
});

test('real private filesystem Skill provider loads only the explicit quota fixture home with includeDefaultRoots enabled', async (t) => {
  const [{ Context }, { SkillRegistry }, filesystem] = await Promise.all([
    load('cordis'),
    load('dsh-skill'),
    load('dsh-skill-filesystem'),
  ]);
  const home = temporary(t, 'auriga-dedicated-private-'),
    project = join(home, 'workspace'),
    agentsHome = join(home, '.agents'),
    dshHome = join(home, '.dsh');
  mkdirSync(join(project, '.git'), { recursive: true });
  const skillDir = join(agentsHome, 'skills', 'private-fixture');
  mkdirSync(skillDir, { recursive: true });
  writeFileSync(
    join(skillDir, 'SKILL.md'),
    '---\nname: private-fixture\ndescription: Synthetic private fixture\n---\nPrivate fixture body.\n',
  );
  const ctx = new Context();
  try {
    new SkillRegistry(ctx);
    await ctx.plugin(filesystem, {
      providerName: 'auriga-dedicated-local',
      includeDefaultRoots: true,
      dshHome,
      agentsHome,
      customSkillDirs: [],
      watch: false,
      watchFollowSymlinks: false,
    });
    const skill = await ctx.skills.get('private-fixture', { cwd: project });
    assert.equal(skill.provider, 'auriga-dedicated-local');
    assert.match(skill.content, /Private fixture body/);
    assert.equal(resolve(skill.resourceBase.path), resolve(skillDir));
  } finally {
    await ctx.fiber.dispose();
  }
});

test('actual Shell, sandbox, Jobs and tool providers assemble lazily without running a Shell or native containment probe', async (t) => {
  const home = temporary(t, 'auriga-dedicated-providers-');
  const modules = new Map(
    await Promise.all(
      [
        'cordis',
        'dsh-system-prompt',
        'dsh-session',
        'dsh-session-projection',
        'dsh-working-directory',
        'dsh-tools',
        'dsh-shell-env',
        'dsh-subprocess-local',
        'dsh-sandbox-local',
        'dsh-sandbox-policy',
        'dsh-fs-sandbox',
        'dsh-bash-sandbox',
        'dsh-user-approval',
        'dsh-permission-presets',
        'dsh-jobs-local',
        'dsh-tool-jobs',
        'dsh-tool-bash',
      ].map(async (name) => [name, await load(name)]),
    ),
  );
  const { Context } = modules.get('cordis'),
    ctx = new Context();
  try {
    for (const [name, config] of [
      ['dsh-system-prompt', {}],
      ['dsh-session', {}],
      ['dsh-session-projection', {}],
      ['dsh-working-directory', { defaultDirectory: home }],
      ['dsh-tools', { mode: 'native' }],
      ['dsh-shell-env', {}],
      ['dsh-subprocess-local', {}],
      [
        'dsh-sandbox-local',
        {
          runnerCommand: [],
          runnerFailureSignatures: [],
          probeTimeoutMs: 5000,
        },
      ],
      ['dsh-sandbox-policy', { mode: 'workspace-write', workspaceRoot: home }],
      ['dsh-fs-sandbox', { cwd: home }],
      ['dsh-bash-sandbox', { cwd: home, timeoutMs: 60000 }],
      ['dsh-user-approval', { policy: 'ask' }],
      ['dsh-permission-presets', {}],
      ['dsh-jobs-local', { maxConcurrentJobsPerOwner: 4 }],
      ['dsh-tool-jobs', { completionDelivery: 'quiet' }],
      [
        'dsh-tool-bash',
        { enableRunInBackground: true, promoteOnTimeout: true },
      ],
    ]) {
      const module = modules.get(name);
      await ctx.plugin(module.default ?? module, config);
    }
    assert.equal(ctx.shell.sandboxMode, 'workspace-write');
    assert.equal(ctx.fs.sandboxMode, 'workspace-write');
    assert.equal(typeof ctx.subprocess.spawn, 'function');
    assert.equal(typeof ctx.shell.execute, 'function');
    assert.ok(ctx.tools.get('bash'));
    assert.ok(ctx.tools.get('job_list'));
    assert.ok(ctx.tools.get('job_kill'));
    const id = ctx.jobs.start({
      kind: 'fixture',
      label: 'inert lifecycle fixture',
      run: () => ({
        done: Promise.resolve({ status: 'completed' }),
        cancel() {},
      }),
    });
    assert.equal(
      (await ctx.jobs.wait(id, 1000)).status,
      'completed',
      'actual tool-jobs mounted a registry controller',
    );
  } finally {
    await ctx.fiber.dispose();
  }
});
