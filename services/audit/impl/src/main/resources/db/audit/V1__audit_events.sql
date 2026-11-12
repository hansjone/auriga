-- Central audit store deliberately permits platform-wide reads via its admin API only.
CREATE TABLE event (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, occurred_at timestamptz NOT NULL,
 actor text NOT NULL, action text NOT NULL, result text NOT NULL,
 payload jsonb NOT NULL
);
CREATE INDEX event_time_id ON event(occurred_at DESC,id DESC);
CREATE INDEX event_actor_time ON event(actor,occurred_at DESC,id DESC);
CREATE INDEX event_action_time ON event(action,occurred_at DESC,id DESC);
ALTER TABLE event ENABLE ROW LEVEL SECURITY;
ALTER TABLE event FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_event_read ON event FOR SELECT USING (
 tenant_id = audit.auriga_current_tenant()
 OR current_setting('auriga.audit_admin_read', true) = 'true'
);
CREATE POLICY audit_event_insert ON event FOR INSERT WITH CHECK (
 tenant_id = audit.auriga_current_tenant()
);
