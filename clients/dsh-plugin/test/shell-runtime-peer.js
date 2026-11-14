import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { brokerFixture } from './broker-fixture.js';
import { createConnectRuntime } from '../src/local/runtime.js';
import { createConnectRoutes } from '../src/local/routes.js';
import { installNativeLoginRPC } from '../src/local/native-rpc.js';

/** Real plugin/runtime/routes with the schema-checked C boundary replaced.
 * No password handler, platform token store, HTTP fetch or real broker launch.
 */
export async function shellRuntimePeer(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-shell-peer-'));
  const connection = brokerFixture();
  options.configure?.(connection);
  const runtime = await createConnectRuntime({
    directory,
    config: { platformOrigin: '' },
    ctx: options.ctx ?? {},
    connection,
  });
  t.after(async () => {
    await runtime.close();
    await rm(directory, { recursive: true, force: true });
  });
  const peer = { principal: { kind: 'owner' } };
  const request = runtime.nativeRequest(peer);
  const calls = [];
  let handler;
  installNativeLoginRPC(
    {
      connection: {
        register(_ctx, _path, callback) {
          handler = callback;
        },
      },
    },
    () => runtime,
  );
  const rpc = (
    method,
    payload = { args: {} },
    source = peer,
    signal = new AbortController().signal,
  ) => {
    calls.push({ method, payload });
    return handler(method, payload, signal, source);
  };
  async function http(
    path,
    {
      method = 'GET',
      body,
      admitted = true,
      sameOrigin = true,
      csrf = true,
      contentType = 'application/json',
    } = {},
  ) {
    const route = createConnectRoutes({
      runtime,
      admission: {
        nativeAuth: { requestRejection: () => (admitted ? undefined : 401) },
        sameOrigin: () => sameOrigin,
      },
    });
    const req = Readable.from(
      body === undefined
        ? []
        : [Buffer.from(typeof body === 'string' ? body : JSON.stringify(body))],
    );
    Object.assign(req, {
      url: path,
      method,
      headers: {
        'content-type': contentType,
        ...(csrf ? { 'x-auriga-csrf': runtime.status(request).csrf } : {}),
      },
    });
    if (admitted) runtime.bindNativeRequest(peer, req);
    let status, headers, value;
    await route(req, {
      writeHead(code, fields) {
        status = code;
        headers = fields;
      },
      end(data) {
        value = data;
      },
    });
    return { status, headers, body: JSON.parse(value) };
  }
  return { directory, connection, runtime, request, peer, rpc, calls, http };
}
