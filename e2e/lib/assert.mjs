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
  const secretKey =
    /authorization|cookie|password|passphrase|otp|secret|token|ticket|nonce|session|csrf/i;
  const seen = new WeakSet();
  function redact(input) {
    if (typeof input === 'string') {
      try {
        const parsed = JSON.parse(input);
        if (parsed !== input) return redact(parsed);
      } catch {
        /* Plain text is handled below. */
      }
      return input;
    }
    if (!input || typeof input !== 'object') return input;
    if (seen.has(input)) return '<circular>';
    seen.add(input);
    if (Array.isArray(input)) return input.map(redact);
    return Object.fromEntries(
      Object.entries(input).map(([key, item]) => [
        key,
        secretKey.test(key) ? '<redacted>' : redact(item),
      ]),
    );
  }
  const redacted = redact(value);
  let s = typeof redacted === 'string' ? redacted : JSON.stringify(redacted);
  if (typeof s !== 'string') s = '<unserializable>';
  s = s.replace(/Bearer\s+[A-Za-z0-9._\-+=/]+/gi, 'Bearer <redacted>');
  s = s.replace(
    /["']?([\w-]*(?:authorization|cookie|password|otp|secret|token|ticket|nonce|session|csrf)[\w-]*)["']?\s*[:=]\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^,}\s]*)/gi,
    '$1=<redacted>',
  );
  s = s.replace(
    /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g,
    '<redacted-jwt>',
  );
  s = s.replace(
    /(__Host-)?auriga_[a-z0-9_]*=[^;,\s"']+/gi,
    'auriga_*=<redacted>',
  );
  if (s.length > maxLen) s = s.slice(0, maxLen) + '…';
  return s;
}

/** Edge explicitly authenticates the instance cookie before routing. */
export function assertInstanceUnauthorized(page) {
  if (
    page.status === 401 &&
    page.body?.code === 'edge.unauthorized' &&
    !String(page.text || '').includes('fake-dsh')
  )
    return;
  throw new Error(`expected 401 edge.unauthorized, got ${page.status}`);
}

/** A stopped route is unavailable; authentication and unrelated 5xx cannot pass. */
export function assertStoppedEvidence(inspect, page) {
  if (!inspect.ok || inspect.field !== 'false')
    throw new Error('stopped container must exist with Running=false');
  if (page.status !== 503 || page.body?.code !== 'edge.instance_not_running')
    throw new Error(
      `stopped route expected 503 edge.instance_not_running, got ${page.status}`,
    );
}

export function assertRevocationClose(info, deleteSentAt, peerClose) {
  if (
    !info ||
    !Number.isFinite(info.at) ||
    !Number.isFinite(deleteSentAt) ||
    info.at <= deleteSentAt
  )
    throw new Error('websocket closed before the revoke request');
  // The current edge cancels the hijacked TCP proxy; a DSH Close frame instead
  // indicates an independent application timeout or error.
  if (peerClose)
    throw new Error('DSH Close frame cannot prove edge revocation');
}

/** The platform pre-warms 120 seconds before the alarm (Lifecycle.WAKE_LEAD). */
export function assertAlarmWake(inspect, wakeAt, stoppedAt, instance) {
  const startedAt = Date.parse(inspect.startedAt);
  if (
    !inspect.ok ||
    !inspect.running ||
    !Number.isFinite(startedAt) ||
    startedAt <= stoppedAt ||
    startedAt < wakeAt - 120_000 - 5000
  )
    throw new Error(
      'container did not start after stop within alarm pre-warm window',
    );
  const nextWakeAt = instance?.nextWakeAt;
  if (
    nextWakeAt &&
    (!Number.isFinite(Date.parse(nextWakeAt)) ||
      Date.parse(nextWakeAt) <= wakeAt)
  )
    throw new Error('alarm nextWakeAt was not consumed or advanced');
}

/**
 * Instance edge response: healthy fake-dsh page vs not.
 * @param {{ status: number, text?: string }} page
 */
export function edgeInstanceOk(page) {
  return page.status === 200 && String(page.text || '').includes('fake-dsh');
}
