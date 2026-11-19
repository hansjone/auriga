/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, realpathSync, statSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseAllDocuments, visit, isAlias } from 'yaml';
import { root } from './public.js';
import {
  DEDICATED_MODULE,
  DEDICATED_SDK_PACKAGES,
  DEDICATED_LAUNCHER_PACKAGES,
  DEDICATED_PROFILE_DIR,
  SDK_INSTALLATION_ANCHOR,
  assertDedicatedRuntimeProfile,
  assertDedicatedBaselinePreserved,
  createDedicatedInstalledSdkResolver,
  createDedicatedProfileResolver,
  createDedicatedResourceModuleResolver,
  createDedicatedResourceProfileResolver,
} from '../../src/dedicated/profile-resolution.js';

const docs = parseAllDocuments(
  readFileSync(
    new URL(
      '../../../../deploy/nas/dedicated-runtime-profile.example.yml',
      import.meta.url,
    ),
    'utf8',
  ),
);
assert.equal(docs.length, 1);
assert.deepEqual(docs[0].errors, []);
const rows = docs[0].toJS({ maxAliasCount: 0 }),
  byName = new Map(rows.map((row) => [row.name, row]));
const pkg = (short) => '@deepseek-ai/dsh-' + short;
const require = root && createRequire(join(root, 'package.json'));
const load = (name) => import(pathToFileURL(require.resolve(name)).href);
const skip = !root && 'requires the locked rc.3 public SDK';
const aurigaRoot = process.env.AURIGA_PROFILE_AURIGA_PACKAGE_ROOT;
const installedResolver = () =>
  createDedicatedInstalledSdkResolver({
    requireFrom(anchor) {
      assert.equal(anchor, SDK_INSTALLATION_ANCHOR);
      return require;
    },
    realpath(path) {
      return path === dirname(SDK_INSTALLATION_ANCHOR)
        ? realpathSync(root)
        : realpathSync(path);
    },
  });

test('sealed dedicated recipe has real native roles, one immutable Auriga entry, quota paths and no executable YAML', () => {
  visit(docs[0], (_key, node) => {
    assert.equal(isAlias(node), false);
    assert.notEqual(node?.tag, 'tag:yaml.org,2002:js');
    assert.notEqual(node?.tag, 'tag:yaml.org,2002:merge');
  });
  assert.equal(rows.length, 83);
  assert.equal(DEDICATED_SDK_PACKAGES.length, 82);
  assertDedicatedRuntimeProfile(rows);
  assert.deepEqual(
    rows.slice(0, -1).map((row) => row.name),
    DEDICATED_SDK_PACKAGES,
  );
  assert.equal(rows.at(-1).name, DEDICATED_MODULE);
  assert.equal(rows.at(-1).id, 'auriga-dsh');
  for (const denied of [
    'base',
    'web-app',
    'config-editor',
    'settings',
    'hmr',
    'llm-deepseek-api-key',
    'llm-pi-ai',
  ])
    assert.equal(byName.has(pkg(denied)), false);
  for (const required of [
    'plugin-manager',
    'skill-filesystem',
    'jobs-local',
    'tool-jobs',
    'bash-sandbox',
    'sandbox-local',
    'sandbox-policy',
    'subprocess-local',
    'api-job-controller',
  ])
    assert.ok(byName.has(pkg(required)));
  assert.equal(
    byName.get(pkg('skill-filesystem')).config.includeDefaultRoots,
    true,
  );
});

test('baseline rejects missing providers, mode/owner/config escape and budget changes while allowing only trusted renderer slots', () => {
  for (const mutate of [
    (copy) =>
      copy.splice(
        copy.findIndex((row) => row.id === 'shell'),
        1,
      ),
    (copy) =>
      (copy.find((row) => row.id === 'jobs').config.maxConcurrentJobsPerOwner =
        100),
    (copy) => (copy.find((row) => row.id === 'tools').config.mode = 'ptc'),
    (copy) =>
      copy
        .find((row) => row.id === 'private-skills')
        .config.customSkillDirs.push('/tmp/untrusted'),
    (copy) =>
      (copy.find((row) => row.id === 'private-skills').config.bundledSkillDir =
        '/tmp/untrusted'),
    (copy) => (copy.at(-1).name = 'auriga-dsh'),
    (copy) => (copy.at(-1).name = 'file:///run/auriga/plugin.js'),
    (copy) => (copy.at(-1).config.owner = true),
    (copy) => (copy[0].config.cwd = '/tmp/unquota'),
    (copy) => (copy[1].config = { __jsExpr: 'process.env.SDK' }),
    (copy) => copy.push({ id: 'extra', name: pkg('config-editor') }),
  ]) {
    const copy = structuredClone(rows);
    mutate(copy);
    assert.throws(() => assertDedicatedRuntimeProfile(copy), /rejected/);
  }
  const rendered = structuredClone(rows);
  rendered.at(-1).config.configFile =
    '/var/lib/auriga/pools/11111111-2222-4333-8444-555555555555/control/dedicated-host.json';
  rendered.find((row) => row.id === 'webserver').config = {
    host: '0.0.0.0',
    port: 45678,
  };
  rendered.find((row) => row.id === 'default-model').config.model =
    'approved-platform-route';
  assertDedicatedRuntimeProfile(rendered);
});

