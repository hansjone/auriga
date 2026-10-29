import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { setImmediate } from 'node:timers';
import { createConnectRuntime } from '../src/local/runtime.js';

// In-process platform peer with a generated ES256 device. Real runtime/JWKS/
// encrypted vault/Team/session issuance; no real IAM or native host claim.
export async function runtimePeer(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-runtime-peer-'));
  const config = {
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
    ...options.config,
  };
  const { publicKey, privateKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  const claims = {
    iss: config.issuer,
    aud: 'auriga',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    tid: randomUUID(),
    sub: randomUUID(),
    did: randomUUID(),
    iat: Math.floor(Date.now() / 1000),
    exp:
      Math.floor(Date.now() / 1000) + (options.accessLifetimeSeconds ?? 3600),
  };
  const unsigned = [{ alg: 'ES256', kid: 'test-key', typ: 'at+jwt' }, claims]
    .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
    .join('.');
  const credential = {
    claims,
    refreshToken: 'invalid-revocation-test-proof',
    accessToken:
      unsigned +
      '.' +
      sign('sha256', Buffer.from(unsigned), {
        key: privateKey,
        dsaEncoding: 'ieee-p1363',
      }).toString('base64url'),
  };
  const registered = new Set(),
    requests = [],
    endpointId = randomUUID();
  const nativeFetch = globalThis.fetch;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    const path = new URL(url).pathname;
    requests.push(path);
    if (path === '/oauth2/revoke' && options.revokeHttp)
      return nativeFetch(url, init);
    if (path === '/api/iam/v1/network/register' && options.networkRegister)
      return options.networkRegister(url, init);
    let data;
    if (path === '/oauth2/jwks')
      data = {
        keys: [
          {
            ...publicKey.export({ format: 'jwk' }),
            kid: 'test-key',
            alg: 'ES256',
            use: 'sig',
          },
        ],
      };
    else if (path === '/oauth2/revoke') data = {};
    else if (path === '/ai/team/node/register')
      data = { endpoint: { id: endpointId } };
    else if (path === '/ai/mcp') {
      if (options.teamRequest) return options.teamRequest(url, init);
      const body = JSON.parse(init.body);
      if (body.method === 'notifications/initialized')
        return new Response(null, { status: 202 });
      const result =
        body.method === 'initialize'
          ? { protocolVersion: '2025-06-18' }
          : body.method === 'tools/list'
            ? {
                tools: [
                  'auriga_list_endpoints',
                  'auriga_open_thread',
                  'auriga_reply_thread',
                  'auriga_read_thread',
                ].map((name) => ({ name, inputSchema: { type: 'object' } })),
              }
            : {};
      data = { jsonrpc: '2.0', id: body.id, result };
    } else throw new Error('unexpected platform path ' + path);
    return Response.json(data);
  });
  const runtime = await createConnectRuntime({
    directory,
    config,
    ctx: {
      webServer: { protocol: 'http:' },
      tools: {
        register(tool) {
          registered.add(tool.name);
          return () => registered.delete(tool.name);
        },
      },
      sessionController: { create() {}, prompt() {} },
      sessions: { get() {}, flush() {} },
    },
  });
  t.after(async () => {
    runtime.close();
    await rm(directory, { recursive: true, force: true });
  });
  await runtime.vault.update((s) => {
    s.credential = credential;
  });
  const request = { headers: {} };
  request.headers.cookie = runtime.prepare(request).setCookie.split(';')[0];
  const result = await runtime.login(request, { action: 'resume' });
  if (!options.teamRequest) {
    const deadline = Date.now() + 3000;
    while (!runtime.isReady()) {
      assertReady();
      await new Promise((resolve) => setImmediate(resolve));
    }
    function assertReady() {
      if (Date.now() > deadline) throw new Error('Team fixture not ready');
    }
  }
  request.headers.cookie = result.setCookie[0].split(';')[0];
  request.headers['x-auriga-csrf'] = runtime.status(request).csrf;
  return { runtime, request, directory, credential, registered, requests };
}
