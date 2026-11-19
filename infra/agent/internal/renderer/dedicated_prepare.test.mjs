// Real locked rc.3 SDK, real persistent profile/bundle files and production
// preparation/launcher source. Fixed Linux paths are mapped by builtin seams
// into one synthetic directory; boot is observed, never starts full DSH.
import assert from 'node:assert/strict';
import test from 'node:test';
import * as fs from 'node:fs';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { createRequire, registerHooks } from 'node:module';
import { join, resolve, relative, sep } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const [sdkPath, rendered, publicResource] = process.argv.slice(2);
assert.ok(
  sdkPath && rendered && publicResource,
  'usage: dedicated_prepare.test.mjs <approved SDK root> <Go fixture root> <Go public plugin fixture.json>',
);
const sdkRoot = fs.realpathSync(resolve(sdkPath));
const root = fs.realpathSync(resolve(rendered));
const aurigaRoot = fs.realpathSync(resolve('clients/dsh-plugin'));
const require = createRequire(join(sdkRoot, 'package.json'));
assert.equal(require('yaml/package.json').version, '2.9.1');
registerHooks({
  resolve(specifier, context, next) {
    return specifier === 'yaml'
      ? { url: pathToFileURL(require.resolve('yaml')).href, shortCircuit: true }
      : next(specifier, context);
  },
});
const normal =
  await import('../../../../clients/dsh-plugin/src/dedicated/profile-resolution.js');
const protocol =
  await import('../../../../clients/dsh-plugin/src/shared/protocol.js');
const intentApi =
  await import('../../../../clients/dsh-plugin/src/dedicated/intent.js');
const { createDedicatedPreparation, dedicatedPaths: p } = await import(
  pathToFileURL(join(root, 'input', 'dedicated-prepare.mjs')).href
);
const { createDedicatedPoolLauncher } = await import(
  pathToFileURL(join(root, 'input', 'dedicated-launcher.mjs')).href
);
const sdk = await import(
  pathToFileURL(require.resolve('@deepseek-ai/dsh-app-boot')).href
);
const original = JSON.parse(fs.readFileSync(publicResource));
const sha = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const actualSdk = normal.createDedicatedInstalledSdkResolver({
  requireFrom: () => require,
  realpath: (path) => fs.realpathSync(path === '/opt/dsh' ? sdkRoot : path),
});
const caseInput = join(root, 'case-input');
fs.mkdirSync(caseInput, { recursive: true });
for (const name of fs.readdirSync(join(root, 'input')))
  fs.writeFileSync(
    join(caseInput, name),
    fs.readFileSync(join(root, 'input', name)),
  );
const mappings = [
  ['/run/auriga/dedicated-prepared', join(root, 'output')],
  ['/run/auriga/dedicated-resources', join(root, 'cache')],
  ['/run/auriga', caseInput],
  ['/home/dsh/.host', join(root, 'home')],
  ['/opt/auriga-dsh', aurigaRoot],
  ['/opt/dsh', sdkRoot],
  [
    '/opt/auriga-tools/pnpm',
    fs.realpathSync(join(sdkRoot, '..', '..', 'tools', 'pnpm')),
  ],
];
const map = (path) => {
  if (typeof path !== 'string') return path;
  if (path.startsWith('\\') && !path.startsWith('\\\\'))
    path = path.replaceAll('\\', '/');
  for (const [virtual, actual] of mappings)
    if (path === virtual || path.startsWith(virtual + '/'))
      return join(actual, path.slice(virtual.length));
  return path;
};
const unmap = (path) => {
  for (const [virtual, actual] of mappings) {
    if (path === actual) return virtual;
    if (path.startsWith(actual + sep))
      return virtual + '/' + relative(actual, path).split(sep).join('/');
  }
  return path;
};
const virtualUrl = (url) => 'file://' + unmap(fileURLToPath(url));
for (const dir of ['home', 'cache', 'output'])
  fs.mkdirSync(join(root, dir), { recursive: true });
