-- Example business tables: tenant_id on every table, RLS enabled through the common helper.
CREATE TABLE widget (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  name text NOT NULL
);
SELECT auriga_enable_tenant_rls('widget');

CREATE TABLE widget_seen (
  event_id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  widget_id uuid NOT NULL
);
SELECT auriga_enable_tenant_rls('widget_seen');
