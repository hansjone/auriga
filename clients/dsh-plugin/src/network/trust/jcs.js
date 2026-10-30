/* global TextDecoder */
import { Buffer } from 'node:buffer';
import { SessionError } from '../session/errors.js';

/** @param {string} detail */
function bad(detail) {
  throw new SessionError('network.protocol_error', detail);
}

/** ES2022-compatible lone-surrogate guard; strings are never normalized.
 * @param {string} value */
function wellFormed(value) {
  return !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(
    value,
  );
}

/** RFC 8785 over the protocol's safe-integer JSON subset. Unknown keys remain.
 * @param {any} value
 * @returns {string}
 */
export function canonicalize(value) {
  let count = 0;
  /** @param {any} v @param {number} depth @returns {string} */
  function visit(v, depth) {
    if (depth > 16 || ++count > 8192) bad('JSON complexity');
    if (v === null || typeof v === 'boolean') return JSON.stringify(v);
    if (typeof v === 'number') {
      if (!Number.isSafeInteger(v)) bad('unsafe number');
      return JSON.stringify(v);
    }
    if (typeof v === 'string') {
      if (!wellFormed(v) || Buffer.byteLength(v) > 16384) bad('invalid string');
      return JSON.stringify(v);
    }
    if (Array.isArray(v)) {
      if (v.length > 256) bad('array size');
      return '[' + Array.from(v, (x) => visit(x, depth + 1)).join(',') + ']';
    }
    if (
      !v ||
      typeof v !== 'object' ||
      ![Object.prototype, null].includes(Object.getPrototypeOf(v))
    )
      bad('non-JSON value');
    const keys = Object.keys(v).sort(); // JS sort compares UTF-16 code units.
    if (keys.length > 128) bad('object size');
    return (
      '{' +
      keys
        .map((k) => {
          if (!wellFormed(k) || Buffer.byteLength(k) > 16384)
            bad('invalid key');
          return JSON.stringify(k) + ':' + visit(v[k], depth + 1);
        })
        .join(',') +
      '}'
    );
  }
  const result = visit(value, 1);
  if (Buffer.byteLength(result) > 65536) bad('payload size');
  return result;
}

/** Reject ambiguous wire JSON before its original representation is lost.
 * @param {Uint8Array} bytes
 * @returns {any}
 */
export function parseStrictJson(bytes) {
  if (!bytes || !bytes.byteLength || bytes.byteLength > 65536)
    bad('payload size');
  let text = '';
  try {
    text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(
      bytes,
    );
  } catch {
    return bad('invalid UTF-8');
  }
  let i = 0;
  let count = 0;
  const ws = () => {
    while (i < text.length && ' \t\r\n'.includes(text[i])) i++;
  };
  function str() {
    if (text[i] !== '"') return bad('expected string');
    const start = i++;
    while (i < text.length) {
      const c = text[i++];
      if (c === '\\') {
        i++;
        continue;
      }
      if (c !== '"') continue;
      let value;
      try {
        value = JSON.parse(text.slice(start, i));
      } catch {
        return bad('invalid string');
      }
      if (!wellFormed(value) || Buffer.byteLength(value) > 16384)
        bad('invalid string');
      return value;
    }
    return bad('unterminated string');
  }
  /** @param {number} depth @returns {any} */
  function val(depth) {
    if (depth > 16 || ++count > 8192) bad('JSON complexity');
    ws();
    const c = text[i];
    if (c === '"') return str();
    if (c === '{') {
      i++;
      ws();
      const out = Object.create(null);
      if (text[i] === '}') {
        i++;
        return out;
      }
      let members = 0;
      for (;;) {
        ws();
        const k = str();
        if (Object.hasOwn(out, k) || ++members > 128)
          bad('duplicate key or object size');
        ws();
        if (text[i++] !== ':') bad('expected colon');
        out[k] = val(depth + 1);
        ws();
        if (text[i] === ',') {
          i++;
          continue;
        }
        if (text[i++] === '}') return out;
        bad('expected object end');
      }
    }
    if (c === '[') {
      i++;
      ws();
      const out = /** @type {any[]} */ ([]);
      if (text[i] === ']') {
        i++;
        return out;
      }
      for (;;) {
        out.push(val(depth + 1));
        if (out.length > 256) bad('array size');
        ws();
        if (text[i] === ',') {
          i++;
          continue;
        }
        if (text[i++] === ']') return out;
        bad('expected array end');
      }
    }
    const token = /^(?:true|false|null|-?(?:0|[1-9][0-9]*))/.exec(
      text.slice(i),
    );
    if (!token) return bad('invalid value');
    i += token[0].length;
    const value = JSON.parse(token[0]);
    if (typeof value === 'number' && !Number.isSafeInteger(value))
      bad('unsafe number');
    return value;
  }
  const result = val(1);
  ws();
  if (i !== text.length) bad('trailing data');
  return result;
}