const files = {
  ...fs,
  realpathSync(path) {
    return path === '/usr/local/bin/node'
      ? path
      : unmap(fs.realpathSync(map(path)));
  },
  lstatSync(path) {
    return fs.lstatSync(map(path));
  },
  readFileSync(path, ...args) {
    return fs.readFileSync(map(path), ...args);
  },
};
for (const method of ['openSync', 'mkdirSync', 'existsSync', 'readdirSync'])
  files[method] = (path, ...args) => fs[method](map(path), ...args);
files.renameSync = (from, to) => fs.renameSync(map(from), map(to));
const load = (dir) =>
  sdk.loadProfileDirectory(
    'auriga-dedicated',
    map(dir),
    join(sdkRoot, 'package.json'),
  );
let boots = 0,
  packages = 0,
  lastArgs,
  lastContext;
const normalizer = {
  ...normal,
  resolveDedicatedSdkModule: (name) => virtualUrl(actualSdk(name)),
  resolveDedicatedRuntimeProfile: normal.createDedicatedProfileResolver({
    resolveModule: (name) => virtualUrl(actualSdk(name)),
  }),
  resolveDedicatedResourceProfile:
    normal.createDedicatedResourceProfileResolver({
      resolveModule: (approval) => {
        const resolver = normal.createDedicatedResourceModuleResolver({
          requireFrom: (anchor) => createRequire(map(anchor)),
          realpath: (path) => fs.realpathSync(map(path)),
          readFile: (path, ...args) => fs.readFileSync(map(path), ...args),
        });
        return virtualUrl(resolver(approval));
      },
    }),
};
const importModule = async (url) => {
  if (url === 'file:///opt/auriga-dsh/src/shared/protocol.js') return protocol;
  if (url === 'file:///opt/auriga-dsh/src/dedicated/profile-resolution.js')
    return normalizer;
  if (url === 'file:///opt/auriga-dsh/src/dedicated/intent.js')
    return {
      ...intentApi,
      readIntent: (file, binding) =>
        intentApi.readIntent(map(file), binding, () => original.now),
      loadResources: async (intent, cache) =>
        (await intentApi.loadResources(intent, map(cache))).map((row) => ({
          ...row,
          artifactFile: unmap(row.artifactFile),
        })),
    };
  assert.equal(url, virtualUrl(actualSdk('@deepseek-ai/dsh-app-boot')));
  return {
    ...sdk,
    initProfile: (dir, bundles) => sdk.initProfile(map(dir), bundles),
    loadProfileDirectory: (_name, dir) => load(dir),
    readProfilePatches: (name, context, loaded) =>
      sdk.readProfilePatches(
        name,
        {
          ...context,
          dir: map(context.dir),
          home: map(context.home),
          patchPath: map(context.patchPath),
        },
        loaded,
      ),
    createRuntimeResolution: (options) =>
      sdk.createRuntimeResolution({
        ...options,
        installAnchor: join(sdkRoot, 'package.json'),
        home: map(options.home),
      }),
    async boot(...args) {
      boots++;
      lastArgs = args;
      const { Context } = await import(
        pathToFileURL(require.resolve('@deepseek-ai/cordis')).href
      );
      const { Loader } = await import(
        pathToFileURL(require.resolve('@deepseek-ai/cordis-plugin-loader')).href
      );
      const ctx = new Context();
      new Loader(ctx);
      const provided = new Map();
      await args[3]({
        provide(name, value) {
          provided.set(name, value);
          ctx.provide(name, value);
        },
        async plugin(plugin, options) {
          assert.equal(plugin, sdk.PluginPackages);
          packages++;
          await ctx.plugin(plugin, options);
        },
      });
      lastContext = provided.get('profileContext');
      return {
        aurigaDedicated: {},
        pluginManager: {},
        skills: {},
        webServer: {
          register() {
            return () => {};
          },
        },
        fiber: ctx.fiber,
      };
    },
  };
};
const prepare = () =>
  createDedicatedPreparation({
    files,
    importModule,
    requireFrom: () => require,
    execPath: '/usr/local/bin/node',
    nodeVersion: '22.19.0',
  })();
