-- Durable CAS records outlive REMOVED tombstones and reinstalls.
CREATE TABLE catalog_instance_installation (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, instance_id uuid NOT NULL, owner_id uuid NOT NULL,
 package_id uuid NOT NULL, revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 resource jsonb NOT NULL, selection_revision bigint NOT NULL CHECK(selection_revision BETWEEN 1 AND 9007199254740991),
 collected_generation bigint CHECK(collected_generation BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,instance_id,package_id),
 FOREIGN KEY(tenant_id,package_id) REFERENCES catalog_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_instance_installation');
ALTER TABLE catalog_installation ADD COLUMN collected_generation bigint;
-- Legacy enabled=false means DISABLED, never an inferred REMOVED tombstone.
ALTER TABLE catalog_installation DISABLE ROW LEVEL SECURITY;
UPDATE catalog_installation SET document=jsonb_set(jsonb_set(document,'{resource,installationRevision}',to_jsonb(revision)),
 '{resource,desiredState}',COALESCE(document->'resource'->'desiredState',to_jsonb(CASE WHEN (document->'resource'->>'enabled')::boolean THEN 'ENABLED' ELSE 'DISABLED' END)));
ALTER TABLE catalog_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_installation FORCE ROW LEVEL SECURITY;
