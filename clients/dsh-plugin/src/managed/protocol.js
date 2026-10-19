// @ts-nocheck
/** Current plugin protocol major version (contracts/plugin). */
export const PROTOCOL_VERSION = 1;

/** Platform supports current and previous major versions. */
export const MIN_SUPPORTED_PROTOCOL = Math.max(1, PROTOCOL_VERSION - 1);

/**
 * @param {number|string|undefined|null} clientMajor
 * @returns {{ ok: true, major: number } | { ok: false, code: 'plugin.protocol_unsupported', params: { current: number, minSupported: number } }}
 */
export function checkProtocol(clientMajor) {
  const major =
    typeof clientMajor === 'string' ? Number.parseInt(clientMajor, 10) : clientMajor;
  if (
    typeof major !== 'number' ||
    !Number.isInteger(major) ||
    major < MIN_SUPPORTED_PROTOCOL
  ) {
    return {
      ok: false,
      code: 'plugin.protocol_unsupported',
      params: {
        current: PROTOCOL_VERSION,
        minSupported: MIN_SUPPORTED_PROTOCOL,
      },
    };
  }
  return { ok: true, major };
}

/** @param {string} pluginVersion @param {string} dshVersion */
export function userAgent(pluginVersion, dshVersion) {
  return `auriga-dsh/${pluginVersion} dsh/${dshVersion}`;
}

export function protocolInfo() {
  return {
    current: PROTOCOL_VERSION,
    minSupported: MIN_SUPPORTED_PROTOCOL,
  };
}
