import { test } from 'node:test';
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import fs, {
  chmodSync,
  cpSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
import * as runtime from '../../deploy/compose/real-dsh/runtime.mjs';
import { normalizeSourceModes } from '../../deploy/compose/real-dsh/source-modes.mjs';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const dockerfile = readFileSync(
  join(repository, 'deploy/compose/real-dsh/Dockerfile'),
  'utf8',
);
const incrementalDockerfile = readFileSync(
  join(repository, 'deploy/compose/real-dsh/Dockerfile.incremental'),
  'utf8',
);
const nodeImage = JSON.parse(
  readFileSync(join(repository, 'deploy/compose/real-dsh/versions.json')),
).nodeImage;
const mode = (path) => lstatSync(path).mode & 0o7777;
const baseImage = `registry.example.com/auriga/real-dsh@sha256:${'a'.repeat(64)}`;
const imageTag = 'registry.example.com/auriga/real-dsh:plugin-update';

async function inspectBuildContext(
  t,
  inspect,
  { root = repository, incremental = false, error, base = baseImage } = {},
) {
  const stop = new Error('Stop at the Docker build boundary');
  const spawn = t.mock.method(childProcess, 'spawnSync', (command, args) => {
    assert.equal(command, 'docker');
    assert.equal(args[0], 'build');
    inspect(args.at(-1), args);
    throw stop;
  });
  syncBuiltinESMExports();
  const previousImage = process.env.NODE_IMAGE;
  const previousArgv = process.argv;
  const entry = join(
    root,
    'deploy/compose/scripts',
    incremental ? 'build-real-dsh-incremental.mjs' : 'build-real-dsh.mjs',
  );
  process.argv = [process.execPath, entry];
  if (incremental)
    process.argv.push('--base-runtime-image', base, '--image-tag', imageTag);
  process.env.NODE_IMAGE = nodeImage;
  try {
    const script = pathToFileURL(entry);
    script.searchParams.set('test', t.name);
    await assert.rejects(import(script), error ?? ((err) => err === stop));
    assert.equal(spawn.mock.callCount(), error ? 0 : 1);
  } finally {
    process.argv = previousArgv;
    if (previousImage === undefined) delete process.env.NODE_IMAGE;
    else process.env.NODE_IMAGE = previousImage;
    spawn.mock.restore();
    syncBuiltinESMExports();
  }
}

// Recreate the recipe's plugin COPY inputs without installing or booting DSH.
// Unlike checking source files alone, this catches a file omitted from the image.
function copyImagePlugin(directory, context) {
  const plugin = join(context, 'clients/dsh-plugin');
  for (const line of dockerfile.split(/\r?\n/)) {
    const copy = /^COPY clients\/dsh-plugin\/(\S+) (\.\/\S*)$/.exec(line);
    if (!copy) continue;
    const [, source, target] = copy;
    if (source === 'package*.json') {
      for (const name of ['package.json', 'package-lock.json'])
        cpSync(join(plugin, name), join(directory, name));
    } else {
      cpSync(join(plugin, source), resolve(directory, target), {
        recursive: true,
      });
    }
  }
}

test('real DSH image contains every declared Auriga package entry and overlay', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-image-package-'));
  try {
    const image = join(directory, 'image');
    mkdirSync(image);
    await inspectBuildContext(t, (context) => copyImagePlugin(image, context));
    const pkg = JSON.parse(readFileSync(join(image, 'package.json')));
    for (const entry of [
      pkg.main,
      ...Object.values(pkg.exports),
      pkg.dsh.bundle.patch,
    ])
      assert.ok(
        readFileSync(resolve(image, entry)).length > 0,
        `image is missing declared plugin file ${entry}`,
      );
    const files = runtime.pluginPackageFiles(image);
    assert.ok(files.includes('cordis.patch.yml'));
    assert.ok(files.includes('src/plugin.js'));
    assert.ok(files.includes('src/client.js'));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

for (const mask of [0o077, 0o022]) {
  const umask = mask.toString(8).padStart(3, '0');
  test(
    `full build sends traversable plugin source under umask ${umask}`,
    { skip: process.platform === 'win32' && 'POSIX permission bits required' },
    async (t) => {
      const previousMask = process.umask(mask);
      try {
        await inspectBuildContext(t, (context) => {
          const source = 'clients/dsh-plugin/src';
          const modulePath = `${source}/network/base-tools-host.js`;
          assert.equal(
            mode(context),
            0o700,
            'temporary context must stay private',
          );
          assert.equal(mode(join(context, source)), 0o755, source);
          assert.equal(mode(join(context, source, 'network')), 0o755);
          assert.equal(mode(join(context, modulePath)), 0o644, modulePath);
          assert.deepEqual(
            readFileSync(join(context, modulePath)),
            readFileSync(join(repository, modulePath)),
          );
        });
      } finally {
        process.umask(previousMask);
      }
    },
  );

  test(
    `incremental build normalizes source copied under umask ${umask}`,
    { skip: process.platform === 'win32' && 'POSIX permission bits required' },
    () => {
      const directory = mkdtempSync(
        join(tmpdir(), 'auriga-incremental-source-'),
      );
      const previousMask = process.umask(mask);
      try {
        const source = join(directory, 'src');
        const nested = join(source, 'network');
        const modulePath = join(nested, 'plugin.js');
        const tool = join(nested, 'tool');
        const patch = join(directory, 'cordis.patch.yml');
        const bytes = Buffer.from([0x00, 0xff, 0x0a]);
        mkdirSync(nested, { recursive: true });
        writeFileSync(modulePath, bytes);
        writeFileSync(tool, bytes, { mode: 0o700 });
        writeFileSync(patch, bytes, { mode: 0o600 });
        assert.equal(mode(nested), 0o777 & ~mask);

        childProcess.execFileSync(process.execPath, [
          join(repository, 'deploy/compose/real-dsh/source-modes.mjs'),
          source,
          patch,
        ]);

        assert.equal(mode(directory), 0o700);
        assert.equal(mode(source), 0o755);
        assert.equal(mode(nested), 0o755);
        assert.equal(mode(modulePath), 0o644);
        assert.equal(mode(tool), 0o744);
        assert.equal(mode(patch), 0o644);
        for (const path of [modulePath, tool, patch])
          assert.deepEqual(readFileSync(path), bytes);
      } finally {
        process.umask(previousMask);
        rmSync(directory, { recursive: true, force: true });
      }
    },
  );
}

test('incremental recipe normalizes newly copied plugin inputs before generating proofs', () => {
  const copy = incrementalDockerfile.indexOf(
    'COPY real-dsh-inputs/clients/dsh-plugin/src ',
  );
  const normalize = incrementalDockerfile.indexOf(
    '/recipe/source-modes.mjs /opt/auriga-dsh/src',
  );
  const manifest = incrementalDockerfile.indexOf(
    '/opt/fixture/build.mjs manifest',
  );
  assert.ok(copy >= 0 && copy < normalize && normalize < manifest);
});

function sourceFixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-checkout-fixture-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const root = join(directory, 'checkout');
  const outside = join(directory, 'outside');
  const put = (path, bytes) => {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, bytes);
  };
  for (const path of [
    'scripts/build-real-dsh.mjs',
    'scripts/build-real-dsh-incremental.mjs',
    'scripts/real-dsh-context.mjs',
    'scripts/runtime-image-input.mjs',
    'real-dsh/Dockerfile',
    'real-dsh/Dockerfile.incremental',
    'real-dsh/package.json',
    'real-dsh/package-lock.json',
    'real-dsh/versions.json',
    'real-dsh/build.mjs',
    'real-dsh/inputs.mjs',
    'real-dsh/runtime.mjs',
    'real-dsh/start.mjs',
    'real-dsh/incremental.mjs',
    'real-dsh/incremental-fs.mjs',
    'real-dsh/source-modes.mjs',
  ]) {
    const relative = `deploy/compose/${path}`;
    put(join(root, relative), readFileSync(join(repository, relative)));
  }
  const plugin = join(root, 'clients/dsh-plugin');
  const marker = 'synthetic data outside the build allowlist';
  const pluginBytes = Buffer.from([0x00, 0xff, 0x0a]);
  for (const name of [
    'package.json',
    'package-lock.json',
    'LICENSE',
    'cordis.patch.yml',
  ])
    put(join(plugin, name), name);
  put(join(plugin, 'src/network/entry.js'), pluginBytes);
  for (const path of [
    '.git/config',
    'var/install-material.txt',
    'clients/dsh-plugin/node_modules/marker.txt',
  ])
    put(join(root, path), marker);
  put(join(outside, 'marker.txt'), marker);
  for (const name of [
    'package.json',
    'package-lock.json',
    'LICENSE',
    'cordis.patch.yml',
  ])
    put(join(outside, name), marker);
  put(join(outside, 'src/network/entry.js'), marker);
  const replaceWithLink = (path, target, type) => {
    renameSync(path, join(directory, 'displaced'));
    symlinkSync(target, path, type);
  };
  return {
    directory,
    root,
    plugin,
    outside,
    marker,
    pluginBytes,
    replaceWithLink,
  };
}

const linkedInputs = {
  'checkout root': ({ directory, root }) => {
    const alias = join(directory, 'alias');
    symlinkSync(root, alias, 'junction');
    return alias;
  },
  'intermediate plugin directory': ({ plugin, outside, replaceWithLink }) =>
    replaceWithLink(plugin, outside, 'junction'),
  'plugin source root': ({ plugin, outside, replaceWithLink }) =>
    replaceWithLink(join(plugin, 'src'), outside, 'junction'),
  'directory inside plugin source': ({ plugin, outside, replaceWithLink }) =>
    replaceWithLink(join(plugin, 'src/network'), outside, 'junction'),
  'direct input path linked to a directory': ({
    plugin,
    outside,
    replaceWithLink,
  }) => replaceWithLink(join(plugin, 'cordis.patch.yml'), outside, 'junction'),
  'direct input file symlink': ({ plugin, outside, replaceWithLink }) =>
    replaceWithLink(
      join(plugin, 'cordis.patch.yml'),
      join(outside, 'marker.txt'),
      'file',
    ),
};

for (const incremental of [false, true]) {
  const build = incremental ? 'incremental' : 'full';
  test(`${build} builder sends only the checked private allowlist to Docker`, async (t) => {
    const fixture = sourceFixture(t);
    let temp;
    await inspectBuildContext(
      t,
      (context, args) => {
        temp = context;
        const payload = incremental
          ? join(context, 'real-dsh-inputs')
          : context;
        const filename = incremental ? 'Dockerfile.incremental' : 'Dockerfile';
        assert.equal(
          args[args.indexOf('-f') + 1],
          join(payload, 'deploy/compose/real-dsh', filename),
        );
        if (incremental) {
          assert.ok(args.includes(`BASE_RUNTIME_IMAGE=${baseImage}`));
          assert.equal(args[args.indexOf('-t') + 1], imageTag);
          for (const [, source] of incrementalDockerfile.matchAll(
            /^COPY (\S+) /gm,
          )) {
            assert.ok(source.startsWith('real-dsh-inputs/'), source);
            assert.ok(fs.globSync(source, { cwd: context }).length > 0, source);
          }
        }
        for (const path of [
          '.git',
          'var',
          'clients/dsh-plugin/node_modules',
          'outside',
        ])
          assert.equal(existsSync(join(payload, path)), false, path);
        assert.deepEqual(readdirSync(payload).sort(), ['clients', 'deploy']);
        assert.deepEqual(
          readFileSync(
            join(payload, 'clients/dsh-plugin/src/network/entry.js'),
          ),
          fixture.pluginBytes,
        );
        for (const entry of readdirSync(payload, {
          recursive: true,
          withFileTypes: true,
        })) {
          if (entry.isFile())
            assert.equal(
              readFileSync(join(entry.parentPath, entry.name)).includes(
                fixture.marker,
              ),
              false,
            );
        }
      },
      { root: fixture.root, incremental },
    );
    assert.equal(existsSync(temp), false);
  });

  for (const [name, link] of Object.entries(linkedInputs)) {
    test(`${build} builder refuses a link at ${name} before copying`, async (t) => {
      const fixture = sourceFixture(t);
      let root;
      try {
        root = link(fixture) ?? fixture.root;
      } catch (error) {
        if (
          name === 'direct input file symlink' &&
          process.platform === 'win32' &&
          error.code === 'EPERM'
        ) {
          t.skip('Windows file symlink creation requires permission');
          return;
        }
        throw error;
      }
      const copy = t.mock.method(fs, 'cpSync');
      try {
        await inspectBuildContext(t, undefined, {
          root,
          incremental,
          error: /Build source link refused/,
        });
        assert.equal(copy.mock.callCount(), 0);
      } finally {
        copy.mock.restore();
        syncBuiltinESMExports();
      }
    });
  }
}

test('incremental builder refuses a tag as the base before starting Docker', async (t) => {
  await inspectBuildContext(t, undefined, {
    incremental: true,
    base: imageTag,
    error: /complete image digest/,
  });
});

test('source permission normalization rejects links without changing their target', () => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-source-links-'));
  try {
    const source = join(directory, 'src');
    const target = join(directory, 'outside');
    const link = join(source, 'link');
    mkdirSync(source);
    mkdirSync(target);
    chmodSync(target, 0o700);
    const originalMode = mode(target);
    symlinkSync(target, link, 'junction');

    assert.throws(() => normalizeSourceModes(link), /links are refused/);
    assert.throws(() => normalizeSourceModes(source), /links are refused/);
    assert.equal(mode(target), originalMode);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('package closure check rejects missing, directory and escaping declared entries', () => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-package-files-'));
  const pkg = {
    main: './plugin.js',
    exports: { '.': './plugin.js', './client': './client.js' },
    dsh: { bundle: { patch: ['./cordis.patch.yml'] } },
  };
  const save = () =>
    writeFileSync(join(directory, 'package.json'), JSON.stringify(pkg));
  try {
    writeFileSync(join(directory, 'plugin.js'), 'export {};');
    writeFileSync(join(directory, 'client.js'), 'export {};');
    save();
    assert.throws(
      () => runtime.pluginPackageFiles(directory),
      /cordis\.patch\.yml/,
    );
    mkdirSync(join(directory, 'cordis.patch.yml'));
    assert.throws(
      () => runtime.pluginPackageFiles(directory),
      /must be a file/,
    );
    rmSync(join(directory, 'cordis.patch.yml'), { recursive: true });
    writeFileSync(join(directory, 'cordis.patch.yml'), '- insert: []\n');
    assert.deepEqual(runtime.pluginPackageFiles(directory), [
      'client.js',
      'cordis.patch.yml',
      'plugin.js',
    ]);
    pkg.exports['./client'] = './../outside.js';
    save();
    assert.throws(
      () => runtime.pluginPackageFiles(directory),
      /outside the plugin package/,
    );
    pkg.exports['./client'] = './missing.js';
    save();
    assert.throws(() => runtime.pluginPackageFiles(directory), /missing\.js/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
