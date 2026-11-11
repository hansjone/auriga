-- Business mutations and their immutable audit envelope share the caller's transaction.
CREATE TABLE ai.audit_outbox (
 tenant_id uuid NOT NULL, id uuid NOT NULL, envelope jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 next_attempt_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,id)
);
CREATE INDEX audit_outbox_pending ON ai.audit_outbox(tenant_id,next_attempt_at,id) WHERE published_at IS NULL;
ALTER TABLE ai.audit_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai.audit_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai.audit_outbox
 USING (tenant_id = nullif(current_setting('auriga.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = nullif(current_setting('auriga.tenant_id',true),'')::uuid);
