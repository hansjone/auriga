CREATE TABLE catalog_assignment (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
 package_id uuid NOT NULL, version_id uuid NOT NULL,
 scope text NOT NULL CHECK(scope IN ('tenant','member','group','instance')),
 target_id uuid NOT NULL,
 installation_mode text NOT NULL CHECK(installation_mode IN ('required','default','optional')),
 active boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(tenant_id,package_id,version_id) REFERENCES catalog_version(tenant_id,package_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_assignment');
CREATE INDEX catalog_assignment_scope ON catalog_assignment(tenant_id,scope,target_id) WHERE active;
