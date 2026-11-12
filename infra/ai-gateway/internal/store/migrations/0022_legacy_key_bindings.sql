-- Only the installation's authenticated administrator can import legacy key hashes.
CREATE TABLE ai.relay_legacy_key (
 tenant_id uuid NOT NULL, digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
 key_id uuid NOT NULL, expires_at timestamptz,
 PRIMARY KEY (tenant_id,digest),
 FOREIGN KEY (tenant_id,key_id) REFERENCES ai.relay_key(tenant_id,id)
);
ALTER TABLE ai.relay_legacy_key ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai.relay_legacy_key FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai.relay_legacy_key
 USING (tenant_id = nullif(current_setting('auriga.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = nullif(current_setting('auriga.tenant_id',true),'')::uuid);
