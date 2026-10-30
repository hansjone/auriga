// Dedicated native-fetch regression: observe the built-in dispatcher without
// changing its connections, TLS policy, timeouts, or process lifetime.
import assert from 'node:assert/strict';
import { channel } from 'node:diagnostics_channel';
import { writeSync } from 'node:fs';

const started = performance.now();
let sequence = 0;
const requests = new WeakMap();
const code = (value) => typeof value === 'string' && /^[A-Z0-9_]{1,64}$/.test(value) ? value : undefined;
const errorNames = new Set(['Error', 'TypeError', 'SyntaxError', 'AssertionError', 'RangeError', 'AbortError']);
const errorFields = (error) => ({
  name: errorNames.has(error?.name) ? error.name : 'unknown',
  code: code(error?.code), causeCode: code(error?.cause?.code),
});
function record(stage, fields = {}) {
  // Synchronous small records survive the parent's deadline kill. Never log
  // raw events, error messages/stacks, URLs, headers, bodies, or environment.
  writeSync(2, JSON.stringify({ stage, ms: Math.round(performance.now() - started), ...fields }) + '\n');
}

record('startup', { node: process.version, undici: process.versions.undici });
process.on('beforeExit', (exitCode) => record('before-exit', { exitCode }));
process.on('exit', (exitCode) => record('exit', { exitCode }));
// Connection events describe the pool, not an individual request.
channel('undici:client:beforeConnect').subscribe(() => record('connect-start'));
channel('undici:client:connected').subscribe(() => record('connected'));
channel('undici:client:connectError').subscribe(({ error }) => record('connect-error', errorFields(error)));
channel('undici:request:create').subscribe(({ request }) => {
  requests.set(request, sequence);
  record('request-created', { request: sequence });
});
for (const [name, stage] of [['bodySent', 'request-sent'], ['headers', 'response-headers'], ['trailers', 'response-received']]) {
  channel('undici:request:' + name).subscribe(({ request, response }) => record(stage, {
    request: requests.get(request), ...(response ? { status: response.statusCode } : {}),
  }));
}
channel('undici:request:error').subscribe(({ request, error }) => record('request-error', {
  request: requests.get(request), ...errorFields(error),
}));

const origin = process.argv[2];
const options = {
  headers: { authorization: 'Bearer ai-device', accept: 'application/json' },
  credentials: 'omit', redirect: 'error', cache: 'no-store',
  referrerPolicy: 'no-referrer',
};
async function request(method, path, init) {
  sequence += 1;
  record('fetch-start', { request: sequence, method, path });
  const response = await fetch(origin + path, init);
  const body = await response.text();
  record('response-body', { request: sequence, status: response.status });
  return { response, body };
}

try {
  for (const [method, path] of [
    ['GET', '/ai/v1/models'],
    ['POST', '/ai/v1/chat/completions'],
    ['GET', '/api/ai/v1/me/usage?limit=2'],
    ['POST', '/api/ai/v1/network/submit'],
  ]) {
    const { response } = await request(method, path, {
      ...options, method,
      ...(method === 'POST' ? {
        headers: { ...options.headers, 'content-type': 'application/json' },
        body: '{}',
      } : {}),
    });
    assert.equal(response.status, 202);
    record('assertions-passed', { request: sequence });
  }
  for (const headers of [
    { cookie: 'auriga_admin_session=browser' },
    { origin },
    { referer: origin + '/' },
    { 'sec-fetch-site': 'same-origin' },
  ]) {
    for (const [method, path] of [
      ['GET', '/ai/v1/models'],
      ['POST', '/api/ai/v1/network/submit'],
    ]) {
      const { response, body } = await request(method, path, {
        ...options, method, headers: { ...options.headers, ...headers },
      });
      assert.equal(response.status, 403);
      assert.equal(JSON.parse(body).code, 'common.forbidden');
      record('assertions-passed', { request: sequence });
    }
  }
  record('complete', { requests: sequence });
  console.log('node=' + process.version + ' native-fetch=4 rejected=8');
} catch (error) {
  record('failure', { request: sequence, ...errorFields(error) });
  process.exitCode = 1;
}
