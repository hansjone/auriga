import { TextDecoder } from 'node:util';
import { AdapterError, check } from './guard.js';

export const EVENT_LIMIT = 256 * 1024;
export const TOTAL_LIMIT = 8 * 1024 * 1024;

/** One operation owns every listener, timer, reader and abort signal.
 * @param {AbortSignal | undefined} caller */
export function operation(caller) {
  const controller = new AbortController();
  let code = 'ABORTED';
  let expiresAt = Infinity;
  const totalDeadline = Date.now() + 300000;
  /** @type {ReadableStreamDefaultReader<Uint8Array> | undefined} */
  let reader;
  /** @type {ReadableStream<Uint8Array> | null} */
  let body = null;
  /** @type {(() => void)[]} */
  const cleanups = [];
  /** @param {string} reason */
  const abort = (reason) => {
    if (controller.signal.aborted) return;
    code = reason;
    controller.abort();
    if (reader) void reader.cancel().catch(() => {});
    else if (body) void body.cancel().catch(() => {});
  };
  /** @param {AbortSignal} signal @param {string} reason */
  const watch = (signal, reason) => {
    const listener = () => abort(reason);
    signal.addEventListener('abort', listener, { once: true });
    cleanups.push(() => signal.removeEventListener('abort', listener));
    if (signal.aborted) listener();
  };
  if (caller) watch(caller, 'ABORTED');
  const totalTimer = setTimeout(() => abort('TIMEOUT'), 300000);
  cleanups.push(() => clearTimeout(totalTimer));
  return {
    signal: controller.signal,
    abort,
    watch,
    /** @param {Response} response */
    adopt(response) {
      body = response.body;
      if (controller.signal.aborted && body) void body.cancel().catch(() => {});
    },
    /** @param {number} expiry */
    expire(expiry) {
      expiresAt = expiry;
      const remaining = expiry - Date.now();
      if (remaining <= 0) {
        abort('AUTH');
        return;
      }
      const timer = setTimeout(() => abort('AUTH'), remaining);
      cleanups.push(() => clearTimeout(timer));
    },
    assert() {
      if (Date.now() >= expiresAt) abort('AUTH');
      else if (Date.now() >= totalDeadline) abort('TIMEOUT');
      if (controller.signal.aborted) throw new AdapterError(code);
    },
    /** Race even a misbehaving injected port; do not wait for fetch to honor abort.
     * @template T @param {Promise<T>} promise @returns {Promise<T>} */
    wait(promise) {
      return new Promise((resolve, reject) => {
        const aborted = () => {
          cleanup();
          reject(new AdapterError(code));
        };
        const timer = setTimeout(() => abort('TIMEOUT'), 30000);
        const cleanup = () => {
          clearTimeout(timer);
          controller.signal.removeEventListener('abort', aborted);
        };
        controller.signal.addEventListener('abort', aborted, { once: true });
        promise.then(
          (value) => {
            cleanup();
            if (controller.signal.aborted) reject(new AdapterError(code));
            else resolve(value);
          },
          (error) => {
            cleanup();
            reject(error);
          },
        );
        if (controller.signal.aborted) aborted();
      });
    },
    /** @param {ReadableStreamDefaultReader<Uint8Array>} current */
    attach(current) {
      reader = current;
      if (controller.signal.aborted) void reader.cancel().catch(() => {});
    },
    close() {
      abort('ABORTED');
      for (const cleanup of cleanups) cleanup();
      cleanups.length = 0;
    },
  };
}

/** @param {Response} response @param {ReturnType<typeof operation>} op @param {number} [max] */
async function* bytes(response, op, max = TOTAL_LIMIT) {
  check(response.body, 'SERVER');
  const reader = response.body.getReader();
  op.attach(reader);
  let total = 0;
  try {
    while (true) {
      op.assert();
      const next = await op.wait(reader.read());
      if (next.done) break;
      check(next.value instanceof Uint8Array, 'SERVER');
      total += next.value.byteLength;
      check(total <= max, 'SERVER');
      yield next.value;
    }
  } finally {
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** @param {Response} response @param {ReturnType<typeof operation>} op */
export async function readJson(response, op) {
  const chunks = [];
  for await (const chunk of bytes(response, op, 1048576)) chunks.push(chunk);
  return JSON.parse(
    new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)),
  );
}

/** Bounded byte parser, supporting CR/LF/CRLF and fragmented UTF-8.
 * No event queue: the consumer pulls one completed event at a time.
 * @param {Response} response @param {ReturnType<typeof operation>} op */
export async function* readSse(response, op) {
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const line = new Uint8Array(EVENT_LIMIT);
  let length = 0,
    eventSize = 0,
    previousCR = false;
  /** @type {string[]} */
  let data = [];
  for await (const chunk of bytes(response, op)) {
    for (const byte of chunk) {
      op.assert();
      if (previousCR && byte === 10) {
        previousCR = false;
        if (eventSize) {
          eventSize++;
          check(eventSize <= EVENT_LIMIT, 'SERVER');
        }
        continue;
      }
      previousCR = false;
      eventSize++;
      check(eventSize <= EVENT_LIMIT, 'SERVER');
      if (byte === 10 || byte === 13) {
        previousCR = byte === 13;
        if (length === 0) {
          const event = data.join('\n');
          data = [];
          eventSize = 0;
          if (event) yield event;
        } else {
          const value = decoder.decode(line.subarray(0, length));
          if (value.startsWith('data:'))
            data.push(value.slice(5).replace(/^ /, ''));
          else if (value === 'data') data.push('');
          else
            check(
              value.startsWith(':') || /^(event|id|retry):/.test(value),
              'SERVER',
            );
          length = 0;
        }
      } else {
        check(length < EVENT_LIMIT, 'SERVER');
        line[length++] = byte;
      }
    }
  }
  // EOF is never completion: even an unterminated [DONE] is rejected.
  throw new AdapterError('SERVER');
}
