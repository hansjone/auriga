# policy

Node-local `agent-policy.yaml`: allowlisted artifact URL prefixes and image registries, plus per-instance resource caps. Loaded by `run` from `AURIGA_AGENT_POLICY_FILE` or `<dataRoot>/agent-policy.yaml`.

`allowedImageRepos` remains a literal registry/path prefix allowlist, not an exact
repository allowlist. Images must retain the existing `repo@sha256:<64 hex>` shape.
Optional `allowedImageRefs` further restricts that allowlist to complete image
references matched byte for byte; both checks must pass. For example:

```yaml
allowedImageRepos:
  - registry.example.com/auriga/
allowedImageRefs:
  - registry.example.com/auriga/dsh@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Omitting `allowedImageRefs` leaves the existing prefix behavior unchanged; an
explicit `allowedImageRefs: []` denies every image. Entries are not trimmed,
normalized, case folded, or expanded as aliases; an empty string is not a
wildcard. An exact match cannot bypass empty or nonmatching `allowedImageRepos`.
Production release verification separately validates the strict OCI locator
before calling this policy; this extra list only narrows local authorization.
`AllowAny*` switches are ignored when loading YAML. The in-memory test-only
`AllowAnyImage` switch does not bypass a non-nil `AllowedImageRefs` list.
