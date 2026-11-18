// @ts-nocheck
import { request } from 'node:http';
import { Buffer } from 'node:buffer';
import { TextDecoder } from 'node:util';
import { fail } from './protocol.js';

const operations = new Set([
  'health',
  'resolve',
  'register',
  'lookup',
  'resourceSnapshot',
  'beginTask',
  'issueCapability',
  'finishTask',
  'authorizeCapability',
  'readCredentials',
  'model',
  'send',
  'receive',
]);
const errors = new Set([
  'common.not_found',
  'common.conflict',
  'common.forbidden',
  'common.unauthenticated',
  'common.unavailable',
  'common.validation_failed',
  'shared.binding_mismatch',
  'shared.expired',
  'shared.revoked',
  'shared.signature_invalid',
]);
const maximum = 16 * 1024 * 1024;

/** Fixed host-to-node socket; no URL, proxy, redirect, or service credential enters the carrier. */
export function createSharedNodeTransport(socketPath) {
  const active = new Set();
  let closed = false;
  async function open(operation, body, signal) {
    if (closed || signal?.aborted) fail('common.unavailable');
    if (!operations.has(operation)) fail('common.validation_failed');
    const bytes = Buffer.from(JSON.stringify(body));
    if (bytes.length > maximum) fail('common.validation_failed');
    const outgoing = request({
      socketPath,
      path: '/_auriga/shared-rpc/' + operation,
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'content-length': String(bytes.length),
      },
      signal,
    });
    active.add(outgoing);
    outgoing.setTimeout(30_000, () =>
      outgoing.destroy(new Error('common.unavailable')),
    );
    outgoing.once('close', () => active.delete(outgoing));
    const pending = new Promise((resolve, reject) => {
      outgoing.once('response', resolve);
      outgoing.once('error', () =>
        reject(
          Object.assign(new Error('common.unavailable'), {
            code: 'common.unavailable',
          }),
        ),
      );
    });
    outgoing.end(bytes);
    return pending;
  }
  async function read(response) {
    let size = 0;
    const chunks = [];
    for await (const chunk of response) {
      size += chunk.length;
      if (size > maximum) {
        response.destroy();
        fail('common.unavailable');
      }
      chunks.push(chunk);
    }
    let value;
    try {
      value = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    } catch {
      fail('common.unavailable');
    }
    if (response.statusCode !== 200)
      fail(errors.has(value?.code) ? value.code : 'common.unavailable');
    return value;
  }
  return {
    async call(operation, body, signal) {
      const response = await open(operation, body, signal);
      try {
        if (response.headers['content-type'] !== 'application/json')
          fail('common.unavailable');
        return await read(response);
      } finally {
        response.destroy();
      }
    },
    async *stream(operation, body, signal) {
      const response = await open(operation, body, signal);
      try {
        if (response.statusCode !== 200) {
          await read(response);
          return;
        }
        if (response.headers['content-type'] !== 'application/x-ndjson')
          fail('common.unavailable');
        let pending = '';
        const decoder = new TextDecoder('utf-8', { fatal: true });
        for await (const bytes of response) {
          pending += decoder.decode(bytes, { stream: true });
          if (Buffer.byteLength(pending) > maximum) fail('common.unavailable');
          let newline;
          while ((newline = pending.indexOf('\n')) >= 0) {
            const line = pending.slice(0, newline);
            pending = pending.slice(newline + 1);
            if (!line) fail('common.unavailable');
            yield JSON.parse(line);
          }
        }
        pending += decoder.decode();
        if (pending.length) fail('common.unavailable');
      } finally {
        response.destroy();
      }
    },
    close() {
      closed = true;
      for (const outgoing of active) outgoing.destroy();
      active.clear();
    },
  };
}
