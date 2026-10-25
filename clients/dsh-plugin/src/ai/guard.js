/** Local, sanitized diagnostics: never attach provider errors, bodies or causes. */
export class AdapterError extends Error {
  /** @param {string} code */
  constructor(code) {
    super(`Auriga AI: ${code}`);
    this.code = code;
  }
}
/** @param {unknown} condition @param {string} [code] @returns {asserts condition} */
export function check(condition, code = 'UNSUPPORTED_OPTION') {
  if (!condition) throw new AdapterError(code);
}
/** Dynamic wire boundary. All values must be validated before use.
 * @param {unknown} value @returns {Record<string, any>} */
export function object(value) {
  check(value !== null && typeof value === 'object' && !Array.isArray(value));
  return /** @type {Record<string, any>} */ (value);
}
/** @param {unknown} value @param {string[]} keys */
export function only(value, keys) {
  const record = object(value);
  check(Object.keys(record).every((key) => keys.includes(key)));
  return record;
}
/** @param {unknown} value @param {number} max @param {number} [min] @returns {asserts value is string} */
export function text(value, max, min = 0) {
  check(
    typeof value === 'string' && value.length >= min && value.length <= max,
  );
}
/** @param {unknown} value @param {number} [min] @returns {asserts value is number} */
export function integer(value, min = 0) {
  check(Number.isSafeInteger(value) && /** @type {number} */ (value) >= min);
}
/** @param {unknown} value @returns {asserts value is string} */
export function toolName(value) {
  text(value, 64, 1);
  check(/^[A-Za-z0-9_-]+$/.test(value));
}
