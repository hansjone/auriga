/* global Blob */
// Incremental SHA-256 keeps EXE memory bounded and also works on HTTP admin origins.
// Full chunks are a multiple of the 64-byte compression block. Only the tail is padded.
export async function sha256(
  file: Blob,
  update: (percentage: number) => void = () => {},
) {
  const constants = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  const hash = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const words = new Uint32Array(64);
  const rotate = (value: number, bits: number) =>
    (value >>> bits) | (value << (32 - bits));
  function block(bytes: DataView, offset: number) {
    for (let i = 0; i < 16; i++) words[i] = bytes.getUint32(offset + i * 4);
    for (let i = 16; i < 64; i++) {
      const previous = words[i - 15]!;
      const recent = words[i - 2]!;
      const sigma0 =
        rotate(previous, 7) ^ rotate(previous, 18) ^ (previous >>> 3);
      const sigma1 = rotate(recent, 17) ^ rotate(recent, 19) ^ (recent >>> 10);
      words[i] = words[i - 16]! + sigma0 + words[i - 7]! + sigma1;
    }
    let [a, b, c, d, e, f, g, h] = Array.from(hash) as [
      number,
      number,
      number,
      number,
      number,
      number,
      number,
      number,
    ];
    for (let i = 0; i < 64; i++) {
      const sum1 = rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25);
      const choice = (e & f) ^ (~e & g);
      const temp1 = (h + sum1 + choice + constants[i]! + words[i]!) >>> 0;
      const sum0 = rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + sum0 + majority) >>> 0;
    }
    [a, b, c, d, e, f, g, h].forEach((value, i) => {
      hash[i] = hash[i]! + value;
    });
  }
  let tail = new Uint8Array();
  const chunkSize = 4 * 1024 * 1024;
  for (let offset = 0; offset < file.size; offset += chunkSize) {
    const bytes = new Uint8Array(
      await file.slice(offset, offset + chunkSize).arrayBuffer(),
    );
    const full = bytes.length - (bytes.length % 64);
    const data = new DataView(bytes.buffer);
    for (let start = 0; start < full; start += 64) block(data, start);
    tail = bytes.slice(full);
    update(
      Math.round(
        (Math.min(offset + bytes.length, file.size) / file.size) * 100,
      ),
    );
  }
  const padded = new Uint8Array(tail.length < 56 ? 64 : 128);
  padded.set(tail);
  padded[tail.length] = 0x80;
  const ending = new DataView(padded.buffer);
  ending.setUint32(
    padded.length - 8,
    Math.floor((file.size * 8) / 0x100000000),
  );
  ending.setUint32(padded.length - 4, (file.size * 8) >>> 0);
  for (let offset = 0; offset < padded.length; offset += 64)
    block(ending, offset);
  return Array.from(hash, (value) => value.toString(16).padStart(8, '0')).join(
    '',
  );
}
