import { Buffer } from 'node:buffer';
import { isIP } from 'node:net';
import { TextDecoder } from 'node:util';
import { inflateRawSync } from 'node:zlib';
import { validateArchive } from '../clients/dsh-plugin/src/resources/archive.js';
import { parseStrictJson } from '../clients/dsh-plugin/src/resources/json.js';

const recordSize = 512;
const maxExpandedBytes = 256 * 1024 * 1024;
const maxPackageJsonBytes = 1024 * 1024;
const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
const npmName = /^(?:@[a-z0-9-][a-z0-9._-]*\/)?[a-z0-9-][a-z0-9._-]*$/;
const npmBuiltFields = [
  'onlyBuiltDependencies',
  'allowBuilds',
  'onlyBuiltDependenciesFile',
  'dangerouslyAllowAllBuilds',
];

export function catalogTrim(value) {
  // ResourceBody.text and CatalogManifest.range use Java String.trim().
  // eslint-disable-next-line no-control-regex
  return value.replace(/^[\x00-\x20]+|[\x00-\x20]+$/g, '');
}

export function validCatalogHost(host) {
  if (host.includes(':')) {
    // InetAddress returns IPv4 for IPv4-mapped IPv6, which is not admitted here.
    return (
      /^[0-9a-f:]+$/.test(host) &&
      isIP(host) === 6 &&
      !new URL(`http://[${host}]`).hostname.startsWith('[::ffff:')
    );
  }
  if (/^[0-9.]+$/.test(host)) return isIP(host) === 4;
  return host
    .split('.')
    .every((part) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part));
}

function invalid(detail) {
  throw new Error(`Invalid plugin tgz: ${detail}`);
}

function inflateCatalogMember(archive) {
  // The shared validator already checks gzip integrity. Inspect the member
  // boundary here since gunzip also accepts concatenated members and padding.
  const payload = Buffer.from(
    archive.buffer,
    archive.byteOffset,
    archive.byteLength,
  );
  const header = payload.subarray(0, Math.min(65536, payload.length - 8));
  const flags = header[3];
  let start = 10;
  if (!header.subarray(0, 3).equals(Uint8Array.of(31, 139, 8)) || flags & 0xe0)
    invalid('gzip header');
  if (flags & 4) {
    if (start + 2 > header.length) invalid('gzip header');
    start += 2 + header.readUInt16LE(start);
  }
  for (const flag of [8, 16]) {
    if (!(flags & flag)) continue;
    const end = header.indexOf(0, start);
    if (end < 0) invalid('gzip header');
    start = end + 1;
  }
  if (flags & 2) start += 2;
  if (start > header.length) invalid('gzip header');
  const { buffer, engine } = inflateRawSync(payload.subarray(start), {
    info: true,
    maxOutputLength: Math.min(maxExpandedBytes, payload.length * 100),
  });
  if (start + engine.bytesWritten + 8 !== payload.length)
    invalid('expected one gzip member with no trailing bytes');
  return buffer;
}

function tarText(field) {
  const end = field.indexOf(0);
  if (end < 0) return decoder.decode(field);
  if (field.subarray(end).some(Boolean)) invalid('ustar name padding');
  return decoder.decode(field.subarray(0, end));
}

function tarNumber(field) {
  const raw = field.toString('latin1');
  if (!/^ *[0-7]*[ \0]*$/.test(raw)) invalid('ustar numeric field');
  return Number.parseInt(raw, 8) || 0;
}

