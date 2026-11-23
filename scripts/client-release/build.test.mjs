import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, join, resolve } from 'node:path';
import {
  collectPluginModulePaths,
  launchOfflineBuild,
  resolveBuildPaths,
} from './build.mjs';

function makeTempDir(t, prefix) {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  t.after(() =>
    rmSync(dir, {
      recursive: true,
      force: true,
      maxRetries: 3,
      retryDelay: 100,
    }),
  );
  return dir;
}

test('launchOfflineBuild sets worker TEMP/TMP to <out>/tmp and cleans it up by default', (t) => {
  const base = makeTempDir(t, 'auriga-client-build-temp-');
  const sourceRoot = join(base, 'src');
  const kitDir = join(base, 'kit');
  const outDir = join(base, 'out');
  mkdirSync(sourceRoot, { recursive: true });
  mkdirSync(kitDir, { recursive: true });
  writeFileSync(join(sourceRoot, 'README.md'), 'hello\n');

  let capturedEnv;
  let tempExistedDuringBuild = false;
  launchOfflineBuild(
    { offline: true, out: outDir },
    { directory: kitDir, manifestSha256: '0'.repeat(64) },
    {
      root: sourceRoot,
      spawn: (_cmd, _args, opts) => {
        capturedEnv = opts.env;
        tempExistedDuringBuild = existsSync(join(outDir, 'tmp'));
        return { status: 0 };
      },
    },
  );

  assert.equal(tempExistedDuringBuild, true);
  assert.equal(capturedEnv.TEMP, join(outDir, 'tmp'));
  assert.equal(capturedEnv.TMP, join(outDir, 'tmp'));
  const tempKeys = Object.keys(capturedEnv).filter((k) =>
    /^(temp|tmp)$/i.test(k),
  );
  assert.deepEqual(tempKeys.sort(), ['TEMP', 'TMP']);
  assert.equal(existsSync(join(outDir, 'tmp')), false);
});

test('launchOfflineBuild respects explicit --temp and rejects temp inside readonly devkit', (t) => {
  const base = makeTempDir(t, 'auriga-client-build-custom-temp-');
  const sourceRoot = join(base, 'src');
  const kitDir = join(base, 'kit');
  const outDir = join(base, 'out');
  const customTemp = join(base, 'my-temp');
  mkdirSync(sourceRoot, { recursive: true });
  mkdirSync(kitDir, { recursive: true });
  writeFileSync(join(sourceRoot, 'README.md'), 'hello\n');

  let capturedEnv;
  launchOfflineBuild(
    resolveBuildPaths({ offline: true, out: outDir, temp: customTemp }),
    { directory: kitDir, manifestSha256: '0'.repeat(64) },
    {
      root: sourceRoot,
      spawn: (_cmd, _args, opts) => {
        capturedEnv = opts.env;
        return { status: 0 };
      },
    },
  );

  assert.equal(capturedEnv.TEMP, resolve(customTemp));
  assert.equal(capturedEnv.TMP, resolve(customTemp));
  assert.equal(existsSync(customTemp), true);

  assert.throws(
    () =>
      launchOfflineBuild(
        {
          offline: true,
          out: join(base, 'out-2'),
          temp: join(kitDir, 'bad-temp'),
        },
        { directory: kitDir, manifestSha256: '0'.repeat(64) },
        { root: sourceRoot, spawn: () => ({ status: 0 }) },
      ),
    /临时目录不得位于只读 devkit 内/,
  );
});

test('collectPluginModulePaths merges env override, kit plugin-deps, and workspace node_modules in order', (t) => {
  const base = makeTempDir(t, 'auriga-client-plugin-paths-');
  const root = join(base, 'repo');
  const kit = join(base, 'kit');
  const extra = join(base, 'extra-modules');
  const pluginNm = join(root, 'clients/dsh-plugin/node_modules');
  const kitPluginNm = join(kit, 'plugin-deps/node_modules');
  const webNm = join(root, 'web/node_modules');
  const rootNm = join(root, 'node_modules');
  for (const dir of [extra, pluginNm, kitPluginNm, webNm, rootNm])
    mkdirSync(dir, { recursive: true });

  const paths = collectPluginModulePaths(root, kit, {
    AURIGA_CLIENT_PLUGIN_MODULES: [extra, kitPluginNm, ''].join(delimiter),
  });

  assert.deepEqual(paths, [extra, kitPluginNm, pluginNm, webNm, rootNm]);
});
