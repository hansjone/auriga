import { gzipSync, gunzipSync } from 'node:zlib';
import { TextDecoder } from 'node:util';
import { validatePrivateSkill } from './private-skills.js';
import { refuse } from './trust.js';
import { encodeUstar } from './ustar.js';
const decoder = new TextDecoder('utf-8', { fatal: true });
export const skillRootDependencyFiles = new Set([
  'requirements.txt',
  'pyproject.toml',
  'package.json',
  'uv.lock',
  'poetry.lock',
]);

/** @param {string} path */
export function skillDirectoryValid(path) {
  return (
    Buffer.byteLength(path) <= 240 &&
    path.split('/').length <= 7 &&
    /^(?:references|assets|scripts)(?:\/.+)?$/.test(path) &&
    path
      .split('/')
      .every(
        (p) =>
          p &&
          p !== '.' &&
          p !== '..' &&
          !/[. ]$/.test(p) &&
          !/[\\:]/.test(p) &&
          ![...p].some((c) => c.charCodeAt(0) < 32) &&
          !/^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(p),
      )
  );
}

const docExtensions = new Set([
  '.md',
  '.txt',
  '.json',
  '.yaml',
  '.yml',
  '.toml',
  '.ini',
  '.cfg',
  '.csv',
  '.sql',
]);
const scriptExtensions = new Set([
  ...docExtensions,
  '.py',
  '.sh',
  '.bash',
  '.ps1',
  '.js',
  '.mjs',
  '.cjs',
  '.ts',
  '.mts',
  '.cts',
  '.lock',
]);

/** @param {string} path */
function collisionKey(path) {
  return path.normalize('NFC').toUpperCase().toLowerCase().normalize('NFC');
}

/** @param {string} path */
function extensionLower(path) {
  const slash = path.lastIndexOf('/');
  const dot = path.lastIndexOf('.');
  return dot > slash ? path.slice(dot).toLowerCase() : '';
}

/** @param {string} path */
export function skillPathValid(path) {
  const ext = extensionLower(path);
  const allowed =
    path === 'SKILL.md' ||
    skillRootDependencyFiles.has(path) ||
    ((path.startsWith('references/') || path.startsWith('assets/')) &&
      docExtensions.has(ext)) ||
    (path.startsWith('scripts/') && scriptExtensions.has(ext));
  return (
    Buffer.byteLength(path) <= 240 &&
    path.split('/').length <= 8 &&
    allowed &&
    path
      .split('/')
      .every(
        (p) =>
          p &&
          p !== '.' &&
          p !== '..' &&
          !/[. ]$/.test(p) &&
          !/[\\:]/.test(p) &&
          ![...p].some((c) => c.charCodeAt(0) < 32) &&
          !/^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(p),
      )
  );
}
/** @param {Record<string,Buffer>} files @param {{name:string,description:string}} expected */
function validateFiles(files, expected) {
  const seen = new Set();
  let total = 0;
  for (const [path, bytes] of Object.entries(files)) {
    const key = collisionKey(path);
    if (
      !skillPathValid(path) ||
      seen.has(key) ||
      bytes.length > (path === 'SKILL.md' ? 262144 : 2097152) ||
      (total += bytes.length) > 20971520 ||
      decoder.decode(bytes).includes('\0')
    )
      refuse('common.validation_failed');
    seen.add(key);
  }
  if (!files['SKILL.md'] || seen.size > 512) refuse('common.validation_failed');
  const meta = validatePrivateSkill(decoder.decode(files['SKILL.md']));
  if (meta.name !== expected.name || meta.description !== expected.description)
    refuse('common.validation_failed');
}
/** Strict text-only USTAR; no extraction until every member was validated.
 * @param {Buffer} archive @param {{name:string,description:string}} expected */
export function unpackSkill(archive, expected) {
  if (!archive.length || archive.length > 5242880)
    refuse('common.validation_failed');
  const tar = gunzipSync(archive, { maxOutputLength: 20971520 });
  if (tar.length % 512 || tar.length > archive.length * 100)
    refuse('common.validation_failed');
  /** @type {Record<string,Buffer>} */ const files = Object.create(null);
  /** @type {Map<string, boolean>} */ const seen = new Map();
  let pos = 0,
    entries = 0,
    ended = false;
  /** @param {Buffer} b */ const string = (b) =>
    decoder.decode(b.subarray(0, b.indexOf(0) < 0 ? b.length : b.indexOf(0)));
  /** @param {Buffer} b */ const octal = (b) => {
    const text = b.toString('ascii').replace(/\0/g, '').trim();
    if (!/^[0-7]+$/.test(text)) refuse('common.validation_failed');
    return parseInt(text, 8);
  };
  while (pos + 512 <= tar.length) {
    const h = tar.subarray(pos, pos + 512);
    pos += 512;
    if (h.every((b) => b === 0)) {
      if (pos + 512 > tar.length || tar.subarray(pos).some((b) => b !== 0))
        refuse('common.validation_failed');
      ended = true;
      break;
    }
    const sum = h.reduce((n, b, i) => n + (i >= 148 && i < 156 ? 32 : b), 0);
    if (
      ++entries > 512 ||
      h.subarray(257, 265).toString('ascii') !== 'ustar\0' + '00' ||
      sum !== octal(h.subarray(148, 156))
    )
      refuse('common.validation_failed');
    const prefix = string(h.subarray(345, 500)),
      leaf = string(h.subarray(0, 100));
    const path = (prefix ? prefix + '/' : '') + leaf;
    const size = octal(h.subarray(124, 136)),
      mode = octal(h.subarray(100, 108)),
      type = h[156];
    const isDir = type === 53;
    const key = collisionKey(path.replace(/\/$/, ''));
    if (
      seen.has(key) ||
      [...seen.entries()].some(
        ([existing, existingDir]) =>
          (!existingDir && key.startsWith(existing + '/')) ||
          (!isDir && existing.startsWith(key + '/')),
      ) ||
      mode & 0o7000 ||
      (!isDir && mode & 0o111) ||
      pos + Math.ceil(size / 512) * 512 > tar.length ||
      h.subarray(157, 257).some((b) => b !== 0)
    )
      refuse('common.validation_failed');
    seen.set(key, isDir);
    if (type === 53) {
      const dir = path.replace(/\/$/, '');
      if (
        size ||
        !['references', 'assets', 'scripts'].includes(dir.split('/')[0]) ||
        !skillPathValid(dir + '/file.txt')
      )
        refuse('common.validation_failed');
    } else {
      if (![0, 48].includes(type) || !skillPathValid(path))
        refuse('common.validation_failed');
      files[path] = Buffer.from(tar.subarray(pos, pos + size));
    }
    pos += Math.ceil(size / 512) * 512;
  }
  if (!ended) refuse('common.validation_failed');
  validateFiles(files, expected);
  return files;
}
/** Freeze original local text bytes as a deterministic non-executable USTAR.
 * @param {Record<string,string|Buffer>} source @param {{name:string,description:string}} expected */
export function packSkill(source, expected) {
  const files = Object.fromEntries(
    Object.entries(source).map(([k, v]) => [k, Buffer.from(v)]),
  );
  validateFiles(files, expected);
  const archive = gzipSync(
    encodeUstar(files, () => refuse('common.validation_failed')),
  );
  unpackSkill(archive, expected);
  return archive;
}
