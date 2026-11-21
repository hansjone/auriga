// Pure production-bootstrap composition. Real pinned public SDK exports and
// renderer output are consumed; boot itself is observed, never starts DSH.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  createInstalledSdkResolver,
  createSharedProfileResolver,
  SHARED_SDK_PACKAGES,
} from '../../../../clients/dsh-plugin/src/shared/profile-resolution.js';

const sdkRoot = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
const rendered = process.env.AURIGA_RENDERED_SHARED_PROFILE;
assert.ok(
  sdkRoot && rendered,
  'set pinned SDK root and renderer fixture directory; see renderer/testdata/README.md',
);
const { createSharedPoolLauncher } = await import(
  pathToFileURL(join(rendered, 'pool-launcher.mjs')).href
);
const sdkRequire = createRequire(join(resolve(sdkRoot), 'package.json'));
const yaml = sdkRequire('js-yaml');
const bootSdk = await import(
  pathToFileURL(sdkRequire.resolve('@deepseek-ai/dsh-app-boot')).href
);
const pkgRoot = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../clients/dsh-plugin',
);
const profileText = readFileSync(join(rendered, 'pool-profile.yml'), 'utf8');
const sourceRows = yaml.load(profileText, { schema: yaml.JSON_SCHEMA });
const installed = createInstalledSdkResolver({
  requireFrom(anchor) {
    assert.equal(anchor, '/opt/dsh/package.json');
    return sdkRequire;
  },
  realpath(path) {
    return realpathSync(path === '/opt/dsh' ? sdkRoot : path);
  },
});
const normalize = createSharedProfileResolver({ resolveModule: installed });

function fixture() {
  const observed = {
    provided: new Map(),
    signals: new Map(),
    bootCalls: 0,
    disposed: 0,
    ready: 0,
    mounts: [],
  };
  const state = {
    manifest: JSON.parse(readFileSync(join(pkgRoot, 'package.json'), 'utf8')),
    binding: JSON.parse(
      readFileSync(join(rendered, 'pool-binding.json'), 'utf8'),
    ),
    logical: profileText,
    entry: readFileSync(join(rendered, 'pool-entry.yml'), 'utf8'),
    network: {
      eth0: [{ internal: false, family: 'IPv4', address: '192.0.2.10' }],
    },
    env: {
      AURIGA_MODE: 'shared',
      AURIGA_INSTANCE_ID: '00000000-0000-4000-8000-000000000002',
    },
    normalize,
    ports: true,
    bootManifest: JSON.parse(
      readFileSync(
        sdkRequire.resolve('@deepseek-ai/dsh-app-boot/package.json'),
        'utf8',
      ),
    ),
    outsideSdk: false,
  };
  const host = {
    aurigaShared: {},
    aurigaSharedPorts: {},
    webServer: {
      register(route) {
        observed.health = route;
        return () => observed.ready--;
      },
    },
    fiber: {
      async dispose() {
        observed.disposed++;
      },
    },
  };
  const require = (name) => {
    assert.equal(name, 'js-yaml');
    return yaml;
  };
  require.resolve = (name) =>
    name === 'auriga-dsh/package.json'
      ? join(pkgRoot, 'package.json')
      : state.outsideSdk && name === '@deepseek-ai/dsh-app-boot'
        ? join(pkgRoot, 'src/plugin.js')
        : sdkRequire.resolve(name);
  const launch = createSharedPoolLauncher({
    requireFrom(anchor) {
      assert.equal(anchor, '/opt/dsh/package.json');
      return require;
    },
    readFile(path) {
      if (path === '/run/auriga/pool-binding.json')
        return JSON.stringify(state.binding);
      if (path === '/opt/auriga-dsh/package.json')
        return JSON.stringify(state.manifest);
      if (path === '/run/auriga/pool-profile.yml') return state.logical;
      if (path === '/run/auriga/pool-entry.yml') return state.entry;
      if (
        path ===
        realpathSync(
          sdkRequire.resolve('@deepseek-ai/dsh-app-boot/package.json'),
        )
      )
        return JSON.stringify(state.bootManifest);
      assert.fail('unexpected bootstrap file read');
    },
    realpath(path) {
      if (path === '/opt/dsh') return realpathSync(sdkRoot);
      if (path === '/opt/auriga-dsh') return realpathSync(pkgRoot);
      if (path.startsWith('/opt/auriga-dsh/'))
        return realpathSync(
          join(pkgRoot, ...path.slice('/opt/auriga-dsh/'.length).split('/')),
        );
      return realpathSync(path);
    },
    async importModule(url) {
      if (url === 'file:///opt/auriga-dsh/src/shared/profile-resolution.js')
        return {
          resolveSharedRuntimeProfile(rows) {
            observed.prepared = rows;
            observed.normalized = state.normalize(rows);
            return observed.normalized;
          },
        };
      assert.equal(
        url,
        pathToFileURL(sdkRequire.resolve('@deepseek-ai/dsh-app-boot')).href,
      );
      return {
        async boot(...args) {
          observed.bootCalls++;
          observed.args = args;
          args[3]({
            provide(key, value) {
              observed.provided.set(key, value);
            },
          });
          observed.provided.get('appReady').onReady(() => observed.ready++);
          if (!state.ports) host.aurigaSharedPorts = undefined;
          return host;
        },
      };
    },
    mkdir(path, options) {
      observed.mounts.push([path, options]);
    },
    interfaces: () => state.network,
    env: state.env,
    onSignal(name, callback) {
      observed.signals.set(name, callback);
    },
    exit(code) {
      observed.exit = code;
    },
  });
  return { state, observed, launch };
}

