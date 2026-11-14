/** Read the embedded ZIP as data; never execute a downloaded installer for updates. */
import { openSync, closeSync, readSync, writeSync, statSync } from 'node:fs';
import { Buffer } from 'node:buffer';

export function unpackSetup(file, output) {
  const input = openSync(file, 'r');
  let destination;
  try {
    const size = statSync(file).size;
    const trailer = Buffer.alloc(16),
      header = Buffer.alloc(2);
    if (size < 19) throw new Error('invalid setup archive');
    readSync(input, header, 0, 2, 0);
    readSync(input, trailer, 0, 16, size - 16);
    let length = Number(trailer.readBigInt64LE(8));
    if (
      header.toString('ascii') !== 'MZ' ||
      trailer.toString('ascii', 0, 8) !== 'AURZIP01' ||
      !Number.isSafeInteger(length) ||
      length < 1 ||
      length > size - 18
    )
      throw new Error('invalid setup archive');
    destination = openSync(output, 'wx', 0o600);
    const buffer = Buffer.alloc(1024 * 1024);
    let position = size - 16 - length;
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
