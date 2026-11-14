/* global Headers, FormData */
import { dispatchResource } from './broker-dispatch.js';
import { parseStrictJson } from './json.js';

/** Resource adapters retain their existing signed-wire validation. This private
 * route selector resolves only frozen operation metadata; it never performs HTTP
 * or forwards a target/header/identity to a broker as a generic request.
 * @param {any} runtime @param {string} path @param {any} [options] */
export async function resourceRequest(runtime, path, options = {}) {
  const fail = () => {
    throw Object.assign(new Error('common.validation_failed'), {
      code: 'common.validation_failed',
    });
  };
  if (
    typeof path !== 'string' ||
    !path.startsWith('/') ||
    path.startsWith('//') ||
    /[%\\#]/.test(path.split('?')[0]) ||
    path.split('/').some((p) => p === '..' || p === '.')
  )
    fail();
  const url = new URL(path, 'https://local.invalid');
  const method = (options.method ?? 'GET').toLowerCase();
  const matches = [];
  for (const operation of runtime.operations ?? []) {
    if (!operation.route || operation.method !== method) continue;
    const parts = operation.route.split('/'),
      actual = url.pathname.split('/');
    if (parts.length !== actual.length) continue;
    const params = /** @type {Record<string,any>} */ ({});
    let match = true;
    for (let i = 0; i < parts.length; i++) {
      if (/^\{[^}]+\}$/.test(parts[i]))
        params[parts[i].slice(1, -1)] = actual[i];
      else if (parts[i] !== actual[i]) match = false;
    }
    if (match) matches.push({ operation, params });
  }
  if (matches.length !== 1 || !runtime.broker) fail();
  const { operation, params } = matches[0];
  const input = /** @type {Record<string,any>} */ ({ ...params });
  for (const [key, value] of url.searchParams) {
    if (Object.hasOwn(input, key)) fail();
    input[key] = key === 'limit' ? Number(value) : value;
  }
  const headers = Object.fromEntries(new Headers(options.headers).entries());
  const safe = /** @type {Record<string,string>} */ ({});
  for (const name of operation.requestHeaders)
    if (headers[name] !== undefined) safe[name] = headers[name];
  if (Object.keys(safe).length) input.headers = safe;
  if (options.body !== undefined) {
    if (options.body instanceof FormData) {
      const body = /** @type {Record<string,any>} */ ({});
      for (const [key, value] of options.body.entries()) {
        if (key === 'metadata') {
          if (typeof value !== 'string' && value.size > 1048576) fail();
          const bytes =
            typeof value === 'string'
              ? Buffer.from(value)
              : Buffer.from(await value.arrayBuffer());
          if (bytes.length > 1048576) fail();
          body.metadata = parseStrictJson(bytes, true);
        } else if (typeof value === 'string') body[key] = value;
        else {
          const ref = await runtime.upload(
            Buffer.from(await value.arrayBuffer()),
            value.type,
          );
          if (key === 'artifacts') (body.artifacts ??= []).push(ref);
          else body[key] = ref;
        }
      }
      body.artifacts ??= [];
      input.body = body;
    } else
      input.body =
        typeof options.body === 'string'
          ? JSON.parse(options.body)
          : options.body;
  }
  let output;
  try {
    output = await dispatchResource(runtime.broker, operation.operation, input);
  } catch (error) {
    // Keep the adapters' post-response identity fence and HTTP-category mapping.
    // Broker errors carry no upstream body or credentials across this boundary.
    const status = {
      'common.validation_failed': 400,
      'common.unauthenticated': 401,
      'common.forbidden': 403,
      'common.not_found': 404,
      'common.conflict': 409,
      'common.precondition_failed': 412,
      'common.rate_limited': 429,
      'common.unavailable': 503,
    }[/** @type {{code?:string}} */ (error)?.code ?? ''];
    if (!status) throw error;
    return Response.json({}, { status });
  }
  let value = output.body;
  if (value?.streamId) value = await runtime.readStream(value.streamId);
  return new Response(
    value instanceof Uint8Array ? new Uint8Array(value) : JSON.stringify(value),
    {
      status: output.statusCode,
      headers: output.headers,
    },
  );
}
