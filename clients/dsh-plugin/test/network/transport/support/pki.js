import {
  generateKeyPairSync,
  randomBytes,
  sign,
  X509Certificate,
} from 'node:crypto';

// Minimal DER writer, just enough to mint a root -> intermediate -> leaf chain
// for tests without an openssl dependency. Nothing here touches system trust.

const len = (n) => {
  if (n < 0x80) return Buffer.from([n]);
  const bytes = [];
  for (let v = n; v > 0; v = Math.floor(v / 256)) bytes.unshift(v & 0xff);
  return Buffer.from([0x80 | bytes.length, ...bytes]);
};
const tlv = (tag, ...parts) => {
  const body = Buffer.concat(parts);
  return Buffer.concat([Buffer.from([tag]), len(body.length), body]);
};
const seq = (...p) => tlv(0x30, ...p);
const set = (...p) => tlv(0x31, ...p);
const oid = (s) => {
  const [a, b, ...rest] = s.split('.').map(Number);
  const out = [a * 40 + b];
  for (const n of rest) {
    const chunk = [n & 0x7f];
    for (let v = n >> 7; v > 0; v >>= 7) chunk.unshift((v & 0x7f) | 0x80);
    out.push(...chunk);
  }
  return tlv(0x06, Buffer.from(out));
};
const int = (buf) => {
  let b = Buffer.from(buf);
  while (b.length > 1 && b[0] === 0 && b[1] < 0x80) b = b.subarray(1);
  if (b[0] & 0x80) b = Buffer.concat([Buffer.from([0]), b]);
  return tlv(0x02, b);
};
const utf8 = (s) => tlv(0x0c, Buffer.from(s));
const bitString = (b) => tlv(0x03, Buffer.concat([Buffer.from([0]), b]));
const octet = (b) => tlv(0x04, b);
const bool = (v) => tlv(0x01, Buffer.from([v ? 0xff : 0]));
const utcTime = (d) =>
  tlv(
    0x17,
    Buffer.from(d.toISOString().replace(/[-:T]/g, '').slice(2, 14) + 'Z'),
  );

const ECDSA_SHA256 = seq(oid('1.2.840.10045.4.3.2'));
const name = (cn) => seq(set(seq(oid('2.5.4.3'), utf8(cn))));
const ext = (id, critical, value) =>
  seq(oid(id), ...(critical ? [bool(true)] : []), octet(value));

function certificate({ subject, issuer, publicKey, signer, serial, ca }) {
  const now = Date.now();
  const extensions = [
    ext('2.5.29.19', true, seq(...(ca ? [bool(true)] : []))),
    ext(
      '2.5.29.15',
      true,
      // keyCertSign|cRLSign for CAs, digitalSignature for leaves
      ca
        ? Buffer.from([0x03, 0x02, 0x01, 0x06])
        : Buffer.from([0x03, 0x02, 0x07, 0x80]),
    ),
  ];
  if (!ca) {
    extensions.push(ext('2.5.29.37', false, seq(oid('1.3.6.1.5.5.7.3.1'))));
    extensions.push(
      ext(
        '2.5.29.17',
        false,
        seq(
          tlv(0x82, Buffer.from('localhost')),
          tlv(0x87, Buffer.from([127, 0, 0, 1])),
        ),
      ),
    );
  }
  const tbs = seq(
    tlv(0xa0, int(Buffer.from([2]))),
    int(serial),
    ECDSA_SHA256,
    name(issuer),
    seq(utcTime(new Date(now - 3600_000)), utcTime(new Date(now + 86400_000))),
    name(subject),
    publicKey.export({ type: 'spki', format: 'der' }),
    tlv(0xa3, seq(...extensions)),
  );
  const signature = sign('sha256', tbs, { key: signer, dsaEncoding: 'der' });
  return seq(tbs, ECDSA_SHA256, bitString(signature));
}

const pem = (der) =>
  `-----BEGIN CERTIFICATE-----\n${der
    .toString('base64')
    .match(/.{1,64}/g)
    .join('\n')}\n-----END CERTIFICATE-----\n`;

/**
 * Root CA -> intermediate CA -> localhost leaf, all ECDSA P-256.
 * `rootPem` is the only thing a client needs to trust.
 */
export function createTestPki() {
  const keys = () => generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  const root = keys();
  const inter = keys();
  const leaf = keys();
  const rootDer = certificate({
    subject: 'Auriga Test Root',
    issuer: 'Auriga Test Root',
    publicKey: root.publicKey,
    signer: root.privateKey,
    serial: randomBytes(8),
    ca: true,
  });
  const interDer = certificate({
    subject: 'Auriga Test Intermediate',
    issuer: 'Auriga Test Root',
    publicKey: inter.publicKey,
    signer: root.privateKey,
    serial: randomBytes(8),
    ca: true,
  });
  const leafDer = certificate({
    subject: 'localhost',
    issuer: 'Auriga Test Intermediate',
    publicKey: leaf.publicKey,
    signer: inter.privateKey,
    serial: randomBytes(8),
    ca: false,
  });
  const rootPem = pem(rootDer);
  const certPem = pem(leafDer) + pem(interDer);
  const keyPem = leaf.privateKey.export({ type: 'pkcs8', format: 'pem' });
  // Fail early if the hand-built DER is not a valid chain.
  const rootCert = new X509Certificate(rootPem);
  const interCert = new X509Certificate(pem(interDer));
  const leafCert = new X509Certificate(pem(leafDer));
  if (
    !interCert.verify(rootCert.publicKey) ||
    !leafCert.verify(interCert.publicKey) ||
    !leafCert.checkHost('localhost') ||
    !leafCert.checkIP('127.0.0.1')
  ) {
    throw new Error('generated test PKI is invalid');
  }
  return { rootPem, certPem, keyPem };
}
