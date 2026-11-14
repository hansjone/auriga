-- Exact request bytes survive event retries, restarts, and resolution outages.
CREATE TABLE unified_resource_intent (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, instance_id uuid NOT NULL,
  owner_id uuid NOT NULL, selection_revision bigint NOT NULL CHECK (selection_revision > 0),
  request bytea NOT NULL, applied boolean NOT NULL DEFAULT false,
  UNIQUE(tenant_id,instance_id,selection_revision),
  FOREIGN KEY(tenant_id,instance_id) REFERENCES instance(tenant_id,id)
);
SELECT auriga_enable_tenant_rls('unified_resource_intent');
ALTER TABLE instance ADD COLUMN unified_refresh_pending boolean NOT NULL DEFAULT false;
