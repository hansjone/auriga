// Deterministic POSIX ustar + gzip, no archive dependency or host-specific tar flags.
import { createReadStream, createWriteStream } from 'node:fs';
import { createGzip } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';
import { join } from 'node:path';
import { inventory } from './lib.mjs';

export function header(name, item) {
  const block = Buffer.alloc(512);
  function field(offset, length, value) {
    if (Buffer.byteLength(value) > length) throw new Error(`ustar field too long: ${value}`);
    block.write(value, offset, length, 'utf8');
  }
  let base = name, prefix = '';
  if (Buffer.byteLength(name) > 100) {
    let split = name.lastIndexOf('/');
    while (split > 0 && (Buffer.byteLength(name.slice(0, split)) > 155 || Buffer.byteLength(name.slice(split + 1)) > 100)) split = name.lastIndexOf('/', split - 1);
    if (split <= 0) throw new Error(`Path too long for ustar: ${name}`);
    prefix = name.slice(0, split); base = name.slice(split + 1);
  }
  field(0, 100, base);
  const octal = (offset, length, value) => field(offset, length, value.toString(8).padStart(length - 1, '0') + '\0');
  octal(100, 8, parseInt(item.mode, 8)); octal(108, 8, 0); octal(116, 8, 0);
  octal(124, 12, item.size); octal(136, 12, 0);
  block.fill(32, 148, 156);
  field(156, 1, item.type === 'directory' ? '5' : item.type === 'symlink' ? '2' : item.type === 'pax' ? 'x' : '0');
  if (item.target) field(157, 100, item.target);
  field(257, 6, 'ustar\0'); field(263, 2, '00'); field(345, 155, prefix);
  const checksum = block.reduce((sum, byte) => sum + byte, 0);
  field(148, 8, checksum.toString(8).padStart(6, '0') + '\0 ');
  return block;
}
export async function pack(root, name, destination) {
  const entries = await inventory(root, '', false);
  function paxRecord(key, value) {
    const suffix = ` ${key}=${value}\n`;
    let length = Buffer.byteLength(suffix) + 1;
    while (length !== Buffer.byteLength(suffix) + String(length).length) length = Buffer.byteLength(suffix) + String(length).length;
    return Buffer.from(`${length}${suffix}`);
  }
  async function* tar() {
    yield header(name, { mode: '0755', size: 0, type: 'directory' });
    for (const [index, item] of entries.entries()) {
      const path = `${name}/${item.path}`;
      // POSIX ustar doesn't declare a charset. Windows bsdtar otherwise decodes UTF-8 as ACP.
      const needsPax = /[^\x20-\x7e]/.test(path) || Buffer.byteLength(path) > 250 || Buffer.byteLength(path.split('/').at(-1)) > 100 || (item.target && (Buffer.byteLength(item.target) > 100 || /[^\x20-\x7e]/.test(item.target)));
      if (needsPax) {
        const payload = Buffer.concat([paxRecord('path', path), ...(item.target ? [paxRecord('linkpath', item.target)] : [])]);
        yield header(`PaxHeaders/${index}`, { type: 'pax', mode: '0644', size: payload.length });
        yield payload;
        if (payload.length % 512) yield Buffer.alloc(512 - payload.length % 512);
      }
      yield header(needsPax ? `entries/${index}` : path, needsPax && item.target ? { ...item, target: 'pax-link' } : item);
      if (item.type === 'file') {
        for await (const chunk of createReadStream(join(root, item.path))) yield chunk;
        if (item.size % 512) yield Buffer.alloc(512 - item.size % 512);
      }
    }
    yield Buffer.alloc(1024);
  }
  await pipeline(Readable.from(tar()), createGzip({ level: 9 }), createWriteStream(destination));
}