const emptyOutput = () => {
  for (const name of fs.readdirSync(map(p.output))) {
    assert.ok(['config.json', 'manifest.json'].includes(name));
    fs.unlinkSync(join(map(p.output), name));
  }
};

function tar(files) {
  const blocks = [];
  const octal = (value, size) =>
    value.toString(8).padStart(size - 1, '0') + '\0';
  for (const [name, content] of Object.entries(files)) {
    const bytes = Buffer.from(content),
      header = Buffer.alloc(512);
    header.write('package/' + name);
    header.write(octal(0o644, 8), 100);
    header.write(octal(0, 8), 108);
    header.write(octal(0, 8), 116);
    header.write(octal(bytes.length, 12), 124);
    header.write(octal(0, 12), 136);
    header.fill(32, 148, 156);
    header[156] = 48;
    header.write('ustar\0', 257);
    header.write('00', 263);
    header.write(
      octal(
        header.reduce((n, b) => n + b, 0),
        7,
      ) + ' ',
      148,
    );
    blocks.push(
      header,
      bytes,
      Buffer.alloc((512 - (bytes.length % 512)) % 512),
    );
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

await test('Go-rendered fixed helper uses real rc.3 initialization and emits a sealed Agent output', async () => {
  const proof = await prepare();
  assert.equal(proof.startedBundles.length, 0);
  assert.equal(
    JSON.parse(fs.readFileSync(map(p.output + '/config.json'))).length,
    83,
  );
  const first = fs.readFileSync(map(p.profile + '/package.json'));
  emptyOutput();
  await prepare();
  assert.deepEqual(fs.readFileSync(map(p.profile + '/package.json')), first);
  assert.equal(boots, 0);
});

await test('same helper verifies current TGZ bytes, selected public bundle and preserved user/home layers before boot', async () => {
  const name = '@fixture/prepared-resource',
    patch =
      '- insert:\n    - id: approved-resource\n      name: "@fixture/prepared-resource"\n      config: {value: bundle}\n';
  const packageFiles = {
    'package.json': JSON.stringify({
      name,
      version: '1.0.0',
      type: 'module',
      exports: { '.': './index.js', './package.json': './package.json' },
      dsh: { bundle: { patch: './cordis.patch.yml' } },
    }),
    'index.js': 'export default function Fixture() {}\n',
    'cordis.patch.yml': patch,
  };
  const payload = tar(packageFiles),
    artifactDigest = sha(payload);
  const sourceManifest = Object.entries(original.files).find(([key]) =>
    key.endsWith('/manifest.json'),
  )[1];
  const manifest = JSON.parse(Buffer.from(sourceManifest, 'base64'));
  manifest.plugin.packageName = name;
  manifest.version = '1.0.0';
  manifest.artifacts[0].digest = artifactDigest;
  manifest.artifacts[0].sizeBytes = payload.length;
  const manifestBytes = Buffer.from(JSON.stringify(manifest)),
    manifestDigest = sha(manifestBytes);
  const oldIntent = JSON.parse(fs.readFileSync(map(p.intent)));
  const resourceId = JSON.parse(original.intent).resources[0].resourceId;
  oldIntent.view.resources = [
    {
      resourceId,
      digest: manifestDigest,
      leaseId: resourceId,
      expiresAt: original.now + 3600,
    },
  ];
  oldIntent.resources = [
    {
      resourceId,
      manifestDigest,
      artifactDigest,
      artifactBytes: payload.length,
      installationRevision: 1,
    },
  ];
  fs.writeFileSync(map(p.intent), protocol.canonical(oldIntent));
  const input = JSON.parse(fs.readFileSync(map(p.input)));
  input.view = oldIntent.view;
  input.intentDigest = sha(Buffer.from(protocol.canonical(oldIntent)));
  fs.writeFileSync(map(p.input), JSON.stringify(input));
  for (const [digest, file, bytes] of [
    [artifactDigest, 'payload.tgz', payload],
    [manifestDigest, 'manifest.json', manifestBytes],
  ]) {
    fs.mkdirSync(map(p.cache + '/' + digest.slice(7)), { recursive: true });
    fs.writeFileSync(map(p.cache + '/' + digest.slice(7) + '/' + file), bytes);
  }
  const packageDir = map(p.profile + '/node_modules/' + name);
  fs.mkdirSync(packageDir, { recursive: true });
  for (const [file, bytes] of Object.entries(packageFiles))
    fs.writeFileSync(join(packageDir, file), bytes);
  const profileManifest = JSON.parse(
    fs.readFileSync(map(p.profile + '/package.json')),
  );
  profileManifest.dependencies[name] =
    'file:' + p.cache + '/' + artifactDigest.slice(7) + '/payload.tgz';
  profileManifest.dsh.profile.bundles = [name];
  fs.writeFileSync(
    map(p.profile + '/package.json'),
    JSON.stringify(profileManifest),
  );
  fs.writeFileSync(
    map(p.profile + '/cordis.patch.yml'),
    '- id: approved-resource\n  config: {value: user}\n',
  );
  fs.writeFileSync(
    map(p.home + '/cordis.patch.yml'),
    '- id: approved-resource\n  config: {value: home}\n',
  );
  emptyOutput();
  const proof = await prepare();
  assert.deepEqual(proof.startedBundles, [name]);
  const rows = JSON.parse(fs.readFileSync(map(p.output + '/config.json')));
  assert.equal(rows.length, 84);
  const resource = rows.find((row) => row.id === 'approved-resource');
  assert.equal(resource.config.value, 'home');
  assert.equal(resource.name, 'file://' + unmap(join(packageDir, 'index.js')));
  assert.equal(
    (await import(pathToFileURL(join(packageDir, 'index.js')).href)).default
      .name,
    'Fixture',
  );
  const signals = new Map();
  const ctx = await createDedicatedPoolLauncher({
    files,
    importModule,
    env: {
      AURIGA_MODE: 'dedicated',
      AURIGA_INSTANCE_ID: input.binding.physicalInstanceId,
    },
    onSignal: (signal, callback) => signals.set(signal, callback),
    exit: (code) => assert.equal(code, 0),
  })();
  assert.ok(ctx);
  assert.equal(boots, 1);
  assert.equal(packages, 1);
  assert.equal(lastArgs[1], p.output + '/config.json');
  assert.deepEqual(lastArgs[2], []);
  assert.equal(lastArgs[4], 'file:///opt/dsh/package.json');
  assert.equal(lastContext.dir, p.profile);
  assert.equal(lastContext.packageManager.args[0], p.pnpm);
  assert.ok(lastContext.packageManager.args.includes('--offline'));
  assert.ok(lastContext.packageManager.args.includes('--ignore-scripts'));
  await signals.get('SIGTERM')();
  // Keep a known-good output for the optional actual Go readback command.
});

await test('unapproved bytes, protected baseline changes, tool drift and output reuse are rejected', async () => {
  await assert.rejects(prepare); // output is per-attempt, never overwritten
  const configFile = map(p.profile + '/cordis.patch.yml'),
    user = fs.readFileSync(configFile);
  emptyOutput();
  fs.writeFileSync(configFile, '- id: jobs\n  disabled: true\n');
  await assert.rejects(prepare);
  fs.writeFileSync(configFile, user);
  const resourceFile = map(
      p.profile + '/node_modules/@fixture/prepared-resource/index.js',
    ),
    code = fs.readFileSync(resourceFile);
  fs.writeFileSync(resourceFile, 'tampered');
  await assert.rejects(prepare);
  fs.writeFileSync(resourceFile, code);
  await assert.rejects(
    createDedicatedPreparation({
      files,
      importModule,
      requireFrom: () => require,
      execPath: '/usr/local/bin/node',
      nodeVersion: '22.18.0',
    }),
  );
  const old = files.readFileSync;
  files.readFileSync = (file, ...args) =>
    file === '/opt/auriga-tools/pnpm/node_modules/pnpm/package.json'
      ? Buffer.from('{"version":"11.28.6"}')
      : old(file, ...args);
  await assert.rejects(prepare);
  files.readFileSync = old;
  await prepare(); // restore only the independently revalidated good result
  assert.equal(boots, 1);
});
