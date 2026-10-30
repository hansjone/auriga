// User decision 2026-10-25: defer same-machine isolation for local AND managed.
// Restore this requirement only after both modes have verified host/process,
// credential, ledger and IPC isolation and an authoritative capability probe.
// A profile directory or durable flush alone is never proof of isolation.
// This is code policy, not a tool parameter, provider option or environment flag.
const REQUIRE_ISOLATION = false;

export const networkIsolation = Object.freeze({
  required: REQUIRE_ISOLATION,
  isolated: false,
  state: 'unisolated-development',
});

/** Single isolation gate shared by the runtime and standalone HostPort adapter. */
export function assertNetworkIsolation() {
  if (networkIsolation.required && !networkIsolation.isolated)
    throw new Error('network host isolation required');
  return networkIsolation;
}
