import { test } from 'node:test';
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import {
  chmodSync,
  cpSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
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

async function inspectBuildContext(t, inspect) {
  const stop = new Error('Stop at the Docker build boundary');
  const spawn = t.mock.method(childProcess, 'spawnSync', (command, args) => {
    assert.equal(command, 'docker');
    assert.equal(args[0], 'build');
    inspect(args.at(-1));
    throw stop;
  });
  syncBuiltinESMExports();
  const previousImage = process.env.NODE_IMAGE;
  process.env.NODE_IMAGE = nodeImage;
  try {
    const script = new URL(
      '../../deploy/compose/scripts/build-real-dsh.mjs',
      import.meta.url,
    );
    script.searchParams.set('test', t.name);
    await assert.rejects(import(script), (error) => error === stop);
    assert.equal(spawn.mock.callCount(), 1);
  } finally {
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
  const copy = incrementalDockerfile.indexOf('COPY clients/dsh-plugin/src ');
  const normalize = incrementalDockerfile.indexOf(
    '/recipe/source-modes.mjs /opt/auriga-dsh/src',
  );
  const manifest = incrementalDockerfile.indexOf(
    '/opt/fixture/build.mjs manifest',
  );
  assert.ok(copy >= 0 && copy < normalize && normalize < manifest);
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
