CREATE TABLE desktop_audit_receipt (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  created_at timestamptz NOT NULL
);
SELECT auriga_enable_tenant_rls('desktop_audit_receipt');
