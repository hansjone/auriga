# Auriga DSH shared and dedicated modes

Shared pools are for mutually trusted teams only. Members may see each other's files, act with owner capabilities, read each other's sessions and operate internal applications as another member, with audit attributed to the impersonated member. Stalls, crashes and cancellation timeouts interrupt the entire pool. Use a dedicated container for confidentiality or an independent failure domain.

## Accepted first-release risks

1. Native cookies/browserPeer can map different members to the same owner; /connect may also construct an owner. Signed platform headers do not replace native cookies, index snapshots or WS identity. Member policies, including shell bans, may be bypassed. Not all entries are non-owner entries.
2. Native workspaceFiles/read, readBytes and stat accept other members' file paths. Absolute paths, parent paths, Windows junctions and hard links can enable cross-member reads; stat can leak metadata. Fixed working directories, hidden buttons and one realpath check do not provide isolation. File observation and downloads also remain at risk.
3. Memory exhaustion, synchronous stalls and crashes affect the entire pool. A member's execution cannot be selectively hard-killed in-process. Cooperative cancellation waits 30 seconds before an external supervisor conservatively stops the whole pool and execution tree, interrupting others. This is not a 30-second completion guarantee. Unconfirmed stops remain blocked. Expiry or revocation of any unified resource also stops the pool.
4. A can open, read and drive B's session and history through a native owner entry. Enterprise skills bound to B's session credentials may execute A's instructions as B, with both platform and application audit attributed to B. Successful capability checks do not remove impersonation. Controlled non-owner carriers, restricted file services and external member execution domains are future improvements.

## Member entry

Sign in at the portal home, read your placement and select Open Auriga DSH. A first placement defaults to shared. Refresh pending, switching or unavailable placements; never fall back to old sessions. After entitlement and a successful switch, the same button opens your dedicated container. UAC and invited members follow identical rules. Enterprise skills come only from platform unified resources. Ask an administrator for dedicated mode for personal keys, private skills, arbitrary plugins, member-shared skills/MCP, shell or terminal.

## Administration and recovery

Open Members → Details → Auriga DSH · Member mode. Read/write placement permissions and verification within 600 seconds are required. Enter a reason and confirm risks and impacts. Both upgrade and downgrade archive personal files for 30 days, start an empty workspace and discard sessions. Old tasks, .dsh, .env, model keys and plugin runtime state are not restored. File recovery requires an explicit personal request, audited administrator export and scanning, limited to the member's workspace.

202 means registration only. Retain the operation ID and read progress through fencing, draining, preparing, committing or classified failure. Retry failed queries without repeating writes. If a write outcome is unknown, establish its operation status before another write. Blocked is not success and does not return to a live old session; administrators must reconcile. After success, re-enter from the portal and verify an empty workspace and new session.

Announce maintenance and export windows, register the remain-dedicated list, then rebuild members without migrating session history or silently destroying data at first login. The frozen contract currently lacks administrator placement/ETag reads, entitlement changes, batch-list persistence and live capacity/guardian-impact queries. These features remain unavailable pending integration. Progress and portal-ticket endpoints still need real backend and end-to-end validation.

Initial budgets, not measured limits: 20 registered / 4 active per pool; 1 active, 5 GiB / 100,000 inodes per member. Stop admission at 80% memory and add pools at capacity without silently moving running sessions.
