CREATE TABLE msgnet_control_queue (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  target_id text NOT NULL,
  identity_id text NOT NULL,
  control_revision numeric(20,0) NOT NULL,
  package_id text NOT NULL,
  control_package text NOT NULL,
  receipt text,
  UNIQUE (tenant_id, realm, package_id),
  UNIQUE (tenant_id, realm, target_id, identity_id, control_revision)
);
SELECT auriga_enable_tenant_rls('msgnet_control_queue');
INSERT INTO msgnet_control_queue (id, tenant_id, realm, target_id, identity_id, control_revision, package_id, control_package, receipt)
  SELECT id, tenant_id, realm, target_id, identity_id,
    (control_package::jsonb->'control'->'payload'->>'control_revision')::numeric,
    control_package::jsonb->>'package_id', control_package, receipt
  FROM msgnet_control_target;
