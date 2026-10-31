/** Host-owned clock uncertainty, shared with JWT iat/nbf validation.
 * @param {number} [value] */
export function resolveClockToleranceSeconds(value = 60) {
  if (!Number.isSafeInteger(value) || value < 0 || value > 120)
    throw new Error('invalid JWT clock tolerance');
  return value;
}
export const clockToleranceSeconds = resolveClockToleranceSeconds(
  Number(process.env.AURIGA_JWT_CLOCK_SKEW_SECONDS ?? 60),
);
export const clockToleranceMs = clockToleranceSeconds * 1000;
/** Only for a signed platform timestamp against the receiving machine's clock.
 * Local witness deadlines and comparisons within a signed platform window stay strict.
 * @param {number} value @param {number} now */
export const platformStarted = (value, now) =>
  Number.isFinite(value) &&
  Number.isFinite(now) &&
  value <= now + clockToleranceMs;
