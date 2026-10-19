# auriga-dsh

MIT-licensed DSH plugin for Node.js 22+. Package name: **auriga-dsh** (not published by default).

Set `AURIGA_MODE` before initialization:

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

See `docs/dev/dsh-plugin.md` and `contracts/plugin/README.md`.

Protocol major version: **1** (`Auriga-Protocol: 1`).

```powershell
node --test "test/**/*.test.js"
node node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

Runtime dependencies: `@noble/hashes` (Argon2id), `undici` (DNS-pinned OIDC fetch).

Copyright (c) 2026 hansjone. See [LICENSE](LICENSE).
