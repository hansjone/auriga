// Disposable JWKS dependency for the single-service publication test only.
// This does not implement IAM login, admission or managed credentials.
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:https';

const { privateKey, publicKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' });
const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'disposable-publication-test', alg: 'ES256', use: 'sig' };
const encode = (value) => Buffer.from(JSON.stringify(value)).toString('base64url');
const now = Math.floor(Date.now() / 1000);
const body = `${encode({ alg: 'ES256', typ: 'at+jwt', kid: jwk.kid })}.${encode({
  iss: process.env.ISSUER, aud: ['auriga'], sub: randomUUID(), tid: process.env.TENANT,
  ptyp: 'device', ctx: 'device', jti: randomUUID(), iat: now, nbf: now, exp: now + 300,
})}`;
const token = `${body}.${sign('sha256', Buffer.from(body), { key: privateKey, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`;
const otherBody = `${body.split('.')[0]}.${encode({ ...JSON.parse(Buffer.from(body.split('.')[1], 'base64url')), tid: randomUUID() })}`;
const otherToken = `${otherBody}.${sign('sha256', Buffer.from(otherBody), { key: privateKey, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`;
createServer({
  cert: readFileSync('/pki/iam.crt'), key: readFileSync('/pki/iam.key'), ca: readFileSync('/pki/ca.crt'),
  requestCert: true, rejectUnauthorized: true,
}, (request, response) => {
  if (request.url !== '/oauth2/jwks') { response.writeHead(404).end(); return; }
  response.setHeader('content-type', 'application/json');
  response.end(JSON.stringify({ keys: [jwk] }));
}).listen(8081, '0.0.0.0', () => {
  writeFileSync('/fixture/request.config', `header = "Authorization: Bearer ${token}"\n`, { mode: 0o600 });
  writeFileSync('/fixture/request-other.config', `header = "Authorization: Bearer ${otherToken}"\n`, { mode: 0o600 });
});
