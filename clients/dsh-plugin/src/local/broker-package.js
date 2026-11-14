import { createHash } from 'node:crypto';
import {
  readFile,
  lstat,
  readdir,
  realpath,
  mkdir,
  writeFile,
  rename,
  rm,
} from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { fstatSync, readSync } from 'node:fs';
import { resolve, relative, isAbsolute, join } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';

/** Verify all artifact bytes before importing executable code or starting helper.
 * @param {string} directory */
export async function verifyBrokerPackage(directory) {
  if ((await lstat(directory)).isSymbolicLink())
    throw new Error('broker linked path rejected');
  const base = await realpath(directory);
  if ((await lstat(join(base, 'broker-manifest.json'))).isSymbolicLink())
    throw new Error('broker linked manifest rejected');
  const manifest = JSON.parse(
    await readFile(join(base, 'broker-manifest.json'), 'utf8'),
  );
  if (
    manifest.version !== 1 ||
    !Array.isArray(manifest.files) ||
    manifest.files.length > 10000
  )
    throw new Error('broker manifest invalid');
  const seen = new Set();
  for (const entry of manifest.files) {
    if (
      typeof entry.path !== 'string' ||
      entry.path.includes('\\') ||
      isAbsolute(entry.path) ||
      entry.path
        .split('/')
        .some((/** @type {string} */ p) => !p || p === '.' || p === '..') ||
      seen.has(entry.path) ||
      !/^[a-f0-9]{64}$/.test(entry.sha256)
    )
      throw new Error('broker manifest invalid');
    seen.add(entry.path);
    let current = base;
    for (const part of entry.path.split('/')) {
      current = join(current, part);
      if ((await lstat(current)).isSymbolicLink())
        throw new Error('broker linked path rejected');
    }
    const path = await realpath(current),
      rel = relative(base, path);
    if (rel.startsWith('..') || isAbsolute(rel))
      throw new Error('broker path escaped');
    if (
      createHash('sha256')
        .update(await readFile(path))
        .digest('hex') !== entry.sha256
    )
      throw new Error('broker digest mismatch');
  }
  async function walk(/** @type {string} */ dir, prefix = '') {
    for (const row of await readdir(dir, { withFileTypes: true })) {
      const name = prefix + row.name;
      if (row.isSymbolicLink()) throw new Error('broker linked path rejected');
      if (row.isDirectory()) await walk(join(dir, row.name), name + '/');
      else if (name !== 'broker-manifest.json' && !seen.has(name))
        throw new Error('broker unlisted file rejected');
    }
  }
  await walk(base);
  for (const name of ['ipc.mjs', 'schema.mjs', 'windows/shell-helper.exe'])
    if (!seen.has(name)) throw new Error('broker incomplete');
  return { base, manifest };
}

/** Only the inherited private descriptor is accepted. No environment, argv,
 * setting, HTTP header or page may choose a helper or connection capability. */
/** @type {any} */ let descriptorCache;
let descriptorRead = false;
function inheritedDescriptor() {
  if (descriptorRead) return descriptorCache;
  descriptorRead = true;
  try {
    if (!fstatSync(3).isFIFO()) return undefined;
    const bytes = Buffer.alloc(65537);
    let size = 0;
    for (;;) {
      const n = readSync(3, bytes, size, bytes.length - size, null);
      size += n;
      if (!n) break;
      if (size >= bytes.length) throw new Error('broker descriptor limit');
    }
    descriptorCache = JSON.parse(bytes.subarray(0, size).toString('utf8'));
    return descriptorCache;
  } catch {
    return undefined;
  }
}

/** Preserve installation-owned raw-credential policy across plugin restarts.
 * Invalid or linked prior configuration fails closed rather than resetting a
 * previously disabled policy to its permissive default.
 * @param {string} root @param {any} config */
