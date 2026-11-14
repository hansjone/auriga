// 契约夹具专用 RFC9180 base mode，公开合成标签派生测试钥，禁止用于生产。
import {
  createHash,
  createHmac,
  createPrivateKey,
  createPublicKey,
  diffieHellman,
  createCipheriv,
  createDecipheriv,
} from 'node:crypto';
const B = (x) => Buffer.from(x),
  cat = (...x) => Buffer.concat(x),
  u16 = (x) => Buffer.from([x >> 8, x & 255]);
export function syntheticX25519(label) {
  const seed = createHash('sha256')
    .update('Auriga synthetic HPKE test only ' + label)
    .digest();
  return createPrivateKey({
    key: cat(Buffer.from('302e020100300506032b656e04220420', 'hex'), seed),
    format: 'der',
    type: 'pkcs8',
  });
}
const raw = (k) =>
  createPublicKey(k).export({ format: 'der', type: 'spki' }).subarray(-32);
const extract = (salt, ikm) =>
  createHmac('sha256', salt.length ? salt : Buffer.alloc(32))
    .update(ikm)
    .digest();
function expand(prk, info, len) {
  let t = Buffer.alloc(0),
    o = t;
  for (let c = 1; o.length < len; c++) {
    t = createHmac('sha256', prk)
      .update(cat(t, info, Buffer.from([c])))
      .digest();
    o = cat(o, t);
  }
  return o.subarray(0, len);
}
const lex = (suite, salt, label, ikm) =>
  extract(salt, cat(B('HPKE-v1'), suite, B(label), ikm));
const lep = (suite, prk, label, info, len) =>
  expand(prk, cat(u16(len), B('HPKE-v1'), suite, B(label), info), len);
function schedule(dh, enc, pkR) {
  const kem = cat(B('KEM'), u16(32)),
    suite = cat(B('HPKE'), u16(32), u16(1), u16(2));
  const eae = lex(kem, B(''), 'eae_prk', dh),
    shared = lep(kem, eae, 'shared_secret', cat(enc, pkR), 32);
  const context = cat(
    Buffer.from([0]),
    lex(suite, B(''), 'psk_id_hash', B('')),
    lex(suite, B(''), 'info_hash', B('Auriga.NetworkSealedMaterial.v2')),
  );
  const secret = lex(suite, shared, 'secret', B(''));
  return {
    key: lep(suite, secret, 'key', context, 32),
    nonce: lep(suite, secret, 'base_nonce', context, 12),
  };
}
export function sealTest(aad, plaintext) {
  const receiver = syntheticX25519('receiver'),
    sender = syntheticX25519('sender'),
    enc = raw(sender),
    s = schedule(
      diffieHellman({
        privateKey: sender,
        publicKey: createPublicKey(receiver),
      }),
      enc,
      raw(receiver),
    );
  const c = createCipheriv('aes-256-gcm', s.key, s.nonce);
  c.setAAD(B(aad));
  return {
    enc: enc.toString('base64url'),
    ciphertext: cat(c.update(plaintext), c.final(), c.getAuthTag()).toString(
      'base64url',
    ),
  };
}
export function openTest(aad, sealed) {
  const receiver = syntheticX25519('receiver'),
    enc = Buffer.from(sealed.enc, 'base64url'),
    publicKey = createPublicKey({
      key: cat(Buffer.from('302a300506032b656e032100', 'hex'), enc),
      type: 'spki',
      format: 'der',
    }),
    s = schedule(
      diffieHellman({ privateKey: receiver, publicKey }),
      enc,
      raw(receiver),
    );
  const bytes = Buffer.from(sealed.ciphertext, 'base64url'),
    d = createDecipheriv('aes-256-gcm', s.key, s.nonce);
  d.setAAD(B(aad));
  d.setAuthTag(bytes.subarray(-16));
  return cat(d.update(bytes.subarray(0, -16)), d.final()).toString();
}
