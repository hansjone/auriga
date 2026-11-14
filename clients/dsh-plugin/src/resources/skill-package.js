import { gzipSync, gunzipSync } from 'node:zlib';
import { TextDecoder } from 'node:util';
import { validatePrivateSkill } from './private-skills.js';
import { refuse } from './trust.js';
const decoder = new TextDecoder('utf-8', { fatal: true });
/** @param {string} path */
function pathValid(path) {
  return (
    Buffer.byteLength(path) <= 240 &&
    path.split('/').length <= 8 &&
    (path === 'SKILL.md' ||
      /^(references|assets)\/.+\.(md|txt|json|yaml|yml)$/.test(path)) &&
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
    if (
      !pathValid(path) ||
      seen.has(path.normalize('NFC').toLowerCase()) ||
      bytes.length > (path === 'SKILL.md' ? 262144 : 2097152) ||
      (total += bytes.length) > 20971520 ||
      decoder.decode(bytes).includes('\0')
    )
      refuse('common.validation_failed');
    seen.add(path.normalize('NFC').toLowerCase());
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
  const seen = new Set();
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
    const key = path.replace(/\/$/, '').normalize('NFC').toLowerCase();
    const size = octal(h.subarray(124, 136)),
      mode = octal(h.subarray(100, 108)),
      type = h[156];
    if (
      seen.has(key) ||
      mode & 0o7111 ||
      pos + Math.ceil(size / 512) * 512 > tar.length ||
      h.subarray(157, 257).some((b) => b !== 0)
    )
      refuse('common.validation_failed');
    seen.add(key);
    if (type === 53) {
      const dir = path.replace(/\/$/, '');
      if (
        size ||
        !['references', 'assets'].includes(dir.split('/')[0]) ||
        !pathValid(dir + '/file.txt')
      )
        refuse('common.validation_failed');
    } else {
      if (![0, 48].includes(type) || !pathValid(path))
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
 * @param {Record<string,string>} source @param {{name:string,description:string}} expected */
export function packSkill(source, expected) {
  const files = Object.fromEntries(
    Object.entries(source).map(([k, v]) => [k, Buffer.from(v)]),
  );
  validateFiles(files, expected);
  const chunks = [];
  for (const path of Object.keys(files).sort()) {
    const bytes = files[path],
      h = Buffer.alloc(512);
    const split = Buffer.byteLength(path) <= 100 ? -1 : path.lastIndexOf('/');
    const leaf = split < 0 ? path : path.slice(split + 1),
      prefix = split < 0 ? '' : path.slice(0, split);
    if (Buffer.byteLength(leaf) > 100 || Buffer.byteLength(prefix) > 155)
      refuse('common.validation_failed');
    h.write(leaf, 0, 100, 'utf8');
    h.write(prefix, 345, 155, 'utf8');
    h.write('0000600\0', 100);
    h.write('0000000\0', 108);
    h.write('0000000\0', 116);
    h.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124);
    h.write('00000000000\0', 136);
    h.fill(32, 148, 156);
    h[156] = 48;
    h.write('ustar\0' + '00', 257);
    h.write(
      h
        .reduce((n, b) => n + b, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    chunks.push(h, bytes, Buffer.alloc((512 - (bytes.length % 512)) % 512));
  }
  chunks.push(Buffer.alloc(1024));
  const archive = gzipSync(Buffer.concat(chunks));
  unpackSkill(archive, expected);
  return archive;
}
