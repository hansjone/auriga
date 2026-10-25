import { Agent, WebSocket, buildConnector } from 'undici';

/**
 * WebSocket factory that trusts only the test root CA (TLS verification stays on).
 * The upgraded TCP socket is detached from undici's pool, so terminate() destroys
 * it through a reference captured in the connector: that is the only way to drop
 * a connection whose close frame is stuck behind unsent data.
 * @param {string} rootPem
 */
export function createCaSocketFactory(rootPem) {
  const connector = buildConnector({ ca: rootPem });
  return (url) => {
    let tcp;
    const agent = new Agent({
      connect: (opts, callback) =>
        connector(opts, (error, socket) => {
          tcp = socket;
          callback(error, socket);
        }),
    });
    const socket = new WebSocket(url, { dispatcher: agent });
    socket.terminate = () => {
      tcp?.destroy();
      agent.destroy().catch(() => {});
    };
    return socket;
  };
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
