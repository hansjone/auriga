CREATE TABLE catalog_shared_installation (
 tenant_id uuid NOT NULL,
 placement_id uuid NOT NULL,
 package_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 active boolean NOT NULL,
 selection jsonb NOT NULL CHECK(jsonb_typeof(selection)='object'),
 PRIMARY KEY(tenant_id,placement_id,package_id)
);
SELECT auriga_enable_tenant_rls('catalog_shared_installation');
