# auriga-dsh

MIT-licensed DSH plugin for Node.js 22+. Package name: **auriga-dsh** (not published by default).

Installing the published plugin in DSH needs no platform environment variables. Its native main page and independent Auriga settings section load from the packaged UI and show an unconfigured connection. DSH's existing authentication still protects these local routes. Installing does not connect an Auriga account or grant resource access.

Set `AURIGA_MODE` when explicitly selecting a runtime:

- `standalone` (default): local emergency account + pluggable JS identity connectors, single owner, no platform connection.
- `managed`: platform-hosted instance — identity door, plugin/workspace guards, `/_auriga/health`, heartbeat, team panel shell, `ctx.get('auriga')`.
- `connect`: local DSH connected to a team platform (stub).

## Library

```js
import initialize, { enrollEmergency, createManaged } from 'auriga-dsh/library';

const { create } = initialize({ AURIGA_MODE: 'standalone' });
const runtime = await create({
  config: {
    owner: { providers: ['emergency'] },
    connectors: [
      {
        type: 'emergency',
        id: 'emergency',
        config: enrollEmergency('alice', process.env.PASSWORD),
      },
    ],
  },
});
```

Managed runtime (tests / hosts):

```js
const managed = await createManaged({
  env: process.env,
  startHeartbeat: false,
});
managed.apply(ctx); // Cordis-like host
```

Cordis bundle row: `auriga-dsh` (the root export is `src/plugin.js`). The explicit Node-only `auriga-dsh/plugin` export remains available; library callers use `auriga-dsh/library`. Browser panel shell: `auriga-dsh/client`. DSH's browser collector discovers package-root rows, so a bundle row naming only the `/plugin` subpath does not register the browser entry.

## Install in DSH

Add the published package or prepared `.tgz` through DSH's native plugin manager. The package declares `dsh.bundle.patch` and ships `cordis.patch.yml`, its Node/browser entries and built native UI. Installation uses DSH's normal package/script approval policy; no installation hook builds code or contacts headquarters.

For a development directory link, first install the repository's documented workspace dependencies (including this plugin's runtime dependencies), then run from the repository root:

```sh
node clients/dsh-plugin/scripts/package-ui.mjs
```

Add **`clients/dsh-plugin`** in DSH. A fresh checkout does not contain the ignored `ui/` build output; adding an unprepared directory can install the bundle but cannot serve its UI. Rebuild after changing panel sources. To prepare the distributable tarball, run `npm pack` in this plugin directory; its existing `prepack` performs the UI build. A consumer of the resulting tarball needs neither the monorepo nor Vite.

The default installed surface is the unconfigured shell, protected by native DSH authentication. Bundle installation does not complete Auriga login or the pending connect runtime.

The release preparer runs `npm pack` from this monorepo. `prepack` builds the native `web/panel` module and includes `ui/auriga-team-panel.js` and its package-version/SRI metadata. Published installs have no install hook, Vue dependency, frontend build step or requirement to download a homepage from headquarters. Managed mode continues to use the verified platform manifest; it never falls back to packaged UI after verification or loading failure.

The standalone identity library remains available, but its login transactions are not yet mounted as DSH HTTP login routes. Connect authentication, account/security migration and configuration writes are still pending. The unconfigured UI does not claim these capabilities are working.

See `docs/dev/dsh-plugin.md` and `contracts/plugin/README.md`.

Protocol major version: **1** (`Auriga-Protocol: 1`).

```powershell
node --test "test/**/*.test.js"
node node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

Runtime dependencies: `@noble/hashes` (Argon2id), `undici` (DNS-pinned OIDC fetch).

Copyright (c) 2026 hansjone. See [LICENSE](LICENSE).

# Typed local registration compatibility

Nonzero locals retain an immutable `addressType` and typed allocation fingerprint.
Existing sessions are classified only after trusted stat/inspect Header validation:
explicit `origin: 'subagent'` means subagent, an absent origin means session;
unknown or empty origins fail closed. Fork lineage does not grant execution.
New conversation creation fixes a session intent before registration and verifies
the created Header. Subagent execution remains unavailable before N2.

Version 1 allocation history without type keeps its original records, queue and
high-water mark; recovery reports an identity conflict instead of guessing a type
or rewriting old fingerprints. A valid empty history can start typed allocation.
READY includes the original typed pending operation. Registration success requires
matching operation/local/type/fingerprint and valid original registration revision.
The internal trusted port returns the correlated fingerprint; the formal signed
`RegisterResult` has no fingerprint echo, so its request correlation must preserve
the original fingerprint. Missing or mismatched fields cannot settle the queue.

The session module provides pure formal LOCAL_REGISTER frames and verifies signed
responses on its existing request port. This does not install a production WSS
LOCAL_REGISTER provider; runtime still requires that trusted provider externally.
Tests using fake hosts/controllers/brokers do not demonstrate a connected DSH pair.