export async function writeBrokerInstallation(root, config) {
  await mkdir(root, { recursive: true, mode: 0o700 });
  if ((await lstat(root)).isSymbolicLink())
    throw new Error('broker linked installation rejected');
  const file = join(root, 'desktop-origin.json'),
    temp = join(root, randomUUID() + '.tmp');
  let previous;
  try {
    const stat = await lstat(file);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 65536)
      throw new Error('broker installation invalid');
    previous = JSON.parse(await readFile(file, 'utf8'));
    if (
      !previous ||
      typeof previous !== 'object' ||
      Array.isArray(previous) ||
      (Object.hasOwn(previous, 'allowRawAgentToken') &&
        typeof previous.allowRawAgentToken !== 'boolean')
    )
      throw new Error('broker installation invalid');
  } catch (error) {
    if (/** @type {any} */ (error).code !== 'ENOENT') throw error;
  }
  try {
    await writeFile(
      temp,
      JSON.stringify({
        origin: config.platformOrigin,
        issuer: config.issuer ?? config.platformOrigin,
        clientId: config.clientId ?? 'auriga-dsh',
        ...(config.network ? { network: config.network } : {}),
        ...(previous?.allowRawAgentToken !== undefined
          ? { allowRawAgentToken: previous.allowRawAgentToken }
          : {}),
      }),
      { flag: 'wx', mode: 0o600 },
    );
    await rename(temp, file);
  } finally {
    await rm(temp, { force: true });
  }
}

/** @param {{directory?:string,config?:any,dataHome?:string}} [options] */
export async function connectPackagedBroker(options = {}) {
  let descriptor = inheritedDescriptor();
  /** @type {import('node:child_process').ChildProcess|undefined} */ let child;
  const stop = () => {
    if (!child || child.exitCode !== null) return;
    const owned = child;
    if (owned.connected) owned.send({ action: 'close' }, () => {});
    const timer = setTimeout(() => {
      owned.kill();
    }, 5000);
    timer.unref();
    owned.once('exit', () => clearTimeout(timer));
  };
  try {
    if (
      !descriptor &&
      process.platform === 'win32' &&
      options.config?.platformOrigin &&
      options.dataHome
    ) {
      await verifyBrokerPackage(
        options.directory ??
          fileURLToPath(new URL('../../shell-auth/', import.meta.url)),
      );
      const root = join(options.dataHome, 'broker-installation');
      await writeBrokerInstallation(root, options.config);
      // Spawn only our packaged entry, with public storage paths on argv. The
      // descriptor arrives exclusively on the inherited Node IPC pipe.
      child = spawn(
        process.execPath,
        [
          fileURLToPath(new URL('./broker-launcher.js', import.meta.url)),
          root,
          options.dataHome,
        ],
        {
          windowsHide: true,
          stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
        },
      );
      descriptor = await new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          cleanup();
          reject(new Error('broker startup timeout'));
        }, 15000);
        const failed = () => {
          cleanup();
          reject(new Error('broker startup unavailable'));
        };
        const received = (/** @type {any} */ message) => {
          cleanup();
          if (!message?.descriptor)
            reject(new Error('broker startup unavailable'));
          else resolve(message.descriptor);
        };
        const cleanup = () => {
          clearTimeout(timer);
          child?.off('error', failed);
          child?.off('exit', failed);
          child?.off('message', received);
        };
        child?.once('error', failed);
        child?.once('exit', failed);
        child?.once('message', received);
      });
    }
    if (!descriptor) return undefined;
    const directory =
      options.directory ??
      fileURLToPath(new URL('../../shell-auth/', import.meta.url));
    const artifact = await verifyBrokerPackage(directory);
    const helper = artifact.manifest.files.find(
      (/** @type {any} */ e) => e.path === 'windows/shell-helper.exe',
    );
    if (
      descriptor.protocolVersion !== 1 ||
      typeof descriptor.pipe !== 'string' ||
      !/^auriga-shell-[a-f0-9]{32}$/.test(descriptor.pipe) ||
      !/^[A-Za-z0-9_-]{43}$/.test(descriptor.capability) ||
      typeof descriptor.helperPath !== 'string'
    )
      throw new Error('broker descriptor invalid');
    const actual = await realpath(descriptor.helperPath);
    if (
      createHash('sha256')
        .update(await readFile(actual))
        .digest('hex') !== helper.sha256
    )
      throw new Error('broker helper version mismatch');
    const { connectIpc } = await import(
      pathToFileURL(resolve(artifact.base, 'ipc.mjs')).href
    );
    const { requireValid } = await import(
      pathToFileURL(resolve(artifact.base, 'schema.mjs')).href
    );
    const ipc = connectIpc(descriptor),
      close = ipc.close.bind(ipc);
    ipc.close = () => {
      close();
      stop();
    };
    child?.once('exit', () => close());
    child?.on('error', () => close());
    return {
      ipc,
      validate: requireValid,
      operations: JSON.parse(
        await readFile(
          join(artifact.base, 'schemas/plugin/shell-business-operations.json'),
          'utf8',
        ),
      ).operations,
    };
  } catch (error) {
    stop();
    throw error;
  }
}
