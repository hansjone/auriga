import { Buffer } from 'node:buffer';
import { createHash, createPublicKey, verify } from 'node:crypto';
import {
  closeSync,
  cpSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readSync,
  readdirSync,
  writeFileSync,
} from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const dependencyFiles = Object.freeze({
  manifest: 'runtime-dependencies.json',
  corepack: 'corepack.tgz',
  webCache: 'web-cache.tar.gz',
  npmCache: 'npm-cache.tar.gz',
});
export const dependencySources = Object.freeze([
  'package.json',
  'pnpm-workspace.yaml',
  '.npmrc',
  'web/pnpm-lock.yaml',
  'clients/dsh-plugin/package.json',
  'clients/dsh-plugin/package-lock.json',
  'deploy/compose/real-dsh/package.json',
  'deploy/compose/real-dsh/package-lock.json',
  'deploy/compose/real-dsh/versions.json',
]);
const hex = /^[a-f0-9]{64}$/;
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
function physical(path, directory = false) {
  const file = resolve(path),
    parts = [];
  for (let at = file; ; at = dirname(at)) {
    parts.push(at);
    if (at === dirname(at)) break;
  }
  for (const at of parts.reverse())
    if (lstatSync(at).isSymbolicLink())
      throw new Error('Runtime dependency links rejected');
  const st = lstatSync(file);
  if (directory ? !st.isDirectory() : !st.isFile())
    throw new Error('Runtime dependency file type rejected');
  return file;
}
function small(path, max = 32 * 1024 ** 2) {
  const file = physical(path);
  if (lstatSync(file).size > max)
    throw new Error('Runtime dependency metadata too large');
  return readFileSync(file);
}
const json = (path) => JSON.parse(small(path));
export function hashDependencyFile(path) {
  const file = physical(path),
    fd = openSync(file, 'r'),
    h = createHash('sha256'),
    buffer = Buffer.alloc(1024 ** 2);
  try {
    for (;;) {
      const count = readSync(fd, buffer, 0, buffer.length, null);
      if (!count) break;
      h.update(buffer.subarray(0, count));
    }
  } finally {
    closeSync(fd);
  }
  return h.digest('hex');
}
export function dependencyLocks(root) {
  return Object.fromEntries(
    dependencySources.map((name) => [
      name,
      hashDependencyFile(join(root, name)),
    ]),
  );
}
function ordered(value) {
  return JSON.stringify(
    Object.fromEntries(
      Object.entries(value).sort(([a], [b]) => a.localeCompare(b)),
    ),
  );
}
function fileRows(directory) {
  return Object.fromEntries(
    Object.entries(dependencyFiles)
      .filter(([key]) => key !== 'manifest')
      .map(([key, name]) => {
        const file = physical(join(directory, name)),
          size = lstatSync(file).size;
        if (size <= 0 || size > 16 * 1024 ** 3)
          throw new Error('Runtime dependency archive length rejected');
        return [
          key,
          { path: name, bytes: size, sha256: hashDependencyFile(file) },
        ];
      }),
  );
}

