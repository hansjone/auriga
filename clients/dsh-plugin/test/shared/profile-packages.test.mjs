/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, stat } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseAllDocuments, visit, isAlias } from 'yaml';
import {
  SDK_INSTALLATION_ANCHOR,
  SHARED_SDK_PACKAGES,
  createInstalledSdkResolver,
  createSharedProfileResolver,
} from '../../src/shared/profile-resolution.js';
import { realpathSync } from 'node:fs';

const profileUrl = new URL(
  '../../../../deploy/nas/shared-runtime-profile.example.yml',
  import.meta.url,
);
const text = await readFile(profileUrl, 'utf8');
const docs = parseAllDocuments(text);
assert.equal(docs.length, 1);
assert.deepEqual(docs[0].errors, []);
const rows = docs[0].toJS({ maxAliasCount: 0 });
const byName = new Map(rows.map((row) => [row.name, row]));
const browserModule = 'file:///opt/auriga-dsh/src/shared-browser/plugin.js';
const distIndex =
  '/opt/dsh/node_modules/@deepseek-ai/dsh-web-frontend/dist/index.html';
const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
const aurigaRoot = process.env.AURIGA_PROFILE_AURIGA_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip = !root && 'Requires the pinned rc.3 public packages';
const hostRequire = root && createRequire(join(root, 'package.json'));
const pkg = (name) => '@deepseek-ai/dsh-' + name;
const load = async (name) =>
  import(pathToFileURL(hostRequire.resolve(name)).href);

function assertBoundary(input) {
  assert.ok(Array.isArray(input) && input.length > 0);
  const ids = new Set();
  for (const row of input) {
    assert.ok(row.id && !ids.has(row.id));
    ids.add(row.id);
    assert.ok(
      Object.keys(row).every((key) =>
        ['id', 'name', 'config', 'inject'].includes(key),
      ),
    );
    assert.ok(
      row.name === browserModule ||
        row.name === 'auriga-dsh/shared' ||
        row.name === 'auriga-dsh/shared-ports' ||
        /^@deepseek-ai\/dsh-[a-z-]+$/.test(row.name),
    );
    assert.ok(
      !/\b(?:base|web-app|web-frontend|paths|web-runtime|shell|terminal|jobs|subagent|mcp|plugin-manager|authorization|tool-fs|tool-str-replace|credentials-controller)\b/.test(
        row.name.replace(pkg(''), ''),
      ) || row.name === pkg('shell-env'),
    );
  }
  const named = (name) => input.filter((row) => row.name === name);
  for (const name of [
    'auriga-dsh/shared-ports',
    'auriga-dsh/shared',
    browserModule,
  ])
    assert.equal(named(name).length, 1);
  const ports = input.indexOf(named('auriga-dsh/shared-ports')[0]);
  const host = input.indexOf(named('auriga-dsh/shared')[0]);
  const browser = input.indexOf(named(browserModule)[0]);
  assert.ok(ports < host && host < browser);
  assert.equal(input[host].id, 'auriga-dsh');
  assert.deepEqual(input[browser].config, { distIndex });
  assert.deepEqual(named(pkg('host-frontend-static'))[0].config, { distIndex });
  const skill = named(pkg('skill-filesystem'));
  assert.equal(skill.length, 1);
  assert.deepEqual(skill[0].config, {
    providerName: 'auriga-shared',
    includeDefaultRoots: false,
    customSkillDirs: ['/run/auriga/skills-active/current'],
  });
  const presets = named(pkg('agent-preset'));
  assert.equal(presets.length, 5);
  const byPreset = Object.fromEntries(
    presets.map((row) => [row.config.id, row.config]),
  );
  assert.deepEqual(byPreset['auriga-shared'].plugins, []);
  assert.ok(
    ['共享助手', 'Auriga 共享'].includes(byPreset['auriga-shared'].name),
  );
  for (const id of ['standard', 'ptc', 'minimal', 'cordis']) {
    assert.ok(byPreset[id], id);
    const blob = JSON.stringify(byPreset[id].plugins);
    assert.equal(
      /\b(?:tool-bash|tool-pwsh|terminal|tool-fs|tool-jobs|tool-subagent|workflow-ptc|tool-cordis|plugin-manager)\b/.test(
        blob,
      ),
      false,
      id,
    );
  }
  assert.deepEqual(named(pkg('agent-loop'))[0].config.agents, []);
  assert.equal(named(pkg('tools'))[0].config.mode, 'native');
  assert.equal(named(pkg('agent-default-model'))[0].config.model, 'auriga');
}

