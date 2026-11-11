ALTER TABLE ops.alert ADD COLUMN revision integer NOT NULL DEFAULT 1;
ALTER TABLE ops.alert ADD COLUMN queued_revision integer NOT NULL DEFAULT 0;
ALTER TABLE ops.notification_channel ADD COLUMN last_delivery_at timestamptz;
CREATE TABLE ops.tenant_registry (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, resource_tenant uuid NOT NULL UNIQUE
);
SELECT ops.auriga_enable_tenant_rls('ops.tenant_registry');
CREATE TABLE ops.notification_delivery (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, alert_id uuid NOT NULL, channel_id uuid NOT NULL,
 revision integer NOT NULL, payload text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','cancelled')),
 attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_id uuid, lease_until timestamptz, last_error text, delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,alert_id,channel_id,revision)
);
CREATE INDEX notification_due ON ops.notification_delivery(tenant_id,status,next_attempt_at);
SELECT ops.auriga_enable_tenant_rls('ops.notification_delivery');
