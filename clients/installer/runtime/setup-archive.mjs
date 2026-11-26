/** Read the embedded ZIP as data; never execute a downloaded installer for updates. */
import { openSync, closeSync, readSync, writeSync, statSync } from 'node:fs';
import { Buffer } from 'node:buffer';
import { inflateRawSync } from 'node:zlib';

function locateZipSlice(fd, size) {
  const trailer = Buffer.alloc(16),
    header = Buffer.alloc(2);
  if (size < 19) throw new Error('invalid setup archive');
  readSync(fd, header, 0, 2, 0);
  readSync(fd, trailer, 0, 16, size - 16);
  const length = Number(trailer.readBigInt64LE(8));
  if (
    header.toString('ascii') !== 'MZ' ||
    trailer.toString('ascii', 0, 8) !== 'AURZIP01' ||
    !Number.isSafeInteger(length) ||
    length < 1 ||
    length > size - 18
  )
    throw new Error('invalid setup archive');
  return { start: size - 16 - length, length };
}

export function unpackSetup(file, output) {
  const input = openSync(file, 'r');
  let destination;
  try {
    const size = statSync(file).size;
    let { start: position, length } = locateZipSlice(input, size);
    destination = openSync(output, 'wx', 0o600);
    const buffer = Buffer.alloc(1024 * 1024);
    while (length > 0) {
      const count = readSync(
        input,
        buffer,
        0,
        Math.min(buffer.length, length),
        position,
      );
      if (!count) throw new Error('truncated setup archive');
      let written = 0;
      while (written < count)
        written += writeSync(destination, buffer, written, count - written);
      length -= count;
      position += count;
    }
  } finally {
    closeSync(input);
    if (destination !== undefined) closeSync(destination);
  }
}

function readExact(fd, buffer, position) {
  let offset = 0;
  while (offset < buffer.length) {
    const count = readSync(
      fd,
      buffer,
      offset,
      buffer.length - offset,
      position + offset,
    );
    if (count === 0) throw new Error('truncated setup archive');
    offset += count;
  }
}

/**
 * Read a single entry directly from the embedded ZIP without extracting the
 * whole archive (~5ms vs extracting tens of thousands of files).
 */
