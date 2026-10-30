import { openTrustStore } from '../../../clients/dsh-plugin/src/network/trust/store.js';
import { createMailboxClient } from '../../../clients/dsh-plugin/src/network/durable/client.js';

// NatsTransport delivers through its constructor callback. This observation
// adapter adds per-probe listeners without replacing the production callback.
export function assemblyMailboxTransport(transport, listeners) {
  return {
    send: (bytes) => transport.send(bytes),
    stop: () => transport.stop(),
    onMessage(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    get epoch() {
      return transport.epoch;
    },
    get state() {
      return transport.state;
    },
  };
}

// Transport-only probes share a C with the production inbox. They can receive
// a legitimate request-scoped overload while its automatic FETCH is in flight.
// Use the same authenticated, bounded, identical-byte exchange as the plugin;
// never accept an unsigned retry hint or turn overload into a successful probe.
export async function requestAssemblyMailbox({
  prepared,
  cache,
  transport,
  identity,
  ready,
  frame,
  expected,
  signal,
}) {
  const trust = await openTrustStore({
    cache,
    roots: [...prepared.roots],
    realm: identity.realm,
    requireAnnouncement: true,
    refresh: async () => prepared.refresh(trust, signal),
  });
  await prepared.refresh(trust, signal);
  signal.throwIfAborted();
  const client = createMailboxClient({
    transport,
    trust,
    identity,
    ready,
    signal,
    assertCurrent() {
      signal.throwIfAborted();
    },
  });
  return client.request(frame, expected);
}
