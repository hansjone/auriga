// Reuse the existing tested protocol/transport implementations. buildBroker
// bundles this seam into the standalone artifact; no plugin import at runtime.
export { NatsLink } from '../dsh-plugin/src/network/transport/nats-link.js';
export { createDeviceProvider } from '../dsh-plugin/src/network/device-provider.js';
export { openTrustStore } from '../dsh-plugin/src/network/trust/store.js';
export { verifyEnvelopeWithRefresh } from '../dsh-plugin/src/network/trust/verifier.js';
export { parseStrictJson } from '../dsh-plugin/src/network/trust/jcs.js';
export { verifyFetchPage } from '../dsh-plugin/src/network/session/page.js';