test('complete profile is a literal entry list with a single restricted Host and immutable browser export', () => {
  visit(docs[0], (_key, node) => {
    assert.equal(isAlias(node), false);
    assert.notEqual(node?.tag, 'tag:yaml.org,2002:js');
    assert.notEqual(node?.tag, 'tag:yaml.org,2002:merge');
  });
  assertBoundary(rows);
  assert.equal(rows.length, SHARED_SDK_PACKAGES.length + 3 + 4);
  assert.deepEqual(
    [
      ...new Set(
        rows
          .filter((row) => row.name.startsWith('@deepseek-ai/'))
          .map((row) => row.name),
      ),
    ].sort(),
    [...SHARED_SDK_PACKAGES].sort(),
  );
});

test('profile boundary rejects owner root, mutable browser modules, private skill roots and nested preset tools', () => {
  const mutations = [
    (copy) => copy.push({ id: 'owner', name: 'auriga-dsh' }),
    (copy) => copy.push({ id: 'shell', name: pkg('shell') }),
    (copy) => (copy.at(-1).name = 'file:///run/auriga/shared-browser.js'),
    (copy) => (copy.at(-1).name = 'auriga-dsh/shared-browser'),
    (copy) =>
      copy
        .find((row) => row.name === pkg('skill-filesystem'))
        .config.customSkillDirs.push('/home/dsh/.agents/skills'),
    (copy) =>
      copy
        .find(
          (row) =>
            row.name === pkg('agent-preset') &&
            row.config.id === 'auriga-shared',
        )
        .config.plugins.push({
          id: 'shell',
          name: pkg('shell'),
        }),
    (copy) =>
      copy
        .find((row) => row.config?.id === 'standard')
        .config.plugins.push({
          id: 'tool-bash',
          name: pkg('tool-bash'),
        }),
  ];
  for (const mutate of mutations) {
    const copy = structuredClone(rows);
    mutate(copy);
    assert.throws(() => assertBoundary(copy));
  }
});

// These are actual public service providers, including inherited service names.
// This table checks published required inject declarations without starting DSH
// or substituting ctx.provide stubs for the providers being checked.
const services = {
  fs: 'fs-local',
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
};

test(
  'every SDK row resolves through its public export and accepts config; required service metadata closes',
  { skip },
  async () => {
    for (const row of rows.filter((row) =>
      row.name.startsWith('@deepseek-ai/'),
    )) {
      const manifest = JSON.parse(
        await readFile(hostRequire.resolve(row.name + '/package.json'), 'utf8'),
      );
      assert.equal(manifest.version, '0.2.1-rc.3', row.name);
      const module = await load(row.name);
      const plugin = module.default ?? module;
      assert.ok(
        typeof plugin === 'function' || typeof plugin.apply === 'function',
        row.name + ' must be a real Host plugin',
      );
      if (plugin.Config)
        assert.doesNotThrow(() => plugin.Config(row.config ?? {}), row.name);
      const injected = Array.isArray(plugin.inject)
        ? plugin.inject
        : (plugin.inject?.required ?? []);
      for (const service of [...injected, ...(row.inject ?? [])]) {
        if (service === 'loader') continue; // app-boot supplies the real Loader.
        assert.ok(
          services[service],
          `${row.name}: unknown required service ${service}`,
        );
        assert.ok(
          byName.has(pkg(services[service])),
          `${row.name}: missing ${service}`,
        );
      }
    }
    // Required providers of unconditional public Session/WorkspaceController children.
    for (const service of [
      'fileReferences',
      'connection',
      'attachments',
      'directoryPicker',
    ])
      assert.ok(byName.has(pkg(services[service])), service);
  },
);

