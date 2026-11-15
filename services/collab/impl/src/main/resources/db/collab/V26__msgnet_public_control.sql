CREATE TABLE msgnet_public_control (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  root_control text NOT NULL,
  UNIQUE (tenant_id, realm)
);
SELECT auriga_enable_tenant_rls('msgnet_public_control');
