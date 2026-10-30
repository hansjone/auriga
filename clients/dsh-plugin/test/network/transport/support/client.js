import { createWsSocketFactory } from '../../../../src/network/transport/ws-socket.js';

/**
 * Production-shaped factory that trusts only the test root CA.
 * Uses `ws` so terminate() is native (same path as product assembly).
 * @param {string} rootPem
 */
export function createCaSocketFactory(rootPem) {
  return createWsSocketFactory({ ca: rootPem, rejectUnauthorized: true });
}

/** Wait until `check()` is truthy, polling; rejects with `what` after `timeoutMs`. */
export async function waitFor(check, what, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await check();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

/**
 * Plain (unguarded) SDK client standing in for the Broker/adapter side.
 * @param {{ url: string, rootPem: string }} server
 * @param {{ token: string } | { jwt: string, nkeySeed: string }} auth
 */
export async function connectPeer(server, auth) {
  const nats = await import('@nats-io/nats-core');
  const authenticator =
    'token' in auth
      ? nats.tokenAuthenticator(auth.token)
      : nats.jwtAuthenticator(
          auth.jwt,
          new TextEncoder().encode(auth.nkeySeed),
        );
  const createSocket = createCaSocketFactory(server.rootPem);
  return nats.wsconnect({
    servers: server.url,
    reconnect: false,
    authenticator,
    wsFactory: async (url) => ({ socket: createSocket(url), encrypted: true }),
  });
}
