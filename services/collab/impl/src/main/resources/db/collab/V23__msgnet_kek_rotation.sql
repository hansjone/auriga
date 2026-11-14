CREATE TABLE msgnet_kek_rotation (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  realm text NOT NULL,
  current_kek_id text NOT NULL,
  source_kek_ids text NOT NULL,
  completed_at timestamptz,
  UNIQUE (tenant_id, realm)
);
SELECT auriga_enable_tenant_rls('msgnet_kek_rotation');
