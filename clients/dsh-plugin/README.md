# auriga-dsh

MIT-licensed DSH plugin for Node.js 22+. Package name: **auriga-dsh** (not published by default).

Set `AURIGA_MODE` before initialization:

- `standalone` (default): local emergency account + pluggable JS identity connectors, single owner, no platform connection.
- `managed`: platform-managed DSH instance (stub).
- `connect`: local DSH connected to a team platform (stub).

Standalone auth runtime:

```js
import initialize, { enrollEmergency } from 'auriga-dsh';

const { create } = initialize();
const runtime = await create({
  config: {
    // First successful login becomes the owner. When exposing a public OIDC IdP,
    // ALWAYS set `owner` so a random visitor cannot claim the instance.
    owner: {
      providers: ['emergency'], // provider id and/or connector type
      // subject: 'alice',
      // email: 'alice@example.com',
    },
    connectors: [
      {
        type: 'emergency',
        id: 'emergency',
        config: enrollEmergency('alice', process.env.PASSWORD),
      },
      // {
      //   type: 'oidc',
      //   id: 'company-oidc',
      //   config: { issuer, clientId, clientSecret },
      // },
    ],
  },
});
```

Prefer signing in once with the **emergency** account before enabling public OIDC. See `docs/dev/dsh-plugin.md` for connector SPI, Argon2 floors, DNS pinning, CSRF, and error codes.

Protocol major version: **1** (`Auriga-Protocol: 1`). See `../../contracts/plugin/README.md`.

```powershell
node --test "test/**/*.test.js"
node node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

Runtime dependencies: `@noble/hashes` (Argon2id), `undici` (DNS-pinned OIDC fetch).

`pnpm --filter auriga-dsh gen:steps`

Copyright (c) 2026 hansjone. See [LICENSE](LICENSE).
