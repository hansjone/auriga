import { Buffer } from 'node:buffer';
import { readFile } from 'node:fs/promises';
import { request } from 'node:https';
import { Readable } from 'node:stream';

// Use only this run's server CA and explicit submit client keypair. The native
// HTTPS request has no global dispatcher, environment proxy or IAM trust fallback.
function submitOrigin(gateway) {
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
  return origin;
}

async function openSubmit(gateway, { body, headers, signal }) {
  const origin = submitOrigin(gateway);
  const [ca, cert, key] = await Promise.all([
    readFile(gateway.ca),
    readFile(gateway.submitClientCertFile),
    readFile(gateway.submitClientKeyFile),
  ]);
  const timeout = globalThis.AbortSignal.timeout(15_000);
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
        signal: signal
          ? globalThis.AbortSignal.any([signal, timeout])
          : timeout,
        headers,
      },
      (res) => {
        resolve(res);
      },
    );
    req.once('error', reject);
    req.end(body);
  });
}

export async function postSubmit(gateway, body, token, headers = {}) {
  const res = await openSubmit(gateway, {
    body: JSON.stringify(body),
    headers: {
      authorization: `Bearer ${token}`,
      'content-type': 'application/json',
      ...headers,
    },
  });
  const parts = [];
  for await (const part of res) parts.push(part);
  return {
    status: res.statusCode,
    body: Buffer.concat(parts).toString('utf8'),
  };
}

// The isolated SEND-3 host keeps the unmodified production device provider.
// Only its exact submit URL uses this mTLS transport; IAM and every other fetch
// keep the original transport. Responses stream through the production parser,
// size limit and authorization checks, rather than replacing prepared.submit.
export function installSubmitFetch(gateway) {
  const origin = submitOrigin(gateway);
  const original = globalThis.fetch;
  const fetchSubmit = async (input, options = {}) => {
    const url = new URL(input?.url ?? input);
    if (
      url.origin !== origin.origin ||
      url.pathname !== '/api/ai/v1/network/submit'
    )
      return original(input, options);
    if (
      url.search ||
      url.hash ||
      options.method !== 'POST' ||
      typeof options.body !== 'string'
    )
      throw new Error('invalid submit fetch request');
    const res = await openSubmit(gateway, options);
    if (
      options.redirect === 'error' &&
      [301, 302, 303, 307, 308].includes(res.statusCode) &&
      res.headers.location
    ) {
      res.destroy();
      throw new Error('submit redirect refused');
    }
    const noBody = [204, 205, 304].includes(res.statusCode);
    if (noBody) res.resume();
    return new globalThis.Response(noBody ? null : Readable.toWeb(res), {
      status: res.statusCode,
      statusText: res.statusMessage,
      headers: res.headers,
    });
  };
  globalThis.fetch = fetchSubmit;
  return () => {
    if (globalThis.fetch !== fetchSubmit)
      throw new Error('submit fetch ownership changed');
    globalThis.fetch = original;
  };
}
