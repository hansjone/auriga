-- Keep the original call rows and their accounting history intact. A stable
-- member/call fence chooses the first record even when legacy bindings differ.
CREATE TABLE ai.shared_model_call_fence (
 tenant_id uuid NOT NULL,
 member_id uuid NOT NULL,
 call_id uuid NOT NULL,
 binding_hash bytea NOT NULL CHECK (octet_length(binding_hash)=32),
 PRIMARY KEY (tenant_id,member_id,call_id),
 FOREIGN KEY (tenant_id,binding_hash,call_id)
   REFERENCES ai.shared_model_call(tenant_id,binding_hash,call_id)
);
-- The migration owner may not have BYPASSRLS. Temporarily remove FORCE within
-- this migration transaction so existing tenants are all backfilled.
ALTER TABLE ai.shared_model_call NO FORCE ROW LEVEL SECURITY;
INSERT INTO ai.shared_model_call_fence(tenant_id,member_id,call_id,binding_hash)
 SELECT DISTINCT ON (tenant_id,member_id,call_id)
   tenant_id,member_id,call_id,binding_hash
 FROM ai.shared_model_call
 ORDER BY tenant_id,member_id,call_id,created_at,binding_hash;
ALTER TABLE ai.shared_model_call FORCE ROW LEVEL SECURITY;
ALTER TABLE ai.shared_model_call_fence ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai.shared_model_call_fence FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai.shared_model_call_fence
 USING (tenant_id=nullif(current_setting('auriga.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=nullif(current_setting('auriga.tenant_id',true),'')::uuid);
