CREATE TABLE ops.alert (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, source text NOT NULL, subject text NOT NULL,
 severity text NOT NULL CHECK (severity IN ('warning','critical')),
 first_seen timestamptz NOT NULL, last_seen timestamptz NOT NULL,
 acknowledged_at timestamptz, silenced_until timestamptz, resolved_at timestamptz
);
CREATE UNIQUE INDEX alert_active_subject ON ops.alert(tenant_id,source,subject) WHERE resolved_at IS NULL;
CREATE INDEX alert_recent ON ops.alert(tenant_id,last_seen DESC,id DESC);
SELECT ops.auriga_enable_tenant_rls('ops.alert');
CREATE TABLE ops.notification_channel (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, name text NOT NULL, encrypted_target text NOT NULL,
 enabled boolean NOT NULL DEFAULT false, min_severity text NOT NULL CHECK(min_severity IN ('warning','critical')),
 last_test_at timestamptz, UNIQUE(tenant_id,id)
);
SELECT ops.auriga_enable_tenant_rls('ops.notification_channel');