export function sealDependencies(
  root,
  directory,
  nodeImageConfig,
  webSourceLocksSha256,
) {
  physical(directory, true);
  if (
    readdirSync(directory).sort().join(',') !==
    Object.values(dependencyFiles)
      .filter((x) => x !== dependencyFiles.manifest)
      .sort()
      .join(',')
  )
    throw new Error('Only the three fixed dependency archives may be sealed');
  const versions = json(join(root, 'deploy/compose/real-dsh/versions.json'));
  if (
    !/^sha256:[a-f0-9]{64}$/.test(nodeImageConfig ?? '') ||
    !hex.test(webSourceLocksSha256 ?? '')
  )
    throw new Error('Approved Node config and web seed identity required');
  const result = {
    schemaVersion: 1,
    platform: 'linux/amd64',
    nodeVersion: versions.nodeVersion,
    npmVersion: versions.npmVersion,
    nodeImage: { ref: versions.nodeImage, config: nodeImageConfig },
    packageManager: json(join(root, 'package.json')).packageManager,
    locks: dependencyLocks(root),
    webSourceLocksSha256,
    files: fileRows(directory),
  };
  writeFileSync(
    join(directory, dependencyFiles.manifest),
    JSON.stringify(result, null, 2) + '\n',
    { flag: 'wx', mode: 0o644 },
  );
  return result;
}
export function verifyDependencies(root, directory, expectedManifest) {
  physical(directory, true);
  if (
    readdirSync(directory).sort().join(',') !==
    Object.values(dependencyFiles).sort().join(',')
  )
    throw new Error(
      'Runtime dependency closure is incomplete or contains extra files',
    );
  const bytes = small(join(directory, dependencyFiles.manifest));
  if (!hex.test(expectedManifest ?? '') || sha(bytes) !== expectedManifest)
    throw new Error('Approved runtime dependency manifest SHA256 required');
  const m = JSON.parse(bytes),
    v = json(join(root, 'deploy/compose/real-dsh/versions.json'));
  if (
    m.schemaVersion !== 1 ||
    m.platform !== 'linux/amd64' ||
    m.nodeVersion !== v.nodeVersion ||
    m.npmVersion !== v.npmVersion ||
    m.nodeImage?.ref !== v.nodeImage ||
    !/^sha256:[a-f0-9]{64}$/.test(m.nodeImage?.config ?? '') ||
    m.packageManager !== json(join(root, 'package.json')).packageManager ||
    !hex.test(m.webSourceLocksSha256 ?? '') ||
    ordered(m.locks ?? {}) !== ordered(dependencyLocks(root)) ||
    JSON.stringify(m.files) !== JSON.stringify(fileRows(directory))
  )
    throw new Error(
      'Runtime dependency source locks, toolchain, or archive bytes differ',
    );
  return m;
}

