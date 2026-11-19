// This is the real dedicated entrypoint. Its generated configuration is a
// separate Agent-owned readonly mount, never a writable profile include.
import * as fs from 'node:fs';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { relative, isAbsolute, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { dedicatedPaths } from './dedicated-prepare.mjs';

const sha = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const fail = () => {
  throw new Error('Dedicated prepared profile rejected');
};

export function createDedicatedPoolLauncher({
  files = fs,
  importModule = (url) => import(url),
  env = process.env,
  onSignal = (name, listener) => process.on(name, listener),
  exit = (code) => process.exit(code),
} = {}) {
  const read = (file, maximum) => {
    const info = files.lstatSync(file);
    if (!info.isFile() || info.isSymbolicLink() || info.size > maximum) fail();
    return files.readFileSync(file);
  };
  const fingerprint = (file) => {
    const before = files.lstatSync(file);
    if (!before.isFile() || before.isSymbolicLink() || before.size > 268435456)
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
      const hash = createHash('sha256'),
        chunk = Buffer.alloc(65536);
      let size = 0,
        n;
      while ((n = files.readSync(fd, chunk, 0, chunk.length, null)) > 0) {
        size += n;
        if (size > 268435456) fail();
        hash.update(chunk.subarray(0, n));
      }
      return { size, digest: 'sha256:' + hash.digest('hex') };
    } finally {
      files.closeSync(fd);
    }
  };
  return async () => {
    const p = dedicatedPaths;
    const inputBytes = read(p.input, 1048576),
      input = JSON.parse(inputBytes);
    const proof = JSON.parse(read(p.output + '/manifest.json', 4194304));
    const config = read(p.output + '/config.json', 4194304);
    const normalizer = await importModule(
      'file:///opt/auriga-dsh/src/dedicated/profile-resolution.js',
    );
    const { canonical } = await importModule(
      'file:///opt/auriga-dsh/src/shared/protocol.js',
    );
    if (
      input.binding?.mode !== 'dedicated' ||
      env.AURIGA_MODE !== 'dedicated' ||
      env.AURIGA_INSTANCE_ID !== input.binding.physicalInstanceId ||
      proof.schemaVersion !== 1 ||
      proof.attemptId !== input.attemptId ||
      proof.inputDigest !== sha(inputBytes) ||
      proof.imageId !== input.imageId ||
      proof.templateDigest !== input.templateDigest ||
      proof.baselineDigest !== normalizer.DEDICATED_BASELINE_DIGEST ||
      proof.intentDigest !== input.intentDigest ||
      canonical(proof.binding) !== canonical(input.binding) ||
      canonical(proof.view) !== canonical(input.view) ||
      proof.configDigest !== sha(config) ||
      proof.configBytes !== config.length ||
      !Array.isArray(proof.profileFiles) ||
      proof.profileFiles.length > 16384
    )
      fail();
    for (const key of Object.keys(env))
      if (
        /^(AURIGA_OWNER_ID|AURIGA_.*(?:TOKEN|CREDENTIAL).*|DSH_AURIGA_(?:ID|TOKEN)|DSH_TEAM_.*|DSH_SESSION_ID)$/.test(
          key,
        )
      )
        fail();
    const checkFiles = () => {
      for (const file of proof.profileFiles) {
        const suffix = relative(p.profile, file.path);
        if (
          file.path !== p.home + '/cordis.patch.yml' &&
          (isAbsolute(suffix) ||
            suffix === '..' ||
            suffix.startsWith('..' + sep))
        )
          fail();
        const real = files.realpathSync(file.path),
          root = files.realpathSync(p.profile);
        const resolved = relative(root, real);
        if (
          file.path !== p.home + '/cordis.patch.yml' &&
          (isAbsolute(resolved) ||
            resolved === '..' ||
            resolved.startsWith('..' + sep))
        )
          fail();
        const actual = fingerprint(file.path);
        if (actual.size !== file.size || actual.digest !== file.digest) fail();
      }
    };
    checkFiles();
    const sdk = await importModule(
      normalizer.resolveDedicatedSdkModule('@deepseek-ai/dsh-app-boot'),
    );
    const loaded = sdk.loadProfileDirectory(
      'auriga-dedicated',
      p.profile,
      p.anchor,
    );
    if (
      loaded.skippedBundles.length ||
      canonical(loaded.layers.map((row) => row.packageName)) !==
        canonical(proof.startedBundles)
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
      startedBundles: proof.startedBundles,
      packageManager: {
        command: process.execPath,
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
    const resolution = await sdk.createRuntimeResolution({
      installAnchor: p.anchor,
      profile: loaded,
      home: p.home,
    });
    checkFiles();
    env.HOME = '/home/dsh/.host';
    env.DSH_HOME = p.home;
    delete env.DSH_BUNDLED_SKILL_DIR;
    const callbacks = new Set();
    const ctx = await sdk.boot(
      'auriga-dedicated',
      p.output + '/config.json',
      [],
      async (root) => {
        root.provide('profileContext', context);
        root.provide('appReady', {
          onReady(callback) {
            callbacks.add(callback);
            return () => callbacks.delete(callback);
          },
        });
        await root.plugin(sdk.PluginPackages, { resolution });
      },
      'file:///opt/dsh/package.json',
    );
    try {
      if (
        !ctx.aurigaDedicated ||
        !ctx.webServer ||
        !ctx.pluginManager ||
        !ctx.skills
      )
        fail();
      for (const callback of callbacks) await callback();
      const detach = ctx.webServer.register({
        kind: 'prefix',
        path: '/_auriga/health',
        handler(req, res) {
          if (
            req.method !== 'GET' ||
            req.url?.split('?')[0] !== '/_auriga/health'
          ) {
            res.writeHead(404);
            res.end();
            return;
          }
          res.writeHead(200, {
            'Content-Type': 'application/json',
            'Cache-Control': 'no-store',
          });
          res.end(
            JSON.stringify({
              status: 'ok',
              ready: true,
              protocol: 1,
              pluginVersion: JSON.parse(
                read('/opt/auriga-dsh/package.json', 1048576),
              ).version,
            }),
          );
        },
      });
      let stopping = false;
      const stop = async () => {
        if (stopping) return;
        stopping = true;
        detach();
        await ctx.fiber.dispose();
        exit(0);
      };
      onSignal('SIGTERM', stop);
      onSignal('SIGINT', stop);
      return ctx;
    } catch (error) {
      await ctx.fiber.dispose();
      throw error;
    }
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
)
  await createDedicatedPoolLauncher()();
