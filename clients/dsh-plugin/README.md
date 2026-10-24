# auriga-dsh

MIT-licensed DSH plugin for Node.js 22+. Package name: **auriga-dsh** (not published by default).

Installing the published plugin in DSH needs no platform environment variables. Its native main page and independent Auriga settings section load from the packaged UI and show an unconfigured connection. DSH's existing authentication still protects these local routes. Installing does not connect an Auriga account or grant resource access.

Set `AURIGA_MODE` when explicitly selecting a runtime:

- `standalone` (default): local emergency account + pluggable JS identity connectors, single owner, no platform connection.
- `managed`: platform-hosted instance — identity door, plugin/workspace guards, `/_auriga/health`, heartbeat, team panel shell, `ctx.get('auriga')`.
- `connect`: local DSH connected to a team platform (stub).

## Library

```js
import initialize, { enrollEmergency, createManaged } from 'auriga-dsh';

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

Cordis entry: `auriga-dsh/plugin`. Browser panel shell: `auriga-dsh/client`.

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
