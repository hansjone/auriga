# auriga-dsh

An independent MIT-licensed DSH plugin skeleton for Node.js 22 or later.

Set `AURIGA_MODE` before initialization:

- `standalone` (default): local identity providers and emergency access, without a platform connection.
- `managed`: a platform-managed DSH instance.
- `connect`: a local DSH installation connected to a team platform.

The entry point selects an empty implementation. Authentication, DSH hooks,
network requests, and panel loading are planned for later tasks.
Unknown modes fail initialization.

Protocol major version: **1** (`Auriga-Protocol: 1`). The platform supports the
current and previous protocol majors; version 1 currently has minimum 1.
See ../../contracts/plugin/README.md for the protocol contract.

Run `node --test` in this directory. From the monorepo root, run
`pnpm --filter auriga-dsh gen:steps` to regenerate the committed JSDoc types
from the identity steps JSON Schema, including unreachable definitions.
The package has no runtime dependencies and can be copied or published independently.

Copyright (c) 2026 hansjone. See [LICENSE](LICENSE).
