/**
 * Encode bounded, validated regular files as deterministic POSIX USTAR.
 * @param {Record<string,Buffer>} files
 * @param {(path:string) => never} invalid
 */
export function encodeUstar(files, invalid) {
  const chunks = [];
  for (const path of Object.keys(files).sort()) {
    const bytes = files[path],
      h = Buffer.alloc(512);
    let split = -1;
    if (Buffer.byteLength(path) > 100) {
      for (let i = path.indexOf('/'); i >= 0; i = path.indexOf('/', i + 1)) {
        if (
          Buffer.byteLength(path.slice(0, i)) <= 155 &&
          Buffer.byteLength(path.slice(i + 1)) <= 100
        ) {
          split = i;
          break;
        }
      }
      if (split < 0) invalid(path);
    }
    const leaf = split < 0 ? path : path.slice(split + 1),
      prefix = split < 0 ? '' : path.slice(0, split);
    if (Buffer.byteLength(leaf) > 100 || Buffer.byteLength(prefix) > 155)
      invalid(path);
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
  return Buffer.concat(chunks);
}
