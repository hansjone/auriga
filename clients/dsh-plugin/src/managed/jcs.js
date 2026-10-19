// @ts-nocheck
/**
 * RFC 8785 JSON Canonicalization Scheme (JCS).
 * Keys sorted by UTF-16 code units; numbers/strings use JSON.stringify.
 */

/** @param {unknown} value */
export function canonicalize(value) {
  if (value === null) return 'null';
  switch (typeof value) {
    case 'boolean':
      return value ? 'true' : 'false';
    case 'number':
      if (!Number.isFinite(value))
        throw new TypeError('JCS: non-finite number');
      return JSON.stringify(value);
    case 'string':
      return JSON.stringify(value);
    case 'object': {
      if (Array.isArray(value))
        return `[${value.map((item) => canonicalize(item)).join(',')}]`;
      const keys = Object.keys(
        /** @type {Record<string, unknown>} */ (value),
      ).sort();
      const parts = [];
      for (const key of keys) {
        const item = /** @type {Record<string, unknown>} */ (value)[key];
        if (item === undefined)
          throw new TypeError(`JCS: undefined value for key ${key}`);
        parts.push(`${JSON.stringify(key)}:${canonicalize(item)}`);
      }
      return `{${parts.join(',')}}`;
    }
    default:
      throw new TypeError(`JCS: unsupported type ${typeof value}`);
  }
}
