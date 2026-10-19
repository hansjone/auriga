import { standalone } from './modes/standalone.js';
import { managed } from './modes/managed.js';
import { connect } from './modes/connect.js';
import { PROTOCOL_VERSION } from './managed/protocol.js';

/** @typedef {import('./generated/steps').ClientStep} ClientStep */
/** @typedef {'standalone' | 'managed' | 'connect'} Mode */

export { PROTOCOL_VERSION };

/** @param {NodeJS.ProcessEnv} [env] @returns {Mode} */
export function selectMode(env = process.env) {
  const mode = env.AURIGA_MODE ?? 'standalone';
  if (mode !== 'standalone' && mode !== 'managed' && mode !== 'connect')
    throw new Error('Invalid AURIGA_MODE: ' + mode);
  return mode;
}

/**
 * Select a mode placeholder. Standalone / managed hosts call `.create(options)`.
 * @param {NodeJS.ProcessEnv} [env]
 */
export default function initialize(env = process.env) {
  return { standalone, managed, connect }[selectMode(env)]();
}

export { standalone, managed, connect };
export {
  createStandalone,
  createStandaloneCore,
  enrollEmergency,
  createMemoryStore,
  hashPassword,
  generateTotpSecret,
  totpCode,
} from './modes/standalone.js';
export { createManaged } from './modes/managed.js';
export { createEmergencyConnector } from './identity/emergency.js';
export { createOidcConnector } from './identity/oidc.js';
export {
  loadConnectors,
  createBuiltinConnectors,
} from './identity/registry.js';
export {
  createOidcHttp,
  publicAddress,
  localHost,
} from './identity/oidc-http.js';
export {
  verifyIdentityHeader,
  createDoorLock,
  createReplayCache,
} from './managed/door.js';
export {
  NATIVE_PLUGIN_MUTATIONS,
  guardPluginManager,
  guardWorkspaceRegistry,
  isNativePluginManageRequest,
  assertWorkspaceCreatePath,
  DEFAULT_WORKSPACE_ROOT,
} from './managed/guards.js';
export { createActivityProbe } from './managed/activity.js';
export { buildHealthBody, HEALTH_PATH } from './managed/health.js';
export {
  createAurigaSdk,
  provideAurigaSdk,
  SDK_VERSION,
} from './managed/sdk.js';
export {
  checkProtocol,
  protocolInfo,
  MIN_SUPPORTED_PROTOCOL,
} from './managed/protocol.js';
