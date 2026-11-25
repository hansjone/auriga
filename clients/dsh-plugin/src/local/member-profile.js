import { createHash } from 'node:crypto';

/** Names are presentation only. Every read uses the already verified identity;
 * a name never supplies an account, tenant, permission or login state.
 * @param {unknown} value */
export function memberName(value) {
  return typeof value === 'string' &&
    value.trim().length > 0 &&
    value.length <= 256 &&
    !Array.from(value).some(
      (character) =>
        character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127,
    )
    ? value.trim()
    : undefined;
}

/** Presentation only; never fetch an arbitrary scheme or leak URL credentials.
 * @param {unknown} value */
export function memberAvatar(value) {
  if (
    typeof value !== 'string' ||
    value.length > 2048 ||
    !/^https?:\/\//.test(value) ||
    /[\s"'<>\\]/.test(value) ||
    Array.from(value).some(
      (char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127,
    )
  )
    return undefined;
  try {
    const url = new URL(value);
    if (
      url.hostname &&
      !url.username &&
      !url.password &&
      !url.hash &&
      url.port !== '0'
    )
      return value;
  } catch {
    /* Omit malformed optional profile data. */
  }
  return undefined;
}

/** Correlate independently fetched display data, never authorize access.
 * Every identity/revision transition changes this opaque, non-secret fingerprint.
 * @param {any} snapshot */
export function memberProfileBinding(snapshot) {
  const who = snapshot?.identity,
    binding = who?.uacSessionBinding;
  if (
    snapshot?.state !== 'authenticated' ||
    typeof snapshot.shellEpoch !== 'string' ||
    !Number.isSafeInteger(snapshot.revision) ||
    snapshot.revision < 0 ||
    !['tenantId', 'memberId', 'deviceId', 'providerId'].every(
      (k) => typeof who?.[k] === 'string' && who[k],
    )
  )
    return undefined;
  return createHash('sha256')
    .update(
      JSON.stringify([
        snapshot.shellEpoch,
        snapshot.revision,
        who.tenantId,
        who.memberId,
        who.deviceId,
        who.providerId,
        who.empNo ?? null,
        binding?.sourceSessionId ?? null,
        binding?.providerId ?? null,
        binding?.authGeneration ?? null,
      ]),
    )
    .digest('hex');
}
