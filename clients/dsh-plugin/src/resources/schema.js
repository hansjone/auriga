import documents from './schema-data.js';
import { instant, refuse } from './trust.js';
/** Runtime validation of packaged, unmodified frozen schemas. No remote refs.
 * @param {string} file @param {string} name @param {any} value */
export function schemaValid(file, name, value) {
  const docs = /** @type {Record<string,any>} */ (documents);
  /** @param {any} schema @param {any} v @param {string} origin @param {number} depth @returns {boolean} */
  function check(schema, v, origin, depth) {
    if (depth > 64 || schema === false) return false;
    if (schema === true) return true;
    if (schema.$ref) {
      const [path, fragment] = schema.$ref.split('#');
      const target = path
        ? path.split('/').at(-1).replace('.schema.json', '')
        : origin;
      const resolved = fragment
        .split('/')
        .slice(1)
        .reduce(
          (/** @type {any} */ o, /** @type {string} */ k) => o?.[k],
          docs[target],
        );
      return !!resolved && check(resolved, v, target, depth + 1);
    }
    if (
      schema.oneOf &&
      schema.oneOf.filter((/** @type {any} */ s) =>
        check(s, v, origin, depth + 1),
      ).length !== 1
    )
      return false;
    if (
      schema.anyOf &&
      !schema.anyOf.some((/** @type {any} */ s) =>
        check(s, v, origin, depth + 1),
      )
    )
      return false;
    if (
      schema.allOf &&
      !schema.allOf.every((/** @type {any} */ s) =>
        check(s, v, origin, depth + 1),
      )
    )
      return false;
    if (schema.not && check(schema.not, v, origin, depth + 1)) return false;
    if (
      schema.if &&
      check(schema.if, v, origin, depth + 1) &&
      schema.then &&
      !check(schema.then, v, origin, depth + 1)
    )
      return false;
    if (
      Object.hasOwn(schema, 'const') &&
      JSON.stringify(v) !== JSON.stringify(schema.const)
    )
      return false;
    if (schema.enum && !schema.enum.includes(v)) return false;
    if (
      schema.type === 'object' ||
      (v &&
        typeof v === 'object' &&
        !Array.isArray(v) &&
        (schema.properties ||
          schema.required ||
          schema.additionalProperties !== undefined))
    ) {
      if (!v || typeof v !== 'object' || Array.isArray(v)) return false;
      if (
        schema.required?.some((/** @type {string} */ k) => !Object.hasOwn(v, k))
      )
        return false;
      for (const [k, item] of Object.entries(v)) {
        if (schema.properties?.[k]) {
          if (!check(schema.properties[k], item, origin, depth + 1))
            return false;
        } else if (schema.additionalProperties === false) return false;
        else if (
          typeof schema.additionalProperties === 'object' &&
          !check(schema.additionalProperties, item, origin, depth + 1)
        )
          return false;
      }
    }
    if (schema.type === 'array') {
      if (
        !Array.isArray(v) ||
        v.length < (schema.minItems ?? 0) ||
        v.length > (schema.maxItems ?? Infinity)
      )
        return false;
      if (
        schema.uniqueItems &&
        new Set(v.map((i) => JSON.stringify(i))).size !== v.length
      )
        return false;
      if (
        schema.items &&
        !v.every((i) => check(schema.items, i, origin, depth + 1))
      )
        return false;
    }
    if (schema.type === 'string') {
      if (
        typeof v !== 'string' ||
        [...v].length < (schema.minLength ?? 0) ||
        [...v].length > (schema.maxLength ?? Infinity)
      )
        return false;
      if (schema.pattern && !new RegExp(schema.pattern, 'u').test(v))
        return false;
      if (
        schema.format === 'uuid' &&
        !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(
          v,
        )
      )
        return false;
      if (schema.format === 'date-time') {
        try {
          instant(v);
        } catch {
          return false;
        }
      }
      if (schema.format === 'uri') {
        try {
          if (!new URL(v).protocol) return false;
        } catch {
          return false;
        }
      }
    }
    if (
      schema.type === 'integer' &&
      (!Number.isSafeInteger(v) ||
        v < (schema.minimum ?? -Infinity) ||
        v > (schema.maximum ?? Infinity))
    )
      return false;
    if (schema.type === 'boolean' && typeof v !== 'boolean') return false;
    return true;
  }
  return (
    !!docs[file]?.$defs[name] && check(docs[file].$defs[name], value, file, 0)
  );
}
/** @param {string} name @param {any} value */
export function requireSchema(name, value) {
  if (!schemaValid('unified', name, value)) refuse('common.validation_failed');
  return value;
}