export function readSetupEntry(file, targetName, maxBytes = 8 * 1024 * 1024) {
  const fd = openSync(file, 'r');
  try {
    const size = statSync(file).size;
    const { start, length } = locateZipSlice(fd, size);
    const tailLen = Math.min(length, 65557);
    const tail = Buffer.alloc(tailLen);
    readExact(fd, tail, start + length - tailLen);
    let eocdPos = -1;
    for (let i = tailLen - 22; i >= 0; i--) {
      if (
        tail.readUInt32LE(i) === 0x06054b50 &&
        tail.readUInt16LE(i + 20) === tailLen - i - 22
      ) {
        eocdPos = i;
        break;
      }
    }
    if (eocdPos < 0) throw new Error('invalid setup zip directory');
    let cdSize = tail.readUInt32LE(eocdPos + 12);
    let cdOffset = tail.readUInt32LE(eocdPos + 16);
    // Support ZIP64 EOCD locator when standard fields are 0xFFFFFFFF.
    if (cdSize === 0xffffffff || cdOffset === 0xffffffff) {
      if (eocdPos < 20 || tail.readUInt32LE(eocdPos - 20) !== 0x07064b50)
        throw new Error('unsupported zip64 directory');
      const eocd64Offset = Number(tail.readBigUInt64LE(eocdPos - 12));
      if (
        !Number.isSafeInteger(eocd64Offset) ||
        eocd64Offset < 0 ||
        eocd64Offset + 56 > length
      )
        throw new Error('invalid zip64 directory bounds');
      const eocd64 = Buffer.alloc(56);
      readExact(fd, eocd64, start + eocd64Offset);
      if (eocd64.readUInt32LE(0) !== 0x06064b50)
        throw new Error('invalid zip64 directory');
      cdSize = Number(eocd64.readBigUInt64LE(40));
      cdOffset = Number(eocd64.readBigUInt64LE(48));
    }
    if (
      !Number.isSafeInteger(cdSize) ||
      !Number.isSafeInteger(cdOffset) ||
      cdSize < 0 ||
      cdOffset < 0 ||
      cdOffset + cdSize > length ||
      cdSize > 64 * 1024 * 1024
    )
      throw new Error('invalid setup zip directory bounds');
    const cd = Buffer.alloc(cdSize);
    readExact(fd, cd, start + cdOffset);
    const normalizedTarget = targetName.replace(/^\.\//, '');
    let match = null;
    let offset = 0;
    while (offset + 46 <= cd.length) {
      if (cd.readUInt32LE(offset) !== 0x02014b50) break;
      const method = cd.readUInt16LE(offset + 10);
      let compSize = cd.readUInt32LE(offset + 20);
      let uncompSize = cd.readUInt32LE(offset + 24);
      const nameLen = cd.readUInt16LE(offset + 28);
      const extraLen = cd.readUInt16LE(offset + 30);
      const commentLen = cd.readUInt16LE(offset + 32);
      let localOffset = cd.readUInt32LE(offset + 42);
      const entryEnd = offset + 46 + nameLen + extraLen + commentLen;
      if (entryEnd > cd.length) throw new Error('invalid setup zip entry');
      const name = cd
        .toString('utf8', offset + 46, offset + 46 + nameLen)
        .replace(/\\/g, '/')
        .replace(/^\.\//, '');
      if (name === normalizedTarget) {
        if (match) throw new Error('duplicate setup zip entry');
        if (
          compSize === 0xffffffff ||
          uncompSize === 0xffffffff ||
          localOffset === 0xffffffff
        ) {
          let ex = offset + 46 + nameLen;
          const exEnd = ex + extraLen;
          while (ex + 4 <= exEnd) {
            const id = cd.readUInt16LE(ex);
            const sz = cd.readUInt16LE(ex + 2);
            ex += 4;
            if (ex + sz > exEnd) throw new Error('invalid zip64 extra field');
            if (id === 0x0001) {
              let p = ex;
              if (uncompSize === 0xffffffff && p + 8 <= ex + sz) {
                uncompSize = Number(cd.readBigUInt64LE(p));
                p += 8;
              }
              if (compSize === 0xffffffff && p + 8 <= ex + sz) {
                compSize = Number(cd.readBigUInt64LE(p));
                p += 8;
              }
              if (localOffset === 0xffffffff && p + 8 <= ex + sz) {
                localOffset = Number(cd.readBigUInt64LE(p));
              }
              break;
            }
            ex += sz;
          }
        }
        match = { method, compSize, uncompSize, localOffset };
      }
      offset = entryEnd;
    }
    if (!match) return null;
    const { method, compSize, uncompSize, localOffset } = match;
    if (
      !Number.isSafeInteger(uncompSize) ||
      !Number.isSafeInteger(compSize) ||
      !Number.isSafeInteger(localOffset) ||
      uncompSize < 0 ||
      compSize < 0 ||
      localOffset < 0 ||
      uncompSize > maxBytes ||
      compSize > maxBytes ||
      localOffset + 30 > length
    )
      throw new Error('setup entry exceeds limit');
    const lfh = Buffer.alloc(30);
    readExact(fd, lfh, start + localOffset);
    if (lfh.readUInt32LE(0) !== 0x04034b50)
      throw new Error('invalid setup zip entry');
    const lfhNameLen = lfh.readUInt16LE(26);
    const lfhExtraLen = lfh.readUInt16LE(28);
    const dataOffset = localOffset + 30 + lfhNameLen + lfhExtraLen;
    if (dataOffset + compSize > length)
      throw new Error('setup entry exceeds archive bounds');
    const compressed = Buffer.alloc(compSize);
    readExact(fd, compressed, start + dataOffset);
    if (method === 0) {
      if (compSize !== uncompSize)
        throw new Error('stored zip entry size mismatch');
      return compressed;
    }
    if (method === 8) {
      const inflated = inflateRawSync(compressed, {
        maxOutputLength: maxBytes,
      });
      if (inflated.length !== uncompSize)
        throw new Error('inflated zip entry size mismatch');
      return inflated;
    }
    throw new Error(`unsupported zip compression method: ${method}`);
  } finally {
    closeSync(fd);
  }
}

/** Check whether the embedded payload begins with a ZIP local file header (`PK\x03\x04`). */
export function isZipSetupArchive(file) {
  const fd = openSync(file, 'r');
  try {
    const size = statSync(file).size;
    const { start, length } = locateZipSlice(fd, size);
    if (length < 4) return false;
    const magic = Buffer.alloc(4);
    readExact(fd, magic, start);
    return magic.readUInt32LE(0) === 0x04034b50;
  } catch {
    return false;
  } finally {
    closeSync(fd);
  }
}

export function readSetupManifest(file) {
  const raw = readSetupEntry(file, 'payload/manifest.json');
  if (!raw) throw new Error('manifest.json missing in setup archive');
  return JSON.parse(raw.toString('utf8'));
}
