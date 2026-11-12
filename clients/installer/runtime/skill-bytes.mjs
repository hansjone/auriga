import { TextDecoder } from 'node:util';
import { gunzipSync } from 'node:zlib';
import { lstatSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';
import { readBounded, sha } from './resource-observer.mjs';

// P1 text-only payloads. Never invoke an extractor on attacker-controlled paths.
export function skillFiles(archive) {
  const tar = gunzipSync(archive, { maxOutputLength: 16 * 1024 * 1024 });
  const files = new Map();
  let offset = 0;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    offset += 512;
    if (header.every((b) => b === 0)) break;
    const field = (a, b) =>
      header.subarray(a, b).toString('utf8').replace(/\0.*$/s, '').trim();
    const name = [field(345, 500), field(0, 100)].filter(Boolean).join('/');
    const size = Number.parseInt(field(124, 136) || '0', 8);
    const stored = Number.parseInt(field(148, 156), 8);
    let sum = 0;
    for (let i = 0; i < 512; i++) sum += i >= 148 && i < 156 ? 32 : header[i];
    if (
      stored !== sum ||
      !Number.isSafeInteger(size) ||
      size < 0 ||
      offset + size > tar.length ||
      !name ||
      name.startsWith('/') ||
      name.includes('\\') ||
      name.includes(':') ||
      name
        .split('/')
        .some(
          (s) =>
            s === '..' || s === '.' || (!s && s !== name.split('/').at(-1)),
        )
    )
      throw new Error('unsafe Skill tar');
    const type = header[156];
    if (type !== 0 && type !== 48 && type !== 53)
      throw new Error('Skill links/extensions are not accepted');
    if (type !== 53) {
      if (files.has(name) || files.size >= 512 || size > 2097152)
        throw new Error('Skill payload limit');
      const bytes = tar.subarray(offset, offset + size);
      if (bytes.includes(0)) throw new Error('Skill payload is not text');
      // Fatal UTF-8 decoding proves text without normalizing the signed bytes.
      new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      files.set(name, sha(bytes));
    }
    offset += Math.ceil(size / 512) * 512;
  }
  if (!files.has('SKILL.md')) throw new Error('Skill entry missing');
  return files;
}

export function verifySkillDirectory(directory, expected, checkedFiles) {
  const found = new Map();
  function walk(path, prefix = '') {
    assertLocalPath(path);
    if (!lstatSync(path).isDirectory())
      throw new Error('Skill root is not directory');
    for (const name of readdirSync(path)) {
      const file = join(path, name);
      assertLocalPath(file);
      const key = prefix + name;
      if (lstatSync(file).isDirectory()) walk(file, key + '/');
      else {
        const digest = sha(readBounded(file));
        found.set(key, digest);
        checkedFiles.push({ file, digest });
      }
      if (found.size > 512) throw new Error('Skill file limit');
    }
  }
  walk(directory);
  if (
    found.size !== expected.size ||
    [...expected].some(([file, digest]) => found.get(file) !== digest)
  )
    throw new Error('materialized Skill differs from signed artifact');
}
