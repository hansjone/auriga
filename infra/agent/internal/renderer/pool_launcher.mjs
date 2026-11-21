import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { mkdirSync, readFileSync, realpathSync } from 'node:fs';
import {
  join,
  resolve,
  posix,
  relative,
  isAbsolute,
  sep,
  dirname,
} from 'node:path';
import { networkInterfaces } from 'node:os';
import { isIPv4 } from 'node:net';

const sdkAnchor = '/opt/dsh/package.json';
const sdkAnchorUrl = 'file:///opt/dsh/package.json';
const aurigaRoot = '/opt/auriga-dsh';
const logicalProfile = '/run/auriga/pool-profile.yml';
const entryRoot = '/run/auriga/pool-entry.yml';
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;

function within(root, target) {
  const value = relative(root, target);
  return (
    value !== '' &&
    value !== '..' &&
    !value.startsWith('..' + sep) &&
    !isAbsolute(value)
  );
}

/** Internal builtin seams for pure bootstrap tests. Production passes no
 * arguments; paths, imports and SDK anchors are never YAML/env/CLI choices. */
export function createSharedPoolLauncher({
  requireFrom = createRequire,
  importModule = (url) => import(url),
  readFile = readFileSync,
  realpath = realpathSync,
  mkdir = mkdirSync,
  interfaces = networkInterfaces,
  env = process.env,
  onSignal = (signal, listener) => process.on(signal, listener),
  exit = (code) => process.exit(code),
} = {}) {
  return async () => {
    const binding = JSON.parse(
      readFile('/run/auriga/pool-binding.json', 'utf8'),
    );
    if (
      binding.schemaVersion !== 1 ||
      binding.mode !== 'shared' ||
      !uuid.test(binding.physicalInstanceId) ||
      !uuid.test(binding.tenantId) ||
      env.AURIGA_MODE !== 'shared' ||
      binding.physicalInstanceId !== env.AURIGA_INSTANCE_ID
    )
      throw new Error('Shared pool installation mismatch');
    for (const key of Object.keys(env)) {
      if (
        /^(AURIGA_OWNER_ID|AURIGA_.*(?:TOKEN|CREDENTIAL).*|DSH_AURIGA_(?:ID|TOKEN)|DSH_TEAM_.*|DSH_SESSION_ID)$/.test(
          key,
        )
      )
        throw new Error('Shared process received personal material');
    }
    const require = requireFrom(sdkAnchor);
    const manifestPath = posix.join(aurigaRoot, 'package.json');
    const pkg = JSON.parse(readFile(manifestPath, 'utf8'));
    if (
      pkg.name !== 'auriga-dsh' ||
      pkg.exports?.['./shared'] !== './src/shared/plugin.js' ||
      pkg.exports?.['./shared-ports'] !== './src/shared/ports-plugin.js' ||
      pkg.exports?.['./shared-browser'] !== './src/shared-browser/plugin.js' ||
      realpath(require.resolve('auriga-dsh/package.json')) !==
        realpath(manifestPath) ||
      realpath(posix.join(aurigaRoot, pkg.exports['./shared-browser'])) !==
        join(realpath(aurigaRoot), 'src/shared-browser/plugin.js')
    )
      throw new Error('Shared readonly Auriga export mismatch');
    const sdkRoot = realpath('/opt/dsh');
    const bootPath = realpath(require.resolve('@deepseek-ai/dsh-app-boot'));
    const bootManifestPath = realpath(
      require.resolve('@deepseek-ai/dsh-app-boot/package.json'),
    );
    if (!within(sdkRoot, bootPath) || !within(sdkRoot, bootManifestPath))
      throw new Error('Shared boot SDK escaped its readonly anchor');
    const bootManifest = JSON.parse(readFile(bootManifestPath, 'utf8'));
    const publicRoot = bootManifest.exports?.['.'];
    const bootExport =
      typeof publicRoot === 'string'
        ? publicRoot
        : publicRoot &&
            Object.keys(publicRoot).every((key) =>
              ['types', 'default'].includes(key),
            )
          ? publicRoot.default
          : undefined;
    if (
      bootManifest.name !== '@deepseek-ai/dsh-app-boot' ||
      bootManifest.version !== '0.2.1-rc.3' ||
      typeof bootExport !== 'string' ||
      !bootExport.startsWith('./') ||
      !within(dirname(bootManifestPath), bootPath) ||
      realpath(resolve(dirname(bootManifestPath), bootExport)) !== bootPath
    )
      throw new Error('Shared boot SDK public export mismatch');
    if (!within(sdkRoot, realpath(require.resolve('js-yaml'))))
      throw new Error('Shared YAML parser escaped its SDK');
    const { boot } = await importModule(pathToFileURL(bootPath).href);
    const { resolveSharedRuntimeProfile } = await importModule(
      'file:///opt/auriga-dsh/src/shared/profile-resolution.js',
    );
    const yaml = require('js-yaml');
    const rows = yaml.load(readFile(logicalProfile, 'utf8'), {
      schema: yaml.JSON_SCHEMA,
    });
    if (readFile(entryRoot, 'utf8').trim() !== '[]')
      throw new Error('Shared boot root must be immutable and empty');
    if (!Array.isArray(rows) || rows.length !== 65)
      throw new Error('Complete shared profile required');
    const listeners = rows.filter(
      (row) => row.name === '@deepseek-ai/dsh-host-webserver',
    );
    if (listeners.length !== 1 || listeners[0].config?.host !== '127.0.0.1')
      throw new Error('Fixed shared listener required');
    const addresses = [
      ...new Set(
        Object.values(interfaces()).flatMap((entries) =>
          (entries ?? [])
            .filter(
              (entry) =>
                entry.internal === false &&
                entry.family === 'IPv4' &&
                isIPv4(entry.address) &&
                !entry.address.startsWith('127.'),
            )
            .map((entry) => entry.address),
        ),
      ),
    ];
    if (addresses.length !== 1)
      throw new Error('Shared host requires one isolated IPv4 address');
    const prepared = rows.map((row) =>
      row === listeners[0]
        ? { ...row, config: { ...row.config, host: addresses[0] } }
        : row,
    );
    const normalized = resolveSharedRuntimeProfile(prepared);
    const home = '/home/dsh/.host';
    mkdir(posix.join(home, '.dsh'), { recursive: true });
    env.HOME = home;
    env.DSH_HOME = posix.join(home, '.dsh');
    const profile = {
      name: 'auriga-shared',
      dir: '/run/auriga',
      home: env.DSH_HOME,
      cwd: home,
      installAnchor: sdkAnchor,
      patchPath: '/run/auriga/unused.patch.yml',
      overlays: [],
      startedBundles: [],
      telemetryDisabledEnv: '1',
    };
    const callbacks = new Set();
    // rc.3 boot takes a file path, not an EntryList. Its public insert patch
    // mounts exactly the normalized rows into our fixed readonly empty root.
    const ctx = await boot(
      'auriga-shared',
      entryRoot,
      [{ insert: normalized }],
      (root) => {
        root.provide('profileContext', profile);
        root.provide('appReady', {
          onReady(fn) {
            callbacks.add(fn);
            return () => callbacks.delete(fn);
          },
        });
      },
      sdkAnchorUrl,
    );
    try {
      if (!ctx.aurigaShared || !ctx.webServer || !ctx.aurigaSharedPorts)
        throw new Error('Shared host authority ports unavailable');
      for (const fn of callbacks) await fn();
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
              pluginVersion: pkg.version,
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
  await createSharedPoolLauncher()();