test('SDK resolver refuses alias, escaping paths, unapproved packages and divergent exports', () => {
  const base = resolve('/fixture/sdk'),
    file = join(
      base,
      'node_modules/@deepseek-ai/dsh-bash-sandbox/lib/index.js',
    );
  const standard = {
    name: pkg('bash-sandbox'),
    exports: {
      '.': { types: './lib/types/index.d.ts', default: './lib/index.js' },
    },
  };
  const make = (manifest, target = file) =>
    createDedicatedInstalledSdkResolver({
      requireFrom: () => ({ resolve: () => target }),
      realpath: (path) =>
        path === dirname(SDK_INSTALLATION_ANCHOR) ? base : path,
      readFile: (path) => {
        if (path.endsWith(join('lib', 'package.json')))
          throw Object.assign(new Error('missing'), { code: 'ENOENT' });
        return JSON.stringify(manifest);
      },
    });
  assert.throws(() => make(standard)(pkg('base')), /rejected/);
  assert.throws(() => make(standard)('file:///run/evil.js'), /rejected/);
  assert.throws(
    () => make({ ...standard, name: pkg('bash-local') })(pkg('bash-sandbox')),
    /rejected/,
  );
  assert.throws(
    () => make(standard, resolve('/outside/index.js'))(pkg('bash-sandbox')),
    /rejected/,
  );
  assert.throws(
    () =>
      make({
        ...standard,
        exports: { '.': { require: './other.cjs', import: './lib/index.js' } },
      })(pkg('bash-sandbox')),
    /rejected/,
  );
  assert.throws(
    () =>
      make({ ...standard, exports: { '.': '../outside.js' } })(
        pkg('bash-sandbox'),
      ),
    /rejected/,
  );
});

const services = {
  fs: 'fs-sandbox',
  llm: 'llm',
  systemPrompt: 'system-prompt',
  sessions: 'session',
  sessionProjections: 'session-projection',
  sessionPersistence: 'session-persistence-jsonl',
  sessionQuery: 'session-query',
  storage: 'storage',
  storageDomain: 'storage-domain',
  workingDirectory: 'working-directory',
  agents: 'agent',
  tools: 'tools',
  agentPresets: 'agent-preset-registry',
  agentDefaultModel: 'agent-default-model',
  skills: 'skill',
  shellEnv: 'shell-env',
  commands: 'commands',
  credentials: 'credentials-local',
  connection: 'client-connection',
  attachments: 'attachment-local',
  fileUploads: 'client-file-upload',
  fileReferences: 'file-reference-local',
  workspaceRegistry: 'workspace',
  directoryPicker: 'host-directory-picker-browse',
  remoteAccess: 'api-remote-access',
  typert: 'typert-registry',
  typertGateway: 'api-gateway',
  webServer: 'host-webserver',
  clientModules: 'client-modules',
  pluginManager: 'plugin-manager',
  subprocess: 'subprocess-local',
  sandbox: 'sandbox-local',
  sandboxPolicy: 'sandbox-policy',
  shell: 'bash-sandbox',
  approval: 'user-approval',
  permissionPresets: 'permission-presets',
  jobs: 'jobs-local',
  jobController: 'api-job-controller',
};
test(
  'all 82 locked public modules accept the exact config and required native inject providers close',
  { skip },
  async () => {
    for (const row of rows.slice(0, -1)) {
      const manifest = JSON.parse(
        readFileSync(require.resolve(row.name + '/package.json'), 'utf8'),
      );
      assert.equal(manifest.version, '0.2.1-rc.3', row.name);
      const module = await load(row.name),
        plugin = module.default ?? module;
      assert.ok(
        typeof plugin === 'function' || typeof plugin.apply === 'function',
        row.name,
      );
      if (plugin.Config)
        assert.doesNotThrow(() => plugin.Config(row.config ?? {}), row.name);
      for (const dependency of Array.isArray(plugin.inject)
        ? plugin.inject
        : Object.keys(plugin.inject?.required ?? {})) {
        if (['loader', 'profileContext'].includes(dependency)) continue;
        assert.ok(
          services[dependency] && byName.has(pkg(services[dependency])),
          row.name + ' missing required ' + dependency,
        );
      }
    }
  },
);

