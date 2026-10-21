# renderer

Deterministic instance config into `meta/` (package.json, cordis.patch.yml, env, identity-keys.json, instance.token). Unseals HPKE secrets; token files are mode 0600 and never logged.

`cordis.patch.yml` is a DSH Loader patch list (`dsh web --patch`): an `insert` of the `auriga-dsh` plugin row (config: `trustedHosts`, spec patches `plugins.auriga-dsh.*`) and one `@deepseek-ai/dsh-mcp-client` row per team MCP server (token read by a `!!js` file expression, never inlined), then `webserver` (bind `0.0.0.0` for Docker, `127.0.0.1` otherwise) and `web-runtime` (no URL print, `trustedHosts`) overrides, then spec patches `plugins.<row>.<key…>` as whole-config row overrides. Renderer-owned rows and keys cannot be patched; model providers fail closed until they are written into the user settings document.

`AURIGA_PLATFORM_URL` comes from `Options.PlatformURL` (agent env `AURIGA_AGENT_INSTANCE_PLATFORM_URL`), never from the gRPC control address; `trustedHosts` is `<instanceId>.<Options.InstanceParentDomain>` (agent env `AURIGA_AGENT_INSTANCE_PARENT_DOMAIN`).
