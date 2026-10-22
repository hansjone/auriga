# renderer

Deterministic instance config into `meta/` (package.json, cordis.patch.yml, env, identity-keys.json, instance.token). Unseals HPKE secrets; token files are mode 0600 and never logged.

`cordis.patch.yml` is a DSH Loader patch list (`dsh web --patch`): an `insert` of the `auriga-dsh` plugin row (config: protected `browserOrigin`, spec patches `plugins.auriga-dsh.*`) and one `@deepseek-ai/dsh-mcp-client` row per team MCP server (token read by a `!!js` file expression, never inlined), then `webserver` (bind `0.0.0.0` for Docker, `127.0.0.1` otherwise) and `web-runtime` (no URL print, `trustedHosts`) overrides, then spec patches `plugins.<row>.<key…>` as whole-config row overrides. Renderer-owned rows and keys cannot be patched; model providers fail closed until they are written into the user settings document.

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