// Same raw-SHA256SUMS/Ed25519 format as the existing devkit. The source's
// previously approved public key, never a cache-supplied replacement, is used.
function signedInventory(directory, keyFile) {
  physical(directory, true);
  const key = createPublicKey(small(keyFile, 64 * 1024)),
    sums = small(join(directory, 'SHA256SUMS'), 4 * 1024 ** 2),
    encoded = small(join(directory, 'SHA256SUMS.sig'), 1024)
      .toString('ascii')
      .trim();
  if (
    key.asymmetricKeyType !== 'ed25519' ||
    !/^[A-Za-z0-9+/]{86}==$/.test(encoded) ||
    !verify(null, sums, key, Buffer.from(encoded, 'base64'))
  )
    throw new Error('Runtime devkit signature rejected');
  const entries = new Map();
  for (const line of sums.toString('utf8').trimEnd().split('\n')) {
    if (!/^[a-f0-9]{64} {2}[^\r\n]+$/.test(line))
      throw new Error('Runtime devkit inventory rejected');
    const name = line.slice(66);
    if (!safeRelative(name) || entries.has(name))
      throw new Error('Runtime devkit inventory path rejected');
    entries.set(name, line.slice(0, 64));
  }
  return {
    file(name) {
      if (!safeRelative(name) || !entries.has(name))
        throw new Error('Runtime devkit file is unlisted');
      const path = physical(join(directory, name));
      if (hashDependencyFile(path) !== entries.get(name))
        throw new Error('Runtime devkit file SHA256 rejected');
      return path;
    },
    metadata(name) {
      return json(this.file(name));
    },
  };
}
function safeRelative(name) {
  return (
    typeof name === 'string' &&
    name !== '' &&
    !isAbsolute(name) &&
    !/[\\\r\n\t:]/.test(name) &&
    name.split('/').every((part) => part && part !== '.' && part !== '..')
  );
}
export function runtimeKit(
  root,
  { verifyKey = process.env.AURIGA_REAL_DSH_VERIFY_KEY } = {},
) {
  const binding = json(join(root, '.auriga-devkit.json'));
  const version =
    binding.baseline?.upstreamVersion || binding.baseline?.version;
  if (
    !isAbsolute(binding.root ?? '') ||
    !hex.test(binding.lockDigest ?? '') ||
    !/^[-A-Za-z0-9._]+$/.test(version ?? '') ||
    !/^[a-f0-9]{40}$/.test(binding.baseline?.commit ?? '')
  )
    throw new Error('Matching runtime devkit binding required');
  const key = verifyKey ?? join(root, 'release-public.pem');
  if (!verifyKey) {
    const source = signedInventory(root, key).metadata('auriga-source.json');
    if (
      source.schemaVersion !== 1 ||
      source.source?.upstreamVersion !== version ||
      source.source?.commit !== binding.baseline?.commit ||
      source.locks?.digest !== binding.lockDigest
    )
      throw new Error('Runtime devkit source binding differs');
  }
  const inventory = signedInventory(binding.root, key);
  let kit,
    files,
    catalog = false;
  try {
    const c = inventory.metadata('devkit-catalog.json'),
      entry = c.versions?.[version];
    if (
      c.schemaVersion !== 1 ||
      !entry ||
      entry.kit?.locks?.digest !== binding.lockDigest ||
      entry.commit !== binding.baseline?.commit
    )
      throw new Error('Runtime devkit does not cover source');
    kit = entry.kit;
    files = entry.files;
    catalog = true;
  } catch (error) {
    if (
      lstatSync(join(binding.root, 'devkit-catalog.json'), {
        throwIfNoEntry: false,
      })
    )
      throw error;
    kit = inventory.metadata('devkit.json');
    if (kit.locks?.digest !== binding.lockDigest)
      throw new Error('Runtime devkit lock binding differs');
  }
  if (kit.schemaVersion !== 1)
    throw new Error('Runtime devkit schema rejected');
  return {
    kit,
    file(name, expected) {
      if (!safeRelative(name))
        throw new Error('Runtime devkit member rejected');
      if (catalog) {
        const value = files?.[name];
        if (!hex.test(value ?? '') || (expected && value !== expected))
          throw new Error('Runtime devkit blob differs');
        const path = inventory.file('blobs/' + value);
        return path;
      }
      const path = inventory.file('cache/' + name);
      if (expected && hashDependencyFile(path) !== expected)
        throw new Error('Runtime devkit artifact differs');
      return path;
    },
  };
}
export function stageDependencies(
  root,
  destination,
  {
    directory = process.env.AURIGA_REAL_DSH_DEPENDENCIES_DIR,
    manifestSha256 = process.env.AURIGA_REAL_DSH_DEPENDENCIES_SHA256,
    verifyKey,
  } = {},
) {
  let sources, expected;
  if (directory) {
    if (!isAbsolute(directory))
      throw new Error('Runtime dependency directory must be absolute');
    expected = manifestSha256;
    verifyDependencies(root, directory, expected);
    sources = Object.fromEntries(
      Object.values(dependencyFiles).map((name) => [
        name,
        join(directory, name),
      ]),
    );
  } else {
    const selected = runtimeKit(root, { verifyKey }),
      spec = selected.kit.release?.build?.runtimeDependencies;
    expected = spec?.manifest?.sha256;
    sources = {};
    for (const [key, name] of Object.entries(dependencyFiles)) {
      const a = spec?.[key];
      if (
        a?.url !== 'runtime-dependencies/' + name ||
        !hex.test(a.sha256 ?? '')
      )
        throw new Error('Matching runtime dependency input is missing');
      sources[name] = selected.file(a.url, a.sha256);
    }
  }
  mkdirSync(destination, { recursive: false });
  for (const [name, file] of Object.entries(sources))
    cpSync(physical(file), join(destination, name));
  return verifyDependencies(root, destination, expected);
}
export function runtimeBaseInput(
  root,
  metadata,
  { nodeImage = process.env.NODE_IMAGE, verifyKey } = {},
) {
  const manifest = metadata.nodeImage.ref.split('@')[1];
  if (nodeImage) {
    if (nodeImage !== metadata.nodeImage.ref)
      throw new Error('NODE_IMAGE differs from the pinned runtime source');
    return { from: nodeImage, config: metadata.nodeImage.config, manifest };
  }
  const selected = runtimeKit(root, { verifyKey }),
    entries = selected.kit.images?.filter((x) => x.role === 'tool-runtime'),
    entry = entries?.length === 1 ? entries[0] : undefined;
  const ref = selected.kit.release?.build?.tools?.runtime,
    identity = /^(?:[^\s]+@)?(sha256:[a-f0-9]{64})$/.exec(ref ?? '')?.[1];
  if (
    !entry ||
    entry.ref !== ref ||
    ![manifest, metadata.nodeImage.config].includes(identity) ||
    entry.tag !== 'auriga/devkit-tool-runtime:' + identity.slice(7, 31) ||
    entry.file !== 'images/tool-runtime.tar'
  )
    throw new Error('Pinned runtime base image is missing from devkit');
  const archive = selected.file(entry.file);
  assertRuntimeImageArchive(archive, metadata.nodeImage.config, entry.tag);
  return {
    from: entry.tag,
    config: metadata.nodeImage.config,
    manifest,
    archive,
  };
}