test(
  'full native client roster closes declared dependencies and readonly bundle assets',
  { skip },
  () => {
    const frontend = dirname(
      require.resolve(pkg('web-frontend') + '/package.json'),
    );
    const code = readdirSync(join(frontend, 'dist/assets'))
      .filter((file) => file.endsWith('.js'))
      .map((file) => readFileSync(join(frontend, 'dist/assets', file), 'utf8'))
      .find((code) =>
        code.includes('"@deepseek-ai/dsh-client-ui-primitives":'),
      );
    assert.ok(
      code,
      'locked frontend must really provide its builtin primitives',
    );
    const staticFactory = code.match(
      /function ([A-Za-z_$][\w$]*)\(\)\{return\{[^{}]*"@deepseek-ai\/dsh-client-ui-primitives":[^{}]*\}\}/,
    );
    assert.ok(staticFactory);
    assert.ok(code.includes('staticModules:' + staticFactory[1] + '()'));
    const builtins = new Set(
      [...staticFactory[0].matchAll(/"(@deepseek-ai\/[^" ]+)":/g)].map(
        (match) => match[1],
      ),
    );
    for (const row of rows.slice(0, -1)) {
      const path = require.resolve(row.name + '/package.json'),
        manifest = JSON.parse(readFileSync(path, 'utf8')),
        client = manifest.dsh?.client;
      if (!client || client.platform !== 'web') continue;
      for (const dependency of client.inject ?? [])
        assert.ok(
          byName.has(dependency) || builtins.has(dependency),
          row.name + ' missing client ' + dependency,
        );
      const exported = manifest.exports['./client'],
        target = typeof exported === 'string' ? exported : exported.default;
      assert.ok(statSync(resolve(dirname(path), target)).isFile(), row.name);
    }
  },
);

test(
  'all normalized SDK roots retain public class identity and actual ClientModuleRegistry metadata below /run',
  { skip },
  async () => {
    const normalize = createDedicatedProfileResolver({
        resolveModule: installedResolver(),
      }),
      normalized = normalize(rows);
    const [{ ClientModuleRegistry }, { ModuleLoader }] = await Promise.all([
      load(pkg('client-modules')),
      load('@deepseek-ai/cordis-plugin-loader'),
    ]);
    const registry = Object.create(ClientModuleRegistry.prototype);
    registry.ctx = { loader: { internal: ModuleLoader.fromInternal() } };
    registry.pkgMeta = new Map();
    assert.ok(
      registry.ctx.loader.internal?.resolveSync,
      'requires --expose-internals',
    );
    for (const [index, row] of rows.slice(0, -1).entries()) {
      assert.equal(await import(normalized[index].name), await load(row.name));
      assert.deepEqual(
        registry.resolveMeta(
          normalized[index].name,
          'file:///run/auriga/dedicated-profile.yml',
        ),
        registry.resolveMeta(
          row.name,
          pathToFileURL(join(root, 'package.json')).href,
        ),
      );
    }
    assert.equal(Object.isFrozen(normalized), true);
    assert.equal(Object.isFrozen(normalized[0].config), true);
    for (const name of DEDICATED_LAUNCHER_PACKAGES)
      assert.equal(await import(installedResolver()(name)), await load(name));
  },
);

test(
  'dedicated exact file entry discovers Auriga client while the bare subpath does not',
  {
    skip:
      skip || (!aurigaRoot && 'requires the current integrated Auriga package'),
  },
  async () => {
    const [{ ClientModuleRegistry }, { ModuleLoader }] = await Promise.all([
      load(pkg('client-modules')),
      load('@deepseek-ai/cordis-plugin-loader'),
    ]);
    const registry = Object.create(ClientModuleRegistry.prototype);
    registry.ctx = { loader: { internal: ModuleLoader.fromInternal() } };
    registry.pkgMeta = new Map();
    const manifest = JSON.parse(
      readFileSync(join(aurigaRoot, 'package.json'), 'utf8'),
    );
    assert.equal(manifest.exports['./dedicated'], './src/dedicated/plugin.js');
    const file = pathToFileURL(
      resolve(aurigaRoot, manifest.exports['./dedicated']),
    ).href;
    assert.equal(
      registry.resolveMeta(
        'auriga-dsh/dedicated',
        'file:///run/auriga/dedicated-profile.yml',
      ),
      null,
    );
    const meta = registry.resolveMeta(
      file,
      'file:///run/auriga/dedicated-profile.yml',
    );
    assert.equal(meta.packageName, 'auriga-dsh');
    assert.equal(meta.meta.clientPath, join(aurigaRoot, 'src/client.js'));
  },
);

