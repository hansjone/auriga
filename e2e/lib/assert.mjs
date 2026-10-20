/**
 * Strict e2e assertions (unit-tested so desired-only / soft 4xx cannot silently pass).
 */

/** Actual InstancePhase must match; desired alone is never success. */
export function phaseReached(body, want) {
  const phase = String(body?.phase || '').toLowerCase();
  const target = String(want || '').toLowerCase();
  return phase === target;
}

/** Ready running instance (wake / cold-start complete). */
export function instanceReadyRunning(body) {
  const phase = String(body?.phase || '').toLowerCase();
  return !!body?.ready && phase === 'running';
}

/**
 * Session revoke must be an explicit success status from the IAM contract.
 * 4xx / "reachable" is failure (closes the old soft-pass hole).
 */
export function assertRevokeSuccess(status, body) {
  if (status === 204 || status === 200) return;
  const err = new Error(
    `revoke expected 200/204, got ${status}` +
      (body !== undefined ? `; bodySummary=${safeSummary(body)}` : ''),
  );
  err.code = 'revoke_not_ok';
  throw err;
}

/** Docker inspect Running flag: only true means running. */
export function containerIsRunning(inspectRunningField) {
  const v = String(inspectRunningField ?? '')
    .trim()
    .toLowerCase();
  return v === 'true' || v === '1';
}

/**
 * Redact secrets from values before logging. Never print cookie/token values.
 * @param {unknown} value
 * @param {number} [maxLen]
 */
export function safeSummary(value, maxLen = 240) {
  if (value == null) return String(value);
  let s;
  if (typeof value === 'string') s = value;
  else {
    try {
      s = JSON.stringify(value);
    } catch {
      s = String(value);
    }
  }
  s = s.replace(/Bearer\s+[A-Za-z0-9._\-+=/]+/gi, 'Bearer <redacted>');
  s = s.replace(
    /(authorization|cookie|set-cookie|password|totp|otp|secret|token)\s*[:=]\s*["']?[^"',}\s]*/gi,
    '$1=<redacted>',
  );
  s = s.replace(
    /(__Host-)?auriga_[a-z0-9_]*=[^;,\s"']+/gi,
    'auriga_*=<redacted>',
  );
  if (s.length > maxLen) s = s.slice(0, maxLen) + '…';
  return s;
}

/**
 * Instance edge response: healthy fake-dsh page vs not.
 * @param {{ status: number, text?: string }} page
 */
export function edgeInstanceOk(page) {
  return (
    page.status === 200 && String(page.text || '').includes('fake-dsh')
  );
}