// Inspect only Docker save metadata; never extract an image or execute its tools.
export function assertRuntimeImageArchive(path, expectedConfig, expectedTag) {
  const file = physical(path),
    size = lstatSync(file).size,
    fd = openSync(file, 'r'),
    members = new Map();
  let position = 0,
    count = 0;
  try {
    while (position + 512 <= size) {
      const header = Buffer.alloc(512);
      if (readSync(fd, header, 0, 512, position) !== 512)
        throw new Error('Runtime image tar header truncated');
      position += 512;
      if (header.every((byte) => byte === 0)) break;
      if (++count > 200000)
        throw new Error('Runtime image tar has too many members');
      const text = (start, length) =>
        header
          .subarray(start, start + length)
          .toString('utf8')
          .replace(/\0.*$/s, '');
      const octal = (start, length) => {
        const value = text(start, length).trim();
        if (!/^[0-7]+$/.test(value))
          throw new Error('Runtime image tar numeric field rejected');
        return Number.parseInt(value, 8);
      };
      const checksum = octal(148, 8),
        actual = header.reduce(
          (sum, byte, index) => sum + (index >= 148 && index < 156 ? 32 : byte),
          0,
        );
      if (actual !== checksum)
        throw new Error('Runtime image tar checksum rejected');
      const bytes = octal(124, 12),
        type = text(156, 1),
        rawName = (text(345, 155) ? text(345, 155) + '/' : '') + text(0, 100);
      let name = rawName.replace(/^(?:\.\/)+/, '');
      if (type === '5') name = name.replace(/\/$/, '');
      if (type === '5' && !name) {
        position += Math.ceil(bytes / 512) * 512;
        continue;
      }
      if (
        !safeRelative(name) ||
        members.has(name) ||
        !Number.isSafeInteger(bytes) ||
        position + bytes > size
      )
        throw new Error('Runtime image tar member rejected');
      members.set(name, { position, bytes, type });
      position += Math.ceil(bytes / 512) * 512;
    }
    const read = (name) => {
      const row = members.get(name);
      if (
        !row ||
        !['', '0'].includes(row.type) ||
        row.bytes <= 0 ||
        row.bytes > 8 * 1024 ** 2
      )
        throw new Error('Runtime image metadata missing or invalid');
      const bytes = Buffer.alloc(row.bytes);
      if (readSync(fd, bytes, 0, bytes.length, row.position) !== bytes.length)
        throw new Error('Runtime image metadata truncated');
      return bytes;
    };
    const manifests = JSON.parse(read('manifest.json'));
    if (
      !Array.isArray(manifests) ||
      manifests.length !== 1 ||
      !manifests[0].RepoTags?.includes(expectedTag) ||
      !safeRelative(manifests[0].Config)
    )
      throw new Error('Runtime image archive tag/config mapping rejected');
    const bytes = read(manifests[0].Config),
      config = JSON.parse(bytes);
    if (
      'sha256:' + sha(bytes) !== expectedConfig ||
      config.os !== 'linux' ||
      config.architecture !== 'amd64'
    )
      throw new Error(
        'Runtime image archive config differs from approved mapping',
      );
    return true;
  } finally {
    closeSync(fd);
  }
}

export function assertRuntimeBase(actual, expected) {
  // Classic engines expose config .Id; containerd may expose the source
  // manifest .Id. Keep the two reviewed identities distinct, never infer one.
  if (
    actual?.Os !== 'linux' ||
    actual?.Architecture !== 'amd64' ||
    ![expected.config, expected.manifest].includes(actual?.Id)
  )
    throw new Error(
      'Local runtime base does not match approved manifest/config mapping',
    );
  return actual.Id === expected.config ? 'config' : 'manifest';
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const [action, root, directory, config, webSeed] = process.argv.slice(2);
  if (action === 'seal' && webSeed)
    console.log(
      JSON.stringify(
        sealDependencies(resolve(root), resolve(directory), config, webSeed),
      ),
    );
  else if (action === 'verify' && config)
    console.log(
      JSON.stringify(
        verifyDependencies(resolve(root), resolve(directory), config),
      ),
    );
  else
    throw new Error(
      'Use dependencies.mjs seal source directory NodeConfig webSeedSHA | verify source directory manifestSHA',
    );
}
