CREATE TABLE catalog_shared_view (
 tenant_id uuid NOT NULL,
 placement_id uuid NOT NULL,
 member_id uuid NOT NULL,
 pool_id uuid NOT NULL,
 mode text NOT NULL CHECK(mode IN ('shared','dedicated')),
 epoch bigint NOT NULL CHECK(epoch BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 view_id uuid NOT NULL,
 authorization_digest text NOT NULL CHECK(authorization_digest ~ '^sha256:[a-f0-9]{64}$'),
 revocation_revision bigint NOT NULL CHECK(revocation_revision BETWEEN 1 AND 9007199254740991),
 expires_at bigint NOT NULL,
 document jsonb NOT NULL,
 PRIMARY KEY(tenant_id,placement_id),
 UNIQUE(tenant_id,view_id)
);
SELECT auriga_enable_tenant_rls('catalog_shared_view');
