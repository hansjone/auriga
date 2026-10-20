# ingress

Edge-facing mTLS entry for instance reverse-proxy, file access, and log tails.

## Listen

- Multi-node: TCP (`AURIGA_AGENT_INGRESS_LISTEN` / `--ingress-listen`)
- Single-node: Unix socket (`<agentDir>/ingress.sock`) or Windows named pipe (`auriga-agent-ingress`)

Only client certificates with URI SAN `auriga://edge` (or `auriga://edge/<id>`) are accepted.

## Routes

| Path | Behavior |
|---|---|
| `/i/<id>/…` | Reverse-proxy HTTP/WebSocket to the instance. Forwards verified `Auriga-Identity`; forged/invalid values are stripped. |
| `/files/<id>/…` | File ops gated by workspace Ed25519 file tickets (`typ=auriga-file+jwt`). Path escape rejected (`..`, NUL, absolute, out-of-root symlink). Zip uses UTF-8 name flag. Uploads create parent dirs. |
| `/logs/<id>` | Tail of the instance rolling log (default 50 MiB via `RollingWriter`). |
| `/metrics` | Prometheus text metrics for ingress request count. |

## Identity as instance account

`IdentityExecutor` is the hook for `setfsuid` / Windows impersonation; default `DirectExecutor` runs as the agent process until storage wires real accounts.