test(
  'native client roster closes the real dsh.client inject graph and all published bundles exist',
  { skip },
  async () => {
    const manifests = new Map();
    for (const row of rows.filter((row) =>
      row.name.startsWith('@deepseek-ai/'),
    )) {
      const path = hostRequire.resolve(row.name + '/package.json');
      manifests.set(row.name, {
        path,
        value: JSON.parse(await readFile(path, 'utf8')),
      });
    }
    for (const [name, { path, value }] of manifests) {
      const client = value.dsh?.client;
      if (!client || client.platform !== 'web') continue;
      for (const dependency of client.inject ?? [])
        assert.ok(
          manifests.has(dependency),
          `${name} lacks client dependency ${dependency}`,
        );
      const exported = value.exports['./client'];
      const bundle = typeof exported === 'string' ? exported : exported.default;
      assert.ok((await stat(resolve(dirname(path), bundle))).isFile(), name);
    }
  },
);

test(
  'static and member frontend use the same real rc.3 index with existing entry assets',
  { skip },
  async () => {
    const path = join(root, distIndex.slice('/opt/dsh/'.length));
    const html = await readFile(path, 'utf8');
    const sources = [
      ...html.matchAll(/(?:src|href)="(\.\/assets\/[^"?#]+)[^"\s]*"/g),
    ];
    assert.ok(sources.length > 0);
    for (const [, source] of sources)
      assert.ok((await stat(resolve(dirname(path), source))).isFile());
    assert.throws(
      () => hostRequire.resolve(pkg('web-frontend')),
      /ERR_PACKAGE_PATH_NOT_EXPORTED/,
    );
  },
);

test(
  'real ClientModuleRegistry discovers Auriga client from the exact readonly public-export target, not bare subpaths',
  {
    skip:
      skip ||
      (!aurigaRoot &&
        'Requires the integrated Auriga package with shared-browser export'),
  },
  async () => {
    const { ClientModuleRegistry } = await load(pkg('client-modules'));
    const { ModuleLoader } = await load('@deepseek-ai/cordis-plugin-loader');
    const internal = ModuleLoader.fromInternal();
    assert.ok(
      internal?.resolveSync,
      'Run this pure resolution test with --expose-internals',
    );
    const registry = Object.create(ClientModuleRegistry.prototype);
    registry.ctx = { loader: { internal } };
    registry.pkgMeta = new Map();
    const manifest = JSON.parse(
      await readFile(join(aurigaRoot, 'package.json'), 'utf8'),
    );
    assert.equal(
      manifest.exports['./shared-browser'],
      './src/shared-browser/plugin.js',
    );
    const module = pathToFileURL(
      resolve(aurigaRoot, manifest.exports['./shared-browser']),
    ).href;
    assert.equal(
      new URL(browserModule).pathname,
      '/opt/auriga-dsh' + manifest.exports['./shared-browser'].slice(1),
    );
    const base = pathToFileURL(join(root, 'package.json')).href;
    for (const bare of [
      'auriga-dsh/shared',
      'auriga-dsh/shared-ports',
      'auriga-dsh/shared-browser',
    ])
      assert.equal(registry.resolveMeta(bare, base), null);
    const actual = registry.resolveMeta(module, base);
    assert.equal(actual.packageName, 'auriga-dsh');
    assert.equal(actual.meta.clientPath, join(aurigaRoot, 'src/client.js'));
    assert.ok((await stat(actual.meta.clientPath)).isFile());
    assert.equal(
      registry.locatePkgJson(module, base).path,
      join(aurigaRoot, 'package.json'),
    );
  },
);

test(
  'the unnormalized /run profile loses SDK client discovery despite a boot import anchor',
  { skip },
  async () => {
    const { ClientModuleRegistry } = await load(pkg('client-modules'));
    const { ModuleLoader } = await load('@deepseek-ai/cordis-plugin-loader');
    const internal = ModuleLoader.fromInternal();
    assert.ok(internal?.resolveSync);
    const registry = Object.create(ClientModuleRegistry.prototype);
    registry.ctx = { loader: { internal } };
    registry.pkgMeta = new Map();
    const withoutProjection = 'file:///run/auriga/pool-profile.yml';
    const withSdkResolution = pathToFileURL(join(root, 'package.json')).href;
    assert.equal(
      registry.resolveMeta(pkg('client-modules'), withoutProjection),
      null,
    );
    assert.equal(
      registry.resolveMeta(pkg('client-modules'), withSdkResolution)
        .packageName,
      pkg('client-modules'),
    );
  },
);

function installedResolver(overrides = {}) {
  return createInstalledSdkResolver({
    requireFrom(anchor) {
      assert.equal(anchor, SDK_INSTALLATION_ANCHOR);
      return hostRequire;
    },
    realpath(path) {
      return path === dirname(SDK_INSTALLATION_ANCHOR)
        ? realpathSync(root)
        : realpathSync(path);
    },
    ...overrides,
  });
}

test('SDK normalization rejects unknown modules, aliased packages, escaping realpaths and divergent conditional exports', () => {
  const sdk = resolve('/fixture/sdk');
  const candidate = join(sdk, 'node_modules/@deepseek-ai/dsh-llm/lib/index.js');
  const standard = {
    name: pkg('llm'),
    exports: {
      '.': { types: './lib/types/index.d.ts', default: './lib/index.js' },
    },
  };
  let calls = 0;
  const make = (manifest, path = candidate) =>
    createInstalledSdkResolver({
      requireFrom(anchor) {
        assert.equal(anchor, SDK_INSTALLATION_ANCHOR);
        calls++;
        return { resolve: () => path };
      },
      realpath: (value) =>
        value === dirname(SDK_INSTALLATION_ANCHOR) ? sdk : value,
      readFile(path) {
        if (path.endsWith(join('lib', 'package.json')))
          throw Object.assign(new Error('missing'), { code: 'ENOENT' });
        return JSON.stringify(manifest);
      },
    });
  assert.throws(() => make(standard)('file:///tmp/malicious.js'), /rejected/);
  assert.throws(() => make(standard)(pkg('shell')), /rejected/);
  assert.equal(calls, 0);
  assert.throws(
    () => make({ ...standard, name: pkg('shell') })(pkg('llm')),
    /rejected/,
  );
  assert.throws(
    () => make(standard, resolve('/outside/index.js'))(pkg('llm')),
    /rejected/,
  );
  assert.throws(
    () =>
      make({
        ...standard,
        exports: { '.': { require: './lib/index.js', import: './other.js' } },
      })(pkg('llm')),
    /rejected/,
  );
  assert.throws(
    () => make({ ...standard, exports: { '.': '../outside.js' } })(pkg('llm')),
    /rejected/,
  );
  assert.throws(
    () => make({ ...standard, exports: { '.': './other.js' } })(pkg('llm')),
    /rejected/,
  );
  const normalize = createSharedProfileResolver({
    resolveModule: () => assert.fail('unknown row must never resolve'),
  });
  assert.throws(() => normalize([{ id: 'x', name: pkg('shell') }]), /rejected/);
});

test(
  'normalized public SDK files retain actual module class identity and client discovery from the /run tree',
  { skip },
  async () => {
    const normalize = createSharedProfileResolver({
      resolveModule: installedResolver(),
    });
    const normalized = normalize(rows);
    const { ClientModuleRegistry } = await load(pkg('client-modules'));
    const { ModuleLoader } = await load('@deepseek-ai/cordis-plugin-loader');
    const internal = ModuleLoader.fromInternal();
    assert.ok(internal?.resolveSync);
    const registry = Object.create(ClientModuleRegistry.prototype);
    registry.ctx = { loader: { internal } };
    registry.pkgMeta = new Map();
    for (const [index, row] of rows.entries()) {
      if (!row.name.startsWith('@deepseek-ai/')) {
        assert.equal(normalized[index].name, row.name);
        continue;
      }
      assert.ok(normalized[index].name.startsWith('file:'));
      assert.equal(
        await import(normalized[index].name),
        await load(row.name),
        row.name,
      );
      const fromInstalled = registry.resolveMeta(
        row.name,
        pathToFileURL(join(root, 'package.json')).href,
      );
      const fromRun = registry.resolveMeta(
        normalized[index].name,
        'file:///run/auriga/pool-profile.yml',
      );
      assert.deepEqual(fromRun, fromInstalled, row.name);
    }
    assert.equal(
      rows[0].name,
      pkg('fs-local'),
      'the installation-owned logical input is unchanged',
    );
  },
);
