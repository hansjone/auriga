// Fresh test keys only. Called inside an isolated Docker fixture container.
import * as fs from 'node:fs';
import * as crypto from 'node:crypto';
import assert from 'node:assert/strict';

const mode = process.argv[2];
const hash = (algorithm, code, encoding) => crypto.createHash(algorithm).update(code).digest(encoding);
const oldCode = 'export const panel = 0;';
if (mode === 'seed') {
  const { privateKey } = crypto.generateKeyPairSync('ed25519');
  const jwk = privateKey.export({ format: 'jwk' });
  const publicRaw = Buffer.from(jwk.x, 'base64url');
  fs.writeFileSync('/keys/panel.key', Buffer.concat([Buffer.from(jwk.d, 'base64url'), publicRaw]), { mode: 0o600 });
  fs.writeFileSync('/keys/jwk.json', JSON.stringify(jwk), { mode: 0o600 });
  const now = Date.now();
  const entry = `auriga-team-panel-${hash('sha256', oldCode, 'hex')}.js`;
  const body = {
    element: 'auriga-team-panel', entry,
    expiresAt: new Date(now + 6 * 86400000).toISOString().replace('.000Z', 'Z'),
    integrity: { [entry]: `sha384-${hash('sha384', oldCode, 'base64')}` },
    issuedAt: new Date(now - 24 * 86400000).toISOString().replace('.000Z', 'Z'),
    minPluginVersion: '0.0.0', protocol: 1, schemaVersion: 1, version: '0.0.0',
  };
  // Go RFC3339 encoding uses whole seconds.
  body.issuedAt = body.issuedAt.replace(/\.\d+Z$/, 'Z');
  body.expiresAt = body.expiresAt.replace(/\.\d+Z$/, 'Z');
  const signature = { alg: 'EdDSA', kid: `panel-${hash('sha256', publicRaw, 'hex').slice(0, 32)}`, value: crypto.sign(null, Buffer.from(JSON.stringify(body)), privateKey).toString('base64url') };
  fs.writeFileSync('/out/manifest.json', JSON.stringify({ ...body, signature }));
  fs.writeFileSync(`/out/${entry}`, oldCode);
  fs.writeFileSync('/dist/auriga-team-panel.js', 'export const panel = 1;');
  fs.writeFileSync('/config/identity.json', JSON.stringify({ keys: [{ ...jwk, kid: 'fixture', state: 'CURRENT' }] }));
  for (const file of ['ca.crt', 'edge.key']) fs.copyFileSync(`/input/${file}`, `/config/${file}`);
  fs.chmodSync('/config/edge.key', 0o644);
  const port = Number(process.argv[3]);
  fs.writeFileSync('/config/edge.json', JSON.stringify({ listen: `0.0.0.0:${port}`, portalHost: 'portal.example.com', adminHost: 'admin.example.com', secureCookies: false, upstreams: { iam: 'http://127.0.0.1:1', org: 'http://127.0.0.1:1', workspace: 'http://127.0.0.1:1' }, static: { panelRoot: '/out' }, identityKeysFile: '/config/identity.json', nodeTLS: { caFile: '/config/ca.crt', certFile: '/config/ca.crt', keyFile: '/config/edge.key' } }));
  for (const dir of ['/keys', '/out', '/dist']) {
    fs.chownSync(dir, 10001, 10001);
    for (const name of fs.readdirSync(dir)) fs.chownSync(`${dir}/${name}`, 10001, 10001);
  }
} else if (mode === 'age') {
  const m = JSON.parse(fs.readFileSync('/out/manifest.json'));
  delete m.signature;
  m.issuedAt = new Date(Date.now() - 24 * 86400000).toISOString().replace(/\.\d+Z$/, 'Z');
  m.expiresAt = new Date(Date.now() + 6 * 86400000).toISOString().replace(/\.\d+Z$/, 'Z');
  const key = crypto.createPrivateKey({ key: JSON.parse(fs.readFileSync('/keys/jwk.json')), format: 'jwk' });
  const pub = Buffer.from(key.export({ format: 'jwk' }).x, 'base64url');
  const signature = { alg: 'EdDSA', kid: `panel-${hash('sha256', pub, 'hex').slice(0, 32)}`, value: crypto.sign(null, Buffer.from(JSON.stringify(m)), key).toString('base64url') };
  fs.writeFileSync('/out/manifest.json', JSON.stringify({ ...m, signature }));
} else if (mode === 'rollback') {
  fs.writeFileSync('/dist/auriga-team-panel.js', oldCode);
} else if (mode === 'restore') {
  fs.writeFileSync('/dist/auriga-team-panel.js', 'export const panel = 1;');
} else if (mode === 'verify') {
  const m = JSON.parse(fs.readFileSync('/out/manifest.json'));
  assert.equal(m.version, '0.0.1');
  assert.ok(Date.parse(m.expiresAt) - Date.now() > 29 * 86400000);
} else {
  throw new Error(`unknown fixture mode ${mode}`);
}
