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
Its `models` catalog requires unique public IDs. Each `contextWindow` and
`maxOutputTokens` capability is optional: unknown values are omitted, never zero
or an invented default. Provided values are positive safe integers at most
9,007,199,254,740,991 (`contextWindow` is at least 2); when both are known,
output must be smaller than context. Producers and consumers check this pair.
These are DSH bootstrap/request metadata, not extra relay invocation limits.
An unknown or one-sided capability does not invalidate other authorized models
or require identity rotation. DSH native defaults apply only inside DSH;
later manual capability synchronization uses the current authorized catalog.
No credential is placed in Profile, environment, image, public YAML or logs.
The native pi-ai route stores only the reserved credential reference
`apiKeyEnv: AURIGA_MANAGED_MODEL`. Its service-owned resolver reads the sole
`/run/auriga/model-credential.json` (0600) for an operation bound to the managed
provider, target, configuration revision and current owner. An old snapshot
cannot acquire authority from a later configuration change.
The obsolete `model-keys.json` copy is removed. The native `credentials` service
keeps its own writable user store, including personal references and OAuth records.
This is a platform-only model-provider profile, separate from personal profiles.
Key replacement uses the existing material restart path; invalid installation
configuration clears previously delivered model material on reconciliation.
Settings and profile describe only the reference, never the managed key value.
The browser credential Remote does not expose `resolve`; the native credential
store and process-global environment do not contain the managed key. The former
Loader expression and its describe exposure have been removed.
Gateway lease-ceiling checks permit five seconds of IAM clock skew; actual
expiry and per-call online authorization remain mandatory.
