import { Buffer } from 'node:buffer';
import { canonicalize, parseStrictJson } from './jcs.js';
import { positiveVersion, timeValue } from './keyset.js';

import { schema } from '../../generated/network/auto-renewal-v2-schema.js';
export { schema };
const definitions = /** @type {Record<string, any>} */ (schema.$defs);
export const jsonLimits = Object.freeze({
  maxBytes: 32 * 1024 * 1024,
  maxArray: 65536,
  maxNodes: 600000,
  maxDepth: 32,
});
/** @param {any} value */
export const jcs = (value) => canonicalize(value, jsonLimits);
/** @param {any} value */
export const detach = (value) =>
  parseStrictJson(
    typeof value === 'string'
      ? Buffer.from(value)
      : value instanceof Uint8Array
        ? value
        : Buffer.from(jcs(value)),
    jsonLimits,
  );
/** @param {string} code @returns {never} */
export function reject(code) {
  throw new Error(code);
}
const keywords = new Set([
  '$schema',
  '$id',
  '$comment',
  '$defs',
  '$ref',
  'title',
  'description',
  'type',
  'properties',
  'required',
  'additionalProperties',
  'const',
  'enum',
  'pattern',
  'format',
  'minimum',
  'maximum',
  'minItems',
  'maxItems',
  'uniqueItems',
  'minLength',
  'maxLength',
  'items',
  'allOf',
  'oneOf',
  'not',
]);
/** @param {any} node */
function audit(node) {
  for (const key of Object.keys(node))
    if (!keywords.has(key)) reject('unsupported_schema');
  for (const child of Object.values(node.properties ?? {})) audit(child);
  for (const child of Object.values(node.$defs ?? {})) audit(child);
  for (const child of node.allOf ?? []) audit(child);
  for (const child of node.oneOf ?? []) audit(child);
  if (node.items) audit(node.items);
  if (node.not) audit(node.not);
}
audit(schema);
/** Strict evaluation of the frozen schema's finite keyword set.
 * @param {any} value @param {any} rule @returns {boolean} */
function matches(value, rule) {
  if (
    rule.oneOf &&
    rule.oneOf.filter((/** @type {any} */ branch) => matches(value, branch))
      .length !== 1
  )
    return false;
  if (rule.$ref)
    return matches(value, definitions[rule.$ref.split('/').at(-1)]);
  if (
    rule.allOf &&
    !rule.allOf.every((/** @type {any} */ r) => matches(value, r))
  )
    return false;
  if (rule.not && matches(value, rule.not)) return false;
  if (Object.hasOwn(rule, 'const') && value !== rule.const) return false;
  if (rule.enum && !rule.enum.includes(value)) return false;
  switch (rule.type) {
    case 'object':
      return (
        value !== null &&
        typeof value === 'object' &&
        !Array.isArray(value) &&
        (rule.required ?? []).every((/** @type {string} */ k) =>
          Object.hasOwn(value, k),
        ) &&
        Object.keys(value).every((k) =>
          Object.hasOwn(rule.properties ?? {}, k)
            ? matches(value[k], rule.properties[k])
            : rule.additionalProperties !== false,
        )
      );
    case 'array':
      return (
        Array.isArray(value) &&
        value.length >= (rule.minItems ?? 0) &&
        value.length <= (rule.maxItems ?? Infinity) &&
        (!rule.uniqueItems || new Set(value.map(jcs)).size === value.length) &&
        value.every((v) => matches(v, rule.items))
      );
    case 'string':
      return (
        typeof value === 'string' &&
        [...value].length >= (rule.minLength ?? 0) &&
        [...value].length <= (rule.maxLength ?? Infinity) &&
        (!rule.pattern || new RegExp(rule.pattern).test(value)) &&
        (!rule.format ||
          (rule.format === 'date-time' && Number.isFinite(timeValue(value)))) &&
        (rule.pattern !== '^[1-9][0-9]{0,19}$' || positiveVersion(value))
      );
    case 'integer':
      return (
        Number.isSafeInteger(value) &&
        value >= (rule.minimum ?? -Infinity) &&
        value <= (rule.maximum ?? Infinity)
      );
    case 'boolean':
      return typeof value === 'boolean';
    default:
      return rule.type === undefined;
  }
}
/** @param {string} type @param {any} value @param {string} [code] */
export function structure(type, value, code = 'invalid_chain') {
  if (!definitions[type] || !matches(value, definitions[type])) reject(code);
  return value;
}
