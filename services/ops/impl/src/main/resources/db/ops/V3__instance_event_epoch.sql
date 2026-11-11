CREATE TABLE ops.instance_event_epoch (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL UNIQUE,
 activated_at timestamptz NOT NULL DEFAULT now()
);
SELECT ops.auriga_enable_tenant_rls('ops.instance_event_epoch');
