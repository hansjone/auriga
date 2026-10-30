# renderer

Deterministic instance config into `meta/` (package.json, cordis.patch.yml, env, identity-keys.json, instance.token). Unseals HPKE secrets; token files are mode 0600 and never logged.

`Secrets.device_credential` is independently HPKE-sealed with that field name and
the current specification generation. It becomes `device-credential.json` (0600)
and only its path enters `AURIGA_DEVICE_CREDENTIAL_FILE`. Missing material removes
the old file. Its bytes participate in the rendered digest so rotation reaches
an already running instance. The managed plugin reuses the desktop device network
provider; `instance.token` remains exclusively for instance control.

`Secrets.model_credential` is separate trusted model material. It is validated
against the instance identity and becomes `model-credential.json` (0600).
Only this material can configure the native `llm-pi-ai` managed route. Its
Authorization is a Loader `!!js` file expression; the sole model file is read at
evaluation. Obsolete `model-keys.json` copies are removed, and the user's native
`credentials` row is left intact and writable. The managed instance uses a
platform-only model-provider profile, with the standard native `llm-pi-ai` row.
The public patch and profile contain no inline key. Known and accepted: only
the instance owner sees the evaluated managed Authorization in `settings/describe`,
within the MANAGED-N1 same-host boundary (backlog B-MM-DESCRIBE). The native
probe warns for that exact value and fails for every other key exposure.

`cordis.patch.yml` is a DSH Loader patch list (`dsh web --patch`): an `insert` of the `auriga-dsh` plugin row (config: protected `browserOrigin`, spec patches `plugins.auriga-dsh.*`) and one `@deepseek-ai/dsh-mcp-client` row per team MCP server (token read by a `!!js` file expression, never inlined), then `webserver` (bind `0.0.0.0` for Docker, `127.0.0.1` otherwise) and `web-runtime` (no URL print, `trustedHosts`) overrides, then spec patches `plugins.<row>.<key…>` as whole-config row overrides. Renderer-owned rows and keys cannot be patched; arbitrary Profile model providers fail closed. Only separately sealed managed model material can configure the renderer-owned `llm-pi-ai` row.

The managed plugin row uses both `id: auriga-dsh` and `name: auriga-dsh`.
DSH's browser collector accepts package-root names; the Node subpath
`auriga-dsh/plugin` does not discover the package's browser half. This requires
the package root export to be the Cordis host entry `src/plugin.js`, as declared
by the current `clients/dsh-plugin/package.json`; the library export is
`auriga-dsh/library`.
The renderer change alone does not update an installed package or validate a
managed image/deployment; the installed image must actually contain that package.

The opt-in `TestCordisRealDSHBrowserCollector` probes the actual locked DSH
collector with a rendered row and the old-name control. Both managed Node
entries activate; only the package-root row contributes `auriga-dsh` and its
`src/client.js` to the browser graph. See
[`docs/dev/pr118-dsh-collector.md`](../../../../docs/dev/pr118-dsh-collector.md)
for reproduction and the limits of this module-level evidence.

`Options.Access` is the existing `deploymentconfig.Load` result from the agent's
`AURIGA_DEPLOYMENT_ACCESS_FILE`. A missing/zero configuration fails rendering.
`browserOrigin` uses the validated subdomain scheme/port with `<instanceId>.<parentDomain>`
in domain mode, or the exact portal Origin in IP/path mode. The whole key is reserved
against profile patches. Native `web-runtime.trustedHosts` contains one explicit
`host:effectivePort`, even for port 80/443; the plugin separately enforces the scheme.
`AURIGA_PLATFORM_URL` is derived only from `InstanceControl().Origin` and cannot leak
its internal DNS alias into either browser field. The previous independent agent
platform URL/parent domain/container environment variables now require explicit migration.
The public Compose recipe mounts the same access.json read-only into edge and agent;
its current fixed domain bootstrap is a test recipe, with public CLI input in IP2d
and real domain/IP browser verification in IP3.
