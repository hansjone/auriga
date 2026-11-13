-- Flyway applies this migration transactionally under its DDL lock.
ALTER TABLE catalog_ticket ADD COLUMN consumed_at timestamptz;
ALTER TABLE resource_operation DISABLE ROW LEVEL SECURITY;
UPDATE resource_operation SET response='{"completed":true}'::jsonb
WHERE operation LIKE 'catalog/ticket/%' OR operation LIKE 'catalog/renew/%';
ALTER TABLE resource_operation ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_operation FORCE ROW LEVEL SECURITY;

ALTER TABLE catalog_version ADD CONSTRAINT catalog_release_sequence_unique
 UNIQUE(tenant_id,package_id,release_sequence);
CREATE FUNCTION catalog_proof_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'catalog migration proofs are immutable' USING ERRCODE='23514';
END $$;
CREATE TRIGGER catalog_proof_immutable BEFORE UPDATE OR DELETE ON catalog_migration_proof
 FOR EACH ROW EXECUTE FUNCTION catalog_proof_immutable();

-- Reserve installation counters independently of the currently applied workspace spec.
CREATE TABLE catalog_instance_counter (
 tenant_id uuid NOT NULL, instance_id uuid NOT NULL, package_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 PRIMARY KEY(tenant_id,instance_id,package_id),
 FOREIGN KEY(tenant_id,package_id) REFERENCES catalog_package(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('catalog_instance_counter');
ALTER TABLE catalog_instance_counter DISABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_instance_installation DISABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_distribution DISABLE ROW LEVEL SECURITY;
INSERT INTO catalog_instance_counter(tenant_id,instance_id,package_id,revision)
 SELECT tenant_id,instance_id,package_id,max(revision) FROM (
 SELECT tenant_id,instance_id,package_id,revision FROM catalog_instance_installation
 UNION ALL
 SELECT tenant_id,instance_id,(r->'identity'->>'packageId')::uuid,
 COALESCE(NULLIF((r->>'installationRevision')::bigint,0),1)
 FROM catalog_distribution CROSS JOIN LATERAL jsonb_array_elements(resolution->'resources') r
 ) reserved GROUP BY tenant_id,instance_id,package_id;
ALTER TABLE catalog_instance_counter ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_instance_counter FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_instance_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_instance_installation FORCE ROW LEVEL SECURITY;

ALTER TABLE catalog_distribution ADD COLUMN lifecycle text NOT NULL DEFAULT 'pending'
 CHECK(lifecycle IN ('pending','active','superseded'));
ALTER TABLE catalog_distribution ADD COLUMN next_reconcile_at timestamptz;
UPDATE catalog_distribution SET lifecycle='superseded';
UPDATE catalog_distribution d SET lifecycle='pending',next_reconcile_at=CURRENT_TIMESTAMP
 WHERE NOT EXISTS(SELECT 1 FROM catalog_distribution newer WHERE newer.tenant_id=d.tenant_id
 AND newer.instance_id=d.instance_id AND newer.selection_revision>d.selection_revision);
ALTER TABLE catalog_distribution ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_distribution FORCE ROW LEVEL SECURITY;
CREATE INDEX catalog_distribution_due ON catalog_distribution(tenant_id,next_reconcile_at,id)
 WHERE lifecycle IN ('pending','active') AND next_reconcile_at IS NOT NULL;
ALTER TABLE catalog_revocation_delivery ADD COLUMN next_reconcile_at timestamptz NOT NULL DEFAULT 'epoch';
CREATE INDEX catalog_revocation_pending ON catalog_revocation_delivery(tenant_id,next_reconcile_at,id)
 WHERE completed_revision<revision;
