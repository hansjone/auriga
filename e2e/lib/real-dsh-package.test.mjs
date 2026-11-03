import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import * as runtime from '../../deploy/compose/real-dsh/runtime.mjs';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const dockerfile = readFileSync(
  join(repository, 'deploy/compose/real-dsh/Dockerfile'),
  'utf8',
);
const buildScript = readFileSync(
  join(repository, 'deploy/compose/scripts/build-real-dsh.mjs'),
  'utf8',
);

function copyBuildContext(directory) {
  const list = /for \(const path of \[([\s\S]*?)\]\)/.exec(buildScript);
  assert.ok(list, 'public build script must declare its COPY input list');
  const inputs = [...list[1].matchAll(/'([^']+)'/g)];
  assert.ok(inputs.length > 0);
  for (const [, source] of inputs) {
    const destination = join(directory, source);
    mkdirSync(dirname(destination), { recursive: true });
    cpSync(join(repository, source), destination, { recursive: true });
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

test('real DSH image contains every declared Auriga package entry and overlay', () => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-image-package-'));
  try {
    const context = join(directory, 'context');
    const image = join(directory, 'image');
    mkdirSync(image);
    copyBuildContext(context);
    copyImagePlugin(image, context);
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
