/* global Headers, Request */
import { randomBytes, createHash } from 'node:crypto';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { createFileSink } from './file.js';
import { writeStructuredDiagnostic } from '../network/diagnostics.js';

const owners = new Set();
/** @type {ReturnType<typeof createFileSink> | undefined} */ let sink;
/** @param {string} text */
export function mask(text) {
  return text
    .replace(
      /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
      '****',
    )
    .replace(/([a-z][a-z0-9+.-]*:\/\/[^/\s:@]*):[^/\s@]+@/gi, '$1:****@')
    .replace(/\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+/gi, '$1 ****')
    .replace(/\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*/g, '****')
    .replace(
      /(["']?(?:password|passwd|pwd|secret|(?:access|refresh|session|id)[_-]?token|token|client[_-]?secret|credential|totp|private[_-]?key|api[_-]?key|[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?|authorization|(?:set-)?cookie)["']?\s*[:=]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^"'\\\s,;}]+)/gi,
      (_all, prefix, value) =>
        prefix +
        (value.startsWith('"')
          ? '"****"'
          : value.startsWith("'")
            ? "'****'"
            : '****'),
    )
    .replace(/((?:set-)?cookie\s*:\s*)[^\r\n"\\]+/gi, '$1****');
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
    const line =
      mask(
        JSON.stringify({
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
                cause:
                  error.cause instanceof Error ? error.cause.stack : undefined,
              }
            : {}),
        }),
      ) + '\n';
    if (Buffer.byteLength(line) > 65536) return;
    sink?.write(line);
    const owner = [...owners].at(-1);
    if (owner) writeStructuredDiagnostic(owner, line);
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
  const context = {
    trace_id: parent.slice(3, 35),
    method: init?.method ?? 'GET',
    path: url.pathname,
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
