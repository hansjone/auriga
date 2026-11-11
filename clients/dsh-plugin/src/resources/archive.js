import { gunzipSync } from 'node:zlib';
import { TextDecoder } from 'node:util';
import { parseStrictJson } from './json.js';
import { refuse } from './trust.js';
const decoder = new TextDecoder('utf-8', { fatal: true });
/** Validate without extracting or executing. Only the same bounded strict USTAR
 * subset accepted by the resource service is supported. Package-manager scripts
 * and network-resolved dependencies are not admitted as signed local bytes.
 * @param {Uint8Array} archive @param {{name:string,version:string}} expected */
export function validateArchive(archive, expected) {
  if (!archive.byteLength || archive.byteLength > 67108864)
    return refuse('resource.apply_failed');
  const data = gunzipSync(archive, { maxOutputLength: 268435456 });
  if (data.length > archive.byteLength * 100 || data.length % 512)
    return refuse('resource.apply_failed');
  /** @type {Map<string,Buffer>} */ const files = new Map();
  const paths = new Set();
  let offset = 0,
    entries = 0,
    ended = false;
  /** @param {Buffer} b */
  const string = (b) =>
    decoder.decode(b.subarray(0, b.indexOf(0) < 0 ? b.length : b.indexOf(0)));
  /** @param {Buffer} b */
  const octal = (b) => {
    const s = b.toString('ascii').replace(/\0/g, '').trim();
    if (!/^[0-7]*$/.test(s)) return refuse('resource.apply_failed');
    const n = parseInt(s || '0', 8);
    if (!Number.isSafeInteger(n)) return refuse('resource.apply_failed');
    return n;
  };
  while (offset + 512 <= data.length) {
    const header = data.subarray(offset, offset + 512);
    offset += 512;
    if (header.every((b) => b === 0)) {
      if (
        offset + 512 > data.length ||
        data.subarray(offset).some((b) => b !== 0)
      )
        return refuse('resource.apply_failed');
      ended = true;
      break;
    }
    if (
      ++entries > 4096 ||
      header.subarray(257, 263).toString('ascii') !== 'ustar\0' ||
      header.subarray(263, 265).toString('ascii') !== '00'
    )
      return refuse('resource.apply_failed');
    const checksum = header.reduce(
      (sum, b, i) => sum + (i >= 148 && i < 156 ? 32 : b),
      0,
    );
    if (checksum !== octal(header.subarray(148, 156)))
      return refuse('resource.apply_failed');
    const prefix = string(header.subarray(345, 500)),
      name = string(header.subarray(0, 100));
    const path = (prefix ? prefix + '/' : '') + name;
    if (
      !path ||
      /[\\:]/.test(path) ||
      [...path].some((c) => c.charCodeAt(0) < 32) ||
      path.startsWith('/') ||
      path.split('/').includes('..')
    )
      return refuse('resource.apply_failed');
    const normalized = path.replace(/^(?:\.\/)+/, '').replace(/\/$/, '');
    if (
      !normalized ||
      normalized
        .split('/')
        .some(
          (p) =>
            !p ||
            p === '.' ||
            /[. ]$/.test(p) ||
            /^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(p),
        ) ||
      paths.has(normalized)
    )
      return refuse('resource.apply_failed');
    paths.add(normalized);
    const kind = header[156],
      size = octal(header.subarray(124, 136));
    if (
      ![0, 48, 53].includes(kind) ||
      (kind === 53 && size !== 0) ||
      octal(header.subarray(100, 108)) & 0o7000 ||
      offset + size > data.length
    )
      return refuse('resource.apply_failed');
    if (kind !== 53)
      files.set(normalized, data.subarray(offset, offset + size));
    offset += Math.ceil(size / 512) * 512;
  }
  if (!ended) return refuse('resource.apply_failed');
  const root = files.has('package/package.json') ? 'package/' : '';
  const manifestBytes = files.get(root + 'package.json');
  if (!manifestBytes || manifestBytes.length > 1048576)
    return refuse('resource.apply_failed');
  const manifest = parseStrictJson(manifestBytes);
  if (
    manifest.name !== expected.name ||
    manifest.version !== expected.version ||
    !manifest.dsh?.bundle ||
    typeof manifest.dsh.bundle.patch !== 'string'
  )
    return refuse('resource.apply_failed');
  const patch = manifest.dsh.bundle.patch.replace(/^\.\//, '');
  if (
    patch.startsWith('/') ||
    /[\\:]/.test(patch) ||
    patch.split('/').includes('..') ||
    !files.has(root + patch)
  )
    return refuse('resource.apply_failed');
  // No lifecycle script can run, including scripts of vendored dependencies.
  for (const [path, bytes] of files)
    if (path.endsWith('/package.json') || path === 'package.json') {
      if (bytes.length > 1048576) return refuse('resource.apply_failed');
      const pkg = parseStrictJson(bytes);
      if (pkg.scripts && Object.keys(pkg.scripts).length)
        return refuse('resource.apply_failed');
      if (pkg.pnpm?.onlyBuiltDependencies?.length || pkg.pnpm?.allowBuilds)
        return refuse('resource.apply_failed');
      const base = path.slice(0, -'package.json'.length);
      const bundled = pkg.bundledDependencies ?? pkg.bundleDependencies;
      for (const dep of Object.keys({
        ...pkg.dependencies,
        ...pkg.optionalDependencies,
      })) {
        if (
          !/^(@[a-z0-9._-]+\/)?[a-z0-9._-]+$/.test(dep) ||
          !(
            bundled === true ||
            (Array.isArray(bundled) && bundled.includes(dep))
          ) ||
          !files.has(base + 'node_modules/' + dep + '/package.json')
        )
          return refuse('resource.apply_failed');
      }
      for (const peer of Object.keys(pkg.peerDependencies ?? {}))
        if (!/^@deepseek-ai\/dsh(?:$|-)/.test(peer))
          return refuse('resource.apply_failed');
    }
  return { name: manifest.name, version: manifest.version };
}
