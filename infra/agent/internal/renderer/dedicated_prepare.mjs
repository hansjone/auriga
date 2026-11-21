// Fixed one-shot entry in the SAME verified runtime image. No boot, network,
// child process, socket or user-selected import/command runs during preparation.
import * as fs from 'node:fs';
import { Buffer } from 'node:buffer';
import { createHash, randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { posix, resolve, relative, isAbsolute, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { Readable } from 'node:stream';
import { createGunzip } from 'node:zlib';

export const dedicatedPaths = Object.freeze({
  anchor: '/opt/dsh/package.json',
  profile: '/home/dsh/.host/.dsh/profiles/auriga-dedicated',
  home: '/home/dsh/.host/.dsh',
  output: '/run/auriga/dedicated-prepared',
  input: '/run/auriga/dedicated-input.json',
  intent: '/run/auriga/dedicated-intent.json',
  cache: '/run/auriga/dedicated-resources',
  pnpm: '/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs',
});
const sha = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const fail = () => {
  throw new Error('Dedicated preparation rejected');
};
const inside = (root, file) => {
  const value = relative(root, file);
  return (
    value !== '' &&
    value !== '..' &&
    !value.startsWith('..' + sep) &&
    !isAbsolute(value)
  );
};

// validateArchive has already checked the complete strict USTAR grammar. This
// second, streaming projection hashes files without retaining a second 256MiB
// decompressed archive. It neither extracts files nor imports resource code.
async function archiveFiles(bytes) {
  const result = new Map();
  let pending = Buffer.alloc(0),
    remaining = 0,
    padding = 0,
    current,
    hash;
  let total = 0,
    ended = false;
  const text = (b) =>
    b.subarray(0, b.indexOf(0) < 0 ? b.length : b.indexOf(0)).toString('utf8');
  for await (const chunk of Readable.from([bytes]).pipe(createGunzip())) {
    total += chunk.length;
    if (total > 268435456 || total > bytes.length * 100) fail();
    pending = Buffer.concat([pending, chunk]);
    while (pending.length) {
      if (ended) {
        pending = Buffer.alloc(0);
        break;
      }
      if (remaining) {
        const size = Math.min(remaining, pending.length);
        hash?.update(pending.subarray(0, size));
        pending = pending.subarray(size);
        remaining -= size;
        if (remaining === 0 && current)
          result.set(current.path, {
            size: current.size,
            digest: 'sha256:' + hash.digest('hex'),
          });
      } else if (padding) {
        const size = Math.min(padding, pending.length);
        pending = pending.subarray(size);
        padding -= size;
      } else {
        if (pending.length < 512) break;
        const header = pending.subarray(0, 512);
        pending = pending.subarray(512);
        if (header.every((v) => v === 0)) {
          ended = true;
          continue;
        }
        const path = (
          (text(header.subarray(345, 500))
            ? text(header.subarray(345, 500)) + '/'
            : '') + text(header.subarray(0, 100))
        )
          .replace(/^(?:\.\/)+/, '')
          .replace(/^package\//, '');
        const size = parseInt(text(header.subarray(124, 136)).trim() || '0', 8);
        if (!Number.isSafeInteger(size) || size < 0 || result.size >= 4096)
          fail();
        remaining = size;
        padding = (512 - (size % 512)) % 512;
        current = header[156] === 53 ? undefined : { path, size };
        hash = current && createHash('sha256');
        if (!remaining && current)
          result.set(path, { size, digest: 'sha256:' + hash.digest('hex') });
      }
    }
  }
  if (!ended || remaining || padding || !result.has('package.json')) fail();
  return result;
}

/** Builtin seams for pure tests only. Production supplies no arguments; paths,
 * SDK/tool pins and imports cannot come from CLI, environment or YAML. */
export function createDedicatedPreparation({
  files = fs,
  requireFrom = createRequire,
  importModule = (url) => import(url),
  execPath = process.execPath,
  nodeVersion = process.versions.node,
} = {}) {
  const read = (file, limit = 1048576) => {
    const info = files.lstatSync(file);
    if (!info.isFile() || info.isSymbolicLink() || info.size > limit) fail();
    return files.readFileSync(file);
  };
  const json = (file) => JSON.parse(read(file).toString('utf8'));
  const fingerprint = (file, limit) => {
    const before = files.lstatSync(file);
    if (!before.isFile() || before.isSymbolicLink() || before.size > limit)
      fail();
    const fd = files.openSync(
      file,
      files.constants.O_RDONLY | (files.constants.O_NOFOLLOW ?? 0),
    );
    try {
      const opened = files.fstatSync(fd);
      if (
        opened.ino !== before.ino ||
        opened.dev !== before.dev ||
        opened.size !== before.size
      )
        fail();
      const chunk = Buffer.alloc(65536),
        hash = createHash('sha256');
      let size = 0,
        n;
      while ((n = files.readSync(fd, chunk, 0, chunk.length, null)) > 0) {
        size += n;
        if (size > limit) fail();
        hash.update(chunk.subarray(0, n));
      }
      if (size !== before.size) fail();
      return { size, digest: 'sha256:' + hash.digest('hex') };
    } finally {
      files.closeSync(fd);
    }
  };
  const outputFile = (name, bytes) => {
    const path = dedicatedPaths.output + '/' + name;
    const temporary = path + '.' + randomUUID() + '.tmp';
    const fd = files.openSync(temporary, 'wx', 0o600);
    try {
      files.writeFileSync(fd, bytes);
      files.fsyncSync(fd);
    } finally {
      files.closeSync(fd);
    }
    files.renameSync(temporary, path);
    if (process.platform === 'linux') {
      const fd = files.openSync(dedicatedPaths.output, 'r');
      try {
        files.fsyncSync(fd);
      } finally {
        files.closeSync(fd);
      }
    }
  };
  return async () => {
    const p = dedicatedPaths,
      inputBytes = read(p.input),
      input = JSON.parse(inputBytes);
    if (
      nodeVersion !== '22.19.0' ||
      files.realpathSync(execPath) !== '/usr/local/bin/node' ||
      json('/opt/auriga-tools/pnpm/node_modules/pnpm/package.json').version !==
        '11.28.5' ||
      !files.lstatSync(p.pnpm).isFile()
    )
      fail();
    const { canonical } = await importModule(
      'file:///opt/auriga-dsh/src/shared/protocol.js',
    );
    const normalizer = await importModule(
      'file:///opt/auriga-dsh/src/dedicated/profile-resolution.js',
    );
    const { readIntent, loadResources } = await importModule(
      'file:///opt/auriga-dsh/src/dedicated/intent.js',
    );
    const pkg = json('/opt/auriga-dsh/package.json');
    if (
      pkg.name !== 'auriga-dsh' ||
      pkg.exports?.['./dedicated'] !== './src/dedicated/plugin.js' ||
      files.realpathSync('/opt/auriga-dsh/src/dedicated/plugin.js') !==
        '/opt/auriga-dsh/src/dedicated/plugin.js'
    )
      fail();
    if (
      input.schemaVersion !== 1 ||
      input.binding?.mode !== 'dedicated' ||
      input.binding.physicalInstanceId !==
        input.view?.binding?.physicalInstanceId ||
      canonical(input.binding) !== canonical(input.view.binding) ||
      input.baselineDigest !== normalizer.DEDICATED_BASELINE_DIGEST ||
      !/^[a-f0-9-]{36}$/.test(input.attemptId) ||
      !/^sha256:[a-f0-9]{64}$/.test(input.imageId)
    )
      fail();
    const intent = readIntent(p.intent, input.binding);
    if (
      intent.intentDigest !== input.intentDigest ||
      canonical(intent.value.view) !== canonical(input.view)
    )
      fail();
    const wanted = await loadResources(intent, p.cache);
    const require = requireFrom(p.anchor);
    const bootUrl = normalizer.resolveDedicatedSdkModule(
      '@deepseek-ai/dsh-app-boot',
    );
    if (
      json(require.resolve('@deepseek-ai/dsh-app-boot/package.json'))
        .version !== '0.2.1-rc.3'
    )
      fail();
    const sdk = await importModule(bootUrl);
    const yaml = require('js-yaml');
    if (
      !inside(
        files.realpathSync('/opt/dsh'),
        files.realpathSync(require.resolve('js-yaml')),
      )
    )
      fail();
    const logicalBytes = read('/run/auriga/dedicated-profile.json');
    if (sha(logicalBytes) !== input.templateDigest) fail();
    const logical = JSON.parse(logicalBytes);
    normalizer.assertDedicatedRuntimeProfile(logical);
    const baseline = normalizer.resolveDedicatedRuntimeProfile(logical);
    // Every parent is beneath the single quota home, with no link redirection.
    for (const path of [
      p.home,
      posix.dirname(p.profile),
      p.profile,
      '/home/dsh/.host/workspace',
      '/home/dsh/.host/.agents',
    ]) {
      files.mkdirSync(path, { recursive: true, mode: 0o700 });
      if (
        !files.lstatSync(path).isDirectory() ||
        files.lstatSync(path).isSymbolicLink() ||
        files.realpathSync(path) !== path
      )
        fail();
    }
    for (const path of [
      p.profile + '/.npmrc',
      p.profile + '/.pnpmfile.cjs',
      p.home + '/.npmrc',
      '/home/dsh/.host/.npmrc',
    ])
      if (files.existsSync(path)) fail();
    sdk.initProfile(p.profile, []);
    const manifest = json(p.profile + '/package.json');
    if (
      Object.keys(manifest).some(
        (key) =>
          ![
            'name',
            'private',
            'dependencies',
            'dsh',
            'packageManager',
          ].includes(key),
      ) ||
      manifest.private !== true ||
      manifest.name !== 'dsh-profile-auriga-dedicated' ||
      (manifest.packageManager !== undefined &&
        manifest.packageManager !== 'pnpm@11.28.5') ||
      Object.keys(manifest.dsh ?? {}).join(',') !== 'profile' ||
      Object.keys(manifest.dsh.profile ?? {}).join(',') !== 'bundles' ||
      !Array.isArray(manifest.dsh.profile.bundles)
    )
      fail();
    const plugins = new Map(
      wanted
        .filter((row) => row.kind === 'plugin')
        .map((row) => [row.name, row]),
    );
    const controls = [],
      approved = [],
      installed = new Map();
    const remember = (file, limit = 1048576) => {
      const bytes = read(file, limit);
      if (controls.length >= 16384) fail();
      controls.push({ path: file, size: bytes.length, digest: sha(bytes) });
      return bytes;
    };
    for (const file of [
      'package.json',
      'cordis.patch.yml',
      'pnpm-workspace.yaml',
    ])
      remember(p.profile + '/' + file);
    for (const file of [
      p.home + '/cordis.patch.yml',
      p.profile + '/' + sdk.PROFILE_COMPATIBILITY_FILENAME,
    ])
      if (files.existsSync(file)) {
        if (file.endsWith(sdk.PROFILE_COMPATIBILITY_FILENAME)) fail();
        remember(file);
      }
    for (const [name, spec] of Object.entries(manifest.dependencies ?? {})) {
      const row = plugins.get(name);
      if (
        !row ||
        typeof spec !== 'string' ||
        !spec.startsWith('file:') ||
        posix.resolve(p.profile, spec.slice(5)) !== row.artifactFile
      )
        fail();
      const directory = files.realpathSync(p.profile + '/node_modules/' + name);
      if (!inside(files.realpathSync(p.profile + '/node_modules'), directory))
        fail();
      const archive = read(row.artifactFile, 67108864);
      if (sha(archive) !== row.artifactDigest) fail();
      const expected = await archiveFiles(archive);
      const actual = new Set();
      const scan = (path, prefix = '', ancestors = new Set()) => {
        const real = files.realpathSync(path);
        if (
          ancestors.has(real) ||
          (real !== directory && !inside(directory, real))
        )
          fail();
        const next = new Set([...ancestors, real]);
        for (const entry of files.readdirSync(path, { withFileTypes: true })) {
          const key = prefix + entry.name,
            file = path + '/' + entry.name;
          const info = files.lstatSync(file);
          if (info.isDirectory()) scan(file, key + '/', next);
          else {
            if (!info.isFile() || info.isSymbolicLink() || actual.size >= 16384)
              fail();
            const proof = expected.get(key),
              actualProof = fingerprint(file, 268435456);
            if (
              !proof ||
              proof.size !== actualProof.size ||
              proof.digest !== actualProof.digest ||
              controls.length >= 16384
            )
              fail();
            actual.add(key);
            controls.push({ path: file, ...proof });
          }
        }
      };
      scan(directory);
      if (actual.size !== expected.size) fail();
      const packageJson = json(directory + '/package.json');
      if (packageJson.name !== name || packageJson.version !== row.version)
        fail();
      const patchFile =
        directory + '/' + packageJson.dsh.bundle.patch.replace(/^\.\//, '');
      const patches = yaml.load(read(patchFile, 1048576).toString('utf8'), {
        schema: yaml.JSON_SCHEMA,
      });
      if (!Array.isArray(patches)) fail();
      for (const patch of patches) {
        if (
          Object.keys(patch).join(',') !== 'insert' ||
          !Array.isArray(patch.insert)
        )
          fail();
        for (const entry of patch.insert) {
          if (
            !entry ||
            typeof entry.id !== 'string' ||
            typeof entry.name !== 'string' ||
            (entry.name !== name && !entry.name.startsWith(name + '/'))
          )
            fail();
          approved.push({
            id: entry.id,
            packageName: name,
            specifier: entry.name,
            version: row.version,
          });
        }
      }
      installed.set(name, directory);
    }
    const selected = manifest.dsh.profile.bundles;
    if (
      new Set(selected).size !== selected.length ||
      selected.some((name) => !installed.has(name))
    )
      fail();
    const loaded = sdk.loadProfileDirectory(
      'auriga-dedicated',
      p.profile,
      p.anchor,
    );
    if (
      loaded.skippedBundles.length ||
      loaded.layers.length !== selected.length ||
      loaded.layers.some(
        (layer) =>
          files.realpathSync(layer.packageDir) !==
          installed.get(layer.packageName),
      )
    )
      fail();
    const context = {
      name: 'auriga-dedicated',
      dir: p.profile,
      home: p.home,
      cwd: '/home/dsh/.host/workspace',
      installAnchor: p.anchor,
      patchPath: p.profile + '/cordis.patch.yml',
      overlays: [],
      startedBundles: loaded.layers.map((layer) => layer.packageName),
      packageManager: {
        command: execPath,
        args: [
          p.pnpm,
          '--offline',
          '--ignore-scripts',
          '--store-dir',
          p.home + '/pnpm-store',
        ],
        env: {
          COREPACK_ENABLE_NETWORK: '0',
          PNPM_HOME: p.home + '/pnpm-home',
          XDG_CACHE_HOME: '/home/dsh/.host/.cache',
        },
      },
    };
    const patches = sdk.readProfilePatches('auriga-dedicated', context, loaded);
    const composed = sdk.composeEntries([[{ insert: baseline }], patches]);
    const resolved = normalizer.resolveDedicatedResourceProfile(
      baseline,
      composed,
      approved.filter((row) => selected.includes(row.packageName)),
    );
    // Compute the real public two-anchor resolution before accepting the output;
    // boot's PluginPackages will reconstruct it against these verified files.
    await sdk.createRuntimeResolution({
      installAnchor: p.anchor,
      profile: loaded,
      home: p.home,
    });
    for (const file of controls) {
      const actual = fingerprint(file.path, 268435456);
      if (actual.size !== file.size || actual.digest !== file.digest) fail();
    }
    if (
      sha(read(p.input)) !== sha(inputBytes) ||
      readIntent(p.intent, input.binding).intentDigest !== input.intentDigest
    )
      fail();
    const config = Buffer.from(canonical(resolved) + '\n');
    if (config.length > 4194304 || controls.length > 16384) fail();
    const proof = {
      schemaVersion: 1,
      attemptId: input.attemptId,
      binding: input.binding,
      view: input.view,
      intentDigest: input.intentDigest,
      baselineDigest: input.baselineDigest,
      templateDigest: input.templateDigest,
      inputDigest: sha(inputBytes),
      imageId: input.imageId,
      configDigest: sha(config),
      configBytes: config.length,
      profileFiles: controls,
      startedBundles: context.startedBundles,
    };
    if (files.readdirSync(p.output).length) fail();
    const manifestBytes = Buffer.from(canonical(proof) + '\n');
    if (manifestBytes.length > 4194304) fail();
    outputFile('config.json', config);
    outputFile('manifest.json', manifestBytes);
    return proof;
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
)
  await createDedicatedPreparation()();
