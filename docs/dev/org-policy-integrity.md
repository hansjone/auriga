# Policy writes and workspace values

`putPolicy` serializes all three existing policy keys (`workspace`, `login`,
`egress`) with the existing tenant structural lock before reading the version.
The lock remains held through the surrounding transaction. Under READ COMMITTED,
two writers using the same `If-Match` cannot both commit; a write without
`If-Match` still increments the latest version. Under REPEATABLE READ, an old
snapshot fails with `common.conflict` and the caller must start a new transaction.
No retry occurs inside the old transaction. A rejected update also rolls back
earlier writes in its outer transaction.

For `workspace`, the value must be an object. Known numeric fields require JSON
integers with the existing schema bounds: `idleMinutes >= 5` and
`maxConcurrentStarts >= 1`. The three flags require booleans, and
`defaultDshVersion` requires a string. Optional fields remain optional; null is
not an omitted field. Unknown fields and exact integer values are retained.
There is no additional maximum, version-string restriction, or default filling.
Invalid values return `common.validation_failed` without changing the policy
version, value, update metadata, or events. This change adds no policy events.
`login` and `egress` retain their existing value handling.

The HTTP controller, permissions, error contracts, schema ownership and tenant
RLS remain unchanged. A nonexistent tenant or missing policy retains
`org.not_found`; an unknown policy key retains `org.conflict`.

`OrgPolicyIntegrityTest` uses real PostgreSQL service roles and FORCE RLS. It
checks the three keys, same-version and unversioned races under RC/RR, outer
rollback, invalid workspace fields, missing rows, another real tenant, unknown
fields, and exact HTTP integer parsing/storage beyond JavaScript and int64
ranges. The controller parsing test does not claim to exercise HTTP authorization.
Frontend code must still retain its original ETag and treat 412 as a conflict,
not automatically retry with a newer version. Tenant-profile PATCH is a different
endpoint and has no ETag concurrency guarantee.
