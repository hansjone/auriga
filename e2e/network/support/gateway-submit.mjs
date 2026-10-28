import { Buffer } from 'node:buffer';
import { readFile } from 'node:fs/promises';
import { request } from 'node:https';

// Use only this run's server CA and explicit submit client keypair. The native
// HTTPS request has no global dispatcher, environment proxy or IAM trust fallback.
export async function postSubmit(gateway, body, token, headers = {}) {
  const origin = new URL(gateway.https);
  if (
    origin.protocol !== 'https:' ||
    !['127.0.0.1', '[::1]'].includes(origin.hostname) ||
    origin.username ||
    origin.password ||
    origin.search ||
    origin.hash ||
    origin.pathname !== '/' ||
    [
      'ca',
      'submitServerName',
      'submitClientCertFile',
      'submitClientKeyFile',
    ].some((name) => typeof gateway[name] !== 'string' || !gateway[name])
  )
    throw new Error('invalid private submit TLS metadata');
  const [ca, cert, key] = await Promise.all([
    readFile(gateway.ca),
    readFile(gateway.submitClientCertFile),
    readFile(gateway.submitClientKeyFile),
  ]);
  const payload = JSON.stringify(body);
  return new Promise((resolve, reject) => {
    const req = request(
      origin,
      {
        path: '/api/ai/v1/network/submit',
        method: 'POST',
        ca,
        cert,
        key,
        servername: gateway.submitServerName,
        minVersion: 'TLSv1.2',
        rejectUnauthorized: true,
        agent: false,
        signal: globalThis.AbortSignal.timeout(15_000),
        headers: {
          authorization: `Bearer ${token}`,
          'content-type': 'application/json',
          ...headers,
        },
      },
      (res) => {
        const parts = [];
        res.on('data', (part) => parts.push(part));
        res.once('error', reject);
        res.once('aborted', () => reject(new Error('submit response aborted')));
        res.once('end', () =>
          resolve({
            status: res.statusCode,
            body: Buffer.concat(parts).toString('utf8'),
          }),
        );
      },
    );
    req.once('error', reject);
    req.end(payload);
  });
}
