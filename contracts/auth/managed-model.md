# Managed model key

`IssueManagedDeviceResponse.model_credential` is a separate random secret, never
the network credential or instance control token. Its wire form is
`amc.<tenant>.<member>.<instance>.<device>.<random>`, where identities are canonical
UUIDs and random is 32 cryptographically random bytes in unpadded base64url.
The prefix selects an online verification route; it establishes no identity.

IAM persists a hash and an encrypted idempotent issuance copy under the current
managed-device lineage. Only authenticated `svc:workspace` can issue/revoke it.
The key shares that lineage's 30-day expiry and lifecycle counter, with no overlap
on rotation. It is returned only with current `auriga.connect.models.invoke`.
The current managed-device binding also requires the existing collaboration grant.

Only `svc:ai-gateway` may submit the exact key through `VerifyDeviceAiAccess` with
a delegated non-platform tenant. IAM checks hash, tenant, member, instance,
device, current workspace lifecycle counter/running intent, backing session,
account, membership and model permission on every call. It rechecks the backing
device after remote authorization and returns a lease of at most 300 seconds,
bounded by the backing expiry. It never creates a JWT, refresh grant or idle touch.
All ordinary APIs, OAuth grants, network tickets and Team MCP continue to reject
this model-only key. Dependency failures fail closed.

The trusted file is defined by `plugin/managed-model.schema.json`, separately
HPKE-sealed as `Secrets.model_credential` with current specification generation.
No credential is placed in Profile, environment, image, public YAML or logs.
The native pi-ai route carries only `apiKeyEnv: AURIGA_MANAGED_MODEL_KEY`.
The renderer also writes the native version-1 credential document at
`/run/auriga/model-keys.json` (0600); the managed `credentials` row points to it.
Settings descriptions and profile edits carry references only, never a resolved
Authorization header. This is a platform-only profile, separate from personal
profiles. Key replacement uses the existing material restart path.
Gateway lease-ceiling checks permit five seconds of IAM clock skew; actual
expiry and per-call online authorization remain mandatory.
