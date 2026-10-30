/* global Headers, Request */
import { randomBytes, createHash } from 'node:crypto';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { createFileSink } from './file.js';

const owners = new Set();
/** @type {ReturnType<typeof createFileSink> | undefined} */ let sink;
const maxLogLength = 65536;
const secretKey =
  /^(?:password|passwd|pwd|secret|source[_-]?binding|ticket|(?:access|refresh|session|id)[_-]?token|token|client[_-]?secret|credential|totp|private[_-]?key|api[_-]?key|[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?|authorization|(?:set-)?cookie)$/i;

/** Search forward once per block, including an unterminated final key.
 * @param {string} text */
function maskPrivateKeys(text) {
  const begin = /-----BEGIN (?:[A-Z]{1,16} )?PRIVATE KEY-----/g;
  const end = /-----END (?:[A-Z]{1,16} )?PRIVATE KEY-----/g;
  let out = '',
    cursor = 0,
    match;
  while ((match = begin.exec(text))) {
    end.lastIndex = begin.lastIndex;
    const close = end.exec(text);
    out += text.slice(cursor, match.index) + '****';
    cursor = close ? end.lastIndex : text.length;
    begin.lastIndex = cursor;
    if (cursor === text.length) break;
  }
  return out + text.slice(cursor);
}

/** Tokenize dotted candidates once instead of retrying at every embedded eyJ.
 * @param {string} text */
function maskJWTs(text) {
  const tokens =
    /(?<![A-Za-z0-9_-])([A-Za-z0-9_-]+)\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*/g;
  let out = '',
    cursor = 0,
    match;
  while ((match = tokens.exec(text))) {
    if (!match[1].includes('eyJ')) {
      tokens.lastIndex = match.index + match[1].length + 1;
      continue;
    }
    out += text.slice(cursor, match.index) + '****';
    cursor = tokens.lastIndex;
  }
  return out + text.slice(cursor);
}

/** Scan each complete key once; never restart a suffix search inside a long token.
 * @param {string} text */
function maskFields(text) {
  const keys =
    /(?<![A-Za-z0-9_-])["']?([A-Za-z][A-Za-z0-9_-]*)["']?\s*[:=]\s*/g;
  let out = '',
    cursor = 0;
  for (const match of text.matchAll(keys)) {
    if (match.index < cursor) continue;
    const key = match[1];
    if (
      !secretKey.test(key) &&
      !secretKey.test(key.replaceAll('-', '_')) &&
      !(
        text[match.index - 1] === '\\' &&
        /^[nrt]/.test(key) &&
        secretKey.test(key.slice(1))
      )
    )
      continue;
    let end = match.index + match[0].length;
    const start = end,
      quote = text[start];
    if (quote === '"' || quote === "'") {
      end++;
      while (end < text.length) {
        const c = text[end++];
        if (c === '\\') end++;
        else if (c === quote) break;
      }
      end = Math.min(end, text.length);
      out += text.slice(cursor, start) + quote + '****' + quote;
    } else {
      while (end < text.length && !/[\s,;&}"']/.test(text[end])) end++;
      out += text.slice(cursor, start) + '****';
    }
    cursor = end;
  }
  return out + text.slice(cursor);
}
/** @param {string} text */
export function mask(text) {
  if (text.length > maxLogLength) return '[log omitted: size limit]';
  return maskFields(
    maskJWTs(maskPrivateKeys(text))
      .replace(
        /\b([a-z][a-z0-9+.-]{0,31}:\/\/[^/\s:@]*):[^/\s@]+@/gi,
        '$1:****@',
      )
      .replace(/\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+/gi, '$1 ****')
      .replace(/((?:set-)?cookie\s*:\s*)[^\r\n"\\]+/gi, '$1****'),
  );
}
/** Fixed, finite names on every platform; no host journal is read. */
export function logDirectory() {
  const base =
    process.platform === 'win32'
      ? process.env.LOCALAPPDATA || join(homedir(), 'AppData', 'Local')
      : process.platform === 'darwin'
        ? join(homedir(), 'Library', 'Logs')
        : process.env.XDG_STATE_HOME || join(homedir(), '.local', 'state');
  return join(base, 'Auriga', 'logs');
}
/** @param {any} ctx */
export function installObservability(ctx) {
  if (typeof ctx?.effect !== 'function') return;
  if (owners.has(ctx)) return;
  owners.add(ctx);
  sink ??= createFileSink(logDirectory());
  try {
    ctx.effect(
      () => async () => {
        owners.delete(ctx);
        if (!owners.size) {
          const old = sink;
          sink = undefined;
          await old?.close();
        }
      },
      'auriga-dsh: structured logs',
    );
  } catch {
    owners.delete(ctx);
    if (!owners.size) {
      const old = sink;
      sink = undefined;
      void old?.close().catch(() => {});
    }
  }
}
/** @param {unknown} value @returns {boolean} */
export function validTraceparent(value) {
  return (
    typeof value === 'string' &&
    value.length === 55 &&
    /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/.test(value) &&
    value.slice(3, 35) !== '0'.repeat(32) &&
    value.slice(36, 52) !== '0'.repeat(16)
  );
}
/** A fresh local operation. Reconnect/background operations never reuse it. */
export function newTraceparent() {
  return `00-${randomBytes(16).toString('hex')}-${randomBytes(8).toString('hex')}-01`;
}
/** NATS headers count against the existing max_payload. Correlate the existing
 * logical request ID without changing a single signed/payload byte or budget.
 * This untrusted hash never authorizes anything; retries of the same request
 * share its trace, while background/reconnect work has a fresh request ID.
 * @param {Uint8Array} payload */
export function natsTraceparent(payload) {
  if (payload.byteLength > 65536) return newTraceparent();
  try {
    const id = JSON.parse(Buffer.from(payload).toString('utf8')).request_id;
    if (typeof id === 'string' && /^[A-Za-z0-9._-]{1,128}$/.test(id)) {
      const trace = createHash('sha256')
        .update('auriga-nats-request-v1:' + id)
        .digest('hex')
        .slice(0, 32);
      return `00-${trace}-${randomBytes(8).toString('hex')}-01`;
    }
  } catch {
    /* Invalid business frames remain the protocol validator's job. */
  }
  return newTraceparent();
}
const operations = new Set([
  'source_register',
  'source_get',
  'source_revoke',
  'fetch',
  'ack',
  'submit',
  'send',
  'ping',
  'heartbeat',
  'resume',
  'receipt',
]);
/** Selected, bounded public metadata only. Never expose a whole parsed frame.
 * @param {Uint8Array} payload */
export function publicationContext(payload) {
  try {
    let frame;
    if (payload.byteLength <= 65536) {
      try {
        frame = JSON.parse(Buffer.from(payload).toString('utf8'));
      } catch {
        /* unknown */
      }
    }
    return {
      trace_id: natsTraceparent(payload).slice(3, 35),
      request_id:
        typeof frame?.request_id === 'string' &&
        /^[A-Za-z0-9._-]{1,128}$/.test(frame.request_id)
          ? frame.request_id
          : 'unknown',
      operation: operations.has(frame?.type) ? frame.type : 'unknown',
    };
  } catch {
    return undefined;
  }
}
/** Only Error stacks, including the complete finite cause chain; no custom objects.
 * @param {Error} error */
function causes(error) {
  const seen = new Set([error]),
    result = [];
  let cause = error.cause;
  while (cause instanceof Error && !seen.has(cause)) {
    seen.add(cause);
    result.push(cause.stack);
    cause = cause.cause;
  }
  return result.length ? result.join('\nCaused by: ') : undefined;
}
/** @param {string} level @param {string} message @param {Record<string, unknown>} [fields] @param {unknown} [error] */
export function logEvent(level, message, fields = {}, error) {
  try {
    if (
      level === 'DEBUG' &&
      process.env.AURIGA_LOG_LEVEL?.toUpperCase() !== 'DEBUG'
    )
      return;
    const { trace_id = '', ...context } = fields;
    // Call sites provide selected business context only, never headers/body/env objects.
    const serialized = JSON.stringify({
      '@timestamp': new Date().toISOString(),
      service: 'dsh-plugin',
      level,
      trace_id,
      message,
      ...context,
      ...(sink?.dropped ? { dropped_logs: sink.dropped } : {}),
      ...(error instanceof Error
        ? {
            exception: error.stack,
            cause: causes(error),
          }
        : {}),
    });
    if (
      serialized.length > maxLogLength ||
      Buffer.byteLength(serialized) >= maxLogLength
    )
      return;
    const line = mask(serialized) + '\n';
    if (Buffer.byteLength(line) > 65536) return;
    sink?.write(line);
  } catch {
    /* Logging cannot change business control flow. */
  }
}
/** @param {Parameters<typeof fetch>[0]} input @param {Parameters<typeof fetch>[1]} [init] */
export async function tracedFetch(input, init) {
  return traceFetch(fetch, input, init);
}
/** @param {typeof fetch} fetcher @param {Parameters<typeof fetch>[0]} input @param {Parameters<typeof fetch>[1]} [init] */
export async function traceFetch(fetcher, input, init) {
  const parent = newTraceparent();
  const headers = new Headers(
    init?.headers ?? (input instanceof Request ? input.headers : undefined),
  );
  headers.set('traceparent', parent);
  const url = new URL(input instanceof Request ? input.url : String(input));
  const route =
    /^\/api\/(iam|org|workspace|collab|ai|ops|audit|resource|knowledge)\/v1(?=\/|$)/.exec(
      url.pathname,
    );
  const context = {
    trace_id: parent.slice(3, 35),
    method: init?.method ?? 'GET',
    // Path segments can be invitation/device credentials even on failed calls.
    path: route ? route[0] + '/**' : '/**',
  };
  logEvent('INFO', 'HTTP request started', context);
  try {
    const response = await fetcher(input, {
      ...init,
      headers:
        init?.headers instanceof Headers
          ? headers
          : Object.fromEntries(headers),
    });
    logEvent(response.ok ? 'INFO' : 'WARN', 'HTTP response received', {
      ...context,
      http_status: response.status,
    });
    return response;
  } catch (error) {
    logEvent('ERROR', 'HTTP request failed', context, error);
    throw error;
  }
}