test('renderer -> fixed normalizer -> public rc.3 insert composition is the actual boot input', async () => {
  const { state, observed, launch } = fixture();
  await launch();
  assert.equal(observed.bootCalls, 1);
  assert.equal(observed.args.length, 5);
  assert.equal(observed.args[0], 'auriga-shared');
  assert.equal(observed.args[1], '/run/auriga/pool-entry.yml');
  assert.equal(observed.args[4], 'file:///opt/dsh/package.json');
  assert.deepEqual(Object.keys(observed.args[2][0]), ['insert']);
  assert.strictEqual(observed.args[2][0].insert, observed.normalized);
  assert.deepEqual(
    bootSdk.composeEntries([observed.args[2]]),
    observed.normalized,
  );
  assert.equal(observed.normalized.length, 65);
  assert.equal(
    observed.normalized.filter((row) => row.name.startsWith('@deepseek-ai/'))
      .length,
    0,
  );
  for (const name of SHARED_SDK_PACKAGES)
    assert.equal(
      observed.normalized.filter((row) => row.name === installed(name)).length,
      1,
    );
  assert.equal(
    observed.normalized.at(-1).name,
    'file:///opt/auriga-dsh/src/shared-browser/plugin.js',
  );
  assert.equal(
    observed.prepared.find(
      (row) => row.name === '@deepseek-ai/dsh-host-webserver',
    ).config.host,
    '192.0.2.10',
  );
  assert.equal(
    sourceRows.find((row) => row.name === '@deepseek-ai/dsh-host-webserver')
      .config.host,
    '127.0.0.1',
  );
  assert.deepEqual(
    [...observed.provided.keys()],
    ['profileContext', 'appReady'],
  );
  assert.equal(observed.provided.get('profileContext').dir, '/run/auriga');
  assert.equal(state.env.DSH_HOME, '/home/dsh/.host/.dsh');
  assert.equal(observed.ready, 1);
  await observed.signals.get('SIGTERM')();
  await observed.signals.get('SIGINT')();
  assert.equal(observed.disposed, 1);
  assert.equal(observed.exit, 0);
});

test('actual normalized SDK modules and client metadata retain the public installed identity', async () => {
  const { observed, launch } = fixture();
  await launch();
  const { ClientModuleRegistry } = await import(
    pathToFileURL(sdkRequire.resolve('@deepseek-ai/dsh-client-modules')).href
  );
  const { ModuleLoader } = await import(
    pathToFileURL(sdkRequire.resolve('@deepseek-ai/cordis-plugin-loader')).href
  );
  const registry = Object.create(ClientModuleRegistry.prototype);
  registry.ctx = { loader: { internal: ModuleLoader.fromInternal() } };
  registry.pkgMeta = new Map();
  const runBase = 'file:///run/auriga/';
  let clients = 0;
  for (const name of SHARED_SDK_PACKAGES) {
    const normalized = installed(name);
    assert.strictEqual(
      await import(normalized),
      await import(pathToFileURL(sdkRequire.resolve(name)).href),
    );
    const manifest = JSON.parse(
      readFileSync(sdkRequire.resolve(name + '/package.json'), 'utf8'),
    );
    if (manifest.dsh?.client?.platform === 'web') {
      const meta = registry.resolveMeta(normalized, runBase);
      assert.equal(meta.packageName, name);
      assert.ok(meta.meta.clientPath);
      clients++;
    }
  }
  assert.ok(clients > 10);
  assert.equal(observed.args[2][0].insert, observed.normalized);
});

test('unsafe anchors, owner material, wrong modes and unresolved profiles fail before boot', async (t) => {
  const cases = {
    'wrong mode': (s) => {
      s.binding.mode = 'dedicated';
    },
    'foreign pool': (s) => {
      s.binding.physicalInstanceId = s.binding.tenantId;
    },
    'owner env': (s) => {
      s.env.AURIGA_OWNER_ID = 'synthetic-owner';
    },
    'company env': (s) => {
      s.env.DSH_AURIGA_TOKEN = 'synthetic-forbidden';
    },
    'export alias': (s) => {
      s.manifest.exports['./shared-browser'] = './src/plugin.js';
    },
    'nonempty root': (s) => {
      s.entry = '[{"name":"auriga-dsh"}]';
    },
    'partial profile': (s) => {
      s.logical = '[]';
    },
    'missing interface': (s) => {
      s.network = {};
    },
    'multiple interfaces': (s) => {
      s.network.eth1 = [
        { internal: false, family: 'IPv4', address: '192.0.2.11' },
      ];
    },
    'unresolved package': (s) => {
      s.normalize = () => {
        throw new Error('synthetic SDK resolution failure');
      };
    },
    'SDK escaped anchor': (s) => {
      s.outsideSdk = true;
    },
    'SDK wrong version': (s) => {
      s.bootManifest.version = '0.2.0-rc.2';
    },
    'SDK conditional root': (s) => {
      s.bootManifest.exports['.'].require = './different.cjs';
    },
  };
  for (const [name, mutate] of Object.entries(cases))
    await t.test(name, async () => {
      const { state, observed, launch } = fixture();
      mutate(state);
      await assert.rejects(launch);
      assert.equal(observed.bootCalls, 0);
      assert.equal(observed.ready, 0);
    });
});

test('incomplete host after boot is disposed without publishing ready health', async () => {
  const { state, observed, launch } = fixture();
  state.ports = false;
  await assert.rejects(launch, /authority ports unavailable/);
  assert.equal(observed.disposed, 1);
  assert.equal(observed.health, undefined);
});
