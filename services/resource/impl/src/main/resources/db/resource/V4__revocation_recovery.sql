ALTER TABLE resource_revocations ADD COLUMN needs_resign boolean NOT NULL DEFAULT false;
-- Flyway holds the DDL locks until commit. The owner must see all historical tenants
-- while repairing data; the original forced policies are restored in this transaction.
ALTER TABLE resource_revocations DISABLE ROW LEVEL SECURITY;
ALTER TABLE resource_distribution DISABLE ROW LEVEL SECURITY;
ALTER TABLE resource_distribution ADD COLUMN applied_generation bigint;
UPDATE resource_distribution SET applied_generation=generation WHERE state='applied' AND ready AND observed_generation=generation;
-- A changed signed document must be re-signed with a higher revision before it is served.
UPDATE resource_revocations r SET
  document=jsonb_set(document,'{revokedArtifacts}',d.items),needs_resign=true
FROM (SELECT id,jsonb_agg(item ORDER BY item::text) AS items
      FROM (SELECT DISTINCT r.id,a.item FROM resource_revocations r,
            jsonb_array_elements(r.document->'revokedArtifacts') AS a(item)) x GROUP BY id) d
WHERE r.id=d.id AND jsonb_array_length(r.document->'revokedArtifacts')<>jsonb_array_length(d.items);

CREATE TABLE resource_revoked_artifact (
  tenant_id uuid NOT NULL,package_id uuid NOT NULL,version_id uuid NOT NULL,digest text NOT NULL,
  PRIMARY KEY(tenant_id,package_id,version_id,digest)
);
INSERT INTO resource_revoked_artifact
SELECT DISTINCT r.tenant_id,(a.item->>'packageId')::uuid,(a.item->>'versionId')::uuid,a.item->>'digest'
FROM resource_revocations r,jsonb_array_elements(r.document->'revokedArtifacts') a(item);

CREATE TABLE resource_revocation_delivery (
  tenant_id uuid NOT NULL,instance_id uuid NOT NULL,revision bigint NOT NULL,
  completed_revision bigint NOT NULL DEFAULT 0,
  PRIMARY KEY(tenant_id,instance_id)
);
INSERT INTO resource_revocation_delivery(tenant_id,instance_id,revision)
SELECT DISTINCT d.tenant_id,d.instance_id,r.revision FROM resource_distribution d
JOIN resource_revocations r ON r.tenant_id=d.tenant_id;
SELECT auriga_enable_tenant_rls('resource_revoked_artifact');
SELECT auriga_enable_tenant_rls('resource_revocation_delivery');
ALTER TABLE resource_revocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_revocations FORCE ROW LEVEL SECURITY;
ALTER TABLE resource_distribution ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_distribution FORCE ROW LEVEL SECURITY;