test('public bundle composition cannot rewrite protected baseline roles or introduce a second SDK/Auriga provider', () => {
  const baseline = createDedicatedProfileResolver({
    resolveModule: (name) =>
      'file:///fixture/sdk/' + name.split('/').at(-1) + '.js',
  })(rows);
  assertDedicatedBaselinePreserved(
    baseline,
    JSON.parse(JSON.stringify(baseline)),
  );
  for (const mutate of [
    (copy) => copy.pop(),
    (copy) => (copy[0].config.cwd = '/tmp'),
    (copy) => copy.push({ ...copy[0], id: 'second-fs' }),
    (copy) => copy.push({ id: 'owner', name: 'auriga-dsh' }),
    (copy) => copy.push({ id: 'new-sdk', name: pkg('config-editor') }),
  ]) {
    const copy = JSON.parse(JSON.stringify(baseline));
    mutate(copy);
    assert.throws(
      () => assertDedicatedBaselinePreserved(baseline, copy),
      /rejected/,
    );
  }
  assertDedicatedBaselinePreserved(baseline, [
    ...baseline,
    { id: 'node-approved-resource', name: '@fixture/resource' },
  ]);
});

test('selected resource rows normalize only exact Node-approved module projections without admitting arbitrary file URLs', () => {
  const baseline = createDedicatedProfileResolver({
    resolveModule: (name) =>
      'file:///fixture/sdk/' + name.split('/').at(-1) + '.js',
  })(rows);
  const approval = {
      id: 'approved-resource',
      packageName: '@fixture/resource',
      specifier: '@fixture/resource/module',
      version: '1.0.0',
    },
    file = 'file:///fixture/profile/node_modules/@fixture/resource/module.js';
  const normalize = createDedicatedResourceProfileResolver({
    resolveModule: (value) => {
      assert.deepEqual(value, approval);
      return file;
    },
  });
  const result = normalize(
    baseline,
    [
      ...baseline,
      { id: approval.id, name: approval.specifier, config: { safe: true } },
    ],
    [approval],
  );
  assert.equal(result.at(-1).name, file);
  assert.equal(Object.isFrozen(result), true);
  assert.equal(normalize(baseline, result, [approval]).at(-1).name, file);
  assert.throws(
    () =>
      normalize(
        baseline,
        [...baseline, { id: approval.id, name: 'file:///run/evil.js' }],
        [approval],
      ),
    /rejected/,
  );
  assert.throws(
    () =>
      normalize(
        baseline,
        [...baseline, { id: approval.id, name: approval.specifier }],
        [],
      ),
    /rejected/,
  );
  assert.throws(
    () => normalize(baseline, [...baseline], [approval, approval]),
    /rejected/,
  );
  assert.throws(
    () =>
      normalize(
        baseline,
        [
          ...baseline,
          { id: approval.id, name: approval.specifier, group: true },
        ],
        [approval],
      ),
    /rejected/,
  );
});

test('resource public module lookup pins installed package/version and rejects aliases, SDK replacement and paths outside quota profile', () => {
  const profile = resolve('/fixture/profile'),
    modules = join(profile, 'node_modules'),
    file = join(modules, '@fixture/resource/lib/index.js');
  const approval = {
    packageName: '@fixture/resource',
    specifier: '@fixture/resource',
    version: '1.0.0',
  };
  const make = (
    manifest = { name: approval.packageName, version: approval.version },
    target = file,
  ) =>
    createDedicatedResourceModuleResolver({
      requireFrom(anchor) {
        assert.equal(anchor, DEDICATED_PROFILE_DIR + '/package.json');
        return { resolve: () => target };
      },
      realpath: (path) =>
        path === join(DEDICATED_PROFILE_DIR, 'node_modules') ? modules : path,
      readFile(path) {
        if (path.endsWith(join('lib', 'package.json')))
          throw Object.assign(new Error('missing'), { code: 'ENOENT' });
        return JSON.stringify(manifest);
      },
    });
  assert.equal(make()(approval), pathToFileURL(file).href);
  for (const changed of [
    { specifier: 'file:///run/evil.js' },
    { specifier: '@fixture/resource/../../evil' },
    { packageName: pkg('llm'), specifier: pkg('llm') },
    { version: '2.0.0' },
  ])
    assert.throws(() => make()({ ...approval, ...changed }), /rejected/);
  assert.throws(
    () => make({ name: '@fixture/alias', version: approval.version })(approval),
    /rejected/,
  );
  assert.throws(
    () => make(undefined, resolve('/outside/index.js'))(approval),
    /rejected/,
  );
});
