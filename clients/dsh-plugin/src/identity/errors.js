/**
 * Connector / core fail step helpers. Codes must appear in contracts/.
 * @param {string} code
 * @param {{ en?: string, 'zh-CN'?: string }} [message]
 * @param {boolean} [retryable]
 * @returns {{ type: 'fail', code: string, message?: Record<string, string>, retryable?: boolean }}
 */
export function fail(code, message, retryable = false) {
  /** @type {{ type: 'fail', code: string, message?: Record<string, string>, retryable?: boolean }} */
  const step = { type: 'fail', code };
  if (message) step.message = { en: message.en ?? code, ...message };
  if (retryable) step.retryable = true;
  return step;
}

/**
 * @param {string} code
 * @param {{ en?: string, 'zh-CN'?: string }} [message]
 * @param {boolean} [retryable]
 */
export function failResult(code, message, retryable = false) {
  return { step: fail(code, message, retryable) };
}