function checkPath(path) {
  for (const part of path.split('/')) {
    const base = part.split('.')[0].replace(/[ .]+$/, '').toUpperCase();
    if (
      !part ||
      part === '.' ||
      part === '..' ||
      /[. ]$/.test(part) ||
      // eslint-disable-next-line no-control-regex
      /[\x00-\x1f\x7f-\x9f<>:"|?*\\]/.test(part) ||
      /^(?:CON|PRN|AUX|NUL|CONIN\$|CONOUT\$|(?:COM|LPT)[1-9¹²³])$/.test(base)
    )
      invalid('unsafe catalog path');
  }
}

function inspectCatalogTar(data) {
  const entries = new Map();
  const parents = new Set();
  const files = new Set();
  const metadata = new Map();
  let metadataBytes = 0;
  for (let offset = 0; offset < data.length; ) {
    const header = data.subarray(offset, offset + recordSize);
    if (header.every((byte) => byte === 0)) break;
    if (header[157] !== 0 || header.subarray(500).some(Boolean))
      invalid('unsupported ustar header data');
    for (const [from, to] of [
      [100, 108],
      [108, 116],
      [116, 124],
      [136, 148],
      [148, 156],
      [329, 337],
      [337, 345],
    ])
      tarNumber(header.subarray(from, to));
    const name = tarText(header.subarray(0, 100));
    const prefix = tarText(header.subarray(345, 500));
    const directory = header[156] === 53;
    if (!name || (!directory && name.endsWith('/')))
      invalid('ustar entry name');
    const fullPath = prefix ? prefix + '/' + name : name;
    const path = directory ? fullPath.replace(/\/$/, '') : fullPath;
    checkPath(path);
    if (
      (path !== 'package' && !path.startsWith('package/')) ||
      Buffer.byteLength(path) > 240 ||
      path.split('/').length > 16
    )
      invalid('catalog path exceeds bounds');
    const key = path.normalize('NFC').toUpperCase();
    if (entries.has(key) || (!directory && parents.has(key)))
      invalid('catalog path collision');
    for (
      let slash = key.indexOf('/');
      slash >= 0;
      slash = key.indexOf('/', slash + 1)
    ) {
      const parent = key.slice(0, slash);
      if (entries.get(parent) === false) invalid('catalog path collision');
      parents.add(parent);
    }
    entries.set(key, directory);

    const size = tarNumber(header.subarray(124, 136));
    const start = offset + recordSize;
    const end = start + size;
    offset = start + Math.ceil(size / recordSize) * recordSize;
    if (data.subarray(end, offset).some(Boolean))
      invalid('nonzero ustar padding');
    if (directory) continue;
    if (path.toLowerCase().endsWith('/binding.gyp'))
      invalid('native build file');
    files.add(path);
    if (path.endsWith('/package.json')) {
      metadataBytes += size;
      if (
        size > maxPackageJsonBytes ||
        metadata.size >= 512 ||
        metadataBytes > 16 * maxPackageJsonBytes
      )
        invalid('npm metadata exceeds catalog bounds');
      metadata.set(path, parseStrictJson(data.subarray(start, end)));
    }
  }
  return { files, metadata };
}

const isObject = (value) =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

function checkNpmMetadata(path, pkg, files) {
  if (!isObject(pkg) || Object.hasOwn(pkg, 'gypfile'))
    invalid('invalid package.json or native build declaration');
  if (
    pkg.scripts != null &&
    (!isObject(pkg.scripts) || Object.keys(pkg.scripts).length)
  )
    invalid('npm scripts are not allowed');
  if (
    pkg.pnpm != null &&
    (!isObject(pkg.pnpm) ||
      npmBuiltFields.some((field) => Object.hasOwn(pkg.pnpm, field)))
  )
    invalid('pnpm build declarations are not allowed');
  const bundled = Object.hasOwn(pkg, 'bundledDependencies')
    ? pkg.bundledDependencies
    : pkg.bundleDependencies;
  const base = path.slice(0, -'package.json'.length);
  for (const field of [
    'dependencies',
    'optionalDependencies',
    'peerDependencies',
  ]) {
    const dependencies = pkg[field];
    if (dependencies == null) continue;
    if (!isObject(dependencies))
      invalid(`package.json ${field} must be an object`);
    for (const name of Object.keys(dependencies)) {
      if (field === 'peerDependencies') {
        if (!/^@deepseek-ai\/dsh(?:-[^\r\n\u0085\u2028\u2029]*)?$/.test(name))
          invalid('unsupported npm peer dependency');
      } else if (
        !npmName.test(name) ||
        name === 'node_modules' ||
        name === 'favicon.ico' ||
        !(bundled === true || (Array.isArray(bundled) && bundled.includes(name))) ||
        !files.has(`${base}node_modules/${name}/package.json`)
      )
        invalid('npm dependencies must be declared and bundled');
    }
  }
}

export async function verifyCatalogPluginArchive(payload, manifest) {
  // Keep the existing client archive admission as well as the catalog's
  // stricter TarGzipArchiveValidator and PluginArchiveValidator checks.
  await validateArchive(payload, {
    name: manifest.plugin.packageName,
    version: manifest.version,
  });
  const { files, metadata } = inspectCatalogTar(inflateCatalogMember(payload));
  const root = metadata.get('package/package.json');
  if (!isObject(root)) invalid('missing npm package metadata');
  if (Object.hasOwn(root, 'engines')) {
    if (!isObject(root.engines))
      invalid('package.json engines must be an object');
    if (
      Object.hasOwn(root.engines, 'dsh') &&
      root.engines.dsh !== manifest.plugin.dshVersionRange
    )
      invalid('package.json engines.dsh must exactly match the sidecar DSH range');
  }
  const patch = root.dsh.bundle.patch;
  if (!patch || patch !== catalogTrim(patch) || [...patch].length > 240)
    invalid('invalid package.json DSH patch path');
  for (const [path, pkg] of metadata) checkNpmMetadata(path, pkg, files);
}
