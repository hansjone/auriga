CREATE TABLE catalog_placement_selection (
 tenant_id uuid NOT NULL,
 placement_id uuid NOT NULL,
 package_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 epoch bigint NOT NULL CHECK(epoch BETWEEN 1 AND 9007199254740991),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 release_sequence bigint NOT NULL CHECK(release_sequence BETWEEN 1 AND 9007199254740991),
 desired_state text NOT NULL CHECK(desired_state IN ('ENABLED','DISABLED','REMOVED')),
 identity jsonb NOT NULL CHECK(jsonb_typeof(identity)='object'),
 PRIMARY KEY(tenant_id,placement_id,package_id)
);
SELECT auriga_enable_tenant_rls('catalog_placement_selection');
