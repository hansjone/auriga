CREATE TABLE catalog_assignment_failure (
 tenant_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 target_id uuid NOT NULL,
 package_id uuid NOT NULL,
 scope text NOT NULL CHECK(scope IN ('member','instance')),
 state text NOT NULL DEFAULT 'failed' CHECK(state='failed'),
 error_code text NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,owner_id,target_id,package_id,scope)
);
SELECT auriga_enable_tenant_rls('catalog_assignment_failure');
